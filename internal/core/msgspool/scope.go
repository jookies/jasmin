package msgspool

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Scope is what one pull consumer is permitted to observe.
//
// It is a first-class stored field rather than a property of the token string,
// so narrowing a consumer later is an update to this object and not a new
// authentication model, a second endpoint, or a token format change. The rule
// types that are already anticipated — destination prefix, source address,
// partner, maximum lookback — become *new fields here*, and every one of them
// has to be compiled into the SQL predicate the same way Connectors is.
//
// The one property that must survive every future field: a scope is enforced by
// the query, never by dropping rows after it. Post-filtering leaks through the
// cursor. A consumer would page across rows it may not see, receive short or
// empty pages indistinguishable from "no new messages", and the row counts in
// the audit trail would still describe traffic it is not entitled to know
// exists. See Compile.
type Scope struct {
	// Connectors is the allow-list of termination connector ids this consumer
	// may read.
	//
	// An empty list means "nothing", never "everything". That is the opposite of
	// the usual zero-value-means-unfiltered convention in Query, and it is
	// deliberate: a consumer record written by a buggy caller, a truncated
	// migration or a hand-edited row must fail closed. "Every connector" is not
	// expressible today on purpose; when it is wanted it arrives as an explicit
	// field, so granting it is something someone has to write down.
	Connectors []string `json:"connectors"`
	// IncludeText permits the decoded text and the raw pre-decode bytes. With it
	// false the consumer receives metadata only, and the fields are absent from
	// the response rather than empty — a client must never be able to mistake
	// redaction for an empty message.
	//
	// It is also what selects the audited action: a paged sweep of message text
	// is recorded as a reveal, not as a search.
	IncludeText bool `json:"include_text"`
}

// Normalize trims, de-duplicates and orders the connector list so two scopes
// that mean the same thing compare and render the same way.
func (scope Scope) Normalize() Scope {
	seen := make(map[string]struct{}, len(scope.Connectors))
	connectors := make([]string, 0, len(scope.Connectors))
	for _, connector := range scope.Connectors {
		connector = strings.TrimSpace(connector)
		if connector == "" {
			continue
		}
		if _, duplicate := seen[connector]; duplicate {
			continue
		}
		seen[connector] = struct{}{}
		connectors = append(connectors, connector)
	}
	sort.Strings(connectors)
	scope.Connectors = connectors
	return scope
}

// maxScopeConnectors bounds the allow-list. It is a predicate that goes into an
// IN clause on every poll, and a scope naming ten thousand connectors is a
// mistake rather than a deployment.
const maxScopeConnectors = 256

// Validate refuses a scope that would grant more than it appears to.
func (scope Scope) Validate() error {
	normalized := scope.Normalize()
	if len(normalized.Connectors) == 0 {
		return fmt.Errorf("%w: scope must name at least one connector", ErrInvalidInput)
	}
	if len(normalized.Connectors) > maxScopeConnectors {
		return fmt.Errorf("%w: scope names more than %d connectors", ErrInvalidInput, maxScopeConnectors)
	}
	return nil
}

// Allows reports whether a connector id is inside the scope.
func (scope Scope) Allows(connectorID string) bool {
	for _, connector := range scope.Connectors {
		if connector == connectorID {
			return true
		}
	}
	return false
}

// Describe renders the scope for the access-audit row. It names what was
// enforced, so a later reader of the trail can tell a full-spool read from a
// single-connector one without joining against the consumer record as it stands
// today — which may since have been narrowed, widened or deleted.
func (scope Scope) Describe() string {
	normalized := scope.Normalize()
	return fmt.Sprintf("connectors=[%s],text=%t",
		strings.Join(normalized.Connectors, "|"), normalized.IncludeText)
}

// PullRequest is what a consumer asked for on one poll.
//
// Cursor is the paging mechanism. The time filters are a convenience for a
// backfill or an investigation and are never how a consumer advances: a
// "now - N seconds" window silently loses messages, because a retried delivery
// re-enters the spool with an older received_at after the consumer has moved
// past it, and one missed poll is then a permanent hole. Record.Sequence exists
// precisely so that cannot happen.
type PullRequest struct {
	Cursor string
	Limit  int
	// ConnectorID narrows the read to one connector inside the scope. Asking for
	// a connector outside the scope is refused rather than silently answered
	// with an empty page: an empty page means "no new messages" on this API, and
	// overloading it with "you may not ask that" is how a consumer ends up
	// waiting forever for traffic it will never be shown.
	ConnectorID   string
	DeliveryState DeliveryState
	ReceivedFrom  *time.Time
	ReceivedTo    *time.Time
}

const (
	// DefaultPullLimit is the page size when a consumer names none.
	DefaultPullLimit = 100
	// MaxPullLimit is the ceiling. It matches Service.Search's own limit, so the
	// two cannot drift into a request the service rejects after this package has
	// accepted it.
	MaxPullLimit = 1000
)

// Compile turns a scope and one request into the SearchRequest the audited read
// boundary executes.
//
// Everything the scope restricts ends up in the returned request's filter
// fields, which the repositories compile into SQL. Nothing here is a hint that
// a caller may ignore: Page re-checks the connector predicate is non-empty
// before it reads, because an empty ConnectorIDs is "no filter" to Query and
// would hand the whole spool to a consumer entitled to one connector.
func (scope Scope) Compile(request PullRequest) (SearchRequest, error) {
	normalized := scope.Normalize()
	if err := normalized.Validate(); err != nil {
		return SearchRequest{}, err
	}

	limit := request.Limit
	switch {
	case limit == 0:
		limit = DefaultPullLimit
	case limit < 0 || limit > MaxPullLimit:
		return SearchRequest{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidInput, MaxPullLimit)
	}

	if request.DeliveryState != "" && !request.DeliveryState.Valid() {
		return SearchRequest{}, fmt.Errorf("%w: unknown delivery state %q", ErrInvalidInput, request.DeliveryState)
	}
	if request.ReceivedFrom != nil && request.ReceivedTo != nil &&
		!request.ReceivedTo.After(*request.ReceivedFrom) {
		return SearchRequest{}, fmt.Errorf("%w: received_to must be after received_from", ErrInvalidInput)
	}

	connectors := normalized.Connectors
	if requested := strings.TrimSpace(request.ConnectorID); requested != "" {
		if !normalized.Allows(requested) {
			return SearchRequest{}, fmt.Errorf("%w: connector %q is outside this consumer's scope",
				ErrForbidden, requested)
		}
		connectors = []string{requested}
	}

	return SearchRequest{
		Cursor:        request.Cursor,
		ConnectorIDs:  connectors,
		DeliveryState: request.DeliveryState,
		ReceivedFrom:  request.ReceivedFrom,
		ReceivedTo:    request.ReceivedTo,
		// Spelled out rather than left to the zero value. The pull API returns
		// messages, not segments: handing an application a per-segment receipt
		// row it would store as "the message" is worse than not handing it the
		// message at all. Writing it here means a future change to the default
		// cannot quietly widen what a consumer receives. See
		// Query.IncludeReceiptOnly.
		IncludeReceiptOnly: false,
		// The scope decides this, never the request: a consumer cannot ask for
		// content it was not granted, and cannot decline content in a way that
		// downgrades the audited action from a reveal to a search.
		IncludeContent: normalized.IncludeText,
		Limit:          limit,
	}, nil
}

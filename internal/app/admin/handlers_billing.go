package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pumpitspace/synevyr/internal/core"
	"github.com/pumpitspace/synevyr/internal/core/cdr"
)

// ProvisionedAccount is one account's provisioned commercial state, as supplied
// by the gateway for config-owned users. Admin-managed users are read from the
// store instead. A nil balance or count is the legacy unlimited marker.
type ProvisionedAccount struct {
	Username                     string
	ManagedBy                    string
	GroupID                      string
	Disabled                     bool
	Balance                      *float64
	SubmitSMCount                *int
	EarlyDecrementBalancePercent *int
}

type billingOptions struct {
	cdr            *cdr.Service
	balance        core.BalanceReader
	configAccounts func() []ProvisionedAccount
}

// WithBilling registers the commercial surface on the admin API: the durable
// record routes and the account balance view. It is opt-in because both are
// unavailable in deployments without a CDR-capable repository, and answering
// 404 there is more truthful than serving an empty list.
//
// This is the automation surface — scheduled usage pulls, exports into an
// accounting system — which is why it exists here and not on jCli, whose value
// is replaying the legacy console byte-for-byte.
func WithBilling(service *cdr.Service, balance core.BalanceReader, configAccounts func() []ProvisionedAccount) Option {
	return func(handler *Handler) {
		handler.billing = billingOptions{cdr: service, balance: balance, configAccounts: configAccounts}
	}
}

func (h *Handler) registerBillingRoutes(mux *http.ServeMux) {
	if h.billing.cdr != nil {
		// Prefix routing rather than {id} wildcards, matching the rest of this
		// mux — and it handles the slash inside a CDR id ("<message>/<part>")
		// natively, in raw or percent-encoded form.
		mux.HandleFunc("/admin/cdrs", h.auth(h.adminSearchCDRs))
		mux.HandleFunc("/admin/cdrs/", h.auth(h.adminCDRByID))
		mux.HandleFunc("/admin/billing/summary", h.auth(h.adminUsageSummary))
	}
	if h.billing.balance != nil && h.users != nil {
		mux.HandleFunc("/admin/billing/accounts", h.auth(h.adminBillingAccounts))
	}
}

// adminPrincipal identifies this surface to the CDR service. The admin plane
// has a single shared token, so there is no per-caller identity to record;
// naming the surface is honest, and inventing an actor would put a fabricated
// subject into cdr_access_audit. Console reads carry the real operator name and
// remain distinguishable from these.
func adminPrincipal(roles ...cdr.Role) cdr.Principal {
	return cdr.Principal{Subject: "admin-api", Roles: roles}
}

func writeCDRError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, cdr.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, cdr.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, cdr.ErrForbidden):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, cdr.ErrDisabled):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// billingWindow parses from/to. required rejects an open-ended window, which an
// aggregate must do so it cannot become a full-table scan.
func billingWindow(r *http.Request, required bool) (*time.Time, *time.Time, error) {
	parse := func(name string) (*time.Time, error) {
		raw := strings.TrimSpace(r.URL.Query().Get(name))
		if raw == "" {
			if required {
				return nil, fmt.Errorf("%s is required (RFC3339)", name)
			}
			return nil, nil
		}
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, fmt.Errorf("%s must be an RFC3339 timestamp", name)
		}
		utc := value.UTC()
		return &utc, nil
	}
	from, err := parse("from")
	if err != nil {
		return nil, nil, err
	}
	to, err := parse("to")
	if err != nil {
		return nil, nil, err
	}
	if from != nil && to != nil && !to.After(*from) {
		return nil, nil, errors.New("to must be after from")
	}
	return from, to, nil
}

func billingLimit(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errors.New("limit must be a number")
	}
	return value, nil
}

// adminSearchCDRs serves GET /admin/cdrs?user=&from=&to=&cursor=&limit=.
func (h *Handler) adminSearchCDRs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	from, to, err := billingWindow(r, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit, err := billingLimit(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	page, err := h.billing.cdr.Search(r.Context(), adminPrincipal(cdr.RoleReader), cdr.SearchRequest{
		Cursor:       r.URL.Query().Get("cursor"),
		UserID:       strings.TrimSpace(r.URL.Query().Get("user")),
		MessageID:    strings.TrimSpace(r.URL.Query().Get("message")),
		AdmittedFrom: from,
		AdmittedTo:   to,
		Limit:        limit,
	})
	if err != nil {
		writeCDRError(w, err)
		return
	}
	records := make([]cdrPayload, 0, len(page.Records))
	for _, record := range page.Records {
		records = append(records, projectCDR(record))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"records":     records,
		"next_cursor": page.NextCursor,
	})
}

// adminCDRByID serves /admin/cdrs/<id>, /admin/cdrs/<id>/events and the
// /admin/cdrs/export sub-path. The id may contain slashes, so it is taken as
// everything after the prefix minus a recognised suffix.
func (h *Handler) adminCDRByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/admin/cdrs/")
	if rest == "export" {
		h.adminExportCDRs(w, r)
		return
	}
	events := false
	if trimmed, found := strings.CutSuffix(rest, "/events"); found {
		rest, events = trimmed, true
	}
	if rest == "" {
		writeError(w, http.StatusNotFound, "cdr id required")
		return
	}
	if events {
		history, err := h.billing.cdr.Events(r.Context(), adminPrincipal(cdr.RoleReader), rest)
		if err != nil {
			writeCDRError(w, err)
			return
		}
		payload := make([]cdrEventPayload, 0, len(history))
		for _, event := range history {
			payload = append(payload, projectCDREvent(event))
		}
		writeJSON(w, http.StatusOK, payload)
		return
	}
	record, err := h.billing.cdr.Get(r.Context(), adminPrincipal(cdr.RoleReader), rest)
	if err != nil {
		writeCDRError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projectCDR(record))
}

// adminExportCDRs streams one cursor page of the core's own encoding. The bytes
// are passed through unchanged so an export taken here is identical to one
// taken from the console or any other caller.
func (h *Handler) adminExportCDRs(w http.ResponseWriter, r *http.Request) {
	from, to, err := billingWindow(r, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit, err := billingLimit(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	format := cdr.ExportFormat(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = cdr.ExportJSONL
	}
	page, err := h.billing.cdr.Export(r.Context(), adminPrincipal(cdr.RoleExporter), cdr.ExportRequest{
		Cursor:       r.URL.Query().Get("cursor"),
		UserID:       strings.TrimSpace(r.URL.Query().Get("user")),
		MessageID:    strings.TrimSpace(r.URL.Query().Get("message")),
		AdmittedFrom: from,
		AdmittedTo:   to,
		Limit:        limit,
		Format:       format,
	})
	if err != nil {
		writeCDRError(w, err)
		return
	}
	w.Header().Set("Content-Type", page.ContentType)
	w.Header().Set("X-CDR-Schema-Version", strconv.Itoa(page.SchemaVersion))
	w.Header().Set("X-CDR-Record-Count", strconv.Itoa(page.RecordCount))
	// Paging a download through a header keeps the body a valid file.
	w.Header().Set("X-CDR-Next-Cursor", page.NextCursor)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(page.Payload)
}

func (h *Handler) adminUsageSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	from, to, err := billingWindow(r, true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	summaries, err := h.billing.cdr.Summarize(r.Context(), adminPrincipal(cdr.RoleReader), cdr.SummaryQuery{
		UserID:       strings.TrimSpace(r.URL.Query().Get("user")),
		AdmittedFrom: *from,
		AdmittedTo:   *to,
	})
	if err != nil {
		writeCDRError(w, err)
		return
	}
	payload := make([]map[string]any, 0, len(summaries))
	for _, summary := range summaries {
		payload = append(payload, map[string]any{
			"user_id":          summary.UserID,
			"currency":         summary.Currency,
			"parts":            summary.Parts,
			"messages":         summary.Messages,
			"accepted":         summary.Accepted,
			"rejected":         summary.Rejected,
			"delivered":        summary.Delivered,
			"undelivered":      summary.Undelivered,
			"delivery_pending": summary.DeliveryPending,
			"charged_early":    summary.ChargedEarly,
			"charged_late":     summary.ChargedLate,
			"charged_total":    summary.ChargedTotal(),
			// Quoted late money whose outcome is still PENDING. Reported apart
			// from charged_total because an intent is not revenue.
			"quoted_late_pending": summary.QuotedLatePending,
			"first_admitted_at":   summary.FirstAdmittedAt.UTC().Format(time.RFC3339Nano),
			"last_admitted_at":    summary.LastAdmittedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	writeJSON(w, http.StatusOK, payload)
}

// adminBillingAccounts reports granted and remaining balance per account.
// Granted is what the account was provisioned with; remaining is the live
// billing state. They diverge the moment a customer sends anything, and a
// caller that had only one of them would misreport the other.
func (h *Handler) adminBillingAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	accounts := []ProvisionedAccount{}
	if h.billing.configAccounts != nil {
		accounts = append(accounts, h.billing.configAccounts()...)
	}
	stored, err := h.users.ListUsers(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	for _, user := range stored {
		account, convertErr := provisionedFromSpec(user.Username, user.SpecJSON)
		if convertErr != nil {
			writeError(w, http.StatusInternalServerError, convertErr.Error())
			return
		}
		accounts = append(accounts, account)
	}
	payload := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		entry := map[string]any{
			"username":                        account.Username,
			"managed_by":                      account.ManagedBy,
			"group_id":                        account.GroupID,
			"disabled":                        account.Disabled,
			"granted_balance":                 account.Balance,
			"granted_submit_sm_count":         account.SubmitSMCount,
			"early_decrement_balance_percent": account.EarlyDecrementBalancePercent,
			"billing_mode":                    provisionedBillingMode(account),
		}
		balance, quotaErr := h.liveQuota(r.Context(), account.Username)
		if quotaErr != nil {
			// Reported rather than defaulted to zero: "cannot read" and "no
			// money left" call for opposite actions from whatever consumes this.
			entry["live_error"] = quotaErr.Error()
		} else {
			entry["remaining_balance"] = balance.Balance
			entry["remaining_submit_sm_count"] = balance.SubmitSMCount
		}
		payload = append(payload, entry)
	}
	writeJSON(w, http.StatusOK, payload)
}

type liveQuota struct {
	Balance       *float64
	SubmitSMCount *int
}

func (h *Handler) liveQuota(ctx context.Context, username string) (liveQuota, error) {
	snapshot, err := h.billing.balance.Balance(ctx, username)
	if err != nil {
		return liveQuota{}, err
	}
	quota := liveQuota{}
	if snapshot.Balance != nil {
		value, parseErr := strconv.ParseFloat(*snapshot.Balance, 64)
		if parseErr != nil {
			return liveQuota{}, fmt.Errorf("live balance %q is not a number: %w", *snapshot.Balance, parseErr)
		}
		quota.Balance = &value
	}
	if snapshot.SMSCount != nil {
		value, parseErr := strconv.Atoi(*snapshot.SMSCount)
		if parseErr != nil {
			return liveQuota{}, fmt.Errorf("live submit_sm_count %q is not a number: %w", *snapshot.SMSCount, parseErr)
		}
		quota.SubmitSMCount = &value
	}
	return quota, nil
}

// provisionedFromSpec reads the four commercial fields out of a stored user
// spec. It projects explicitly rather than importing the outbound config type,
// which would add a package dependency for four fields.
func provisionedFromSpec(username, specJSON string) (ProvisionedAccount, error) {
	var spec struct {
		Balance                      *float64 `json:"balance"`
		SubmitSMCount                *int     `json:"submit_sm_count"`
		EarlyDecrementBalancePercent *int     `json:"early_decrement_balance_percent"`
		GroupID                      string   `json:"group_id"`
		Disabled                     bool     `json:"disabled"`
	}
	if err := json.Unmarshal([]byte(specJSON), &spec); err != nil {
		return ProvisionedAccount{}, fmt.Errorf("user %q: stored spec is not valid JSON: %w", username, err)
	}
	return ProvisionedAccount{
		Username: username, ManagedBy: "admin", GroupID: spec.GroupID, Disabled: spec.Disabled,
		Balance: spec.Balance, SubmitSMCount: spec.SubmitSMCount,
		EarlyDecrementBalancePercent: spec.EarlyDecrementBalancePercent,
	}, nil
}

// provisionedBillingMode mirrors the console's derivation. POSTPAID is absent
// on purpose: an early-decrement percentage is validated to 1..100, so a pure
// pay-on-receipt account cannot be expressed through user configuration.
func provisionedBillingMode(account ProvisionedAccount) string {
	if account.Balance == nil {
		return "UNLIMITED"
	}
	if account.EarlyDecrementBalancePercent == nil || *account.EarlyDecrementBalancePercent == 100 {
		return "PREPAID"
	}
	return "SPLIT"
}

type cdrPayload struct {
	ID                 string  `json:"id"`
	MessageID          string  `json:"message_id"`
	PartNumber         int     `json:"part_number"`
	PartCount          int     `json:"part_count"`
	UserID             string  `json:"user_id"`
	GroupID            string  `json:"group_id,omitempty"`
	RouteID            string  `json:"route_id"`
	ConnectorID        string  `json:"connector_id"`
	Ingress            string  `json:"ingress,omitempty"`
	BillID             string  `json:"bill_id,omitempty"`
	Rate               float64 `json:"rate"`
	Currency           string  `json:"currency"`
	EarlyAmount        float64 `json:"early_amount"`
	LateAmount         float64 `json:"late_amount"`
	ActualLateAmount   float64 `json:"actual_late_amount"`
	ChargedTotal       float64 `json:"charged_total"`
	BillingMode        string  `json:"billing_mode"`
	BillingOutcome     string  `json:"billing_outcome"`
	State              string  `json:"state"`
	SMPPStatus         string  `json:"smpp_status,omitempty"`
	SMSCMessageID      string  `json:"smsc_message_id,omitempty"`
	DeliveryState      string  `json:"delivery_state,omitempty"`
	DeliveryStatus     string  `json:"delivery_status,omitempty"`
	DeliveryError      string  `json:"delivery_error,omitempty"`
	AdmittedAt         string  `json:"admitted_at"`
	UpdatedAt          string  `json:"updated_at"`
	TerminalAt         string  `json:"terminal_at,omitempty"`
	DeliveryDoneAt     string  `json:"delivery_done_at,omitempty"`
	DeliveryReceivedAt string  `json:"delivery_received_at,omitempty"`
	LateBillingAt      string  `json:"late_billing_at,omitempty"`
}

type cdrEventPayload struct {
	Key              string  `json:"key"`
	Kind             string  `json:"kind"`
	State            string  `json:"state"`
	AttemptID        int64   `json:"attempt_id,omitempty"`
	SMPPStatus       string  `json:"smpp_status,omitempty"`
	SMSCMessageID    string  `json:"smsc_message_id,omitempty"`
	DeliveryState    string  `json:"delivery_state,omitempty"`
	DeliveryStatus   string  `json:"delivery_status,omitempty"`
	DeliveryError    string  `json:"delivery_error,omitempty"`
	BillingOutcome   string  `json:"billing_outcome,omitempty"`
	ActualLateAmount float64 `json:"actual_late_amount,omitempty"`
	OccurredAt       string  `json:"occurred_at"`
}

func projectCDR(record cdr.Record) cdrPayload {
	return cdrPayload{
		ID: record.ID, MessageID: record.MessageID,
		PartNumber: record.PartNumber, PartCount: record.PartCount,
		UserID: record.UserID, GroupID: record.GroupID, RouteID: record.RouteID,
		ConnectorID: record.ConnectorID, Ingress: record.Ingress, BillID: record.BillID,
		Rate: record.Rate, Currency: record.Currency,
		EarlyAmount: record.EarlyAmount, LateAmount: record.LateAmount,
		ActualLateAmount: record.ActualLateAmount,
		ChargedTotal:     record.EarlyAmount + record.ActualLateAmount,
		BillingMode:      string(record.BillingMode), BillingOutcome: string(record.BillingOutcome),
		State: string(record.State), SMPPStatus: record.SMPPStatus,
		SMSCMessageID: record.SMSCMessageID, DeliveryState: string(record.DeliveryState),
		DeliveryStatus: record.DeliveryStatus, DeliveryError: record.DeliveryError,
		AdmittedAt:         formatBillingTime(record.OccurredAt),
		UpdatedAt:          formatBillingTime(record.UpdatedAt),
		TerminalAt:         formatBillingTimePtr(record.TerminalAt),
		DeliveryDoneAt:     formatBillingTimePtr(record.DeliveryDoneAt),
		DeliveryReceivedAt: formatBillingTimePtr(record.DeliveryReceivedAt),
		LateBillingAt:      formatBillingTimePtr(record.LateBillingAt),
	}
}

func projectCDREvent(event cdr.Event) cdrEventPayload {
	return cdrEventPayload{
		Key: event.Key, Kind: string(event.Kind), State: string(event.State),
		AttemptID: event.AttemptID, SMPPStatus: event.SMPPStatus,
		SMSCMessageID: event.SMSCMessageID, DeliveryState: string(event.DeliveryState),
		DeliveryStatus: event.DeliveryStatus, DeliveryError: event.DeliveryError,
		BillingOutcome: string(event.BillingOutcome), ActualLateAmount: event.ActualLateAmount,
		OccurredAt: formatBillingTime(event.OccurredAt),
	}
}

func formatBillingTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func formatBillingTimePtr(value *time.Time) string {
	if value == nil {
		return ""
	}
	return formatBillingTime(*value)
}

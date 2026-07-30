package adminweb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pumpitspace/synevyr/internal/app/admin"
	"github.com/pumpitspace/synevyr/internal/app/outbound"
	"github.com/pumpitspace/synevyr/internal/core/cdr"
)

// LiveQuota is a principal's remaining billing state, read from the live
// directory rather than from what it was provisioned with. A nil member is the
// legacy unlimited marker and is not the same as zero: unlimited is never
// charged, zero refuses the next submit.
type LiveQuota struct {
	Balance       *float64
	SubmitSMCount *int
}

// GroupQuotaFunc reports a billing group's live shared ceiling.
type GroupQuotaFunc func(gid string) (LiveQuota, bool)

// BillingSettings are the deployment-wide commercial knobs. They come from the
// configuration file and cannot be changed from the browser, so the console
// shows them read-only rather than pretending to own them.
type BillingSettings struct {
	Currency                    string `json:"currency"`
	RetentionDays               int    `json:"retention_days"`
	RetentionBatchSize          int    `json:"retention_batch_size"`
	MaintenanceIntervalSeconds  int    `json:"maintenance_interval_seconds"`
	QuotaPersistIntervalSeconds int    `json:"quota_persist_interval_seconds"`
}

// accountResource is one customer's billing position. Granted and remaining are
// deliberately separate fields: the stored user spec holds what was provisioned,
// the live directory holds what is left, and after any traffic they differ. A
// single "balance" column was the console's most misleading number.
type accountResource struct {
	ID                           string   `json:"id"`
	Username                     string   `json:"username"`
	ManagedBy                    string   `json:"managed_by"`
	GroupID                      string   `json:"group_id,omitempty"`
	Disabled                     bool     `json:"disabled"`
	GrantedBalance               *float64 `json:"granted_balance"`
	RemainingBalance             *float64 `json:"remaining_balance"`
	GrantedSubmitSMCount         *int     `json:"granted_submit_sm_count"`
	RemainingSubmitSMCount       *int     `json:"remaining_submit_sm_count"`
	EarlyDecrementBalancePercent *int     `json:"early_decrement_balance_percent,omitempty"`
	BillingMode                  string   `json:"billing_mode"`
	// LiveError explains why the remaining values are absent for this row. It is
	// set rather than defaulting the numbers to zero, because a zero balance and
	// an unreadable one lead an operator to opposite actions.
	LiveError                string   `json:"live_error,omitempty"`
	GroupGrantedBalance      *float64 `json:"group_granted_balance,omitempty"`
	GroupRemainingBalance    *float64 `json:"group_remaining_balance,omitempty"`
	GroupGrantedSubmitSM     *int     `json:"group_granted_submit_sm_count,omitempty"`
	GroupRemainingSubmitSM   *int     `json:"group_remaining_submit_sm_count,omitempty"`
	GroupDisabled            bool     `json:"group_disabled,omitempty"`
	HTTPThroughputPerSecond  *float64 `json:"http_throughput,omitempty"`
	SMPPsThroughputPerSecond *float64 `json:"smpps_throughput,omitempty"`
}

// billingModeFor derives how a user is charged from their provisioned state.
// POSTPAID is intentionally absent: billing.ValidateParams accepts an early
// percentage of 1..100 only, so a pure pay-on-receipt user cannot be expressed
// through user configuration — only a trusted manager submit carrying its own
// bill produces that mode.
func billingModeFor(balance *float64, earlyPercent *int) string {
	if balance == nil {
		return "UNLIMITED"
	}
	if earlyPercent == nil || *earlyPercent == 100 {
		return "PREPAID"
	}
	return "SPLIT"
}

func (h *Handler) listBillingAccounts(w http.ResponseWriter, r *http.Request) {
	users, err := h.collectUsers(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	groups, groupErr := h.collectGroups(r.Context())
	if groupErr != nil {
		writeServiceError(w, groupErr)
		return
	}
	accounts := make([]accountResource, 0, len(users))
	for _, user := range users {
		account := accountResource{
			ID:                           user.Username,
			Username:                     user.Username,
			ManagedBy:                    user.ManagedBy,
			GroupID:                      user.GroupID,
			Disabled:                     user.Disabled,
			GrantedBalance:               user.Balance,
			GrantedSubmitSMCount:         user.SubmitSMCount,
			EarlyDecrementBalancePercent: user.EarlyDecrementBalancePercent,
			BillingMode:                  billingModeFor(user.Balance, user.EarlyDecrementBalancePercent),
			HTTPThroughputPerSecond:      user.HTTPThroughput,
			SMPPsThroughputPerSecond:     user.SMPPSThroughput,
		}
		live, liveErr := h.liveQuota(r.Context(), user.Username)
		switch {
		case liveErr != nil:
			account.LiveError = liveErr.Error()
		default:
			account.RemainingBalance = live.Balance
			account.RemainingSubmitSMCount = live.SubmitSMCount
		}
		if group, ok := groups[user.GroupID]; ok {
			account.GroupGrantedBalance = group.Balance
			account.GroupGrantedSubmitSM = group.SubmitSMCount
			account.GroupDisabled = group.Disabled
			if h.deps.GroupQuota != nil {
				if quota, found := h.deps.GroupQuota(user.GroupID); found {
					account.GroupRemainingBalance = quota.Balance
					account.GroupRemainingSubmitSM = quota.SubmitSMCount
				}
			}
		}
		accounts = append(accounts, account)
	}
	writeList(w, r, accounts)
}

// liveQuota reads one user's remaining balance and message quota from the live
// directory. BalanceReader returns decimal strings so the core never rounds; the
// console parses them back for display only.
func (h *Handler) liveQuota(ctx context.Context, username string) (LiveQuota, error) {
	if h.deps.BalanceReader == nil {
		return LiveQuota{}, errors.New("live balance lookup is not configured")
	}
	snapshot, err := h.deps.BalanceReader.Balance(ctx, username)
	if err != nil {
		return LiveQuota{}, err
	}
	quota := LiveQuota{}
	if snapshot.Balance != nil {
		value, parseErr := strconv.ParseFloat(*snapshot.Balance, 64)
		if parseErr != nil {
			return LiveQuota{}, fmt.Errorf("live balance %q is not a number: %w", *snapshot.Balance, parseErr)
		}
		quota.Balance = &value
	}
	if snapshot.SMSCount != nil {
		value, parseErr := strconv.Atoi(*snapshot.SMSCount)
		if parseErr != nil {
			return LiveQuota{}, fmt.Errorf("live submit_sm_count %q is not a number: %w", *snapshot.SMSCount, parseErr)
		}
		quota.SubmitSMCount = &value
	}
	return quota, nil
}

// collectUsers merges config-owned and admin-owned users the same way listUsers
// does, so the billing view can never disagree with the user list about who
// exists.
func (h *Handler) collectUsers(ctx context.Context) ([]userResource, error) {
	stored, err := h.deps.Users.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	configUsers := []outbound.UserConfig{}
	if h.deps.ConfigUsers != nil {
		configUsers = h.deps.ConfigUsers()
	}
	resources := make([]userResource, 0, len(configUsers)+len(stored))
	for index, user := range configUsers {
		resources = append(resources, userFromConfig(user, int64(index+1), "config"))
	}
	for _, user := range stored {
		resource, convertErr := toUserResource(user)
		if convertErr != nil {
			return nil, convertErr
		}
		resources = append(resources, resource)
	}
	return resources, nil
}

func (h *Handler) collectGroups(ctx context.Context) (map[string]groupResource, error) {
	groups, err := h.groupResources(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]groupResource, len(groups))
	for _, group := range groups {
		byID[group.GID] = group
	}
	return byID, nil
}

// cdrPrincipal identifies the console operator to the CDR service. The service
// writes an access-audit row for every read and export, so passing the logged-in
// username is the only per-actor trail the console has. It is audit fidelity,
// not access control: every session holds the same power today, and real
// operator roles are a separate design.
func (h *Handler) cdrPrincipal(ctx context.Context, roles ...cdr.Role) cdr.Principal {
	subject := currentUser(ctx)
	if subject == "" {
		subject = "admin-console"
	}
	return cdr.Principal{Subject: subject, Roles: roles}
}

func (h *Handler) requireCDR(w http.ResponseWriter) bool {
	if h.deps.CDR == nil {
		writeError(w, http.StatusServiceUnavailable,
			"commercial records are unavailable: the gateway has no CDR service configured")
		return false
	}
	return true
}

// writeCDRError maps the service's failure modes. ErrDisabled is a conflict, not
// an error page: retention is simply off in this deployment.
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

// cdrResource is the browser projection of one rated part. It is content-free by
// construction: the durable model stores no destination, source or message text,
// so no amount of UI can search by recipient.
type cdrResource struct {
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

func cdrToResource(record cdr.Record) cdrResource {
	return cdrResource{
		ID:                 record.ID,
		MessageID:          record.MessageID,
		PartNumber:         record.PartNumber,
		PartCount:          record.PartCount,
		UserID:             record.UserID,
		GroupID:            record.GroupID,
		RouteID:            record.RouteID,
		ConnectorID:        record.ConnectorID,
		Ingress:            record.Ingress,
		BillID:             record.BillID,
		Rate:               record.Rate,
		Currency:           record.Currency,
		EarlyAmount:        record.EarlyAmount,
		LateAmount:         record.LateAmount,
		ActualLateAmount:   record.ActualLateAmount,
		ChargedTotal:       record.EarlyAmount + record.ActualLateAmount,
		BillingMode:        string(record.BillingMode),
		BillingOutcome:     string(record.BillingOutcome),
		State:              string(record.State),
		SMPPStatus:         record.SMPPStatus,
		SMSCMessageID:      record.SMSCMessageID,
		DeliveryState:      string(record.DeliveryState),
		DeliveryStatus:     record.DeliveryStatus,
		DeliveryError:      record.DeliveryError,
		AdmittedAt:         formatUTC(record.OccurredAt),
		UpdatedAt:          formatUTC(record.UpdatedAt),
		TerminalAt:         formatUTCPtr(record.TerminalAt),
		DeliveryDoneAt:     formatUTCPtr(record.DeliveryDoneAt),
		DeliveryReceivedAt: formatUTCPtr(record.DeliveryReceivedAt),
		LateBillingAt:      formatUTCPtr(record.LateBillingAt),
	}
}

type cdrEventResource struct {
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

func formatUTC(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func formatUTCPtr(value *time.Time) string {
	if value == nil {
		return ""
	}
	return formatUTC(*value)
}

// timeWindow parses the from/to query parameters. required drives whether an
// absent window is an error: search may be open-ended and cursor-paged, while an
// aggregate must be bounded so it cannot become a full-table scan.
func timeWindow(r *http.Request, required bool) (*time.Time, *time.Time, error) {
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

func (h *Handler) searchCDRs(w http.ResponseWriter, r *http.Request) {
	if !h.requireCDR(w) {
		return
	}
	from, to, err := timeWindow(r, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, convErr := strconv.Atoi(raw)
		if convErr != nil {
			writeError(w, http.StatusBadRequest, "limit must be a number")
			return
		}
		limit = value
	}
	page, err := h.deps.CDR.Search(r.Context(),
		h.cdrPrincipal(r.Context(), cdr.RoleReader), cdr.SearchRequest{
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
	records := make([]cdrResource, 0, len(page.Records))
	for _, record := range page.Records {
		records = append(records, cdrToResource(record))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"records":     records,
		"next_cursor": page.NextCursor,
	})
}

func (h *Handler) getCDR(w http.ResponseWriter, r *http.Request) {
	if !h.requireCDR(w) {
		return
	}
	record, err := h.deps.CDR.Get(r.Context(),
		h.cdrPrincipal(r.Context(), cdr.RoleReader), r.PathValue("id"))
	if err != nil {
		writeCDRError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cdrToResource(record))
}

func (h *Handler) listCDREvents(w http.ResponseWriter, r *http.Request) {
	if !h.requireCDR(w) {
		return
	}
	events, err := h.deps.CDR.Events(r.Context(),
		h.cdrPrincipal(r.Context(), cdr.RoleReader), r.PathValue("id"))
	if err != nil {
		writeCDRError(w, err)
		return
	}
	resources := make([]cdrEventResource, 0, len(events))
	for _, event := range events {
		resources = append(resources, cdrEventResource{
			Key:              event.Key,
			Kind:             string(event.Kind),
			State:            string(event.State),
			AttemptID:        event.AttemptID,
			SMPPStatus:       event.SMPPStatus,
			SMSCMessageID:    event.SMSCMessageID,
			DeliveryState:    string(event.DeliveryState),
			DeliveryStatus:   event.DeliveryStatus,
			DeliveryError:    event.DeliveryError,
			BillingOutcome:   string(event.BillingOutcome),
			ActualLateAmount: event.ActualLateAmount,
			OccurredAt:       formatUTC(event.OccurredAt),
		})
	}
	writeJSON(w, http.StatusOK, resources)
}

// exportCDRs streams one cursor page in the requested format. It is a download,
// not a JSON resource: the payload is whatever the core encoded, byte for byte,
// so an export saved from the browser matches one taken any other way.
func (h *Handler) exportCDRs(w http.ResponseWriter, r *http.Request) {
	if !h.requireCDR(w) {
		return
	}
	from, to, err := timeWindow(r, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	format := cdr.ExportFormat(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = cdr.ExportCSV
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, convErr := strconv.Atoi(raw)
		if convErr != nil {
			writeError(w, http.StatusBadRequest, "limit must be a number")
			return
		}
		limit = value
	}
	page, err := h.deps.CDR.Export(r.Context(),
		h.cdrPrincipal(r.Context(), cdr.RoleExporter), cdr.ExportRequest{
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
	extension := "csv"
	if format == cdr.ExportJSONL {
		extension = "jsonl"
	}
	w.Header().Set("Content-Type", page.ContentType)
	w.Header().Set("X-CDR-Schema-Version", strconv.Itoa(page.SchemaVersion))
	w.Header().Set("X-CDR-Record-Count", strconv.Itoa(page.RecordCount))
	// The next cursor rides a header so the caller can page a download without
	// the body ceasing to be a valid file.
	w.Header().Set("X-CDR-Next-Cursor", page.NextCursor)
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=\"cdr-export.%s\"", extension))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(page.Payload)
}

type usageSummaryResource struct {
	ID                string  `json:"id"`
	UserID            string  `json:"user_id"`
	Currency          string  `json:"currency"`
	Parts             int64   `json:"parts"`
	Messages          int64   `json:"messages"`
	Accepted          int64   `json:"accepted"`
	Rejected          int64   `json:"rejected"`
	Delivered         int64   `json:"delivered"`
	Undelivered       int64   `json:"undelivered"`
	DeliveryPending   int64   `json:"delivery_pending"`
	ChargedEarly      float64 `json:"charged_early"`
	ChargedLate       float64 `json:"charged_late"`
	ChargedTotal      float64 `json:"charged_total"`
	QuotedLatePending float64 `json:"quoted_late_pending"`
	FirstAdmittedAt   string  `json:"first_admitted_at"`
	LastAdmittedAt    string  `json:"last_admitted_at"`
}

func (h *Handler) summarizeUsage(w http.ResponseWriter, r *http.Request) {
	if !h.requireCDR(w) {
		return
	}
	from, to, err := timeWindow(r, true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	summaries, err := h.deps.CDR.Summarize(r.Context(),
		h.cdrPrincipal(r.Context(), cdr.RoleReader), cdr.SummaryQuery{
			UserID:       strings.TrimSpace(r.URL.Query().Get("user")),
			AdmittedFrom: *from,
			AdmittedTo:   *to,
		})
	if err != nil {
		writeCDRError(w, err)
		return
	}
	resources := make([]usageSummaryResource, 0, len(summaries))
	for _, summary := range summaries {
		resources = append(resources, usageSummaryResource{
			ID:                summary.UserID + ":" + summary.Currency,
			UserID:            summary.UserID,
			Currency:          summary.Currency,
			Parts:             summary.Parts,
			Messages:          summary.Messages,
			Accepted:          summary.Accepted,
			Rejected:          summary.Rejected,
			Delivered:         summary.Delivered,
			Undelivered:       summary.Undelivered,
			DeliveryPending:   summary.DeliveryPending,
			ChargedEarly:      summary.ChargedEarly,
			ChargedLate:       summary.ChargedLate,
			ChargedTotal:      summary.ChargedTotal(),
			QuotedLatePending: summary.QuotedLatePending,
			FirstAdmittedAt:   formatUTC(summary.FirstAdmittedAt),
			LastAdmittedAt:    formatUTC(summary.LastAdmittedAt),
		})
	}
	writeList(w, r, resources)
}

// settingsUpdate is one operator change. A null value clears the override and
// hands the setting back to the configuration file.
type settingsUpdate struct {
	Name  string `json:"name"`
	Value *int   `json:"value"`
}

// updateBillingSettings applies an override live and then stores it. Only
// settings whose consumers can genuinely re-read them are accepted; currency is
// not among them, because it stamps new records only and changing it mid-window
// splits a customer's usage across two units.
func (h *Handler) updateBillingSettings(w http.ResponseWriter, r *http.Request) {
	if h.deps.Settings == nil {
		writeError(w, http.StatusServiceUnavailable,
			"billing settings are read-only in this deployment: no settings service is configured")
		return
	}
	var update settingsUpdate
	if err := decodeBody(r, &update); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	configured := h.configuredSetting(update.Name)
	var err error
	if update.Value == nil {
		err = h.deps.Settings.Clear(r.Context(), update.Name, configured)
	} else {
		err = h.deps.Settings.Set(r.Context(), update.Name, *update.Value)
	}
	if err != nil {
		if errors.Is(err, admin.ErrSettingUnknown) {
			writeError(w, http.StatusBadRequest,
				update.Name+" cannot be changed here; it is applied once at startup from the configuration file")
			return
		}
		writeServiceError(w, err)
		return
	}
	h.getBillingSettings(w, r)
}

// configuredSetting is the value the configuration file supplies, which is what
// clearing an override must restore.
func (h *Handler) configuredSetting(name string) int {
	settings := BillingSettings{}
	if h.deps.BillingSettings != nil {
		settings = h.deps.BillingSettings()
	}
	switch name {
	case admin.SettingCDRRetentionDays:
		return settings.RetentionDays
	case admin.SettingCDRRetentionBatchSize:
		return settings.RetentionBatchSize
	case admin.SettingCDRMaintenanceIntervalSeconds:
		return settings.MaintenanceIntervalSeconds
	case admin.SettingQuotaPersistIntervalSeconds:
		return settings.QuotaPersistIntervalSeconds
	}
	return 0
}

func (h *Handler) getBillingSettings(w http.ResponseWriter, r *http.Request) {
	settings := BillingSettings{Currency: cdr.DefaultCurrency}
	if h.deps.BillingSettings != nil {
		settings = h.deps.BillingSettings()
	}
	if strings.TrimSpace(settings.Currency) == "" {
		settings.Currency = cdr.DefaultCurrency
	}
	overrides := map[string]int{}
	if h.deps.Settings != nil {
		stored, err := h.deps.Settings.Overrides(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		overrides = stored
		// The payload reports the value actually in force, with the override
		// marked, so the card can say "overriding the configuration file"
		// instead of quietly disagreeing with it.
		if value, ok := overrides[admin.SettingCDRRetentionDays]; ok {
			settings.RetentionDays = value
		}
		if value, ok := overrides[admin.SettingCDRRetentionBatchSize]; ok {
			settings.RetentionBatchSize = value
		}
		if value, ok := overrides[admin.SettingCDRMaintenanceIntervalSeconds]; ok {
			settings.MaintenanceIntervalSeconds = value
		}
		if value, ok := overrides[admin.SettingQuotaPersistIntervalSeconds]; ok {
			settings.QuotaPersistIntervalSeconds = value
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"settings":  settings,
		"overrides": overrides,
		// Currency stays file-only on purpose; see updateBillingSettings.
		"editable_settings": []string{
			admin.SettingCDRRetentionDays,
			admin.SettingCDRRetentionBatchSize,
			admin.SettingCDRMaintenanceIntervalSeconds,
			admin.SettingQuotaPersistIntervalSeconds,
		},
		// Rendering a real currency symbol over unitless legacy route prices
		// would make the console commercially misleading, so the default is
		// ISO 4217 XXX and the UI says why.
		"currency_is_placeholder": settings.Currency == cdr.DefaultCurrency,
		"retention_enabled":       settings.RetentionDays > 0,
		"cdr_available":           h.deps.CDR != nil,
		"editable":                h.deps.Settings != nil,
	})
}

func (h *Handler) reconcileCDRs(w http.ResponseWriter, r *http.Request) {
	if !h.requireCDR(w) {
		return
	}
	report, err := h.deps.CDR.Reconcile(r.Context(),
		h.cdrPrincipal(r.Context(), cdr.RoleOperator))
	if err != nil {
		writeCDRError(w, err)
		return
	}
	issues := make([]map[string]any, 0, len(report.Issues))
	for _, issue := range report.Issues {
		issues = append(issues, map[string]any{"code": issue.Code, "count": issue.Count})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"checked_at": formatUTC(report.CheckedAt),
		"healthy":    report.Healthy(),
		"issues":     issues,
	})
}

// pruneCDRs deletes records past the retention window. It requires an explicit
// typed confirmation because it destroys commercial evidence: the same records
// an invoice dispute would be settled from.
func (h *Handler) pruneCDRs(w http.ResponseWriter, r *http.Request) {
	if !h.requireCDR(w) {
		return
	}
	var request struct {
		Confirm string `json:"confirm"`
	}
	if err := decodeBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(request.Confirm) != "PRUNE" {
		writeError(w, http.StatusBadRequest,
			`pruning deletes commercial records past the retention window; send {"confirm":"PRUNE"} to proceed`)
		return
	}
	result, err := h.deps.CDR.Prune(r.Context(), h.cdrPrincipal(r.Context(), cdr.RoleOperator))
	if err != nil {
		if errors.Is(err, cdr.ErrDisabled) {
			writeError(w, http.StatusConflict,
				"retention is disabled (cdr_retention_days is 0); nothing was pruned")
			return
		}
		writeCDRError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"records": result.Records,
		"events":  result.Events,
		// One call deletes at most one batch; the maintenance loop repeats until
		// a short batch. Say so rather than implying the window is now clear.
		"batched": true,
	})
}

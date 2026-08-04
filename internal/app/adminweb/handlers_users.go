package adminweb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/pumpitspace/synevyr/internal/app/admin"
	"github.com/pumpitspace/synevyr/internal/app/outbound"
	"github.com/pumpitspace/synevyr/internal/app/smppsserver"
	"github.com/pumpitspace/synevyr/internal/core/dlrgate"
)

// userResource is the flat REST shape of one user. Password is write-only
// plaintext: the handler hashes it into the outbound.UserConfig's
// password_sha256 and no password material is ever sent to the browser. An
// empty password on update keeps the stored hash.
type userResource struct {
	ID                           string   `json:"id"`
	Username                     string   `json:"username"`
	UID                          int64    `json:"uid,omitempty"`
	ManagedBy                    string   `json:"managed_by"`
	ExternalID                   string   `json:"external_id,omitempty"`
	Balance                      *float64 `json:"balance"`
	SubmitSMCount                *int     `json:"submit_sm_count"`
	EarlyDecrementBalancePercent *int     `json:"early_decrement_balance_percent"`
	GroupID                      string   `json:"group_id,omitempty"`
	Disabled                     bool     `json:"disabled"`
	HTTPSend                     *bool    `json:"http_send,omitempty"`
	HTTPBulk                     *bool    `json:"http_bulk,omitempty"`
	HTTPBalance                  *bool    `json:"http_balance,omitempty"`
	HTTPRate                     *bool    `json:"http_rate,omitempty"`
	SMPPSSend                    *bool    `json:"smpps_send,omitempty"`
	HTTPLongContent              *bool    `json:"http_long_content,omitempty"`
	SetDLRLevel                  *bool    `json:"set_dlr_level,omitempty"`
	HTTPSetDLRMethod             *bool    `json:"http_set_dlr_method,omitempty"`
	SetSourceAddress             *bool    `json:"set_source_address,omitempty"`
	SetPriority                  *bool    `json:"set_priority,omitempty"`
	SetValidityPeriod            *bool    `json:"set_validity_period,omitempty"`
	SetHexContent                *bool    `json:"set_hex_content,omitempty"`
	SetScheduleDeliveryTime      *bool    `json:"set_schedule_delivery_time,omitempty"`
	FilterDestinationAddress     string   `json:"filter_destination_address,omitempty"`
	FilterSourceAddress          string   `json:"filter_source_address,omitempty"`
	FilterPriority               string   `json:"filter_priority,omitempty"`
	FilterValidityPeriod         string   `json:"filter_validity_period,omitempty"`
	FilterContent                string   `json:"filter_content,omitempty"`
	DefaultSourceAddress         *string  `json:"default_source_address,omitempty"`
	HTTPThroughput               *float64 `json:"http_throughput,omitempty"`
	SMPPSThroughput              *float64 `json:"smpps_throughput,omitempty"`
	SMPPSBind                    *bool    `json:"smpps_bind,omitempty"`
	SMPPSIP                      string   `json:"smpps_ip,omitempty"`
	SMPPSMaxBindings             *int     `json:"smpps_max_bindings,omitempty"`
	Password                     string   `json:"password,omitempty"`
	// DLRGate* are the fork-local per-user DLR registry gate. When enabled, the
	// terminal receipt this user is told is decided by whether the destination
	// has an open activation window, not by what the upstream reported.
	DLRGateEnabled    bool   `json:"dlr_gate_enabled"`
	DLRGateHitStatus  string `json:"dlr_gate_hit_status,omitempty"`
	DLRGateHitError   string `json:"dlr_gate_hit_error,omitempty"`
	DLRGateMissStatus string `json:"dlr_gate_miss_status,omitempty"`
	DLRGateMissError  string `json:"dlr_gate_miss_error,omitempty"`
	// DLRGateKeyID is the public id in this user's registry URL. It is safe to
	// re-read; the token that goes with it is not.
	DLRGateKeyID string `json:"dlr_gate_key_id,omitempty"`
	// DLRGateRotateToken asks for a fresh credential on write. Write-only.
	DLRGateRotateToken bool `json:"dlr_gate_rotate_token,omitempty"`
	// DLRGateToken is the plaintext token, present exactly once — in the
	// response that minted it. Nothing can reprint it, because only a SHA-256
	// proof is stored.
	DLRGateToken       string `json:"dlr_gate_token,omitempty"`
	DLRGateTokenNotice string `json:"dlr_gate_token_notice,omitempty"`
	// LiveBalance and LiveSubmitSMCount are read-only projections of the live
	// directory: what is left now, as opposed to Balance/SubmitSMCount above,
	// which are what the account was provisioned with. They are ignored on write
	// — an operator changes the grant, and charges move the live value.
	LiveBalance       *float64 `json:"live_balance,omitempty"`
	LiveSubmitSMCount *int     `json:"live_submit_sm_count,omitempty"`
}

func userFromConfig(cfg outbound.UserConfig, uid int64, managedBy string) userResource {
	resource := userResource{
		ID:                           cfg.Username,
		Username:                     cfg.Username,
		UID:                          uid,
		ManagedBy:                    managedBy,
		ExternalID:                   cfg.ExternalID,
		Balance:                      cfg.Balance,
		SubmitSMCount:                cfg.SubmitSMCount,
		EarlyDecrementBalancePercent: cfg.EarlyDecrementBalancePercent,
		GroupID:                      cfg.GroupID,
		Disabled:                     cfg.Disabled,
	}
	if credential := cfg.MTCredential; credential != nil {
		resource.HTTPSend = credential.HTTPSend
		resource.HTTPBulk = credential.HTTPBulk
		resource.HTTPBalance = credential.HTTPBalance
		resource.HTTPRate = credential.HTTPRate
		resource.SMPPSSend = credential.SMPPSSend
		resource.HTTPLongContent = credential.HTTPLongContent
		resource.SetDLRLevel = credential.SetDLRLevel
		resource.HTTPSetDLRMethod = credential.HTTPSetDLRMethod
		resource.SetSourceAddress = credential.SetSourceAddress
		resource.SetPriority = credential.SetPriority
		resource.SetValidityPeriod = credential.SetValidityPeriod
		resource.SetHexContent = credential.SetHexContent
		resource.SetScheduleDeliveryTime = credential.SetScheduleDeliveryTime
		resource.FilterDestinationAddress = credential.FilterDestinationAddress
		resource.FilterSourceAddress = credential.FilterSourceAddress
		resource.FilterPriority = credential.FilterPriority
		resource.FilterValidityPeriod = credential.FilterValidityPeriod
		resource.FilterContent = credential.FilterContent
		resource.DefaultSourceAddress = credential.DefaultSourceAddress
		resource.HTTPThroughput = credential.HTTPThroughput
		resource.SMPPSThroughput = credential.SMPPSThroughput
	}
	if credential := cfg.SMPPSCredential; credential != nil {
		resource.SMPPSBind = credential.Bind
		resource.SMPPSIP = credential.IP
		resource.SMPPSMaxBindings = credential.MaxBindings
	}
	if gate := cfg.DLRGate; gate != nil {
		resource.DLRGateEnabled = gate.Enabled
		resource.DLRGateKeyID = gate.KeyID
		resource.DLRGateHitStatus = gate.HitStatus
		resource.DLRGateHitError = gate.HitError
		resource.DLRGateMissStatus = gate.MissStatus
		resource.DLRGateMissError = gate.MissError
	}
	return resource
}

func toUserResource(stored admin.StoredUser) (userResource, error) {
	var cfg outbound.UserConfig
	if err := json.Unmarshal([]byte(stored.SpecJSON), &cfg); err != nil {
		return userResource{}, fmt.Errorf("user %q: stored spec is not valid JSON: %w", stored.Username, err)
	}
	cfg.Username = stored.Username
	return userFromConfig(cfg, stored.UID, "admin"), nil
}

func (h *Handler) configUser(username string) (userResource, bool) {
	if h.deps.ConfigUsers == nil {
		return userResource{}, false
	}
	for index, user := range h.deps.ConfigUsers() {
		if user.Username == username {
			return userFromConfig(user, int64(index+1), "config"), true
		}
	}
	return userResource{}, false
}

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	resources, err := h.collectUsers(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	for index := range resources {
		h.attachLiveQuota(r.Context(), &resources[index])
	}
	writeList(w, r, resources)
}

// attachLiveQuota fills the remaining balance and message quota beside the
// provisioned grant. Balance and SubmitSMCount are what the operator granted;
// after any traffic the live values differ, and showing only the grant in a
// column labelled "balance" was the console's most misleading number. A failed
// read leaves the live fields nil rather than defaulting them to zero.
func (h *Handler) attachLiveQuota(ctx context.Context, resource *userResource) {
	if h.deps.BalanceReader == nil {
		return
	}
	live, err := h.liveQuota(ctx, resource.Username)
	if err != nil {
		return
	}
	resource.LiveBalance = live.Balance
	resource.LiveSubmitSMCount = live.SubmitSMCount
}

func (h *Handler) getUser(w http.ResponseWriter, r *http.Request) {
	stored, err := h.deps.Users.GetUser(r.Context(), r.PathValue("username"))
	if err != nil {
		if resource, ok := h.configUser(r.PathValue("username")); ok {
			h.attachLiveQuota(r.Context(), &resource)
			writeJSON(w, http.StatusOK, resource)
			return
		}
		writeServiceError(w, err)
		return
	}
	resource, err := toUserResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.attachLiveQuota(r.Context(), &resource)
	writeJSON(w, http.StatusOK, resource)
}

// applyUser builds the outbound.UserConfig spec and upserts it through the
// user service (CreateUser keeps the uid when replacing).
func (h *Handler) applyUser(w http.ResponseWriter, r *http.Request, res userResource, passwordSHA256, plaintextPassword string, status int) {
	mtCredential := &outbound.MTCredentialConfig{
		HTTPSend:                 res.HTTPSend,
		HTTPBulk:                 res.HTTPBulk,
		HTTPBalance:              res.HTTPBalance,
		HTTPRate:                 res.HTTPRate,
		SMPPSSend:                res.SMPPSSend,
		HTTPLongContent:          res.HTTPLongContent,
		SetDLRLevel:              res.SetDLRLevel,
		HTTPSetDLRMethod:         res.HTTPSetDLRMethod,
		SetSourceAddress:         res.SetSourceAddress,
		SetPriority:              res.SetPriority,
		SetValidityPeriod:        res.SetValidityPeriod,
		SetHexContent:            res.SetHexContent,
		SetScheduleDeliveryTime:  res.SetScheduleDeliveryTime,
		FilterDestinationAddress: res.FilterDestinationAddress,
		FilterSourceAddress:      res.FilterSourceAddress,
		FilterPriority:           res.FilterPriority,
		FilterValidityPeriod:     res.FilterValidityPeriod,
		FilterContent:            res.FilterContent,
		DefaultSourceAddress:     res.DefaultSourceAddress,
		HTTPThroughput:           res.HTTPThroughput,
		SMPPSThroughput:          res.SMPPSThroughput,
	}
	if res.HTTPSend == nil && res.HTTPBulk == nil && res.HTTPBalance == nil &&
		res.HTTPRate == nil && res.SMPPSSend == nil && res.HTTPLongContent == nil &&
		res.SetDLRLevel == nil && res.HTTPSetDLRMethod == nil &&
		res.SetSourceAddress == nil && res.SetPriority == nil &&
		res.SetValidityPeriod == nil && res.SetHexContent == nil &&
		res.SetScheduleDeliveryTime == nil && res.FilterDestinationAddress == "" &&
		res.FilterSourceAddress == "" && res.FilterPriority == "" &&
		res.FilterValidityPeriod == "" && res.FilterContent == "" &&
		res.DefaultSourceAddress == nil && res.HTTPThroughput == nil &&
		res.SMPPSThroughput == nil {
		mtCredential = nil
	}
	smppsCredential := &outbound.SMPPSCredentialConfig{
		Bind:        res.SMPPSBind,
		IP:          res.SMPPSIP,
		MaxBindings: res.SMPPSMaxBindings,
	}
	if res.SMPPSBind == nil && res.SMPPSIP == "" && res.SMPPSMaxBindings == nil {
		smppsCredential = nil
	}
	// A disabled gate with no chosen statuses is stored as no block at all, so a
	// user who never touched the feature keeps a spec identical to one written
	// before it existed.
	// Whatever credential the stored spec already holds. Read here rather than
	// taken from the request, because the token digest is never sent to the
	// browser and a round trip must not be able to clear it.
	existingGate := h.storedDLRGateCredential(r, res.Username)
	var dlrGate *outbound.DLRGateConfig
	if res.DLRGateEnabled || res.DLRGateHitStatus != "" || res.DLRGateHitError != "" ||
		res.DLRGateMissStatus != "" || res.DLRGateMissError != "" {
		dlrGate = &outbound.DLRGateConfig{
			Enabled:    res.DLRGateEnabled,
			HitStatus:  strings.ToUpper(strings.TrimSpace(res.DLRGateHitStatus)),
			HitError:   strings.TrimSpace(res.DLRGateHitError),
			MissStatus: strings.ToUpper(strings.TrimSpace(res.DLRGateMissStatus)),
			MissError:  strings.TrimSpace(res.DLRGateMissError),
			// Carried forward from whatever the stored spec held, so an ordinary
			// edit does not silently invalidate a partner's live credential.
			KeyID:       existingGate.keyID,
			TokenSHA256: existingGate.tokenSHA256,
		}
	}
	// Mint on the transition to enabled, and on an explicit rotation. Enabling
	// the gate without a credential would publish an endpoint nobody can call,
	// so the switch and the credential are deliberately one action.
	var minted *dlrgate.Credential
	if dlrGate != nil && dlrGate.Enabled && (dlrGate.KeyID == "" || dlrGate.TokenSHA256 == "" || res.DLRGateRotateToken) {
		credential, err := dlrgate.NewCredential()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		dlrGate.KeyID = credential.KeyID
		dlrGate.TokenSHA256 = credential.TokenSHA256
		minted = &credential
	}
	cfg := outbound.UserConfig{
		Username:                     res.Username,
		ExternalID:                   res.ExternalID,
		PasswordSHA256:               passwordSHA256,
		Balance:                      res.Balance,
		SubmitSMCount:                res.SubmitSMCount,
		EarlyDecrementBalancePercent: res.EarlyDecrementBalancePercent,
		GroupID:                      res.GroupID,
		Disabled:                     res.Disabled,
		MTCredential:                 mtCredential,
		SMPPSCredential:              smppsCredential,
		DLRGate:                      dlrGate,
	}
	specJSON, err := json.Marshal(cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.deps.Users.CreateUser(r.Context(), res.Username, string(specJSON)); err != nil {
		writeServiceError(w, err)
		return
	}
	if cfg.SMPPSCredential != nil {
		// Start from the existing bind account and overlay only what this form
		// actually carries. A wholesale replace would silently reset every field
		// the user page does not render — set_dlr_level, set_source_address,
		// set_priority all default to permissive — and blank the IP whitelist,
		// which means "any IPv4". Editing a balance must not widen a bind
		// account's network exposure or re-enable source-address spoofing.
		var account smppsserver.UserConfig
		if existing, err := h.deps.SMPPsUsers.GetUser(r.Context(), res.Username); err == nil {
			if decoded, decodeErr := storedSMPPsUserConfig(existing); decodeErr == nil {
				account = decoded
			}
		}
		if plaintextPassword == "" {
			plaintextPassword = account.Password
		}
		if plaintextPassword != "" {
			account.SystemID = res.Username
			account.Password = plaintextPassword
			account.Disabled = res.Disabled
			if cfg.SMPPSCredential.IP != "" {
				account.IPWhitelist = cfg.SMPPSCredential.IP
			}
			if cfg.SMPPSCredential.MaxBindings != nil {
				account.MaxBindings = cfg.SMPPSCredential.MaxBindings
			}
			if cfg.SMPPSCredential.Bind != nil {
				account.Bind = cfg.SMPPSCredential.Bind
			}
			// Mirror the MT credential as well: legacy serves both protocols from
			// one user record, so a destination fence set on the user applies to
			// their SMPP binds too. Without this the UI would report a restriction
			// the bind path never enforces.
			if credential := cfg.MTCredential; credential != nil {
				account.SMPPSSend = credential.SMPPSSend
				account.SetDLRLevel = credential.SetDLRLevel
				account.SetSourceAddress = credential.SetSourceAddress
				account.SetPriority = credential.SetPriority
				account.FilterDestinationAddress = credential.FilterDestinationAddress
				account.FilterSourceAddress = credential.FilterSourceAddress
				account.FilterPriority = credential.FilterPriority
				account.FilterValidityPeriod = credential.FilterValidityPeriod
				account.FilterContent = credential.FilterContent
				account.DefaultSourceAddress = credential.DefaultSourceAddress
			}
			accountSpec, err := json.Marshal(account)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if err := h.deps.SMPPsUsers.PutUser(r.Context(), res.Username, string(accountSpec)); err != nil {
				writeServiceError(w, err)
				return
			}
		}
	}
	stored, err := h.deps.Users.GetUser(r.Context(), res.Username)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resource, err := toUserResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if minted != nil {
		// The only time this token exists outside the caller's own storage. It is
		// attached to the response that created it and can never be re-read,
		// because the spec holds a SHA-256 proof rather than the secret.
		resource.DLRGateToken = minted.Token
		resource.DLRGateTokenNotice = "Copy this token now. It is shown once and cannot be retrieved again; rotate the credential to issue a new one."
	}
	writeJSON(w, status, resource)
}

// storedDLRGateCredential reads the credential currently on a user's spec.
// A user that does not exist yet, or one with no gate, yields the zero value.
func (h *Handler) storedDLRGateCredential(r *http.Request, username string) struct{ keyID, tokenSHA256 string } {
	var empty struct{ keyID, tokenSHA256 string }
	stored, err := h.deps.Users.GetUser(r.Context(), username)
	if err != nil {
		return empty
	}
	var config outbound.UserConfig
	if err := json.Unmarshal([]byte(stored.SpecJSON), &config); err != nil || config.DLRGate == nil {
		return empty
	}
	return struct{ keyID, tokenSHA256 string }{config.DLRGate.KeyID, config.DLRGate.TokenSHA256}
}

func hashPassword(plaintext string) string {
	digest := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(digest[:])
}

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	var res userResource
	if err := decodeBody(r, &res); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if res.Username == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}
	if res.Password == "" {
		writeError(w, http.StatusBadRequest, "password is required")
		return
	}
	if res.ExternalID == "" {
		res.ExternalID = res.Username
	}
	h.applyUser(w, r, res, hashPassword(res.Password), res.Password, http.StatusCreated)
}

func (h *Handler) updateUser(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	stored, err := h.deps.Users.GetUser(r.Context(), username)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	var currentCfg outbound.UserConfig
	if err := json.Unmarshal([]byte(stored.SpecJSON), &currentCfg); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("user %q: stored spec is not valid JSON: %v", username, err))
		return
	}
	// Start from the stored state so fields the form omits keep their values.
	res := userFromConfig(currentCfg, stored.UID, "admin")
	if err := decodeBody(r, &res); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	res.Username = username // the path is authoritative for the identity
	passwordSHA256 := currentCfg.PasswordSHA256
	if res.Password != "" {
		passwordSHA256 = hashPassword(res.Password)
	}
	if res.ExternalID == "" {
		res.ExternalID = username
	}
	h.applyUser(w, r, res, passwordSHA256, res.Password, http.StatusOK)
}

func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	stored, err := h.deps.Users.GetUser(r.Context(), username)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resource, err := toUserResource(stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.deps.Users.DeleteUser(r.Context(), username); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resource)
}

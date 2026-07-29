package adminweb

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/pumpitspace/jasmin/internal/app/admin"
	"github.com/pumpitspace/jasmin/internal/app/outbound"
	"github.com/pumpitspace/jasmin/internal/app/smppsserver"
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
	stored, err := h.deps.Users.ListUsers(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
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
		resource, err := toUserResource(user)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		resources = append(resources, resource)
	}
	writeList(w, r, resources)
}

func (h *Handler) getUser(w http.ResponseWriter, r *http.Request) {
	stored, err := h.deps.Users.GetUser(r.Context(), r.PathValue("username"))
	if err != nil {
		if resource, ok := h.configUser(r.PathValue("username")); ok {
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
				account.SMPPSSend = cfg.SMPPSCredential.Bind
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
	writeJSON(w, status, resource)
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

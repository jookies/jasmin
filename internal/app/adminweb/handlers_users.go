package adminweb

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/pumpitspace/jasmin/internal/app/admin"
	"github.com/pumpitspace/jasmin/internal/app/outbound"
)

// userResource is the flat REST shape of one user. Password is write-only
// plaintext: the handler hashes it into the outbound.UserConfig's
// password_sha256 and no password material is ever sent to the browser. An
// empty password on update keeps the stored hash.
type userResource struct {
	ID                           string   `json:"id"`
	Username                     string   `json:"username"`
	UID                          int64    `json:"uid,omitempty"`
	ExternalID                   string   `json:"external_id,omitempty"`
	Balance                      *float64 `json:"balance"`
	SubmitSMCount                *int     `json:"submit_sm_count"`
	EarlyDecrementBalancePercent *int     `json:"early_decrement_balance_percent"`
	Password                     string   `json:"password,omitempty"`
}

func toUserResource(stored admin.StoredUser) (userResource, error) {
	var cfg outbound.UserConfig
	if err := json.Unmarshal([]byte(stored.SpecJSON), &cfg); err != nil {
		return userResource{}, fmt.Errorf("user %q: stored spec is not valid JSON: %w", stored.Username, err)
	}
	return userResource{
		ID:                           stored.Username,
		Username:                     stored.Username,
		UID:                          stored.UID,
		ExternalID:                   cfg.ExternalID,
		Balance:                      cfg.Balance,
		SubmitSMCount:                cfg.SubmitSMCount,
		EarlyDecrementBalancePercent: cfg.EarlyDecrementBalancePercent,
	}, nil
}

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	stored, err := h.deps.Users.ListUsers(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	resources := make([]userResource, 0, len(stored))
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
func (h *Handler) applyUser(w http.ResponseWriter, r *http.Request, res userResource, passwordSHA256 string, status int) {
	cfg := outbound.UserConfig{
		Username:                     res.Username,
		ExternalID:                   res.ExternalID,
		PasswordSHA256:               passwordSHA256,
		Balance:                      res.Balance,
		SubmitSMCount:                res.SubmitSMCount,
		EarlyDecrementBalancePercent: res.EarlyDecrementBalancePercent,
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
	h.applyUser(w, r, res, hashPassword(res.Password), http.StatusCreated)
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
	res := userResource{
		Username:                     username,
		ExternalID:                   currentCfg.ExternalID,
		Balance:                      currentCfg.Balance,
		SubmitSMCount:                currentCfg.SubmitSMCount,
		EarlyDecrementBalancePercent: currentCfg.EarlyDecrementBalancePercent,
	}
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
	h.applyUser(w, r, res, passwordSHA256, http.StatusOK)
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

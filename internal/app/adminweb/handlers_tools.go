package adminweb

import (
	"errors"
	"net/http"
	"strings"

	"github.com/pumpitspace/jasmin/internal/core"
)

type accountToolRequest struct {
	Username    string `json:"username"`
	Destination string `json:"destination,omitempty"`
}

func (h *Handler) handleBalanceTool(w http.ResponseWriter, r *http.Request) {
	if h.deps.BalanceReader == nil {
		writeError(w, http.StatusServiceUnavailable, "balance lookup is unavailable")
		return
	}
	var request accountToolRequest
	if err := decodeBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	request.Username = strings.TrimSpace(request.Username)
	if request.Username == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}
	snapshot, err := h.deps.BalanceReader.Balance(r.Context(), request.Username)
	if err != nil {
		writeToolError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"username":        request.Username,
		"balance":         snapshot.Balance,
		"submit_sm_count": snapshot.SMSCount,
	})
}

func (h *Handler) handleRateTool(w http.ResponseWriter, r *http.Request) {
	if h.deps.RateReader == nil {
		writeError(w, http.StatusServiceUnavailable, "rate lookup is unavailable")
		return
	}
	var request accountToolRequest
	if err := decodeBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	request.Username = strings.TrimSpace(request.Username)
	request.Destination = strings.TrimSpace(request.Destination)
	if request.Username == "" || request.Destination == "" {
		writeError(w, http.StatusBadRequest, "username and destination are required")
		return
	}
	quote, err := h.deps.RateReader.Rate(r.Context(), request.Username, request.Destination)
	if err != nil {
		writeToolError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"username":        request.Username,
		"destination":     request.Destination,
		"unit_rate":       quote.UnitRate,
		"submit_sm_count": quote.SubmitSMCount,
	})
}

type sendToolRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	Destination string `json:"destination"`
	Content     string `json:"content"`
	From        string `json:"from,omitempty"`
	Coding      int    `json:"coding,omitempty"`
	DLR         bool   `json:"dlr,omitempty"`
	// DLRUrl is where the receipt is delivered. Requesting a receipt without
	// one is refused rather than accepted: the submit path only registers the
	// dlr:<msgid> record when a URL is present, so the toggle alone produced a
	// success response and a receipt that could never arrive — indistinguishable
	// from a carrier problem for anyone using this to debug one.
	DLRUrl string `json:"dlr_url,omitempty"`
}

func (h *Handler) handleSendTool(w http.ResponseWriter, r *http.Request) {
	if h.deps.Submitter == nil {
		writeError(w, http.StatusServiceUnavailable, "message submission is unavailable")
		return
	}
	var request sendToolRequest
	if err := decodeBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	request.Username = strings.TrimSpace(request.Username)
	request.Destination = strings.TrimSpace(request.Destination)
	if request.Username == "" || request.Password == "" || request.Destination == "" || request.Content == "" {
		writeError(w, http.StatusBadRequest, "username, password, destination and content are required")
		return
	}
	request.DLRUrl = strings.TrimSpace(request.DLRUrl)
	dlrLevel := 0
	if request.DLR {
		if request.DLRUrl == "" {
			writeError(w, http.StatusBadRequest, "dlr_url is required when requesting a delivery receipt")
			return
		}
		dlrLevel = 1
	}
	messageID, err := h.deps.Submitter.Submit(r.Context(), core.SubmitRequest{
		Username:        request.Username,
		Password:        request.Password,
		Destination:     request.Destination,
		Content:         request.Content,
		From:            request.From,
		Coding:          request.Coding,
		DLR:             request.DLR,
		DLRUrl:          request.DLRUrl,
		DLRLevel:        dlrLevel,
		DLRMethod:       "POST",
		SourceConnector: "httpapi",
	})
	if err != nil {
		writeToolError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message_id":  messageID,
		"destination": request.Destination,
	})
}

func writeToolError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, core.ErrAuthentication),
		errors.Is(err, core.ErrInvalidParameter),
		errors.Is(err, core.ErrFilterRejected):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, core.ErrQuotaExceeded),
		errors.Is(err, core.ErrNoLiveConnector),
		errors.Is(err, core.ErrNoRouteMatched):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

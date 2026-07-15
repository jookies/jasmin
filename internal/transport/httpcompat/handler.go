package httpcompat

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/pumpitspace/jasmin/internal/core"
)

const (
	plainContentType = "text/plain"
	jsonContentType  = "application/json"
)

type Dependencies struct {
	Authenticator core.Authenticator
	BalanceReader core.BalanceReader
	RateReader    core.RateReader
	Submitter     core.Submitter
}

type handler struct {
	dependencies Dependencies
}

func NewHandler(dependencies Dependencies) http.Handler {
	h := &handler{dependencies: dependencies}
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", h.ping)
	mux.HandleFunc("/rate", h.rate)
	mux.HandleFunc("/balance", h.balance)
	mux.HandleFunc("/send", h.send)
	return mux
}

func (h *handler) ping(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	writeResponse(w, http.StatusOK, "", "Jasmin/PONG")
}

func (h *handler) rate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	arguments, err := requestArguments(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if missing := firstMissing(arguments, "username", "password", "to"); missing != "" {
		writeJSONError(w, http.StatusBadRequest, mandatoryArgumentError(missing))
		return
	}
	username := arguments["username"]
	if !h.authenticate(w, r, username, arguments["password"], jsonContentType) {
		return
	}
	if h.dependencies.RateReader == nil {
		writeJSONError(w, http.StatusInternalServerError, "Rate backend is not configured")
		return
	}
	quote, err := h.dependencies.RateReader.Rate(r.Context(), username, arguments["to"])
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	body := fmt.Sprintf(`{"unit_rate": %s, "submit_sm_count": %d}`,
		legacyFloat(quote.UnitRate), quote.SubmitSMCount)
	writeResponse(w, http.StatusOK, jsonContentType, body)
}

func (h *handler) balance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	arguments, err := requestArguments(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if missing := firstMissing(arguments, "username", "password"); missing != "" {
		writeJSONError(w, http.StatusBadRequest, mandatoryArgumentError(missing))
		return
	}
	username := arguments["username"]
	if !h.authenticate(w, r, username, arguments["password"], jsonContentType) {
		return
	}
	if h.dependencies.BalanceReader == nil {
		writeJSONError(w, http.StatusInternalServerError, "Balance backend is not configured")
		return
	}
	snapshot, err := h.dependencies.BalanceReader.Balance(r.Context(), username)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	body := fmt.Sprintf(`{"balance": %s, "sms_count": %s}`,
		jsonString(quotaValue(snapshot.Balance)), jsonString(quotaValue(snapshot.SMSCount)))
	writeResponse(w, http.StatusOK, jsonContentType, body)
}

func (h *handler) send(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	arguments, err := requestArguments(r)
	if err != nil {
		writePlainError(w, http.StatusBadRequest, err.Error())
		return
	}
	if missing := firstMissing(arguments, "to", "username", "password"); missing != "" {
		writePlainError(w, http.StatusBadRequest, mandatoryArgumentError(missing))
		return
	}
	_, hasContent := arguments["content"]
	_, hasHexContent := arguments["hex-content"]
	if !hasContent && !hasHexContent {
		writePlainError(w, http.StatusBadRequest, "content or hex-content not present.")
		return
	}
	if hasContent && hasHexContent {
		writePlainError(w, http.StatusBadRequest,
			"content and hex-content cannot be used both in same request.")
		return
	}
	username := arguments["username"]
	if !h.authenticate(w, r, username, arguments["password"], plainContentType) {
		return
	}
	if h.dependencies.Submitter == nil {
		writePlainError(w, http.StatusInternalServerError,
			"Cannot send submit_sm, check SMPPClientManagerPB log file for details")
		return
	}
	messageID, err := h.dependencies.Submitter.Submit(r.Context(), core.SubmitRequest{
		Username:    username,
		Destination: arguments["to"],
		Content:     arguments["content"],
		HexContent:  arguments["hex-content"],
	})
	if err != nil {
		if errors.Is(err, core.ErrNoLiveConnector) {
			writePlainError(w, http.StatusInternalServerError,
				"Cannot send submit_sm, check SMPPClientManagerPB log file for details")
			return
		}
		writePlainError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeResponse(w, http.StatusOK, plainContentType, fmt.Sprintf(`Success %q`, messageID))
}

func (h *handler) authenticate(
	w http.ResponseWriter,
	r *http.Request,
	username string,
	password string,
	contentType string,
) bool {
	if h.dependencies.Authenticator == nil {
		h.authenticationFailure(w, username, contentType)
		return false
	}
	if err := h.dependencies.Authenticator.Authenticate(r.Context(), username, password); err != nil {
		if errors.Is(err, core.ErrAuthentication) {
			h.authenticationFailure(w, username, contentType)
			return false
		}
		if contentType == jsonContentType {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
		} else {
			writePlainError(w, http.StatusInternalServerError, err.Error())
		}
		return false
	}
	return true
}

func (h *handler) authenticationFailure(w http.ResponseWriter, username, contentType string) {
	message := "Authentication failure for username:" + username
	if contentType == jsonContentType {
		writeJSONError(w, http.StatusForbidden, message)
		return
	}
	writePlainError(w, http.StatusForbidden, message)
}

func requestArguments(r *http.Request) (map[string]string, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil && r.Header.Get("Content-Type") != "" {
		return nil, fmt.Errorf("Invalid Content-Type")
	}
	if mediaType == "application/json" {
		var payload map[string]any
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&payload); err != nil {
			return nil, fmt.Errorf("Invalid JSON request")
		}
		arguments := make(map[string]string, len(payload))
		for key, value := range payload {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("Invalid JSON value for argument [%s]", key)
			}
			arguments[key] = text
		}
		return arguments, nil
	}
	if err := r.ParseForm(); err != nil {
		return nil, fmt.Errorf("Invalid form request")
	}
	arguments := make(map[string]string, len(r.Form))
	for key, values := range r.Form {
		if len(values) != 0 {
			arguments[key] = values[0]
		}
	}
	return arguments, nil
}

func firstMissing(arguments map[string]string, keys ...string) string {
	for _, key := range keys {
		if arguments[key] == "" {
			return key
		}
	}
	return ""
}

func mandatoryArgumentError(argument string) string {
	return fmt.Sprintf("Mandatory argument [%s] is not found.", argument)
}

func writePlainError(w http.ResponseWriter, status int, message string) {
	writeResponse(w, status, plainContentType, fmt.Sprintf(`Error %q`, message))
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	encoded, _ := json.Marshal(message)
	writeResponse(w, status, jsonContentType, string(encoded))
}

func writeResponse(w http.ResponseWriter, status int, contentType, body string) {
	if contentType == "" {
		// Presence of the key suppresses net/http content sniffing while emitting
		// no Content-Type field, matching the legacy /ping contract.
		w.Header()["Content-Type"] = nil
	} else {
		w.Header().Set("Content-Type", contentType)
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func methodNotAllowed(w http.ResponseWriter) {
	writePlainError(w, http.StatusMethodNotAllowed, "Method not allowed")
}

func quotaValue(value *string) string {
	if value == nil {
		return "ND"
	}
	return *value
}

func jsonString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func legacyFloat(value float64) string {
	formatted := strconv.FormatFloat(value, 'f', -1, 64)
	if !strings.Contains(formatted, ".") {
		formatted += ".0"
	}
	return formatted
}

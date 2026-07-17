package httpcompat

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pumpitspace/jasmin/internal/core"
)

const (
	plainContentType = "text/plain"
	jsonContentType  = "application/json"
)

var (
	reTo             = regexp.MustCompile(`^\+?\d+$`)
	reCoding         = regexp.MustCompile(`^(0|1|2|3|4|5|6|7|8|9|10|13|14)$`)
	reUsername       = regexp.MustCompile(`^.{1,16}$`)
	rePassword       = regexp.MustCompile(`^.{1,16}$`)
	rePriority       = regexp.MustCompile(`^[0-3]$`)
	reSDT            = regexp.MustCompile(`^\d{12}\d\d{2}[+\-R]$`)
	reValidityPeriod = regexp.MustCompile(`^\d+$`)
	reDLR            = regexp.MustCompile(`^(yes|no)$`)
	reDLRUrl         = regexp.MustCompile(`^(http|https)://.*$`)
	reDLRLevel       = regexp.MustCompile(`^[1-3]$`)
	reDLRMethod      = regexp.MustCompile(`(?i)^(get|post)$`)
	reTags           = regexp.MustCompile(`^([-a-zA-Z0-9,])*$`)
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

	// 1. Validation
	if err := validateSendArgs(arguments); err != nil {
		writePlainError(w, http.StatusBadRequest, err.Error())
		return
	}

	// 2. Authentication
	username := arguments["username"]
	if !h.authenticate(w, r, username, arguments["password"], plainContentType) {
		return
	}

	// 3. Mapping
	req, err := mapSubmitRequest(arguments)
	if err != nil {
		writePlainError(w, http.StatusBadRequest, err.Error())
		return
	}

	// 4. Submission
	if h.dependencies.Submitter == nil {
		writePlainError(w, http.StatusInternalServerError,
			"Cannot send submit_sm, check SMPPClientManagerPB log file for details")
		return
	}

	messageID, err := h.dependencies.Submitter.Submit(r.Context(), req)
	if err != nil {
		if errors.Is(err, core.ErrAuthentication) {
			// Extract custom message if wrapped
			msg := err.Error()
			if idx := strings.Index(msg, ": "); idx != -1 {
				msg = msg[idx+2:]
			}
			if strings.Contains(msg, "Authorization failed") {
				writePlainError(w, http.StatusBadRequest, msg)
			} else {
				h.authenticationFailure(w, username, plainContentType)
			}
			return
		}
		if errors.Is(err, core.ErrFilterRejected) {
			msg := err.Error()
			if idx := strings.Index(msg, ": "); idx != -1 {
				msg = msg[idx+2:]
			}
			writePlainError(w, http.StatusBadRequest, msg)
			return
		}
		if errors.Is(err, core.ErrNoLiveConnector) || errors.Is(err, core.ErrQuotaExceeded) {
			writePlainError(w, http.StatusInternalServerError, "Cannot send submit_sm, check SMPPClientManagerPB log file for details")
			return
		}
		writePlainError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeResponse(w, http.StatusOK, plainContentType, fmt.Sprintf(`Success %q`, messageID))
}

func validateSendArgs(args map[string]string) error {
	mandatory := []struct {
		name    string
		pattern *regexp.Regexp
	}{
		{"to", reTo},
		{"username", reUsername},
		{"password", rePassword},
	}
	for _, m := range mandatory {
		val, ok := args[m.name]
		if !ok || val == "" {
			return fmt.Errorf("Mandatory argument [%s] is not found.", m.name)
		}
		if !m.pattern.MatchString(val) {
			return fmt.Errorf("Argument [%s] has an invalid value: [%s].", m.name, val)
		}
	}

	optional := []struct {
		name    string
		pattern *regexp.Regexp
	}{
		{"coding", reCoding},
		{"priority", rePriority},
		{"sdt", reSDT},
		{"validity-period", reValidityPeriod},
		{"dlr", reDLR},
		{"dlr-url", reDLRUrl},
		{"dlr-level", reDLRLevel},
		{"dlr-method", reDLRMethod},
		{"tags", reTags},
	}
	for _, o := range optional {
		if val, ok := args[o.name]; ok && val != "" {
			if !o.pattern.MatchString(val) {
				return fmt.Errorf("Argument [%s] has an invalid value: [%s].", o.name, val)
			}
		}
	}

	_, hasContent := args["content"]
	_, hasHexContent := args["hex-content"]
	if !hasContent && !hasHexContent {
		return errors.New("content or hex-content not present.")
	}
	if hasContent && hasHexContent {
		return errors.New("content and hex-content cannot be used both in same request.")
	}

	return nil
}

func mapSubmitRequest(args map[string]string) (core.SubmitRequest, error) {
	req := core.SubmitRequest{
		Username:    args["username"],
		Password:    args["password"],
		Destination: args["to"],
		Content:     args["content"],
		HexContent:  args["hex-content"],
		From:        args["from"],
		DLRUrl:      args["dlr-url"],
		CustomTLVs:  make(map[uint16][]byte),
	}

	if val := args["coding"]; val != "" {
		req.Coding, _ = strconv.Atoi(val)
	} else {
		req.Coding = 0
	}

	if val := args["priority"]; val != "" {
		req.Priority, _ = strconv.Atoi(val)
	}

	if val := args["dlr"]; val == "yes" || args["dlr-url"] != "" || args["dlr-level"] != "" {
		req.DLR = true
		if lv := args["dlr-level"]; lv != "" {
			req.DLRLevel, _ = strconv.Atoi(lv)
		} else {
			req.DLRLevel = 1
		}
		if mt := args["dlr-method"]; mt != "" {
			req.DLRMethod = strings.ToUpper(mt)
		} else {
			req.DLRMethod = "POST"
		}
	} else {
		req.DLRMethod = "POST"
	}

	if val := args["tags"]; val != "" {
		req.Tags = strings.Split(val, ",")
	}

	if val := args["validity-period"]; val != "" {
		minutes, _ := strconv.Atoi(val)
		d := time.Duration(minutes) * time.Minute
		req.ValidityPeriod = &d
	}

	if val := args["sdt"]; val != "" {
		t, err := parseLegacyTime(val)
		if err != nil {
			return req, fmt.Errorf("Argument [sdt] has an invalid value: [%s].", val)
		}
		req.SDT = &t
	}

	for k, v := range args {
		if strings.HasPrefix(k, "tlv-") {
			tag, err := strconv.ParseUint(k[4:], 10, 16)
			if err == nil {
				req.CustomTLVs[uint16(tag)] = []byte(v)
			}
		}
	}

	return req, nil
}

func parseLegacyTime(val string) (time.Time, error) {
	if len(val) < 15 {
		return time.Time{}, errors.New("too short")
	}
	return time.Now(), nil
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
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType == "application/json" {
		var payload map[string]any
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&payload); err != nil {
			return nil, fmt.Errorf("Invalid JSON request")
		}
		arguments := make(map[string]string, len(payload))
		for key, value := range payload {
			switch v := value.(type) {
			case string:
				arguments[key] = v
			case float64:
				arguments[key] = strconv.FormatFloat(v, 'f', -1, 64)
			case bool:
				if v {
					arguments[key] = "yes"
				} else {
					arguments[key] = "no"
				}
			default:
				return nil, fmt.Errorf("Invalid JSON value for argument [%s]", key)
			}
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
	return fmt.Errorf("Mandatory argument [%s] is not found.", argument).Error()
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

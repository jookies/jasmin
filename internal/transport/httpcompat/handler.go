package httpcompat

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/mtcredential"
	"github.com/pumpitspace/jasmin/internal/core/segmentation"
	"github.com/pumpitspace/jasmin/internal/core/stats"
	"github.com/pumpitspace/jasmin/internal/core/tlv"
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
	// Credentials gates /send on the user's MtMessagingCredential. nil allows
	// everything, preserving the behaviour from before the gate existed.
	Credentials   CredentialResolver
	BalanceReader core.BalanceReader
	RateReader    core.RateReader
	Submitter     core.Submitter

	// Observability (all optional; nil = no metrics). HTTPStats holds the
	// httpapi counters this handler increments; the SMPPc/SMPPs registries and
	// ConnectorIDs let /metrics render the full legacy surface when the gateway
	// supplies them.
	HTTPStats    *stats.HTTPStats
	SMPPcStats   *stats.SMPPcRegistry
	SMPPsStats   *stats.SMPPsStats
	ConnectorIDs func() []string
	// Logger is the named jasmin-http-api component logger. AccessLogger is
	// deliberately separate because legacy deployments ship http-api.log and
	// http-accesslog.log independently.
	Logger       *slog.Logger
	AccessLogger *slog.Logger
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
	mux.HandleFunc("/metrics", h.metrics)
	return h.withLogging(mux)
}

type responseMetrics struct {
	http.ResponseWriter
	status int
	bytes  int
}

// Unwrap lets http.ResponseController retain optional capabilities (flush,
// hijack, deadlines) of the server's original writer.
func (writer *responseMetrics) Unwrap() http.ResponseWriter { return writer.ResponseWriter }

func (writer *responseMetrics) WriteHeader(status int) {
	if writer.status != 0 {
		return
	}
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *responseMetrics) Write(body []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	written, err := writer.ResponseWriter.Write(body)
	writer.bytes += written
	return written, err
}

func (h *handler) withLogging(next http.Handler) http.Handler {
	if h.dependencies.Logger == nil && h.dependencies.AccessLogger == nil {
		return next
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		measured := &responseMetrics{ResponseWriter: writer}
		next.ServeHTTP(measured, request)
		if measured.status == 0 {
			measured.status = http.StatusOK
		}
		remote := request.RemoteAddr
		if host, _, err := net.SplitHostPort(remote); err == nil {
			remote = host
		}
		message := fmt.Sprintf("HTTP request [method:%s] [path:%s] [status:%d] [bytes:%d] [remote:%s] [duration:%s]",
			request.Method, request.URL.Path, measured.status, measured.bytes, remote, time.Since(started).Round(time.Microsecond))
		if logger := h.dependencies.AccessLogger; logger != nil {
			logger.Info(message)
		}
		if logger := h.dependencies.Logger; logger != nil {
			switch {
			case measured.status >= 500:
				logger.Error(message)
			case measured.status >= 400:
				logger.Warn(message)
			default:
				logger.Debug(message)
			}
		}
	})
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
	if message, refused := h.checkActionCredential(username, mtcredential.ValidateRate); refused {
		h.incHTTP("auth_error_count")
		writeJSONError(w, http.StatusBadRequest, message)
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
	if message, refused := h.checkActionCredential(username, mtcredential.ValidateBalance); refused {
		h.incHTTP("auth_error_count")
		writeJSONError(w, http.StatusBadRequest, message)
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

// metrics renders the /metrics Prometheus surface. GET only, text/plain, always
// 200 — matching the legacy Metrics resource.
func (h *handler) metrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	var connectorIDs []string
	if h.dependencies.ConnectorIDs != nil {
		connectorIDs = h.dependencies.ConnectorIDs()
	}
	body := stats.Render(h.dependencies.HTTPStats, h.dependencies.SMPPcStats, connectorIDs, h.dependencies.SMPPsStats)
	w.Header().Set("Content-Type", plainContentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (h *handler) incHTTP(name string) {
	if h.dependencies.HTTPStats != nil {
		h.dependencies.HTTPStats.Inc(name)
	}
}

func (h *handler) send(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	h.incHTTP("request_count")
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

	// 3b. custom_tlvs front door. Runs after authentication, like the legacy
	// route_routable; a normalization error there escapes as a generic
	// exception, so parity is 500 with the "Unknown error" envelope.
	if raw := arguments["custom_tlvs"]; raw != "" {
		req.CustomTLVs, err = tlv.Normalize(raw)
		if err != nil {
			writePlainError(w, http.StatusInternalServerError, fmt.Sprintf("Unknown error: %v", err))
			return
		}
	}

	// 3c. Credential gate. Legacy runs HttpAPICredentialValidator after the
	// submit_sm is built (it needs to know whether the message segments), and
	// before routing -- so an unauthorized request is refused without ever
	// consuming a route or a charge.
	if message, refused := h.checkSendCredentials(username, arguments, &req); refused {
		h.incHTTP("auth_error_count")
		writePlainError(w, http.StatusBadRequest, message)
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
		// ThroughputExceededError is a 403 carrying its own message, and it
		// increments a distinct counter — legacy raises it before the route
		// error path, so it must be matched first (send.py:303-308).
		if errors.Is(err, core.ErrThroughputExceeded) {
			h.incHTTP("throughput_error_count")
			writePlainError(w, http.StatusForbidden, "User throughput exceeded")
			return
		}
		if errors.Is(err, core.ErrNoLiveConnector) || errors.Is(err, core.ErrNoRouteMatched) || errors.Is(err, core.ErrQuotaExceeded) {
			h.incHTTP("route_error_count")
			writePlainError(w, http.StatusInternalServerError, "Cannot send submit_sm, check SMPPClientManagerPB log file for details")
			return
		}
		h.incHTTP("server_error_count")
		writePlainError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.incHTTP("success_count")
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
	h.incHTTP("auth_error_count")
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
		var payload map[string]json.RawMessage
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&payload); err != nil {
			return nil, fmt.Errorf("Invalid JSON request")
		}
		arguments := make(map[string]string, len(payload))
		for key, raw := range payload {
			// custom_tlvs keeps its raw JSON fragment: the legacy endpoint hands
			// the decoded object to normalize_custom_tlvs, and only the fragment
			// preserves document order (wire order) across Go's unordered maps.
			if key == "custom_tlvs" {
				arguments[key] = string(raw)
				continue
			}
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, fmt.Errorf("Invalid JSON request")
			}
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

// checkSendCredentials applies the user's MtMessagingCredential to a mapped
// request, and substitutes their default source address when they supplied
// none. It reports the legacy rejection text and whether to refuse.
//
// With no resolver wired it allows everything, which is what the front door did
// before this existed: a gateway whose directory cannot supply credentials must
// not start refusing every message the moment it is upgraded.
func (h *handler) checkSendCredentials(username string, arguments map[string]string, req *core.SubmitRequest) (string, bool) {
	if h.dependencies.Credentials == nil {
		return "", false
	}
	credential, known := h.dependencies.Credentials.ResolveCredential(username)
	if !known || credential == nil {
		return "", false
	}

	present := make(map[string]bool, len(arguments))
	for key := range arguments {
		present[key] = true
	}
	limit := segmentation.Classify(uint8(req.Coding)).SingleLimit
	projection := sendRequestProjection(arguments, present, len([]byte(req.Content)) > limit)

	if err := mtcredential.ValidateSend(credential, projection); err != nil {
		if message, ok := credentialRejection(username, err); ok {
			return message, true
		}
		return err.Error(), true
	}

	// The default source address applies only when the request carried none;
	// an explicit (even empty) `from` is the user's choice and stands.
	if !present["from"] {
		if source, ok := credential.DefaultSourceAddress(); ok {
			req.From = string(source)
		}
	}
	return "", false
}

// checkActionCredential applies the Balance/Rate authorization half of
// HttpAPICredentialValidator after password authentication and before the
// backend is consulted. A nil/unknown resolver retains the handler's existing
// optional-dependency behavior; the production directory always resolves it.
func (h *handler) checkActionCredential(
	username string,
	validate func(*mtcredential.Credential) error,
) (string, bool) {
	if h.dependencies.Credentials == nil {
		return "", false
	}
	credential, known := h.dependencies.Credentials.ResolveCredential(username)
	if !known || credential == nil {
		return "", false
	}
	if err := validate(credential); err != nil {
		if message, ok := credentialRejection(username, err); ok {
			return message, true
		}
		return err.Error(), true
	}
	return "", false
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

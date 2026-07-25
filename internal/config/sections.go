package config

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// AMQP is the parsed 'amqp-broker' section (jasmin/queues/configs.py AmqpConfig).
type AMQP struct {
	Host      string
	Username  string
	Password  string
	Vhost     string
	Port      int
	Heartbeat int
	Spec      string

	ReconnectOnLoss       bool
	ReconnectOnFailure    bool
	ReconnectLossDelay    int
	ReconnectFailureDelay int
}

// cloudAMQPPattern mirrors the legacy CLOUDAMQP_URL regex without lookaround;
// the leading/trailing-dash rule on the host is checked separately.
var cloudAMQPPattern = regexp.MustCompile(`^amqps://([a-z]+):([A-Za-z0-9_-]+)@([-a-zA-Z0-9.]{1,63})/([a-z]+)$`)

// LoadAMQP parses the amqp-broker section. CLOUDAMQP_URL, when set, supplies
// host/username/password/vhost via the legacy regex (Heroku), overriding the
// section values for those four fields.
func LoadAMQP(file *File) (AMQP, error) {
	amqp := AMQP{
		Host:     file.Get("amqp-broker", "host", "127.0.0.1"),
		Username: file.Get("amqp-broker", "username", "guest"),
		Password: file.Get("amqp-broker", "password", "guest"),
		Vhost:    file.Get("amqp-broker", "vhost", "/"),
		Spec:     file.Get("amqp-broker", "spec", ""),
	}
	if cloud, ok := file.Lookup("CLOUDAMQP_URL"); ok && cloud != "" {
		groups := cloudAMQPPattern.FindStringSubmatch(cloud)
		if groups == nil || !validHostLabel(groups[3]) {
			return AMQP{}, fmt.Errorf("config: CLOUDAMQP_URL %q is malformed", cloud)
		}
		amqp.Username, amqp.Password, amqp.Host, amqp.Vhost = groups[1], groups[2], groups[3], groups[4]
	}

	var err error
	if amqp.Port, err = file.GetInt("amqp-broker", "port", 5672); err != nil {
		return AMQP{}, err
	}
	if amqp.Heartbeat, err = file.GetInt("amqp-broker", "heartbeat", 0); err != nil {
		return AMQP{}, err
	}
	if amqp.ReconnectOnLoss, err = file.GetBool("amqp-broker", "connection_loss_retry", true); err != nil {
		return AMQP{}, err
	}
	if amqp.ReconnectOnFailure, err = file.GetBool("amqp-broker", "connection_failure_retry", true); err != nil {
		return AMQP{}, err
	}
	if amqp.ReconnectLossDelay, err = file.GetInt("amqp-broker", "connection_loss_retry_delay", 10); err != nil {
		return AMQP{}, err
	}
	if amqp.ReconnectFailureDelay, err = file.GetInt("amqp-broker", "connection_failure_retry_delay", 10); err != nil {
		return AMQP{}, err
	}
	return amqp, nil
}

// URL renders the amqp:// connection string the Go broker client dials. The
// default vhost "/" renders as a trailing slash; any other vhost is
// path-escaped.
func (a AMQP) URL() string {
	userinfo := url.UserPassword(a.Username, a.Password).String()
	base := fmt.Sprintf("amqp://%s@%s:%d/", userinfo, a.Host, a.Port)
	if a.Vhost == "" || a.Vhost == "/" {
		return base
	}
	return base + url.PathEscape(strings.TrimPrefix(a.Vhost, "/"))
}

// Redis is the parsed 'redis-client' section (jasmin/redis/configs.py).
type Redis struct {
	Host     string
	Port     int
	Password string
	DBID     int
	PoolSize int
}

var redisURLPattern = regexp.MustCompile(`^redis://:([a-z0-9]+)@([-a-zA-Z0-9.]{1,63}):(\d+)$`)

// LoadRedis parses the redis-client section. REDIS_URL, when set, supplies
// password/host/port via the legacy regex (Heroku), overriding the section.
func LoadRedis(file *File) (Redis, error) {
	redis := Redis{
		Host:     file.Get("redis-client", "host", "127.0.0.1"),
		Password: file.Get("redis-client", "password", ""),
	}
	var err error
	if redis.Port, err = file.GetInt("redis-client", "port", 6379); err != nil {
		return Redis{}, err
	}
	if url, ok := file.Lookup("REDIS_URL"); ok && url != "" {
		groups := redisURLPattern.FindStringSubmatch(url)
		if groups == nil || !validHostLabel(groups[2]) {
			return Redis{}, fmt.Errorf("config: REDIS_URL %q is malformed", url)
		}
		port, convErr := strconv.Atoi(groups[3])
		if convErr != nil {
			return Redis{}, fmt.Errorf("config: REDIS_URL %q has an invalid port", url)
		}
		redis.Password, redis.Host, redis.Port = groups[1], groups[2], port
	}
	// The legacy default is the string "0" passed to _getint (still yields 0).
	if redis.DBID, err = file.GetInt("redis-client", "dbid", 0); err != nil {
		return Redis{}, err
	}
	if redis.PoolSize, err = file.GetInt("redis-client", "poolsize", 10); err != nil {
		return Redis{}, err
	}
	return redis, nil
}

// URL renders the redis:// connection string, with the password included only
// when set.
func (r Redis) URL() string {
	if r.Password != "" {
		return fmt.Sprintf("redis://:%s@%s:%d/%d", url.QueryEscape(r.Password), r.Host, r.Port, r.DBID)
	}
	return fmt.Sprintf("redis://%s:%d/%d", r.Host, r.Port, r.DBID)
}

// validHostLabel enforces the legacy host rule: 1-63 chars, not starting or
// ending with a dash (the lookaround in the original regex).
func validHostLabel(host string) bool {
	if host == "" || len(host) > 63 {
		return false
	}
	return host[0] != '-' && host[len(host)-1] != '-'
}

// SMPPServer is the parsed 'smpp-server' section (SMPPServerConfig): the SMPPS
// listener bind address and its session timers.
type SMPPServer struct {
	ID             string
	Bind           string
	Port           int
	BillingFeature bool
	LogLevel       string
	LogPrivacy     bool

	SessionInitTimerSecs int
	EnquireLinkTimerSecs int
	InactivityTimerSecs  int
	ResponseTimerSecs    int
	PDUReadTimerSecs     int
}

// LoadSMPPServer parses the smpp-server section.
func LoadSMPPServer(file *File) (SMPPServer, error) {
	server := SMPPServer{
		ID:       file.Get("smpp-server", "id", "smpps_01"),
		Bind:     file.Get("smpp-server", "bind", "0.0.0.0"),
		LogLevel: file.Get("smpp-server", "log_level", "INFO"),
	}
	var err error
	if server.Port, err = file.GetInt("smpp-server", "port", 2775); err != nil {
		return SMPPServer{}, err
	}
	if server.BillingFeature, err = file.GetBool("smpp-server", "billing_feature", true); err != nil {
		return SMPPServer{}, err
	}
	if server.LogPrivacy, err = file.GetBool("smpp-server", "log_privacy", false); err != nil {
		return SMPPServer{}, err
	}
	for _, field := range []struct {
		name   string
		def    int
		target *int
	}{
		{"sessionInitTimerSecs", 30, &server.SessionInitTimerSecs},
		{"enquireLinkTimerSecs", 30, &server.EnquireLinkTimerSecs},
		{"inactivityTimerSecs", 300, &server.InactivityTimerSecs},
		{"responseTimerSecs", 60, &server.ResponseTimerSecs},
		{"pduReadTimerSecs", 10, &server.PDUReadTimerSecs},
	} {
		if *field.target, err = file.GetInt("smpp-server", field.name, field.def); err != nil {
			return SMPPServer{}, err
		}
	}
	return server, nil
}

// BindAddr renders the host:port the SMPPS listener binds.
func (s SMPPServer) BindAddr() string {
	return fmt.Sprintf("%s:%d", s.Bind, s.Port)
}

// HTTPAPI is the parsed 'http-api' section (HTTPApiConfig): the HTTP listener
// bind address and content-handling settings.
type HTTPAPI struct {
	Bind                string
	Port                int
	BillingFeature      bool
	LogPrivacy          bool
	LongContentMaxParts string
	LongContentSplit    string
}

// LoadHTTPAPI parses the http-api section. The bind/port DEFAULTS themselves
// read the API_BIND / API_PORT environment variables (distinct from the
// HTTP_API_* section override), matching the legacy defaults.
func LoadHTTPAPI(file *File) (HTTPAPI, error) {
	bindDefault := "0.0.0.0"
	if value, ok := file.Lookup("API_BIND"); ok && value != "" {
		bindDefault = value
	}
	portDefault := 1401
	if value, ok := file.Lookup("API_PORT"); ok && value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return HTTPAPI{}, fmt.Errorf("config: API_PORT %q is not an integer", value)
		}
		portDefault = parsed
	}

	api := HTTPAPI{
		Bind:                file.Get("http-api", "bind", bindDefault),
		LongContentMaxParts: file.Get("http-api", "long_content_max_parts", "5"),
		LongContentSplit:    file.Get("http-api", "long_content_split", "udh"),
	}
	var err error
	if api.Port, err = file.GetInt("http-api", "port", portDefault); err != nil {
		return HTTPAPI{}, err
	}
	if api.BillingFeature, err = file.GetBool("http-api", "billing_feature", true); err != nil {
		return HTTPAPI{}, err
	}
	if api.LogPrivacy, err = file.GetBool("http-api", "log_privacy", false); err != nil {
		return HTTPAPI{}, err
	}
	return api, nil
}

// BindAddr renders the host:port the HTTP API binds.
func (h HTTPAPI) BindAddr() string {
	return fmt.Sprintf("%s:%d", h.Bind, h.Port)
}

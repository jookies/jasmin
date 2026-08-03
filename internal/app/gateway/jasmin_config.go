package gateway

import (
	"strconv"

	"github.com/pumpitspace/synevyr/internal/config"
)

// ApplyJasmin overlays the infrastructure settings from a parsed jasmin.cfg onto
// a gateway Config: the AMQP broker URL and HTTP listen address, plus the
// timeout/retry policy of whichever optional workers are already enabled in the
// JSON config. It deliberately never touches connectors, routes, users, role, or
// the Postgres DSN — those are supplied by the JSON config, because Jasmin keeps
// connectors/routes/users in Postgres and PB-managed state, not in the .cfg file.
//
// Precedence: when the operator opts in by supplying a jasmin.cfg, its values
// win for the infra fields it owns (amqp_url, listen_address, and the settings
// of enabled workers). A JSON-only deployment never calls this and is unaffected.
//
// Sub-worker AMQP URLs are intentionally left as-is: an empty one already
// inherits Config.Outbound.AMQPURL during validation, so setting the outbound
// URL here is enough to point every in-process worker at the .cfg broker.
func ApplyJasmin(cfg *Config, jasmin *config.Jasmin) {
	cfg.Outbound.AMQPURL = jasmin.AMQP.URL()
	cfg.Outbound.ListenAddress = jasmin.HTTPAPI.BindAddr()
	// long_content_split / long_content_max_parts reach the submit path, which
	// previously hardcoded SAR and 10 parts regardless of what the operator set.
	cfg.Outbound.LongContentSplit = jasmin.HTTPAPI.LongContentSplit
	if parts, convErr := strconv.Atoi(jasmin.HTTPAPI.LongContentMaxParts); convErr == nil {
		cfg.Outbound.LongContentMaxParts = parts
	}
	restThroughput := float64(jasmin.RESTAPI.HTTPThroughputPerWorker)
	restSmartQoS := jasmin.RESTAPI.SmartQoS
	cfg.REST.HTTPThroughputPerWorker = &restThroughput
	cfg.REST.SmartQoS = &restSmartQoS

	if cfg.DLRThrower != nil {
		cfg.DLRThrower.HTTPTimeoutSeconds = float64(jasmin.DLRThrower.TimeoutSecs)
		cfg.DLRThrower.RetryDelaySeconds = float64(jasmin.DLRThrower.RetryDelaySecs)
		cfg.DLRThrower.MaxRetries = jasmin.DLRThrower.MaxRetries
	}
	if cfg.MOThrower != nil {
		cfg.MOThrower.HTTPTimeoutSeconds = float64(jasmin.DeliverSMThrower.TimeoutSecs)
		cfg.MOThrower.RetryDelaySeconds = float64(jasmin.DeliverSMThrower.RetryDelaySecs)
		cfg.MOThrower.MaxRetries = jasmin.DeliverSMThrower.MaxRetries
	}
	if cfg.DLRLookup != nil {
		cfg.DLRLookup.RedisURL = jasmin.Redis.URL()
		cfg.DLRLookup.PID = jasmin.DLR.PID
		cfg.DLRLookup.MaxRetries = jasmin.DLR.LookupMaxRetries
		cfg.DLRLookup.RetryDelaySeconds = float64(jasmin.DLR.LookupRetryDelay)
		cfg.DLRLookup.SMPPReceiptOnSuccessSubmitSmResp = jasmin.DLR.SMPPReceiptOnSuccessSubmitSMResp
	}
	if cfg.SMPPS != nil {
		cfg.SMPPS.BindAddr = jasmin.SMPPServer.BindAddr()
		cfg.SMPPS.EnquireLinkTimeoutSeconds = float64(jasmin.SMPPServer.EnquireLinkTimerSecs)
		// The legacy INI always carries a value, so it is always explicit here.
		inactivity := float64(jasmin.SMPPServer.InactivityTimerSecs)
		cfg.SMPPS.InactivityTimeoutSeconds = &inactivity
	}
	// The SMPPs server bind/unbind lines use the smpp.server.<id> logger.
	cfg.SMPPServerLog.Level = jasmin.SMPPServer.Log.Level
	cfg.SMPPServerLog.File = jasmin.SMPPServer.Log.File
	cfg.SMPPServerLog.Rotate = jasmin.SMPPServer.Log.Rotate
	// The SMS-MT audit line is the sm-listener's (jasmin-sm-listener logger),
	// including its log_file (messages.log) and log_rotate.
	cfg.SubmitAuditLog.Level = jasmin.SMListener.Log.Level
	cfg.SubmitAuditLog.Privacy = jasmin.SMListener.LogPrivacy
	cfg.SubmitAuditLog.File = jasmin.SMListener.Log.File
	cfg.SubmitAuditLog.Rotate = jasmin.SMListener.Log.Rotate
	cfg.RouterLog = componentLog(jasmin.Router.Log)
	cfg.HTTPAPILog = componentLog(jasmin.HTTPAPI.Log)
	cfg.HTTPAccessLog = ComponentLogConfig{
		Level: jasmin.HTTPAPI.Log.Level, File: jasmin.HTTPAPI.AccessLog, Rotate: jasmin.HTTPAPI.Log.Rotate,
	}
	cfg.DLRLog = componentLog(jasmin.DLR.Log)
	cfg.AMQPLog = componentLog(jasmin.AMQP.Log)
	cfg.DLRThrowerLog = componentLog(jasmin.DLRThrower.Log)
	cfg.DeliverSMThrowerLog = componentLog(jasmin.DeliverSMThrower.Log)
}

func componentLog(value config.LogConfig) ComponentLogConfig {
	return ComponentLogConfig{Level: value.Level, File: value.File, Rotate: value.Rotate}
}

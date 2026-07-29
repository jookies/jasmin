package restcompat

import (
	"fmt"
	"math"
	"time"
)

const (
	defaultBatchThroughput     = 8
	defaultMaxPendingTasks     = 10000
	defaultBatchMaxAttempts    = 3
	defaultBatchRetryDelay     = time.Second
	defaultCallbackMaxAttempts = 5
	defaultCallbackRetryDelay  = time.Second
)

// Config is the REST-specific gateway JSON contract. ListenAddress enables the
// standalone REST daemon view; /secure/* remains mounted on the combined public
// handler for compatibility. Pointer QoS fields distinguish an omitted legacy
// default from explicitly configured zero/false.
type Config struct {
	ListenAddress             string   `json:"listen_address,omitempty"`
	HTTPThroughputPerWorker   *float64 `json:"http_throughput_per_worker,omitempty"`
	SmartQoS                  *bool    `json:"smart_qos,omitempty"`
	MaxPendingTasks           int      `json:"max_pending_tasks,omitempty"`
	MaxAttempts               int      `json:"max_attempts,omitempty"`
	RetryDelaySeconds         float64  `json:"retry_delay_seconds,omitempty"`
	CallbackMaxAttempts       int      `json:"callback_max_attempts,omitempty"`
	CallbackRetryDelaySeconds float64  `json:"callback_retry_delay_seconds,omitempty"`
}

func (config Config) Validate() error {
	throughput := config.throughput()
	if math.IsNaN(throughput) || math.IsInf(throughput, 0) || throughput < 0 {
		return fmt.Errorf("rest_api.http_throughput_per_worker must be finite and non-negative")
	}
	for name, value := range map[string]float64{
		"retry_delay_seconds":          config.RetryDelaySeconds,
		"callback_retry_delay_seconds": config.CallbackRetryDelaySeconds,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 ||
			value*float64(time.Second) >= float64(math.MaxInt64) {
			return fmt.Errorf(
				"rest_api.%s must be finite, non-negative and representable", name)
		}
	}
	for name, value := range map[string]int{
		"max_pending_tasks":     config.maxPending(),
		"max_attempts":          config.maxAttempts(),
		"callback_max_attempts": config.callbackMaxAttempts(),
	} {
		if value <= 0 {
			return fmt.Errorf("rest_api.%s must be positive", name)
		}
	}
	return nil
}

func (config Config) Options(store BatchStore) []Option {
	return []Option{
		WithBatchStore(store),
		WithBatchQoS(config.throughput(), config.smartQoS()),
		WithBatchLimits(
			config.maxPending(), config.maxAttempts(), config.retryDelay(),
			config.callbackMaxAttempts(), config.callbackRetryDelay(),
		),
	}
}

func (config Config) throughput() float64 {
	if config.HTTPThroughputPerWorker == nil {
		return defaultBatchThroughput
	}
	return *config.HTTPThroughputPerWorker
}

func (config Config) smartQoS() bool {
	if config.SmartQoS == nil {
		return true
	}
	return *config.SmartQoS
}

func (config Config) maxPending() int {
	if config.MaxPendingTasks == 0 {
		return defaultMaxPendingTasks
	}
	return config.MaxPendingTasks
}

func (config Config) maxAttempts() int {
	if config.MaxAttempts == 0 {
		return defaultBatchMaxAttempts
	}
	return config.MaxAttempts
}

func (config Config) retryDelay() time.Duration {
	if config.RetryDelaySeconds == 0 {
		return defaultBatchRetryDelay
	}
	return time.Duration(config.RetryDelaySeconds * float64(time.Second))
}

func (config Config) callbackMaxAttempts() int {
	if config.CallbackMaxAttempts == 0 {
		return defaultCallbackMaxAttempts
	}
	return config.CallbackMaxAttempts
}

func (config Config) callbackRetryDelay() time.Duration {
	if config.CallbackRetryDelaySeconds == 0 {
		return defaultCallbackRetryDelay
	}
	return time.Duration(config.CallbackRetryDelaySeconds * float64(time.Second))
}

// Resolved values are exposed for gateway config tests and jasmin.cfg overlay
// without duplicating the legacy defaults in another package.
func (config Config) EffectiveHTTPThroughputPerWorker() float64 { return config.throughput() }
func (config Config) EffectiveSmartQoS() bool                   { return config.smartQoS() }

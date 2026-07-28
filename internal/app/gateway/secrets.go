package gateway

import (
	"fmt"
	"os"
	"strings"
)

// Secret-bearing config fields accept indirection so committed configs never
// carry live credentials:
//
//	"env:NAME"     resolves to the NAME environment variable (must be non-empty)
//	"file:/path"   resolves to the file's contents, trailing newline trimmed
//	"literal:..."  passes the remainder through verbatim (escape hatch for
//	               values that genuinely start with "env:" or "file:")
//
// Any other value is used as-is. Resolution happens at LoadConfig, before
// validation, so --check-config fails closed on a missing secret.
func resolveSecretValue(field, value string) (string, error) {
	switch {
	case strings.HasPrefix(value, "env:"):
		name := strings.TrimPrefix(value, "env:")
		if name == "" {
			return "", fmt.Errorf("%w: %s: empty env reference", ErrInvalidConfig, field)
		}
		resolved, ok := os.LookupEnv(name)
		if !ok || resolved == "" {
			return "", fmt.Errorf("%w: %s: environment variable %q is unset or empty", ErrInvalidConfig, field, name)
		}
		return resolved, nil
	case strings.HasPrefix(value, "file:"):
		path := strings.TrimPrefix(value, "file:")
		if path == "" {
			return "", fmt.Errorf("%w: %s: empty file reference", ErrInvalidConfig, field)
		}
		data, err := os.ReadFile(path) // #nosec G304 -- operator-supplied secret path
		if err != nil {
			return "", fmt.Errorf("%w: %s: read secret file: %v", ErrInvalidConfig, field, err)
		}
		resolved := strings.TrimRight(string(data), "\r\n")
		if resolved == "" {
			return "", fmt.Errorf("%w: %s: secret file %q is empty", ErrInvalidConfig, field, path)
		}
		return resolved, nil
	case strings.HasPrefix(value, "literal:"):
		return strings.TrimPrefix(value, "literal:"), nil
	default:
		return value, nil
	}
}

// resolveSecretRefs rewrites every secret-bearing field in place. It touches
// exactly the credentials an operator must not commit: broker/store DSNs,
// SMSC passwords, and SMPPS account passwords.
func resolveSecretRefs(config *Config) error {
	resolve := func(field string, target *string) error {
		resolved, err := resolveSecretValue(field, *target)
		if err != nil {
			return err
		}
		*target = resolved
		return nil
	}
	if err := resolve("outbound.postgres_dsn", &config.Outbound.PostgresDSN); err != nil {
		return err
	}
	if err := resolve("outbound.amqp_url", &config.Outbound.AMQPURL); err != nil {
		return err
	}
	for index := range config.Connectors {
		field := fmt.Sprintf("connectors[%d].password", index)
		if err := resolve(field, &config.Connectors[index].Password); err != nil {
			return err
		}
	}
	if config.DLRLookup != nil {
		if err := resolve("dlr_lookup.amqp_url", &config.DLRLookup.AMQPURL); err != nil {
			return err
		}
		if err := resolve("dlr_lookup.redis_url", &config.DLRLookup.RedisURL); err != nil {
			return err
		}
	}
	if config.DLRThrower != nil {
		if err := resolve("dlr_thrower.amqp_url", &config.DLRThrower.AMQPURL); err != nil {
			return err
		}
	}
	if config.MOThrower != nil {
		if err := resolve("deliver_sm_thrower.amqp_url", &config.MOThrower.AMQPURL); err != nil {
			return err
		}
	}
	if config.SMPPS != nil {
		for index := range config.SMPPS.Users {
			field := fmt.Sprintf("smpps.users[%d].password", index)
			if err := resolve(field, &config.SMPPS.Users[index].Password); err != nil {
				return err
			}
		}
	}
	if config.Admin != nil {
		if err := resolve("admin.token", &config.Admin.Token); err != nil {
			return err
		}
		if config.Admin.WebPassword != "" {
			if err := resolve("admin.web_password", &config.Admin.WebPassword); err != nil {
				return err
			}
		}
	}
	return nil
}

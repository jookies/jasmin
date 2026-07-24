package mothrower

import (
	"context"
	"errors"
	"testing"
)

func TestValidateConfig(t *testing.T) {
	if err := ValidateConfig(Config{AMQPURL: "amqp://x"}); err != nil {
		t.Fatal(err)
	}
	for name, config := range map[string]Config{
		"empty amqp":       {},
		"negative timeout": {AMQPURL: "amqp://x", HTTPTimeoutSeconds: -1},
		"negative retries": {AMQPURL: "amqp://x", MaxRetries: -1},
	} {
		if err := ValidateConfig(config); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("%s: error = %v", name, err)
		}
	}
}

func TestNewServiceRequiresDecoder(t *testing.T) {
	if _, err := NewService(Config{AMQPURL: "amqp://x"}, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v", err)
	}
	_ = context.Background()
}

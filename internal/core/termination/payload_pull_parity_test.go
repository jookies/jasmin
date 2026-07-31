package termination

import (
	"reflect"
	"strings"
	"testing"

	"github.com/pumpitspace/synevyr/internal/core/msgspool"
)

// The push and pull contracts describe the same message and must keep the same
// field names for it, so an application that already parses a delivered payload
// reuses its parser when it switches to (or adds) polling.
//
// The assertion lives here rather than in msgspool because that package cannot
// import this one: termination depends on the spool, not the other way round.
// So the shapes are declared twice by necessity, and this is what keeps the two
// declarations honest.
func TestPullPayloadSharesTheDeliveryContractFieldNames(t *testing.T) {
	push := jsonFieldTypes(t, reflect.TypeOf(DeliveryPayload{}))
	pull := jsonFieldTypes(t, reflect.TypeOf(msgspool.PullMessage{}))

	// Every field the push contract defines must exist in the pull projection,
	// spelled identically.
	for name, pushType := range push {
		pullType, present := pull[name]
		if !present {
			t.Fatalf("delivery field %q is missing from the pull payload", name)
		}
		// text and raw_hex are pointers on the pull side on purpose: a consumer
		// scoped without include_text must see them absent, which a value type
		// cannot express. Their pointed-to type must still match.
		if pullType.Kind() == reflect.Pointer {
			pullType = pullType.Elem()
		}
		if pullType != pushType {
			t.Fatalf("field %q: delivery type %s, pull type %s", name, pushType, pullType)
		}
	}

	// The pull payload adds the push lifecycle, which a pushed message does not
	// need to be told about. Anything else appearing here is a divergence
	// someone should have to justify.
	allowedExtras := map[string]struct{}{
		"delivery_state": {}, "delivery_attempts": {},
	}
	for name := range pull {
		if _, shared := push[name]; shared {
			continue
		}
		if _, allowed := allowedExtras[name]; !allowed {
			t.Fatalf("pull payload field %q has no counterpart in the delivery contract", name)
		}
	}

	// The two fields whose absence is the redaction signal must be pointers, or
	// a metadata-only read would render them as empty strings and a client
	// could not tell a redacted OTP from a blank SMS.
	pullType := reflect.TypeOf(msgspool.PullMessage{})
	for _, name := range []string{"text", "raw_hex"} {
		field, ok := fieldByJSONName(pullType, name)
		if !ok {
			t.Fatalf("pull payload has no %q field", name)
		}
		if field.Type.Kind() != reflect.Pointer {
			t.Fatalf("pull payload %q is %s; it must be a pointer so redaction omits it",
				name, field.Type)
		}
		if !strings.Contains(field.Tag.Get("json"), "omitempty") {
			t.Fatalf("pull payload %q lacks omitempty; a redacted read would render it as null", name)
		}
	}
}

func jsonFieldTypes(t *testing.T, structType reflect.Type) map[string]reflect.Type {
	t.Helper()
	fields := make(map[string]reflect.Type, structType.NumField())
	for index := 0; index < structType.NumField(); index++ {
		field := structType.Field(index)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		fields[name] = field.Type
	}
	return fields
}

func fieldByJSONName(structType reflect.Type, name string) (reflect.StructField, bool) {
	for index := 0; index < structType.NumField(); index++ {
		field := structType.Field(index)
		tagged, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if tagged == name {
			return field, true
		}
	}
	return reflect.StructField{}, false
}

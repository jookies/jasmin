package tlv

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type goldenDocument struct {
	BaselineCommit string       `json:"baseline_commit"`
	Cases          []goldenCase `json:"cases"`
}

type goldenCase struct {
	ID        string          `json:"id"`
	Operation string          `json:"operation"`
	Input     json.RawMessage `json:"input"`
	Expected  goldenExpected  `json:"expected"`
}

type goldenExpected struct {
	Hex    *string         `json:"hex,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *goldenError    `json:"error,omitempty"`
}

type goldenError struct {
	Class   string `json:"class"`
	Message string `json:"message"`
}

type fixtureValue struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type fixtureTLV struct {
	Tag    string       `json:"tag"`
	Length *int         `json:"length"`
	Type   *string      `json:"type"`
	Value  fixtureValue `json:"value"`
}

type fixtureRule struct {
	Tag      uint16 `json:"tag"`
	Type     string `json:"type"`
	Length   *int   `json:"length"`
	Required bool   `json:"required"`
}

type fixtureInput struct {
	TagKey string        `json:"tag_key"`
	Type   *string       `json:"type"`
	Value  *fixtureValue `json:"value"`
	TLVs   []fixtureTLV  `json:"tlvs"`
	Rules  []fixtureRule `json:"rules"`
}

type fixtureResult struct {
	Tag     string  `json:"tag,omitempty"`
	Type    *string `json:"type,omitempty"`
	OK      *bool   `json:"ok,omitempty"`
	Message *string `json:"message,omitempty"`
}

func TestVendorTLVGoldenDifferential(t *testing.T) {
	path := filepath.Join("..", "..", "..", "compat", "fixtures", "vendor-tlv", "baseline.json")
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document goldenDocument
	if err := json.Unmarshal(blob, &document); err != nil {
		t.Fatal(err)
	}
	if document.BaselineCommit != "0aac58e466d583d0f0436df7b8afa3dc96191263" {
		t.Fatalf("unexpected baseline %q", document.BaselineCommit)
	}
	if len(document.Cases) != 29 {
		t.Fatalf("executed cases=%d want=29", len(document.Cases))
	}

	seen := make(map[string]struct{}, len(document.Cases))
	for _, tc := range document.Cases {
		tc := tc
		t.Run(tc.ID, func(t *testing.T) {
			if _, duplicate := seen[tc.ID]; duplicate {
				t.Fatalf("duplicate case id %q", tc.ID)
			}
			seen[tc.ID] = struct{}{}
			var input fixtureInput
			if err := json.Unmarshal(tc.Input, &input); err != nil {
				t.Fatal(err)
			}
			runGoldenCase(t, tc, input)
		})
	}
}

func runGoldenCase(t *testing.T, tc goldenCase, input fixtureInput) {
	t.Helper()
	switch tc.Operation {
	case "parse_tag":
		tag, typeName, err := ParseTagKey(input.TagKey)
		if tc.Expected.Error != nil {
			if err == nil {
				t.Fatalf("expected %s, got tag=%s type=%q", tc.Expected.Error.Class, tag, typeName)
			}
			assertErrorParity(t, tc.Expected, err)
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		var want fixtureResult
		mustUnmarshal(t, tc.Expected.Result, &want)
		if tag.String() != want.Tag || deref(optionalString(typeName)) != deref(want.Type) {
			t.Fatalf("got tag=%s type=%v want tag=%s type=%v", tag, optionalString(typeName), want.Tag, want.Type)
		}
	case "resolve":
		got, err := ResolveTLVTypes(toTLVs(t, input.TLVs), toRules(input.Rules))
		if tc.Expected.Error != nil {
			if err == nil {
				t.Fatalf("expected %s: %s; resolver returned %+v", tc.Expected.Error.Class, tc.Expected.Error.Message, got)
			}
			assertErrorParity(t, tc.Expected, err)
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		var want []fixtureTLV
		mustUnmarshal(t, tc.Expected.Result, &want)
		gotFixture := fromTLVs(t, got)
		if !reflect.DeepEqual(gotFixture, want) {
			t.Fatalf("got=%+v want=%+v", gotFixture, want)
		}
	case "encode_value":
		got, err := EncodeValue(toValue(t, *input.Value), deref(input.Type))
		assertEncodedOutcome(t, tc.Expected, got, err)
	case "encode_custom":
		got, err := EncodeCustomTLVs(toTLVs(t, input.TLVs))
		assertEncodedOutcome(t, tc.Expected, got, err)
	case "validate":
		err := ValidateCustomTLVs(toTLVs(t, input.TLVs), toRules(input.Rules))
		if tc.Expected.Error != nil {
			if err == nil {
				t.Fatalf("expected error %+v", *tc.Expected.Error)
			}
			return
		}
		var want fixtureResult
		mustUnmarshal(t, tc.Expected.Result, &want)
		gotOK := err == nil
		var gotMessage *string
		if err != nil {
			message := err.Error()
			gotMessage = &message
		}
		if want.OK == nil || gotOK != *want.OK || !reflect.DeepEqual(gotMessage, want.Message) {
			t.Fatalf("got ok=%v message=%v want ok=%v message=%v", gotOK, gotMessage, want.OK, want.Message)
		}
	default:
		t.Fatalf("unknown operation %q", tc.Operation)
	}
}

func assertEncodedOutcome(t *testing.T, expected goldenExpected, got []byte, err error) {
	t.Helper()
	if expected.Error != nil {
		if err == nil {
			t.Fatalf("expected %s: %s, got hex=%s", expected.Error.Class, expected.Error.Message, hex.EncodeToString(got))
		}
		assertErrorParity(t, expected, err)
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if expected.Hex == nil || hex.EncodeToString(got) != *expected.Hex {
		t.Fatalf("got hex=%s want=%v", hex.EncodeToString(got), expected.Hex)
	}
}

func toTLVs(t *testing.T, values []fixtureTLV) []TLV {
	t.Helper()
	result := make([]TLV, 0, len(values))
	for _, value := range values {
		tag, ok := new(big.Int).SetString(value.Tag, 10)
		if !ok {
			t.Fatalf("invalid fixture tag %q", value.Tag)
		}
		result = append(result, TLV{Tag: tag, Length: value.Length, Type: deref(value.Type), Value: toValue(t, value.Value)})
	}
	return result
}

func fromTLVs(t *testing.T, values []TLV) []fixtureTLV {
	t.Helper()
	result := make([]fixtureTLV, 0, len(values))
	for _, item := range values {
		result = append(result, fixtureTLV{Tag: item.Tag.String(), Length: item.Length, Type: optionalString(item.Type), Value: fromValue(t, item.Value)})
	}
	return result
}

func toRules(values []fixtureRule) []ConnectorRule {
	result := make([]ConnectorRule, 0, len(values))
	for _, value := range values {
		result = append(result, ConnectorRule{Tag: value.Tag, Type: value.Type, Length: value.Length, Required: value.Required})
	}
	return result
}

func toValue(t *testing.T, value fixtureValue) any {
	t.Helper()
	switch value.Kind {
	case "string":
		return value.Value
	case "int":
		n, ok := new(big.Int).SetString(value.Value, 10)
		if !ok {
			t.Fatalf("invalid fixture integer %q", value.Value)
		}
		if n.IsUint64() {
			return n.Uint64()
		}
		if n.IsInt64() {
			return n.Int64()
		}
		return n
	case "bytes":
		decoded, err := base64.StdEncoding.DecodeString(value.Value)
		if err != nil {
			t.Fatal(err)
		}
		return decoded
	default:
		t.Fatalf("unknown value kind %q", value.Kind)
		return nil
	}
}

func fromValue(t *testing.T, value any) fixtureValue {
	t.Helper()
	switch typed := value.(type) {
	case string:
		return fixtureValue{Kind: "string", Value: typed}
	case []byte:
		return fixtureValue{Kind: "bytes", Value: base64.StdEncoding.EncodeToString(typed)}
	case uint64:
		return fixtureValue{Kind: "int", Value: strconv.FormatUint(typed, 10)}
	case int:
		return fixtureValue{Kind: "int", Value: strconv.Itoa(typed)}
	case int64:
		return fixtureValue{Kind: "int", Value: strconv.FormatInt(typed, 10)}
	case *big.Int:
		return fixtureValue{Kind: "int", Value: typed.String()}
	default:
		t.Fatalf("unsupported result value %T", value)
		return fixtureValue{}
	}
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	copy := value
	return &copy
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func mustUnmarshal(t *testing.T, raw json.RawMessage, target any) {
	t.Helper()
	if len(raw) == 0 {
		t.Fatal("missing expected result")
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatal(fmt.Errorf("decode expected result: %w", err))
	}
}

func assertErrorParity(t *testing.T, expected goldenExpected, err error) {
	t.Helper()
	if expected.Error == nil {
		t.Fatal("missing expected error")
	}
	gotClass := "error"
	var valueErr *ValueError
	if errors.As(err, &valueErr) {
		gotClass = "ValueError"
	}
	if expected.Error.Class != gotClass {
		t.Fatalf("got error class=%q want=%q actual=%q", gotClass, expected.Error.Class, err)
	}
	if errorSemantic(expected.Error.Message) != errorSemantic(err.Error()) {
		t.Fatalf("got error semantic=%q want=%q actual=%q oracle=%q", errorSemantic(err.Error()), errorSemantic(expected.Error.Message), err, expected.Error.Message)
	}
}

func errorSemantic(message string) string {
	switch {
	case strings.Contains(message, "invalid literal"), strings.Contains(message, "invalid integer"):
		return "invalid-integer"
	case strings.Contains(message, "requires 0 <= number <="), strings.Contains(message, "does not fit"):
		return "unsigned-width-overflow"
	default:
		return message
	}
}

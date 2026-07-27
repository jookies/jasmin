package amqpcompat

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// MaxBodySize bounds the defensive body copy made at the ingress boundary.
	MaxBodySize = 1 << 20
	// MaxHeaders and MaxHeaderBytes bound the defensive property-table copy.
	MaxHeaders     = 128
	MaxHeaderBytes = 64 << 10

	maxShortStringBytes = 255
)

var (
	ErrInvalidRoutingKey = errors.New("invalid AMQP routing key")
	ErrInvalidProperties = errors.New("invalid AMQP properties")
	ErrBodyTooLarge      = errors.New("AMQP envelope body too large")
)

// RouteKind identifies the fixture-proven Jasmin routing-key family.
type RouteKind uint8

const (
	RouteUnknown RouteKind = iota
	RouteSubmitSM
	RouteSubmitSMResponse
	RouteDLRSubmitSMResponse
	RouteDLRHTTP
	RouteDLRSMPPS
	RouteBillingSubmitSMResponse
	RouteDeliverSMHTTP
	RouteDeliverSMSMPPS
	RouteDLRDeliverSM
	// RouteDeliverSM is the connector-ingress MO publication deliver.sm.<cid>
	// the legacy SMPPClientSMListener produces and RouterPB consumes.
	RouteDeliverSM
)

func (kind RouteKind) String() string {
	switch kind {
	case RouteSubmitSM:
		return "submit_sm"
	case RouteSubmitSMResponse:
		return "submit_sm_response"
	case RouteDLRSubmitSMResponse:
		return "dlr_submit_sm_response"
	case RouteDLRHTTP:
		return "dlr_http"
	case RouteDLRSMPPS:
		return "dlr_smpps"
	case RouteBillingSubmitSMResponse:
		return "billing_submit_sm_response"
	case RouteDeliverSMHTTP:
		return "deliver_sm_http"
	case RouteDeliverSMSMPPS:
		return "deliver_sm_smpps"
	case RouteDLRDeliverSM:
		return "dlr_deliver_sm"
	case RouteDeliverSM:
		return "deliver_sm"
	default:
		return "unknown"
	}
}

// Route is a parsed routing key. Target is populated for dynamic connector/user routes.
type Route struct {
	kind   RouteKind
	target string
}

func (route Route) Kind() RouteKind { return route.kind }
func (route Route) Target() string  { return route.target }

// ParseRoutingKey recognizes only routing-key families represented by the compatibility scope.
func ParseRoutingKey(key string) (Route, error) {
	if len(key) == 0 || len(key) > maxShortStringBytes {
		return Route{}, fmt.Errorf("%w: length %d", ErrInvalidRoutingKey, len(key))
	}

	switch key {
	case "dlr.submit_sm_resp":
		return Route{kind: RouteDLRSubmitSMResponse}, nil
	case "dlr.deliver_sm":
		return Route{kind: RouteDLRDeliverSM}, nil
	case "dlr_thrower.http":
		return Route{kind: RouteDLRHTTP}, nil
	case "dlr_thrower.smpps":
		return Route{kind: RouteDLRSMPPS}, nil
	case "deliver_sm_thrower.http":
		return Route{kind: RouteDeliverSMHTTP}, nil
	case "deliver_sm_thrower.smpps":
		return Route{kind: RouteDeliverSMSMPPS}, nil
	}

	// The response prefix must be tested before submit.sm. because it is a strict subset.
	if target, ok := dynamicTarget(key, "submit.sm.resp."); ok {
		return Route{kind: RouteSubmitSMResponse, target: target}, nil
	}
	if target, ok := dynamicTarget(key, "deliver.sm."); ok {
		return Route{kind: RouteDeliverSM, target: target}, nil
	}
	if target, ok := dynamicTarget(key, "submit.sm."); ok {
		return Route{kind: RouteSubmitSM, target: target}, nil
	}
	if target, ok := dynamicTarget(key, "bill_request.submit_sm_resp."); ok {
		return Route{kind: RouteBillingSubmitSMResponse, target: target}, nil
	}

	return Route{}, fmt.Errorf("%w: %q", ErrInvalidRoutingKey, key)
}

func dynamicTarget(key, prefix string) (string, bool) {
	if !strings.HasPrefix(key, prefix) {
		return "", false
	}
	target := strings.TrimPrefix(key, prefix)
	if target == "" || strings.Contains(target, ".") {
		return "", false
	}
	return target, true
}

// FieldKind identifies one AMQP field-table value kind represented by the fixtures.
type FieldKind uint8

const (
	FieldInvalid FieldKind = iota
	FieldString
	FieldInteger
	FieldBytes
	// FieldBool carries an AMQP boolean table value — the legacy
	// DeliverSmContent headers (concatenated, will_be_concatenated) publish
	// Python bools through txamqp.
	FieldBool
)

func (kind FieldKind) String() string {
	switch kind {
	case FieldString:
		return "string"
	case FieldInteger:
		return "integer"
	case FieldBytes:
		return "bytes"
	case FieldBool:
		return "bool"
	default:
		return "invalid"
	}
}

// Field is an immutable fixture-proven AMQP field-table value.
type Field struct {
	kind         FieldKind
	stringValue  string
	integerValue int64
	bytesValue   []byte
	boolValue    bool
}

func StringField(value string) Field {
	return Field{kind: FieldString, stringValue: value}
}

func IntegerField(value int64) Field {
	return Field{kind: FieldInteger, integerValue: value}
}

func BytesField(value []byte) Field {
	return Field{kind: FieldBytes, bytesValue: cloneBytes(value)}
}

func BoolField(value bool) Field {
	return Field{kind: FieldBool, boolValue: value}
}

func (field Field) Kind() FieldKind { return field.kind }

func (field Field) String() (string, bool) {
	if field.kind != FieldString {
		return "", false
	}
	return field.stringValue, true
}

func (field Field) Integer() (int64, bool) {
	if field.kind != FieldInteger {
		return 0, false
	}
	return field.integerValue, true
}

func (field Field) Bytes() ([]byte, bool) {
	if field.kind != FieldBytes {
		return nil, false
	}
	return cloneBytes(field.bytesValue), true
}

func (field Field) Bool() (bool, bool) {
	if field.kind != FieldBool {
		return false, false
	}
	return field.boolValue, true
}

func (field Field) clone() Field {
	copy := field
	copy.bytesValue = cloneBytes(field.bytesValue)
	return copy
}

func (field Field) valid() bool {
	return field.kind == FieldString || field.kind == FieldInteger || field.kind == FieldBytes || field.kind == FieldBool
}

func (field Field) wireSize() uint64 {
	switch field.kind {
	case FieldString:
		return uint64(len(field.stringValue))
	case FieldInteger:
		return 8
	case FieldBytes:
		return uint64(len(field.bytesValue))
	case FieldBool:
		return 1
	default:
		return 0
	}
}

// Properties is an immutable subset of AMQP Basic.Properties represented by the fixtures.
type Properties struct {
	messageID   string
	replyTo     string
	hasReplyTo  bool
	priority    uint8
	hasPriority bool
	headers     map[string]Field
	valid       bool
}

type propertyOptions struct {
	replyTo     string
	hasReplyTo  bool
	priority    uint8
	hasPriority bool
}

// PropertyOption configures optional fixture-proven Basic.Properties fields.
type PropertyOption interface {
	apply(*propertyOptions) error
}

type propertyOptionFunc func(*propertyOptions) error

func (option propertyOptionFunc) apply(options *propertyOptions) error {
	return option(options)
}

func WithReplyTo(replyTo string) PropertyOption {
	return propertyOptionFunc(func(options *propertyOptions) error {
		if options.hasReplyTo {
			return fmt.Errorf("%w: duplicate reply-to option", ErrInvalidProperties)
		}
		if replyTo == "" || len(replyTo) > maxShortStringBytes {
			return fmt.Errorf("%w: invalid reply-to length %d", ErrInvalidProperties, len(replyTo))
		}
		options.replyTo = replyTo
		options.hasReplyTo = true
		return nil
	})
}

func WithPriority(priority uint8) PropertyOption {
	return propertyOptionFunc(func(options *propertyOptions) error {
		if options.hasPriority {
			return fmt.Errorf("%w: duplicate priority option", ErrInvalidProperties)
		}
		options.priority = priority
		options.hasPriority = true
		return nil
	})
}

// NewProperties validates and defensively copies fixture-proven AMQP properties.
func NewProperties(messageID string, headers map[string]Field, options ...PropertyOption) (Properties, error) {
	if messageID == "" || len(messageID) > maxShortStringBytes {
		return Properties{}, fmt.Errorf("%w: invalid message-id length %d", ErrInvalidProperties, len(messageID))
	}

	configured := propertyOptions{}
	for index, option := range options {
		if option == nil {
			return Properties{}, fmt.Errorf("%w: nil option at index %d", ErrInvalidProperties, index)
		}
		if err := option.apply(&configured); err != nil {
			return Properties{}, err
		}
	}

	if len(headers) > MaxHeaders {
		return Properties{}, fmt.Errorf("%w: header count %d > %d", ErrInvalidProperties, len(headers), MaxHeaders)
	}
	var headerBytes uint64
	for name, field := range headers {
		if name == "" || len(name) > maxShortStringBytes {
			return Properties{}, fmt.Errorf("%w: invalid header name length %d", ErrInvalidProperties, len(name))
		}
		if !field.valid() {
			return Properties{}, fmt.Errorf("%w: header %q has invalid field kind", ErrInvalidProperties, name)
		}
		headerBytes += uint64(len(name)) + field.wireSize()
		if headerBytes > MaxHeaderBytes {
			return Properties{}, fmt.Errorf("%w: header bytes %d > %d", ErrInvalidProperties, headerBytes, MaxHeaderBytes)
		}
	}

	copiedHeaders := make(map[string]Field, len(headers))
	for name, field := range headers {
		copiedHeaders[name] = field.clone()
	}

	return Properties{
		messageID:   messageID,
		replyTo:     configured.replyTo,
		hasReplyTo:  configured.hasReplyTo,
		priority:    configured.priority,
		hasPriority: configured.hasPriority,
		headers:     copiedHeaders,
		valid:       true,
	}, nil
}

func (properties Properties) MessageID() string { return properties.messageID }

func (properties Properties) CreatedAt() (time.Time, bool) {
	field, ok := properties.headers["created_at"]
	if !ok {
		return time.Time{}, false
	}
	text, ok := field.String()
	if !ok {
		return time.Time{}, false
	}
	// Jasmin format
	t, err := time.Parse("2006-01-02 15:04:05", text)
	return t, err == nil
}

func (properties Properties) Expiration() (time.Time, bool) {
	field, ok := properties.headers["expiration"]
	if !ok {
		return time.Time{}, false
	}
	text, ok := field.String()
	if !ok {
		return time.Time{}, false
	}
	// Try RFC3339 then Jasmin format
	t, err := time.Parse(time.RFC3339, text)
	if err != nil {
		t, err = time.Parse("2006-01-02 15:04:05", text)
	}
	return t, err == nil
}

func (properties Properties) ReplyTo() (string, bool) {
	return properties.replyTo, properties.hasReplyTo
}

func (properties Properties) Priority() (uint8, bool) {
	return properties.priority, properties.hasPriority
}

func (properties Properties) Headers() map[string]Field {
	return cloneHeaders(properties.headers)
}

func (properties Properties) clone() Properties {
	copy := properties
	copy.headers = cloneHeaders(properties.headers)
	return copy
}

func (properties Properties) validate() error {
	if !properties.valid || properties.messageID == "" {
		return ErrInvalidProperties
	}
	return nil
}

// Envelope is an immutable, opaque AMQP delivery boundary.
type Envelope struct {
	routingKey     string
	route          Route
	properties     Properties
	body           []byte
	bodySHA256     [sha256.Size]byte
	pickleProtocol uint8
	hasPickle      bool
}

// NewEnvelope validates metadata, checks the body cap before copying, and stores opaque bytes.
func NewEnvelope(routingKey string, properties Properties, body []byte) (Envelope, error) {
	route, err := ParseRoutingKey(routingKey)
	if err != nil {
		return Envelope{}, err
	}
	if err := properties.validate(); err != nil {
		return Envelope{}, fmt.Errorf("%w: missing validated message-id", ErrInvalidProperties)
	}
	if len(body) > MaxBodySize {
		return Envelope{}, fmt.Errorf("%w: %d > %d", ErrBodyTooLarge, len(body), MaxBodySize)
	}

	copiedBody := cloneBytes(body)
	protocol, hasPickle := detectLegacyPickle(copiedBody)
	return Envelope{
		routingKey:     routingKey,
		route:          route,
		properties:     properties.clone(),
		body:           copiedBody,
		bodySHA256:     sha256.Sum256(copiedBody),
		pickleProtocol: protocol,
		hasPickle:      hasPickle,
	}, nil
}

func (envelope Envelope) RoutingKey() string     { return envelope.routingKey }
func (envelope Envelope) Route() Route           { return envelope.route }
func (envelope Envelope) Properties() Properties { return envelope.properties.clone() }
func (envelope Envelope) Body() []byte           { return cloneBytes(envelope.body) }
func (envelope Envelope) BodySHA256() [sha256.Size]byte {
	return envelope.bodySHA256
}
func (envelope Envelope) LegacyPickleProtocol() (uint8, bool) {
	return envelope.pickleProtocol, envelope.hasPickle
}

func detectLegacyPickle(body []byte) (uint8, bool) {
	if len(body) < 2 || body[0] != 0x80 {
		return 0, false
	}
	return body[1], true
}

func cloneHeaders(headers map[string]Field) map[string]Field {
	copy := make(map[string]Field, len(headers))
	for name, field := range headers {
		copy[name] = field.clone()
	}
	return copy
}

func cloneBytes(value []byte) []byte {
	return append([]byte(nil), value...)
}

package rediscompat

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	MaxKeyBytes               = 512
	MaxRecordFieldBytes       = 64 << 10
	MaxLegacyMultipartPayload = 1 << 20

	dlrKeyPattern       = "dlr:<queue-message-id>"
	queueKeyPattern     = "queue-msgid:<normalized-smpp-message-id>"
	multipartKeyPattern = "longDeliverSm:<connector-id>:<reference>:<destination>"
	redisHashType       = "hash"
)

var (
	ErrInvalidKey      = errors.New("invalid Redis compatibility key")
	ErrInvalidRecord   = errors.New("invalid Redis compatibility record")
	ErrInvalidTTL      = errors.New("invalid Redis compatibility TTL")
	ErrPayloadTooLarge = errors.New("legacy multipart payload too large")
)

// KeyKind identifies a fixture-proven Jasmin Redis key family.
type KeyKind uint8

const (
	KeyUnknown KeyKind = iota
	KeyDLR
	KeyQueueMessage
	KeyLegacyMultipart
)

func (kind KeyKind) String() string {
	switch kind {
	case KeyDLR:
		return "dlr"
	case KeyQueueMessage:
		return "queue_message"
	case KeyLegacyMultipart:
		return "legacy_multipart"
	default:
		return "unknown"
	}
}

// Key is an immutable parsed Redis key.
type Key struct {
	kind        KeyKind
	value       string
	pattern     string
	subject     string
	connectorID string
	reference   uint32
	destination string
}

func (key Key) Kind() KeyKind   { return key.kind }
func (key Key) String() string  { return key.value }
func (key Key) Pattern() string { return key.pattern }

// Subject returns the queue message ID or normalized SMSC ID for simple key families.
func (key Key) Subject() (string, bool) {
	if key.kind != KeyDLR && key.kind != KeyQueueMessage {
		return "", false
	}
	return key.subject, true
}

// MultipartComponents returns legacy multipart key components without exposing payload state.
func (key Key) MultipartComponents() (connectorID string, reference uint32, destination string, ok bool) {
	if key.kind != KeyLegacyMultipart {
		return "", 0, "", false
	}
	return key.connectorID, key.reference, key.destination, true
}

func BuildDLRKey(queueMessageID string) (Key, error) {
	return buildSimpleKey(KeyDLR, "dlr:", dlrKeyPattern, queueMessageID)
}

func BuildQueueMessageKey(normalizedSMSCID string) (Key, error) {
	return buildSimpleKey(KeyQueueMessage, "queue-msgid:", queueKeyPattern, normalizedSMSCID)
}

func BuildLegacyMultipartKey(connectorID string, reference uint32, destination string) (Key, error) {
	if err := validateComponent("connector ID", connectorID); err != nil {
		return Key{}, err
	}
	if err := validateComponent("destination", destination); err != nil {
		return Key{}, err
	}
	value := "longDeliverSm:" + connectorID + ":" + strconv.FormatUint(uint64(reference), 10) + ":" + destination
	if len(value) > MaxKeyBytes {
		return Key{}, fmt.Errorf("%w: length %d > %d", ErrInvalidKey, len(value), MaxKeyBytes)
	}
	return Key{
		kind:        KeyLegacyMultipart,
		value:       value,
		pattern:     multipartKeyPattern,
		connectorID: connectorID,
		reference:   reference,
		destination: destination,
	}, nil
}

func buildSimpleKey(kind KeyKind, prefix, pattern, subject string) (Key, error) {
	if err := validateComponent("subject", subject); err != nil {
		return Key{}, err
	}
	value := prefix + subject
	if len(value) > MaxKeyBytes {
		return Key{}, fmt.Errorf("%w: length %d > %d", ErrInvalidKey, len(value), MaxKeyBytes)
	}
	return Key{kind: kind, value: value, pattern: pattern, subject: subject}, nil
}

func validateComponent(name, value string) error {
	if value == "" || strings.Contains(value, ":") {
		return fmt.Errorf("%w: %s is empty or contains ':'", ErrInvalidKey, name)
	}
	return nil
}

// ParseKey recognizes only fixture-proven key families.
func ParseKey(value string) (Key, error) {
	if value == "" || len(value) > MaxKeyBytes {
		return Key{}, fmt.Errorf("%w: length %d", ErrInvalidKey, len(value))
	}
	if strings.HasPrefix(value, "dlr:") {
		key, err := BuildDLRKey(strings.TrimPrefix(value, "dlr:"))
		if err == nil && key.value == value {
			return key, nil
		}
		return Key{}, fmt.Errorf("%w: %q", ErrInvalidKey, value)
	}
	if strings.HasPrefix(value, "queue-msgid:") {
		key, err := BuildQueueMessageKey(strings.TrimPrefix(value, "queue-msgid:"))
		if err == nil && key.value == value {
			return key, nil
		}
		return Key{}, fmt.Errorf("%w: %q", ErrInvalidKey, value)
	}
	if strings.HasPrefix(value, "longDeliverSm:") {
		parts := strings.Split(value, ":")
		if len(parts) != 4 || parts[0] != "longDeliverSm" || !decimalDigits(parts[2]) {
			return Key{}, fmt.Errorf("%w: %q", ErrInvalidKey, value)
		}
		reference, err := strconv.ParseUint(parts[2], 10, 32)
		if err != nil {
			return Key{}, fmt.Errorf("%w: reference %q", ErrInvalidKey, parts[2])
		}
		key, err := BuildLegacyMultipartKey(parts[1], uint32(reference), parts[3])
		if err == nil && key.value == value {
			return key, nil
		}
		return Key{}, fmt.Errorf("%w: %q", ErrInvalidKey, value)
	}
	return Key{}, fmt.Errorf("%w: unknown family %q", ErrInvalidKey, value)
}

func decimalDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

// FieldKind identifies a fixture-proven Redis hash value kind.
type FieldKind uint8

const (
	FieldInvalid FieldKind = iota
	FieldString
	FieldInteger
)

func (kind FieldKind) String() string {
	switch kind {
	case FieldString:
		return "string"
	case FieldInteger:
		return "integer"
	default:
		return "invalid"
	}
}

// Field is an immutable typed Redis hash value.
type Field struct {
	kind         FieldKind
	stringValue  string
	integerValue int64
}

func StringField(value string) Field { return Field{kind: FieldString, stringValue: value} }
func IntegerField(value int64) Field { return Field{kind: FieldInteger, integerValue: value} }
func (field Field) Kind() FieldKind  { return field.kind }

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

func (field Field) valid() bool {
	return field.kind == FieldString || field.kind == FieldInteger
}

func (field Field) encodedSize() uint64 {
	if field.kind == FieldString {
		return uint64(len(field.stringValue))
	}
	if field.kind == FieldInteger {
		return uint64(len(strconv.FormatInt(field.integerValue, 10)))
	}
	return 0
}

// HashRecord is an immutable fixture-proven Redis hash write description.
type HashRecord struct {
	key        Key
	fields     map[string]Field
	ttlSeconds int64
}

func (record HashRecord) RedisType() string { return redisHashType }
func (record HashRecord) Key() Key          { return record.key }
func (record HashRecord) TTLSeconds() int64 { return record.ttlSeconds }
func (record HashRecord) Fields() map[string]Field {
	return cloneFields(record.fields)
}

func newHashRecord(key Key, expectedKind KeyKind, ttlSeconds int64, fields map[string]Field) (HashRecord, error) {
	if key.kind != expectedKind || key.value == "" {
		return HashRecord{}, fmt.Errorf("%w: key kind %s, want %s", ErrInvalidRecord, key.kind, expectedKind)
	}
	if ttlSeconds <= 0 {
		return HashRecord{}, fmt.Errorf("%w: %d", ErrInvalidTTL, ttlSeconds)
	}
	var fieldBytes uint64
	for name, field := range fields {
		if name == "" || !field.valid() {
			return HashRecord{}, fmt.Errorf("%w: invalid field %q", ErrInvalidRecord, name)
		}
		fieldBytes += uint64(len(name)) + field.encodedSize()
		if fieldBytes > MaxRecordFieldBytes {
			return HashRecord{}, fmt.Errorf("%w: field bytes %d > %d", ErrInvalidRecord, fieldBytes, MaxRecordFieldBytes)
		}
	}
	return HashRecord{key: key, fields: cloneFields(fields), ttlSeconds: ttlSeconds}, nil
}

func cloneFields(fields map[string]Field) map[string]Field {
	copy := make(map[string]Field, len(fields))
	for name, field := range fields {
		copy[name] = field
	}
	return copy
}

// HTTPDLRRequest is the fixture-proven HTTP callback state stored under dlr:<queue-message-id>.
type HTTPDLRRequest struct {
	URL           string
	Level         int64
	Method        string
	Connector     string
	ExpirySeconds int64
}

func NewHTTPDLRRecord(key Key, request HTTPDLRRequest) (HashRecord, error) {
	if request.ExpirySeconds <= 0 {
		return HashRecord{}, fmt.Errorf("%w: %d", ErrInvalidTTL, request.ExpirySeconds)
	}
	if request.URL == "" || request.Connector == "" || request.Level < 1 || request.Level > 3 || (request.Method != "GET" && request.Method != "POST") {
		return HashRecord{}, fmt.Errorf("%w: invalid HTTP DLR request", ErrInvalidRecord)
	}
	return newHashRecord(key, KeyDLR, request.ExpirySeconds, map[string]Field{
		"sc":        StringField("httpapi"),
		"url":       StringField(request.URL),
		"level":     IntegerField(request.Level),
		"method":    StringField(request.Method),
		"connector": StringField(request.Connector),
		"expiry":    IntegerField(request.ExpirySeconds),
	})
}

// SMPPSDLRRequest is the fixture-proven SMPP-server receipt state stored under a DLR key.
type SMPPSDLRRequest struct {
	SystemID      string
	SourceAddrTON string
	SourceAddrNPI string
	// SourceAddress and DestinationAddress are the submit_sm addresses as the
	// ESME sent them. Legacy hmsets the raw PDU bytes (managers/clients.py:637),
	// so a numeric MSISDN lands in Redis as an integer and an alphanumeric
	// sender id as a string — see addressField.
	SourceAddress             string
	DestinationAddrTON        string
	DestinationAddrNPI        string
	DestinationAddress        string
	SubmissionDate            string
	RegisteredDeliveryReceipt string
	ExpirySeconds             int64
}

// addressField reproduces what legacy stores for an SMPP address. Python writes
// the PDU's raw bytes and the capture harness typed them by content, which is
// why the frozen fixture records source_addr as an int: it captured a numeric
// MSISDN. An alphanumeric sender id is equally legal on submit_sm and must stay
// a string rather than being rejected or coerced to zero.
//
// Both forms serialise to identical Redis bytes, so this only decides the
// declared kind — but the golden differential compares kinds, and the read side
// (dlr.correlation) takes the value as a string either way.
func addressField(value string) Field {
	if parsed, err := strconv.ParseInt(value, 10, 64); err == nil && value == strconv.FormatInt(parsed, 10) {
		return IntegerField(parsed)
	}
	return StringField(value)
}

func NewSMPPSDLRRecord(key Key, request SMPPSDLRRequest) (HashRecord, error) {
	if request.ExpirySeconds <= 0 {
		return HashRecord{}, fmt.Errorf("%w: %d", ErrInvalidTTL, request.ExpirySeconds)
	}
	if request.SystemID == "" || request.SourceAddrTON == "" || request.SourceAddrNPI == "" || request.DestinationAddrTON == "" || request.DestinationAddrNPI == "" || request.DestinationAddress == "" || request.SubmissionDate == "" || request.RegisteredDeliveryReceipt == "" {
		return HashRecord{}, fmt.Errorf("%w: missing SMPPS DLR request field", ErrInvalidRecord)
	}
	return newHashRecord(key, KeyDLR, request.ExpirySeconds, map[string]Field{
		"sc":               StringField("smppsapi"),
		"system_id":        StringField(request.SystemID),
		"source_addr_ton":  StringField(request.SourceAddrTON),
		"source_addr_npi":  StringField(request.SourceAddrNPI),
		"source_addr":      addressField(request.SourceAddress),
		"dest_addr_ton":    StringField(request.DestinationAddrTON),
		"dest_addr_npi":    StringField(request.DestinationAddrNPI),
		"destination_addr": addressField(request.DestinationAddress),
		"sub_date":         StringField(request.SubmissionDate),
		"rd_receipt":       StringField(request.RegisteredDeliveryReceipt),
		"expiry":           IntegerField(request.ExpirySeconds),
	})
}

// QueueMessageCorrelation maps a normalized SMSC message ID back to a queue message ID.
type QueueMessageCorrelation struct {
	MessageID     string
	ConnectorType string
	TTLSeconds    int64
}

func NewQueueMessageCorrelation(key Key, correlation QueueMessageCorrelation) (HashRecord, error) {
	if correlation.TTLSeconds <= 0 {
		return HashRecord{}, fmt.Errorf("%w: %d", ErrInvalidTTL, correlation.TTLSeconds)
	}
	if correlation.MessageID == "" || (correlation.ConnectorType != "httpapi" && correlation.ConnectorType != "smppsapi") {
		return HashRecord{}, fmt.Errorf("%w: invalid queue-message correlation", ErrInvalidRecord)
	}
	return newHashRecord(key, KeyQueueMessage, correlation.TTLSeconds, map[string]Field{
		"msgid":          StringField(correlation.MessageID),
		"connector_type": StringField(correlation.ConnectorType),
	})
}

// LegacyMultipartMetadata is safe metadata derived from an opaque Python-owned hash field.
// It intentionally contains no payload bytes.
type LegacyMultipartMetadata struct {
	key             Key
	segmentSequence uint32
	ttlSeconds      int64
	payloadLength   int
	payloadSHA256   [sha256.Size]byte
	pickleProtocol  uint8
	hasPickle       bool
}

func InspectLegacyMultipartPart(key Key, segmentSequence uint32, ttlSeconds int64, payload []byte) (LegacyMultipartMetadata, error) {
	if key.kind != KeyLegacyMultipart || key.value == "" {
		return LegacyMultipartMetadata{}, fmt.Errorf("%w: expected legacy multipart key", ErrInvalidRecord)
	}
	if segmentSequence == 0 {
		return LegacyMultipartMetadata{}, fmt.Errorf("%w: zero segment sequence", ErrInvalidRecord)
	}
	if ttlSeconds <= 0 {
		return LegacyMultipartMetadata{}, fmt.Errorf("%w: %d", ErrInvalidTTL, ttlSeconds)
	}
	if len(payload) == 0 {
		return LegacyMultipartMetadata{}, fmt.Errorf("%w: empty legacy multipart payload", ErrInvalidRecord)
	}
	if len(payload) > MaxLegacyMultipartPayload {
		return LegacyMultipartMetadata{}, fmt.Errorf("%w: %d > %d", ErrPayloadTooLarge, len(payload), MaxLegacyMultipartPayload)
	}
	protocol, hasPickle := detectLegacyPickle(payload)
	return LegacyMultipartMetadata{
		key:             key,
		segmentSequence: segmentSequence,
		ttlSeconds:      ttlSeconds,
		payloadLength:   len(payload),
		payloadSHA256:   sha256.Sum256(payload),
		pickleProtocol:  protocol,
		hasPickle:       hasPickle,
	}, nil
}

func (metadata LegacyMultipartMetadata) RedisType() string { return redisHashType }
func (metadata LegacyMultipartMetadata) Key() Key          { return metadata.key }
func (metadata LegacyMultipartMetadata) SegmentSequence() uint32 {
	return metadata.segmentSequence
}
func (metadata LegacyMultipartMetadata) TTLSeconds() int64  { return metadata.ttlSeconds }
func (metadata LegacyMultipartMetadata) PayloadLength() int { return metadata.payloadLength }
func (metadata LegacyMultipartMetadata) PayloadSHA256() [sha256.Size]byte {
	return metadata.payloadSHA256
}
func (metadata LegacyMultipartMetadata) LegacyPickleProtocol() (uint8, bool) {
	return metadata.pickleProtocol, metadata.hasPickle
}

func detectLegacyPickle(payload []byte) (uint8, bool) {
	if len(payload) < 2 || payload[0] != 0x80 {
		return 0, false
	}
	return payload[1], true
}

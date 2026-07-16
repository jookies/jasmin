package routingfilter

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxPatternBytes = 4 << 10
	MaxFieldBytes   = 1 << 20
	MaxTags         = 256
	MaxTagBytes     = 1 << 10
	MaxIDBytes      = 1 << 10
)

var (
	ErrInvalidDirection = errors.New("invalid routing direction")
	ErrInvalidPattern   = errors.New("invalid compatibility regex pattern")
	ErrInvalidInterval  = errors.New("invalid interval")
	ErrInvalidValue     = errors.New("invalid routing filter value")
	ErrMissingField     = errors.New("required routable field is missing")
	ErrValueTooLarge    = errors.New("routing filter value exceeds compatibility cap")
	ErrTooManyTags      = errors.New("too many routable tags")
)

type Direction string

const (
	MT Direction = "mt"
	MO Direction = "mo"
)

type Kind string

const (
	KindTransparent     Kind = "transparent"
	KindConnector       Kind = "connector"
	KindUser            Kind = "user"
	KindGroup           Kind = "group"
	KindSourceAddr      Kind = "source_addr"
	KindDestinationAddr Kind = "destination_addr"
	KindShortMessage    Kind = "short_message"
	KindDateInterval    Kind = "date_interval"
	KindTimeInterval    Kind = "time_interval"
	KindTag             Kind = "tag"
)

type BytesField struct {
	Present bool
	Value   []byte
}

type RoutableInput struct {
	Direction       Direction
	ConnectorID     string
	UserID          int64
	GroupID         int64
	SourceAddr      BytesField
	DestinationAddr BytesField
	ShortMessage    BytesField
	MessagePayload  BytesField
	Timestamp       time.Time
	Tags            []string
}

type Routable struct {
	direction       Direction
	connectorID     string
	userID          int64
	groupID         int64
	sourceAddr      BytesField
	destinationAddr BytesField
	shortMessage    BytesField
	messagePayload  BytesField
	timestamp       time.Time
	tags            map[string]struct{}
}

type Filter interface {
	Match(Routable) (bool, error)
	Directions() []Direction
	Kind() Kind
}

type filterBase struct {
	kind       Kind
	directions []Direction
}

func NewRoutable(input RoutableInput) (Routable, error) {
	if input.Direction != MT && input.Direction != MO {
		return Routable{}, fmt.Errorf("%w: %q", ErrInvalidDirection, input.Direction)
	}
	if len(input.ConnectorID) > MaxIDBytes {
		return Routable{}, fmt.Errorf("%w: connector id", ErrValueTooLarge)
	}
	if !utf8.ValidString(input.ConnectorID) {
		return Routable{}, fmt.Errorf("%w: connector id is not UTF-8", ErrInvalidValue)
	}
	fields := []struct {
		name  string
		field BytesField
	}{
		{"source_addr", input.SourceAddr},
		{"destination_addr", input.DestinationAddr},
		{"short_message", input.ShortMessage},
		{"message_payload", input.MessagePayload},
	}
	for _, candidate := range fields {
		if len(candidate.field.Value) > MaxFieldBytes {
			return Routable{}, fmt.Errorf("%w: %s", ErrValueTooLarge, candidate.name)
		}
	}
	if len(input.Tags) > MaxTags {
		return Routable{}, fmt.Errorf("%w: %d > %d", ErrTooManyTags, len(input.Tags), MaxTags)
	}
	tags := make(map[string]struct{}, len(input.Tags))
	for _, tag := range input.Tags {
		if len(tag) > MaxTagBytes {
			return Routable{}, fmt.Errorf("%w: tag", ErrValueTooLarge)
		}
		if !utf8.ValidString(tag) {
			return Routable{}, fmt.Errorf("%w: tag is not UTF-8", ErrInvalidValue)
		}
		tags[tag] = struct{}{}
	}
	return Routable{
		direction:       input.Direction,
		connectorID:     input.ConnectorID,
		userID:          input.UserID,
		groupID:         input.GroupID,
		sourceAddr:      cloneField(input.SourceAddr),
		destinationAddr: cloneField(input.DestinationAddr),
		shortMessage:    cloneField(input.ShortMessage),
		messagePayload:  cloneField(input.MessagePayload),
		timestamp:       input.Timestamp,
		tags:            tags,
	}, nil
}

func NewTransparentFilter() Filter {
	return transparentFilter{filterBase: commonBase(KindTransparent)}
}

func NewConnectorFilter(connectorID string) (Filter, error) {
	if len(connectorID) > MaxIDBytes {
		return nil, fmt.Errorf("%w: connector id", ErrValueTooLarge)
	}
	if !utf8.ValidString(connectorID) {
		return nil, fmt.Errorf("%w: connector id is not UTF-8", ErrInvalidValue)
	}
	return connectorFilter{filterBase: filterBase{kind: KindConnector, directions: []Direction{MO}}, connectorID: connectorID}, nil
}

func NewUserFilter(userID int64) Filter {
	return userFilter{filterBase: filterBase{kind: KindUser, directions: []Direction{MT}}, userID: userID}
}

func NewGroupFilter(groupID int64) Filter {
	return groupFilter{filterBase: filterBase{kind: KindGroup, directions: []Direction{MT}}, groupID: groupID}
}

func NewSourceAddrFilter(pattern string) (Filter, error) {
	return newRegexFilter(KindSourceAddr, pattern)
}

func NewDestinationAddrFilter(pattern string) (Filter, error) {
	return newRegexFilter(KindDestinationAddr, pattern)
}

func NewShortMessageFilter(pattern string) (Filter, error) {
	return newRegexFilter(KindShortMessage, pattern)
}

func NewDateIntervalFilter(start, end string) (Filter, error) {
	startDate, err := parseDate(start)
	if err != nil {
		return nil, fmt.Errorf("%w: start date: %v", ErrInvalidInterval, err)
	}
	endDate, err := parseDate(end)
	if err != nil {
		return nil, fmt.Errorf("%w: end date: %v", ErrInvalidInterval, err)
	}
	return dateIntervalFilter{filterBase: commonBase(KindDateInterval), start: startDate, end: endDate}, nil
}

func NewTimeIntervalFilter(start, end string) (Filter, error) {
	startTime, err := parseTimeOfDay(start)
	if err != nil {
		return nil, fmt.Errorf("%w: start time: %v", ErrInvalidInterval, err)
	}
	endTime, err := parseTimeOfDay(end)
	if err != nil {
		return nil, fmt.Errorf("%w: end time: %v", ErrInvalidInterval, err)
	}
	return timeIntervalFilter{filterBase: commonBase(KindTimeInterval), start: startTime, end: endTime}, nil
}

func NewTagFilter(tag string) (Filter, error) {
	if len(tag) > MaxTagBytes {
		return nil, fmt.Errorf("%w: tag", ErrValueTooLarge)
	}
	if !utf8.ValidString(tag) {
		return nil, fmt.Errorf("%w: tag is not UTF-8", ErrInvalidValue)
	}
	return tagFilter{filterBase: commonBase(KindTag), tag: tag}, nil
}

func (base filterBase) Directions() []Direction {
	return append([]Direction(nil), base.directions...)
}

func (base filterBase) Kind() Kind {
	return base.kind
}

type transparentFilter struct{ filterBase }

func (transparentFilter) Match(Routable) (bool, error) { return true, nil }

type connectorFilter struct {
	filterBase
	connectorID string
}

func (filter connectorFilter) Match(routable Routable) (bool, error) {
	return routable.connectorID == filter.connectorID, nil
}

type userFilter struct {
	filterBase
	userID int64
}

func (filter userFilter) Match(routable Routable) (bool, error) {
	return routable.userID == filter.userID, nil
}

type groupFilter struct {
	filterBase
	groupID int64
}

func (filter groupFilter) Match(routable Routable) (bool, error) {
	return routable.groupID == filter.groupID, nil
}

type regexFilter struct {
	filterBase
	pattern *regexp.Regexp
}

func newRegexFilter(kind Kind, pattern string) (Filter, error) {
	if len(pattern) > MaxPatternBytes {
		return nil, fmt.Errorf("%w: pattern", ErrValueTooLarge)
	}
	if !utf8.ValidString(pattern) {
		return nil, fmt.Errorf("%w: pattern is not UTF-8", ErrInvalidPattern)
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPattern, err)
	}
	return regexFilter{filterBase: commonBase(kind), pattern: compiled}, nil
}

func (filter regexFilter) Match(routable Routable) (bool, error) {
	var field BytesField
	switch filter.kind {
	case KindSourceAddr:
		field = routable.sourceAddr
		if !field.Present {
			return false, fmt.Errorf("%w: source_addr", ErrMissingField)
		}
	case KindDestinationAddr:
		field = routable.destinationAddr
		if !field.Present {
			return false, fmt.Errorf("%w: destination_addr", ErrMissingField)
		}
	case KindShortMessage:
		if routable.shortMessage.Present {
			field = routable.shortMessage
		} else if routable.messagePayload.Present {
			field = routable.messagePayload
		} else {
			return false, nil
		}
	default:
		return false, fmt.Errorf("%w: unknown regex filter", ErrInvalidPattern)
	}
	value := strings.ToValidUTF8(string(field.Value), "�")
	location := filter.pattern.FindStringIndex(value)
	return location != nil && location[0] == 0, nil
}

type dateValue int

type dateIntervalFilter struct {
	filterBase
	start dateValue
	end   dateValue
}

func (filter dateIntervalFilter) Match(routable Routable) (bool, error) {
	year, month, day := routable.timestamp.Date()
	current := dateValue(year*10000 + int(month)*100 + day)
	return filter.start <= current && current <= filter.end, nil
}

type timeIntervalFilter struct {
	filterBase
	start int64
	end   int64
}

func (filter timeIntervalFilter) Match(routable Routable) (bool, error) {
	current := timeOfDayNanoseconds(routable.timestamp)
	return filter.start <= current && current <= filter.end, nil
}

type tagFilter struct {
	filterBase
	tag string
}

func (filter tagFilter) Match(routable Routable) (bool, error) {
	_, found := routable.tags[filter.tag]
	return found, nil
}

func (r Routable) Direction() Direction { return r.direction }
func (r Routable) ConnectorID() string { return r.connectorID }
func (r Routable) UserID() int64 { return r.userID }
func (r Routable) GroupID() int64 { return r.groupID }
func (r Routable) SourceAddr() BytesField { return cloneField(r.sourceAddr) }
func (r Routable) DestinationAddr() BytesField { return cloneField(r.destinationAddr) }
func (r Routable) ShortMessage() BytesField { return cloneField(r.shortMessage) }
func (r Routable) MessagePayload() BytesField { return cloneField(r.messagePayload) }
func (r Routable) Timestamp() time.Time { return r.timestamp }
func (r Routable) Tags() []string {
	tags := make([]string, 0, len(r.tags))
	for t := range r.tags {
		tags = append(tags, t)
	}
	return tags
}

func (r *Routable) SetSourceAddr(val []byte) error {
	if len(val) > MaxFieldBytes {
		return ErrValueTooLarge
	}
	r.sourceAddr = BytesField{Present: true, Value: append([]byte(nil), val...)}
	return nil
}
func (r *Routable) SetDestinationAddr(val []byte) error {
	if len(val) > MaxFieldBytes {
		return ErrValueTooLarge
	}
	r.destinationAddr = BytesField{Present: true, Value: append([]byte(nil), val...)}
	return nil
}
func (r *Routable) SetShortMessage(val []byte) error {
	if len(val) > MaxFieldBytes {
		return ErrValueTooLarge
	}
	r.shortMessage = BytesField{Present: true, Value: append([]byte(nil), val...)}
	return nil
}
func (r *Routable) AddTag(tag string) error {
	if len(tag) > MaxTagBytes {
		return ErrValueTooLarge
	}
	if !utf8.ValidString(tag) {
		return ErrInvalidValue
	}
	if len(r.tags) >= MaxTags {
		return ErrTooManyTags
	}
	r.tags[tag] = struct{}{}
	return nil
}
func (r *Routable) RemoveTag(tag string) {
	delete(r.tags, tag)
}

func (r Routable) Clone() Routable {
	newR := r
	newR.sourceAddr = cloneField(r.sourceAddr)
	newR.destinationAddr = cloneField(r.destinationAddr)
	newR.shortMessage = cloneField(r.shortMessage)
	newR.messagePayload = cloneField(r.messagePayload)
	newR.tags = make(map[string]struct{}, len(r.tags))
	for t := range r.tags {
		newR.tags[t] = struct{}{}
	}
	return newR
}

func commonBase(kind Kind) filterBase {
	return filterBase{kind: kind, directions: []Direction{MT, MO}}
}

func cloneField(field BytesField) BytesField {
	return BytesField{Present: field.Present, Value: append([]byte(nil), field.Value...)}
}

func parseDate(value string) (dateValue, error) {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return 0, err
	}
	year, month, day := parsed.Date()
	return dateValue(year*10000 + int(month)*100 + day), nil
}

func parseTimeOfDay(value string) (int64, error) {
	parsed, err := time.Parse("15:04:05", value)
	if err != nil {
		return 0, err
	}
	return timeOfDayNanoseconds(parsed), nil
}

func timeOfDayNanoseconds(value time.Time) int64 {
	return int64(value.Hour())*int64(time.Hour) +
		int64(value.Minute())*int64(time.Minute) +
		int64(value.Second())*int64(time.Second) +
		int64(value.Nanosecond())
}

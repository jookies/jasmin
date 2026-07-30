package picklecompat

import (
	"fmt"
	"time"

	"github.com/pumpitspace/synevyr/internal/transport/gopickle"
)

// awareDatetimeValue builds the pickle for a timezone-aware datetime:
// datetime.datetime(<10-byte>, timezone(timedelta(days, seconds, 0))) — the form
// datetime.fromisoformat(<RFC3339>) produces, which the front-door feeds as
// schedule_delivery_time / validity_period. The 10-byte state is the local clock
// time (BE year(2), month, day, hour, minute, second, BE microseconds(3)); the
// tz offset rides in the separate timezone arg.
func awareDatetimeValue(t time.Time) gopickle.Value {
	microsecond := t.Nanosecond() / 1000
	packed := []byte{
		byte(t.Year() >> 8), byte(t.Year()),
		byte(t.Month()), byte(t.Day()),
		byte(t.Hour()), byte(t.Minute()), byte(t.Second()),
		byte(microsecond >> 16), byte(microsecond >> 8), byte(microsecond),
	}
	_, offsetSeconds := t.Zone()
	return gopickle.Reduce{
		Callable: gopickle.Global{Module: "datetime", Name: "datetime"},
		Args: gopickle.Tuple{
			gopickle.Bytes(packed),
			timezoneValue(offsetSeconds),
		},
	}
}

// timezoneValue builds datetime.timezone(datetime.timedelta(days, seconds, 0)),
// normalising the offset the way Python's timedelta does (0 <= seconds < 86400).
func timezoneValue(offsetSeconds int) gopickle.Value {
	days := offsetSeconds / 86400
	seconds := offsetSeconds % 86400
	if seconds < 0 {
		days--
		seconds += 86400
	}
	delta := gopickle.Reduce{
		Callable: gopickle.Global{Module: "datetime", Name: "timedelta"},
		Args:     gopickle.Tuple{gopickle.Int(int64(days)), gopickle.Int(int64(seconds)), gopickle.Int(0)},
	}
	return gopickle.Reduce{
		Callable: gopickle.Global{Module: "datetime", Name: "timezone"},
		Args:     gopickle.Tuple{delta},
	}
}

// scheduleValidityValue parses an RFC3339(Nano) time string (as the envelope
// builder formats schedule_at / validity_until) into the aware-datetime pickle.
func scheduleValidityValue(rfc3339 string) (gopickle.Value, error) {
	parsed, err := time.Parse(time.RFC3339Nano, rfc3339)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid time %q: %v", ErrNativeCodec, rfc3339, err)
	}
	return awareDatetimeValue(parsed), nil
}

// smppTimeBytes projects a pickled datetime (schedule_delivery_time /
// validity_period) into the SMPP absolute-time wire form
// YYMMDDhhmmss+tenths+quarter-hour-offset+sign, matching the legacy TimeEncoder
// (which the bridge's _time_bytes strips the trailing NUL from). None yields nil.
func smppTimeBytes(value gopickle.Value) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	if _, isNone := value.(gopickle.None); isNone {
		return nil, nil
	}
	if relative, ok := value.(gopickle.Object); ok &&
		relative.Class.Module == "smpp.pdu.smpp_time" &&
		relative.Class.Name == "SMPPRelativeTime" {
		args, ok := relative.Args.(gopickle.Tuple)
		if !ok || len(args) != 6 {
			return nil, poisonSubmitError("relative time has malformed args")
		}
		fields := make([]int, len(args))
		for index, arg := range args {
			integer, ok := arg.(gopickle.Int)
			if !ok || integer < 0 || integer > 99 {
				return nil, poisonSubmitError("relative time field %d malformed", index)
			}
			fields[index] = int(integer)
		}
		return []byte(fmt.Sprintf("%02d%02d%02d%02d%02d%02d000R",
			fields[0], fields[1], fields[2], fields[3], fields[4], fields[5])), nil
	}
	reduce, ok := value.(gopickle.Reduce)
	if !ok {
		return nil, poisonSubmitError("time is not a datetime reduce")
	}
	global, ok := reduce.Callable.(gopickle.Global)
	if !ok || global.Module != "datetime" || global.Name != "datetime" {
		return nil, poisonSubmitError("time callable is not datetime.datetime")
	}
	args, ok := reduce.Args.(gopickle.Tuple)
	if !ok || len(args) < 1 {
		return nil, poisonSubmitError("datetime has no state")
	}
	packed, ok := args[0].(gopickle.Bytes)
	if !ok || len(packed) != 10 {
		return nil, poisonSubmitError("datetime state is not 10 bytes")
	}
	year := int(packed[0])<<8 | int(packed[1])
	microsecond := int(packed[7])<<16 | int(packed[8])<<8 | int(packed[9])
	tenths := microsecond / 100000

	quarterHours := 0
	sign := byte('+')
	if len(args) >= 2 {
		offsetSeconds, err := timezoneOffsetSeconds(args[1])
		if err != nil {
			return nil, err
		}
		quarterHours = offsetSeconds / 900
		if quarterHours < 0 {
			sign = '-'
			quarterHours = -quarterHours
		}
	}
	text := fmt.Sprintf("%02d%02d%02d%02d%02d%02d%d%02d%c",
		year%100, packed[2], packed[3], packed[4], packed[5], packed[6], tenths, quarterHours, sign)
	return []byte(text), nil
}

// timezoneOffsetSeconds extracts the UTC offset (seconds) from a pickled
// datetime.timezone(datetime.timedelta(days, seconds, microseconds)).
func timezoneOffsetSeconds(value gopickle.Value) (int, error) {
	tz, ok := value.(gopickle.Reduce)
	if !ok {
		return 0, poisonSubmitError("tzinfo is not a reduce")
	}
	tzGlobal, ok := tz.Callable.(gopickle.Global)
	if !ok || tzGlobal.Module != "datetime" || tzGlobal.Name != "timezone" {
		return 0, poisonSubmitError("tzinfo is not datetime.timezone")
	}
	tzArgs, ok := tz.Args.(gopickle.Tuple)
	if !ok || len(tzArgs) < 1 {
		return 0, poisonSubmitError("timezone has no delta")
	}
	delta, ok := tzArgs[0].(gopickle.Reduce)
	if !ok {
		return 0, poisonSubmitError("timezone delta is not a reduce")
	}
	deltaGlobal, ok := delta.Callable.(gopickle.Global)
	if !ok || deltaGlobal.Module != "datetime" || deltaGlobal.Name != "timedelta" {
		return 0, poisonSubmitError("delta is not datetime.timedelta")
	}
	deltaArgs, ok := delta.Args.(gopickle.Tuple)
	if !ok || len(deltaArgs) < 1 {
		return 0, poisonSubmitError("timedelta has no args")
	}
	days := 0
	seconds := 0
	if d, ok := deltaArgs[0].(gopickle.Int); ok {
		days = int(d)
	}
	if len(deltaArgs) >= 2 {
		if s, ok := deltaArgs[1].(gopickle.Int); ok {
			seconds = int(s)
		}
	}
	return days*86400 + seconds, nil
}

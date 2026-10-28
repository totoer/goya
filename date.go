package goya

import (
	"math"
	"reflect"
	"time"
)

const (
	dateTimeLayout    = "Mon Jan 02 2006 15:04:05 GMT-0700 (MST)"
	utcDateTimeLayout = "Mon, 02 Jan 2006 15:04:05 GMT"
	isoDateTimeLayout = "2006-01-02T15:04:05.000Z"
	dateLayout        = "Mon Jan 02 2006"
	timeLayout        = "15:04:05 GMT-0700 (MST)"

	maxTime   = 8.64e15
	timeUnset = math.MinInt64
)

type dateObject struct {
	baseObject
	msec int64
}

func dateParse(date string, location *time.Location) (t time.Time, ok bool) {
	d, ok := parseDateISOString(date)
	if !ok {
		d, ok = parseDateOtherString(date)
	}
	if !ok {
		return
	}
	if d.month > 12 ||
		d.day > 31 ||
		d.hour > 24 ||
		d.min > 59 ||
		d.sec > 59 ||
		// special case 24:00:00.000
		(d.hour == 24 && (d.min != 0 || d.sec != 0 || d.msec != 0)) {
		ok = false
		return
	}
	milliseconds := dateCivilMilliseconds([7]float64{float64(d.year), float64(d.month - 1), float64(d.day), float64(d.hour), float64(d.min), float64(d.sec), float64(d.msec)})
	if d.isLocal {
		milliseconds = dateLocalToUTC(milliseconds, location)
	} else {
		milliseconds -= float64(d.timeZoneOffset) * 60000
	}
	clipped, ok := dateClip(milliseconds)
	if !ok {
		return time.Time{}, false
	}
	// Keep an explicitly written offset in Go's text-to-time conversion.
	// Date objects store the instant and use their Runtime timezone on export.
	if !d.isLocal {
		location = time.UTC
		if d.timeZoneOffset != 0 {
			location = time.FixedZone("", d.timeZoneOffset*60)
		}
	}
	return timeFromMsec(clipped).In(location), true
}

func (r *Runtime) newDateObject(t time.Time, isSet bool, proto *Object) *Object {
	v := &Object{runtime: r}
	d := &dateObject{}
	v.self = d
	d.val = v
	d.class = classDate
	d.prototype = proto
	d.extensible = true
	d.init()
	if isSet {
		d.msec = timeToMsec(t)
	} else {
		d.msec = timeUnset
	}
	return v
}

func dateFormat(t time.Time, location *time.Location) string {
	return t.In(location).Format(dateTimeLayout)
}

func timeFromMsec(msec int64) time.Time {
	sec := msec / 1000
	nsec := (msec % 1000) * 1e6
	return time.Unix(sec, nsec)
}

func timeToMsec(t time.Time) int64 {
	return t.Unix()*1000 + int64(t.Nanosecond())/1e6
}

// timeToMsecClip returns the time value of t in milliseconds and whether it is within the range
// allowed by TimeClip. Unlike timeToMsec() and t.UnixMilli() it does not overflow when t is too far
// from the epoch.
func timeToMsecClip(t time.Time) (int64, bool) {
	sec := t.Unix()
	if sec < -maxTime/1000 || sec > maxTime/1000 {
		return 0, false
	}
	msec := sec*1000 + int64(t.Nanosecond())/1e6
	return msec, msec >= -maxTime && msec <= maxTime
}

func (d *dateObject) exportType() reflect.Type {
	return typeTime
}

func (d *dateObject) export(*objectExportCtx) interface{} {
	if d.isSet() {
		return d.time()
	}
	return nil
}

func (d *dateObject) setTimeMs(ms int64) Value {
	if ms >= 0 && ms <= maxTime || ms < 0 && ms >= -maxTime {
		d.msec = ms
		return intToValue(ms)
	}

	d.unset()
	return _NaN
}

func (d *dateObject) setTime(t time.Time) Value {
	if ms, ok := timeToMsecClip(t); ok {
		d.msec = ms
		return intToValue(ms)
	}

	d.unset()
	return _NaN
}

func (d *dateObject) isSet() bool {
	return d.msec != timeUnset
}

func (d *dateObject) unset() {
	d.msec = timeUnset
}

func (d *dateObject) time() time.Time {
	return timeFromMsec(d.msec).In(d.val.runtime.timeZone)
}

func (d *dateObject) timeUTC() time.Time {
	return timeFromMsec(d.msec).In(time.UTC)
}

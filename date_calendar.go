package goya

import (
	"math"
	"math/big"
	"time"
)

// Calendar inputs remain Numbers until TimeClip. Converting components to Go
// integers first both overflows and loses valid cancellation between them.
func dateClip(number float64) (int64, bool) {
	if math.IsNaN(number) || math.IsInf(number, 0) || math.Abs(number) > maxTime {
		return 0, false
	}
	return int64(number), true
}

func dateBalanceMonth(year, month float64) (float64, int) {
	if math.Abs(month) < 1<<53 {
		m := int(math.Mod(month, 12))
		if m < 0 {
			m += 12
		}
		return float64(year + math.Floor(month/12)), m
	}
	// floor(month / 12) is a mathematical-integer operation before its
	// conversion back to Number. Floating division alone can round up here.
	integer, _ := new(big.Float).SetFloat64(month).Int(nil)
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.DivMod(integer, big.NewInt(12), remainder)
	q, _ := new(big.Float).SetInt(quotient).Float64()
	return float64(year + q), int(remainder.Int64())
}

func dateDayFromYear(year float64) float64 {
	if math.Abs(year) < 1e7 {
		return 365*(year-1970) + math.Floor((year-1969)/4) - math.Floor((year-1901)/100) + math.Floor((year-1601)/400)
	}
	// Large finite Numbers still denote exact integers. Compute calendar days
	// without overflowing a Go int or rounding the individual leap-year terms.
	y, _ := new(big.Float).SetFloat64(year).Int(nil)
	days := new(big.Int).Sub(y, big.NewInt(1970))
	days.Mul(days, big.NewInt(365))
	for _, term := range []struct{ origin, divisor, sign int64 }{{1969, 4, 1}, {1901, 100, -1}, {1601, 400, 1}} {
		q := new(big.Int).Sub(y, big.NewInt(term.origin))
		q.Div(q, big.NewInt(term.divisor))
		if term.sign > 0 {
			days.Add(days, q)
		} else {
			days.Sub(days, q)
		}
	}
	result, _ := new(big.Float).SetInt(days).Float64()
	return result
}

func dateCivilMilliseconds(parts [7]float64) float64 {
	for i, part := range parts {
		if math.IsNaN(part) || math.IsInf(part, 0) {
			return math.NaN()
		}
		parts[i] = math.Trunc(part)
	}
	year, month := dateBalanceMonth(parts[0], parts[1])
	if math.IsInf(year, 0) {
		return math.NaN()
	}
	monthDays := [...]float64{0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334}
	day := float64(dateDayFromYear(year) + monthDays[month])
	if month >= 2 && (math.Mod(year, 400) == 0 || math.Mod(year, 4) == 0 && math.Mod(year, 100) != 0) {
		day++
	}
	day = float64(day + parts[2])
	day = float64(day - 1)
	// Explicit conversions require rounding at the specified Number operations,
	// including on architectures where Go may otherwise fuse multiply/add.
	clock := float64(float64(parts[3]*3600000) + float64(parts[4]*60000))
	clock = float64(clock + float64(parts[5]*1000))
	clock = float64(clock + parts[6])
	return float64(float64(day*86400000) + clock)
}

func dateLocalToUTC(wall float64, location *time.Location) float64 {
	if math.IsNaN(wall) || math.IsInf(wall, 0) || math.Abs(wall) > maxTime+2*86400000 {
		return math.NaN()
	}
	// Enumerate exact zone intervals instead of relying on time.Date's choice
	// in a fold/gap. A repeated wall time chooses the earlier instant; a skipped
	// wall time uses the offset before the transition.
	center := timeFromMsec(int64(wall))
	end := center.Add(48 * time.Hour)
	probe := center.Add(-48 * time.Hour)
	offsets := make(map[int]bool)
	gap := math.NaN()
	for {
		zoned := probe.In(location)
		_, before := zoned.Zone()
		offsets[before] = true
		_, next := zoned.ZoneBounds()
		if next.IsZero() || next.After(end) || !next.After(probe) {
			break
		}
		_, after := next.In(location).Zone()
		transition := float64(timeToMsec(next))
		if after > before && wall >= transition+float64(before)*1000 && wall < transition+float64(after)*1000 {
			gap = float64(wall - float64(before)*1000)
		}
		probe = next
	}
	result := math.Inf(1)
	for offset := range offsets {
		candidate := float64(wall - float64(offset)*1000)
		if math.Abs(candidate) > maxTime+2*86400000 {
			continue
		}
		_, actual := timeFromMsec(int64(candidate)).In(location).Zone()
		if float64(candidate+float64(actual)*1000) == wall && candidate < result {
			result = candidate
		}
	}
	if !math.IsInf(result, 1) {
		return result
	}
	return gap
}

func dateFromParts(parts [7]float64, utc bool, location *time.Location) (time.Time, bool) {
	milliseconds := dateCivilMilliseconds(parts)
	if !utc {
		milliseconds = dateLocalToUTC(milliseconds, location)
	}
	if clipped, ok := dateClip(milliseconds); ok {
		return timeFromMsec(clipped), true
	}
	return time.Time{}, false
}

func dateFullYear(year float64) float64 {
	integer := math.Trunc(year)
	if integer >= 0 && integer <= 99 {
		return integer + 1900
	}
	return integer
}

func dateConstructorParts(args []Value) [7]float64 {
	parts := [7]float64{1970, 0, 1, 0, 0, 0, 0}
	for i := 0; i < len(args) && i < len(parts); i++ {
		parts[i] = args[i].ToNumber().ToFloat()
	}
	parts[0] = dateFullYear(parts[0])
	return parts
}

func (r *Runtime) dateSetComponents(call FunctionCall, name string, first, count int, utc, legacyYear bool) Value {
	object := r.toObject(call.This)
	d, ok := object.self.(*dateObject)
	if !ok {
		panic(r.NewTypeError("Method Date.prototype.%s called on incompatible receiver", name))
	}
	// Snapshot before conversions; their side effects may mutate this Date.
	wasValid := d.isSet()
	parts := [7]float64{1970, 0, 1, 0, 0, 0, 0}
	if wasValid {
		base := d.timeUTC()
		if !utc {
			base = base.In(r.timeZone)
		}
		parts = [7]float64{float64(base.Year()), float64(base.Month() - 1), float64(base.Day()), float64(base.Hour()), float64(base.Minute()), float64(base.Second()), float64(base.Nanosecond() / 1e6)}
	}
	n := min(count, max(1, len(call.Arguments)))
	for i := 0; i < n; i++ {
		parts[first+i] = call.Argument(i).ToNumber().ToFloat()
	}
	if !wasValid && first != 0 {
		return _NaN
	}
	if legacyYear {
		parts[0] = dateFullYear(parts[0])
	}
	t, valid := dateFromParts(parts, utc, r.timeZone)
	if !valid {
		d.unset()
		return _NaN
	}
	return d.setTime(t)
}

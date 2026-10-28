package goya

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/totoer/goya/internal/tzdata"
)

func intlResolveTimeZone(identifier string) (string, *time.Location, bool) {
	text := strings.ReplaceAll(identifier, "−", "-")
	if len(text) > 0 && (text[0] == '+' || text[0] == '-') {
		var hours, minutes string
		switch len(text) {
		case 3:
			hours = text[1:3]
			minutes = "00"
		case 5:
			hours, minutes = text[1:3], text[3:5]
		case 6:
			if text[3] != ':' {
				return "", nil, false
			}
			hours, minutes = text[1:3], text[4:6]
		default:
			return "", nil, false
		}
		for _, digit := range hours + minutes {
			if digit < '0' || digit > '9' {
				return "", nil, false
			}
		}
		h, _ := strconv.Atoi(hours)
		m, _ := strconv.Atoi(minutes)
		if h > 23 || m > 59 {
			return "", nil, false
		}
		offset := h*60 + m
		if text[0] == '-' {
			offset = -offset
		}
		sign := "+"
		if offset < 0 {
			sign = "-"
		}
		canonical := fmt.Sprintf("%s%02d:%02d", sign, max(offset, -offset)/60, max(offset, -offset)%60)
		return canonical, time.FixedZone(canonical, offset*60), true
	}
	return tzdata.Lookup(identifier)
}

// SetTimeZone selects the shared Date/Intl default timezone from the embedded
// IANA catalog or an ISO minute offset (for example "+05:30"). New runtimes use
// UTC, independently of the operating system. Call this on the Runtime owner,
// like SetTimeSource. An invalid identifier leaves the setting unchanged.
func (r *Runtime) SetTimeZone(identifier string) error {
	name, location, ok := intlResolveTimeZone(identifier)
	if !ok {
		return fmt.Errorf("unknown timezone identifier %q", identifier)
	}
	r.timeZoneName, r.timeZone = name, location
	return nil
}

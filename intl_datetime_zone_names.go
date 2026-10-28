package goya

import (
	"fmt"
	"strings"
	"time"

	"github.com/totoer/goya/internal/cldrdata"
	"github.com/totoer/goya/internal/tzdata"
)

func intlZoneNode(node cldrdata.Node, name string) cldrdata.Node {
	for _, part := range strings.Split(name, "/") {
		node = node.Get(part)
	}
	return node
}
func intlZoneBoundary(text string) time.Time {
	if len(text) == 10 {
		text += " 00:00"
	}
	value, err := time.ParseInLocation("2006-01-02 15:04", text, time.UTC)
	if err != nil {
		panic("invalid embedded metazone boundary")
	}
	return value
}
func intlMetazone(name string, instant time.Time) string {
	data := cldrdata.Supplemental("metaZones").Get("metaZones", "metazoneInfo", "timezone")
	for _, alias := range tzdata.EquivalentNames(name) {
		uses := intlZoneNode(data, alias)
		for i := 0; i < uses.Len(); i++ {
			record := uses.At(i).Get("usesMetazone")
			from, to := record.Get("_from").String(), record.Get("_to").String()
			if from != "" && instant.Before(intlZoneBoundary(from)) {
				continue
			}
			if to != "" && !instant.Before(intlZoneBoundary(to)) {
				continue
			}
			return record.Get("_mzone").String()
		}
	}
	return ""
}
func intlGMTOffset(locale, numbering string, offset int, long bool) string {
	data := cldrdata.Locale(locale, "timeZoneNames").Get("dates", "timeZoneNames")
	if offset == 0 {
		if zero := data.Get("gmtZeroFormat").String(); zero != "" {
			return zero
		}
		return "GMT"
	}
	positive, negative, _ := strings.Cut(data.Get("hourFormat").String(), ";")
	pattern := positive
	if offset < 0 {
		pattern = negative
	}
	absolute := max(offset, -offset)
	hour, minute := absolute/3600, (absolute%3600)/60
	hours := fmt.Sprintf("%02d", hour)
	if !long {
		hours = fmt.Sprint(hour)
		if minute == 0 {
			pattern = strings.ReplaceAll(pattern, ":mm", "")
		}
	}
	pattern = strings.ReplaceAll(pattern, "HH", hours)
	pattern = strings.ReplaceAll(pattern, "H", hours)
	pattern = strings.ReplaceAll(pattern, "mm", fmt.Sprintf("%02d", minute))
	pattern = intlLocalizeDigits(pattern, numbering)
	return strings.ReplaceAll(data.Get("gmtFormat").String(), "{0}", pattern)
}
func intlTimeZoneDisplay(locale, numbering, name, style string, instant time.Time) string {
	_, offset := instant.Zone()
	long := strings.HasPrefix(style, "long")
	if strings.HasSuffix(style, "Offset") {
		return intlGMTOffset(locale, numbering, offset, long)
	}
	data := cldrdata.Locale(locale, "timeZoneNames").Get("dates", "timeZoneNames")
	width := "short"
	if long {
		width = "long"
	}
	variant := "standard"
	if strings.HasSuffix(style, "Generic") {
		variant = "generic"
	} else if instant.IsDST() {
		variant = "daylight"
	}
	for _, alias := range tzdata.EquivalentNames(name) {
		if text := intlZoneNode(data.Get("zone"), alias).Get(width, variant).String(); text != "" {
			return text
		}
	}
	if metazone := intlMetazone(name, instant); metazone != "" {
		if text := data.Get("metazone", metazone, width, variant).String(); text != "" {
			return text
		}
	}
	if name == "UTC" && style == "short" {
		return "UTC"
	}
	return intlGMTOffset(locale, numbering, offset, long)
}

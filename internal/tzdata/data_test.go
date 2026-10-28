package tzdata

import (
	"slices"
	"testing"
	"time"
)

func TestNamedZonesAndCanonicalAliases(t *testing.T) {
	for input, want := range map[string]string{"utc": "UTC", "Etc/UTC": "UTC", "Etc/GMT": "UTC", "GMT": "UTC", "US/Eastern": "America/New_York", "america/new_york": "America/New_York", "Europe/Bratislava": "Europe/Bratislava", "Atlantic/Reykjavik": "Atlantic/Reykjavik", "Asia/Calcutta": "Asia/Kolkata", "Europe/Kiev": "Europe/Kyiv"} {
		name, location, ok := Lookup(input)
		if !ok || name != want || location == nil {
			t.Errorf("%s: %s %v", input, name, ok)
		}
	}
	for _, name := range []string{"Local", "../etc/localtime", "/etc/localtime", "", "Unknown/Zone", "+01:00"} {
		if _, _, ok := Lookup(name); ok {
			t.Errorf("external or unknown lookup accepted: %s", name)
		}
	}
	names := Names()
	if len(names) < 400 || !slices.IsSorted(names) || !slices.Contains(names, "UTC") {
		t.Fatalf("primary catalog: %d", len(names))
	}
	names[0] = "changed"
	if Names()[0] == "changed" {
		t.Fatal("mutable timezone catalog")
	}
	regions := RegionNames("US")
	if !slices.Contains(regions, "America/New_York") {
		t.Fatal("missing country zones")
	}
	regions[0] = "changed"
	if RegionNames("US")[0] == "changed" {
		t.Fatal("mutable region catalog")
	}
}
func TestCurrentIANA2026eTransitions(t *testing.T) {
	for _, row := range []struct {
		name                           string
		year, month, day, hour, offset int
	}{
		{"America/New_York", 2026, 1, 1, 12, -5 * 3600}, {"America/New_York", 2026, 7, 1, 12, -4 * 3600},
		{"America/Vancouver", 2026, 12, 1, 12, -7 * 3600}, {"America/Edmonton", 2026, 12, 1, 12, -6 * 3600}, {"America/Yellowknife", 2026, 12, 1, 12, -6 * 3600}, {"America/Winnipeg", 2026, 12, 1, 12, -5 * 3600}, {"Africa/Casablanca", 2026, 12, 1, 12, 0},
		{"Europe/Dublin", 1925, 9, 21, 12, 0},
	} {
		_, location, ok := Lookup(row.name)
		if !ok {
			t.Fatal(row.name)
		}
		instant := time.Date(row.year, time.Month(row.month), row.day, row.hour, 0, 0, 0, time.UTC)
		_, offset := instant.In(location).Zone()
		if offset != row.offset {
			t.Errorf("%s %s: %d want %d", row.name, instant, offset, row.offset)
		}
	}
}
func TestEveryPrimaryZoneLoadsFromEmbeddedData(t *testing.T) {
	for _, name := range Names() {
		canonical, location, ok := Lookup(name)
		if !ok || canonical != name || location == nil {
			t.Errorf("primary zone failed: %s", name)
		}
	}
}

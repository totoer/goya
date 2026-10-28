package cldrdata

import (
	"sync"
	"testing"
)

func TestEmbeddedLocaleDocuments(t *testing.T) {
	if got := len(Locales()); got != 25 {
		t.Fatalf("data locales: %d", got)
	}
	for _, locale := range Locales() {
		for _, document := range []string{"numbers", "currencies", "ca-gregorian", "listPatterns", "units", "languages", "timeZoneNames"} {
			if !Locale(locale, document).Exists() {
				t.Errorf("missing %s/%s", locale, document)
			}
		}
	}
	if got := Locale("fr", "numbers").Get("numbers", "symbols-numberSystem-latn", "decimal").String(); got != "," {
		t.Fatalf("French decimal: %q", got)
	}
	if got := Locale("en", "listPatterns").Get("listPatterns", "listPattern-type-standard", "2").String(); got != "{0} and {1}" {
		t.Fatalf("English conjunction: %q", got)
	}
	if !Supplemental("plurals").Get("plurals-type-cardinal", "ru", "pluralRule-count-few").Exists() {
		t.Fatal("missing Russian plural rule")
	}
	keys := Locales()
	keys[0] = "changed"
	if Locales()[0] == "changed" {
		t.Fatal("keys exposed mutable state")
	}
	if Locale("unavailable", "numbers").Exists() || Locale("fr", "unavailable").Get("nested").Exists() {
		t.Fatal("unexpected fallback outside the data catalog")
	}
}

func TestIndependentLocaleReaders(t *testing.T) {
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 100 {
				if !Locale("ru", "ca-gregorian").Get("dates", "calendars", "gregorian", "months").Exists() {
					t.Error("missing immutable date data")
				}
			}
		})
	}
	workers.Wait()
}

package cldrdata

import "testing"

func TestCLDRPluralRules(t *testing.T) {
	for _, row := range []struct{ locale, kind, number, want string }{
		{"en", "cardinal", "1", "one"}, {"en", "cardinal", "1.0", "other"}, {"ru", "cardinal", "1", "one"}, {"ru", "cardinal", "2", "few"}, {"ru", "cardinal", "11", "many"}, {"ru", "cardinal", "21", "one"}, {"ru", "cardinal", "2.0", "other"}, {"ru", "cardinal", "1000000000000000000000000000001", "one"}, {"fr", "cardinal", "0", "one"}, {"fr", "cardinal", "1.0", "one"}, {"fr", "cardinal", "1000000", "many"}, {"ar", "cardinal", "0", "zero"}, {"ar", "cardinal", "2", "two"}, {"ar", "cardinal", "3", "few"}, {"ar", "cardinal", "11", "many"}, {"pl", "cardinal", "12", "many"}, {"pl", "cardinal", "22", "few"}, {"pt-PT", "cardinal", "1.0", "other"}, {"en", "ordinal", "1", "one"}, {"en", "ordinal", "2", "two"}, {"en", "ordinal", "3", "few"}, {"en", "ordinal", "11", "other"}, {"zz", "cardinal", "1", "other"},
	} {
		if got := PluralCategory(row.locale, row.kind, row.number, 0); got != row.want {
			t.Errorf("%s %s %s: %s want %s", row.locale, row.kind, row.number, got, row.want)
		}
	}
	if got := PluralCategory("fr", "cardinal", "1", 6); got != "many" {
		t.Fatalf("compact exponent: %s", got)
	}
}

package cldrdata

import (
	"math/big"
	"testing"
)

func TestExactDecimalRounding(t *testing.T) {
	for _, row := range []struct {
		input, mode, want string
		digits, increment int
	}{
		{"1.005", "halfExpand", "1.01", 2, 1}, {"1.025", "halfEven", "1.02", 2, 1}, {"1.075", "halfEven", "1.10", 2, 5}, {"-1.025", "halfCeil", "1.02", 2, 1}, {"-1.025", "halfFloor", "1.03", 2, 1}, {"-1.021", "floor", "1.03", 2, 1}, {"-1.021", "ceil", "1.02", 2, 1}, {"1.021", "expand", "1.03", 2, 1}, {"1.029", "trunc", "1.02", 2, 1}, {"1.025", "halfTrunc", "1.02", 2, 1}, {"9007199254740993.125", "halfExpand", "9007199254740993.13", 2, 1},
	} {
		d := DecimalFromString(row.input)
		r := d.Quantize(-row.digits, row.increment, row.mode)
		if got := r.Fixed(row.digits); got != row.want {
			t.Errorf("%s %s: %s want %s", row.input, row.mode, got, row.want)
		}
	}
}
func TestDecimalClassification(t *testing.T) {
	for input, want := range map[string]string{"": "0", " 42 ": "42", "1e309": "infinity", "-1e309": "infinity", "1e-99999": "0", "-0": "0", ".125": "0.125", "0xFF": "255", "0b11": "3", "0o77": "63", "0x1_f": "nan", "NaN": "nan", "+0x1": "nan", "1/2": "nan", "1.2.3": "nan", "0x": "nan", "\u00851": "nan"} {
		if got := DecimalFromString(input).Fixed(0); got != want {
			t.Errorf("%q: %s want %s", input, got, want)
		}
	}
	if !DecimalFromString("-1e-99999").Negative || !DecimalFromString("-0").Negative {
		t.Fatal("lost negative zero")
	}
	integer := new(big.Int).Exp(big.NewInt(10), big.NewInt(500), nil)
	value := DecimalFromInteger(integer)
	if value.Special != "" || value.Magnitude() != 500 {
		t.Fatal("BigInt was constrained to binary64")
	}
}
func TestSignificantAndPriorityRounding(t *testing.T) {
	for _, row := range []struct {
		input, kind, want                string
		minSig, maxSig, minFrac, maxFrac int
	}{
		{"0.0012", "significant", "0.00120", 3, 3, 0, 0}, {"1000", "significant", "1000.0", 5, 5, 0, 0}, {"999.99", "significant", "1000", 1, 3, 0, 0}, {"0", "significant", "0.00", 3, 3, 0, 0}, {"1.2345", "morePrecision", "1.235", 1, 4, 0, 2}, {"1.2345", "lessPrecision", "1.23", 1, 4, 0, 2},
		{"1.23", "morePrecision", "1.230", 4, 4, 0, 3}, {"1.23", "lessPrecision", "1.23", 4, 4, 0, 3},
	} {
		options := DecimalRounding{Type: row.kind, MinimumSignificant: row.minSig, MaximumSignificant: row.maxSig, MinimumFraction: row.minFrac, MaximumFraction: row.maxFrac, MinimumInteger: 1}
		if got := options.Round(DecimalFromString(row.input)).Text; got != row.want {
			t.Errorf("%s %s: %s want %s", row.input, row.kind, got, row.want)
		}
	}
}

func TestNumberAndMathematicalValueRounding(t *testing.T) {
	options := DecimalRounding{Type: "fraction", MinimumFraction: 2, MaximumFraction: 2, Increment: 1, Mode: "halfExpand"}
	if got := options.Round(DecimalFromFloat64(1.005)).Text; got != "1.00" {
		t.Fatalf("exact Number: %s", got)
	}
	if got := options.Round(DecimalFromString("1.005")).Text; got != "1.01" {
		t.Fatalf("decimal mathematical value: %s", got)
	}
}

package cldrdata

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Decimal represents the absolute integer Coefficient times 10^Exponent.
// Sign and non-finite values are separate so negative zero is preserved.
// Callers must not mutate Coefficient after sharing a Decimal.
type Decimal struct {
	Coefficient *big.Int
	Exponent    int
	Negative    bool
	Special     string // empty, "nan", or "infinity"
}

func DecimalFromInteger(integer *big.Int) Decimal {
	return Decimal{Coefficient: new(big.Int).Abs(integer), Negative: integer.Sign() < 0}
}
func decimalDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func DecimalFromString(input string) Decimal {
	// Whitespace trimming follows ECMAScript WhiteSpace/LineTerminator, not
	// the broader Go Unicode White_Space set (notably U+0085 is excluded).
	s := strings.TrimFunc(input, func(c rune) bool {
		return c == 0x9 || c == 0xb || c == 0xc || c == 0x20 || c == 0xa0 || c == 0xfeff || c == 0xa || c == 0xd || c == 0x2028 || c == 0x2029 || c == 0x1680 || c >= 0x2000 && c <= 0x200a || c == 0x202f || c == 0x205f || c == 0x3000
	})
	if s == "" {
		return Decimal{Coefficient: new(big.Int)}
	}
	if len(s) > 2 && s[0] == '0' {
		base := 0
		switch s[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			digits := s[2:]
			if digits == "" {
				return Decimal{Special: "nan"}
			}
			for _, c := range []byte(digits) {
				valid := c >= '0' && c < '0'+byte(min(base, 10)) || base == 16 && (c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F')
				if !valid {
					return Decimal{Special: "nan"}
				}
			}
			integer, ok := new(big.Int).SetString(digits, base)
			if !ok {
				return Decimal{Special: "nan"}
			}
			value := DecimalFromInteger(integer)
			f, _ := new(big.Float).SetInt(integer).Float64()
			if math.IsInf(f, 0) {
				return Decimal{Special: "infinity"}
			}
			return value
		}
	}
	negative := false
	unsigned := s
	if unsigned[0] == '+' || unsigned[0] == '-' {
		negative = unsigned[0] == '-'
		unsigned = unsigned[1:]
	}
	if unsigned == "Infinity" {
		return Decimal{Negative: negative, Special: "infinity"}
	}
	coefficient := unsigned
	exponent := 0
	if at := strings.IndexAny(coefficient, "eE"); at >= 0 {
		exponentText := coefficient[at+1:]
		coefficient = coefficient[:at]
		digits := exponentText
		if len(digits) > 0 && (digits[0] == '+' || digits[0] == '-') {
			digits = digits[1:]
		}
		if !decimalDigits(digits) {
			return Decimal{Special: "nan"}
		}
		// Overflowing exponents are clamped only for classification. No power
		// of ten is allocated until after binary64 overflow/underflow checks.
		parsed, err := strconv.ParseInt(exponentText, 10, 32)
		if err != nil {
			if strings.HasPrefix(exponentText, "-") {
				exponent = math.MinInt32
			} else {
				exponent = math.MaxInt32
			}
		} else {
			exponent = int(parsed)
		}
	}
	if at := strings.IndexByte(coefficient, '.'); at >= 0 {
		after := coefficient[at+1:]
		before := coefficient[:at]
		if before == "" && after == "" || before != "" && !decimalDigits(before) || after != "" && !decimalDigits(after) {
			return Decimal{Special: "nan"}
		}
		exponent -= len(after)
		coefficient = before + after
	}
	if !decimalDigits(coefficient) {
		return Decimal{Special: "nan"}
	}
	floatValue, err := strconv.ParseFloat(s, 64)
	if err != nil && err.(*strconv.NumError).Err != strconv.ErrRange {
		return Decimal{Special: "nan"}
	}
	if math.IsInf(floatValue, 0) {
		return Decimal{Negative: negative, Special: "infinity"}
	}
	if floatValue == 0 {
		return Decimal{Coefficient: new(big.Int), Negative: negative}
	}
	integer, _ := new(big.Int).SetString(coefficient, 10)
	return Decimal{Coefficient: integer, Exponent: exponent, Negative: negative}
}
func (d Decimal) IsZero() bool { return d.Special == "" && d.Coefficient.Sign() == 0 }
func (d Decimal) Magnitude() int {
	if d.IsZero() {
		return 0
	}
	return len(d.Coefficient.String()) + d.Exponent - 1
}
func (d Decimal) Scale(power int) Decimal {
	if d.Special == "" && !d.IsZero() {
		d.Exponent += power
	}
	return d
}
func decimalPower(power int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(power)), nil)
}

// Quantize rounds to an integer multiple of increment*10^place. It never
// converts decimal input or large integers through binary floating point.
func (d Decimal) Quantize(place, increment int, mode string) Decimal {
	if d.Special != "" {
		return d
	}
	numerator := new(big.Int).Set(d.Coefficient)
	denominator := big.NewInt(int64(increment))
	shift := d.Exponent - place
	if shift >= 0 {
		numerator.Mul(numerator, decimalPower(shift))
	} else {
		denominator.Mul(denominator, decimalPower(-shift))
	}
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)
	up := false
	if remainder.Sign() != 0 {
		switch mode {
		case "ceil":
			up = !d.Negative
		case "floor":
			up = d.Negative
		case "expand":
			up = true
		case "trunc":
		default:
			comparison := new(big.Int).Lsh(remainder, 1).Cmp(denominator)
			if comparison > 0 {
				up = true
			} else if comparison == 0 {
				switch mode {
				case "halfCeil":
					up = !d.Negative
				case "halfFloor":
					up = d.Negative
				case "halfTrunc":
				case "halfEven":
					up = quotient.Bit(0) != 0
				default:
					up = true
				}
			}
		}
	}
	if up {
		quotient.Add(quotient, big.NewInt(1))
	}
	quotient.Mul(quotient, big.NewInt(int64(increment)))
	return Decimal{Coefficient: quotient, Exponent: place, Negative: d.Negative}
}
func (d Decimal) Fixed(minimumFraction int) string {
	if d.Special != "" {
		return d.Special
	}
	digits := d.Coefficient.String()
	point := len(digits) + d.Exponent
	var integer, fraction string
	if point <= 0 {
		integer = "0"
		fraction = strings.Repeat("0", -point) + digits
	} else if point >= len(digits) {
		integer = digits + strings.Repeat("0", point-len(digits))
	} else {
		integer, fraction = digits[:point], digits[point:]
	}
	fraction = strings.TrimRight(fraction, "0")
	if len(fraction) < minimumFraction {
		fraction += strings.Repeat("0", minimumFraction-len(fraction))
	}
	if fraction != "" {
		return integer + "." + fraction
	}
	return integer
}

type DecimalRounding struct {
	MinimumInteger, MinimumFraction, MaximumFraction, MinimumSignificant, MaximumSignificant int
	Increment                                                                                int
	Mode, Type, TrailingZero                                                                 string
}
type RoundedDecimal struct {
	Value         Decimal
	Text          string
	Magnitude     int
	RoundingPlace int
}

func (options DecimalRounding) Round(value Decimal) RoundedDecimal {
	if value.Special != "" {
		return RoundedDecimal{Value: value, Text: value.Special}
	}
	if options.Increment == 0 {
		options.Increment = 1
	}
	if options.Mode == "" {
		options.Mode = "halfExpand"
	}
	fixedPlace := -options.MaximumFraction
	significantPlace := value.Magnitude() - options.MaximumSignificant + 1
	var significant Decimal
	if options.Type != "" && options.Type != "fraction" {
		significant = value.Quantize(significantPlace, 1, options.Mode)
		significantPlace = significant.Magnitude() - options.MaximumSignificant + 1
	}
	useSignificant := options.Type == "significant" || options.Type == "morePrecision" && significantPlace <= fixedPlace || options.Type == "lessPrecision" && significantPlace > fixedPlace
	place := fixedPlace
	minimumFraction := options.MinimumFraction
	var rounded Decimal
	if useSignificant {
		rounded = significant
		place = significantPlace
		minimumFraction = max(0, options.MinimumSignificant-1-rounded.Magnitude())
	} else {
		rounded = value.Quantize(fixedPlace, options.Increment, options.Mode)
	}
	if options.TrailingZero == "stripIfInteger" && rounded.IsInteger() {
		minimumFraction = 0
	}
	text := rounded.Fixed(minimumFraction)
	integer, _, _ := strings.Cut(text, ".")
	if len(integer) < options.MinimumInteger {
		text = strings.Repeat("0", options.MinimumInteger-len(integer)) + text
	}
	return RoundedDecimal{Value: rounded, Text: text, Magnitude: rounded.Magnitude(), RoundingPlace: place}
}
func (d Decimal) IsInteger() bool {
	if d.Special != "" {
		return false
	}
	if d.Exponent >= 0 || d.IsZero() {
		return true
	}
	remainder := new(big.Int).Mod(d.Coefficient, decimalPower(-d.Exponent))
	return remainder.Sign() == 0
}

// DecimalFromFloat64 uses the exact mathematical value of a finite Number.
// PluralRules applies FormatNumericToString directly to this value; NumberFormat
// instead uses ToIntlMathematicalValue's decimal-string conversion.
func DecimalFromFloat64(number float64) Decimal {
	bits := math.Float64bits(number)
	negative := bits>>63 != 0
	exponentBits := (bits >> 52) & 0x7ff
	mantissa := bits & ((uint64(1) << 52) - 1)
	if exponentBits == 0x7ff {
		if mantissa != 0 {
			return Decimal{Special: "nan"}
		}
		return Decimal{Special: "infinity", Negative: negative}
	}
	if exponentBits == 0 && mantissa == 0 {
		return Decimal{Coefficient: new(big.Int), Negative: negative}
	}
	exponent := -1074
	if exponentBits != 0 {
		mantissa |= uint64(1) << 52
		exponent = int(exponentBits) - 1023 - 52
	}
	coefficient := new(big.Int).SetUint64(mantissa)
	if exponent >= 0 {
		coefficient.Lsh(coefficient, uint(exponent))
		exponent = 0
	} else {
		coefficient.Mul(coefficient, new(big.Int).Exp(big.NewInt(5), big.NewInt(int64(-exponent)), nil))
	}
	return Decimal{Coefficient: coefficient, Exponent: exponent, Negative: negative}
}

func (d Decimal) Float64() float64 {
	if d.Special == "nan" {
		return math.NaN()
	}
	if d.Special == "infinity" {
		if d.Negative {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	f, _ := strconv.ParseFloat(d.Coefficient.String()+"e"+strconv.Itoa(d.Exponent), 64)
	if d.Negative {
		return -f
	}
	return f
}

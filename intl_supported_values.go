package goya

import (
	"sort"

	"github.com/totoer/goya/internal/cldrdata"
	"github.com/totoer/goya/internal/tzdata"
)

func (r *Runtime) intlSupportedValues(call FunctionCall) Value {
	key := call.Argument(0).toString().String()
	var values []string
	switch key {
	case "calendar":
		values = []string{"gregory", "iso8601"}
	case "collation":
		values = []string{"phonebk", "pinyin", "stroke"}
	case "currency":
		for _, code := range cldrdata.Locale("en", "currencies").Get("numbers", "currencies").Keys() {
			valid := len(code) == 3
			for _, c := range code {
				if c < 'A' || c > 'Z' {
					valid = false
				}
			}
			if valid {
				values = append(values, code)
			}
		}
	case "numberingSystem":
		values = append(values, intlNumberingSystems()...)
	case "timeZone":
		values = tzdata.Names()
	case "unit":
		for unit := range intlSimpleUnits {
			values = append(values, unit)
		}
	default:
		r.intlRangeError("invalid Intl.supportedValuesOf key")
	}
	sort.Strings(values)
	return r.intlStringArray(values)
}

package cldrdata

import "testing"

func TestCanonicalLocaleIdentifiers(t *testing.T) {
	for input, want := range map[string]string{"ja-Latn-heploc-hepburn-fonipa": "ja-Latn-alalc97-fonipa", "ja-hepburn-heploc": "ja-alalc97", "EN-us": "en-US", "iw-il": "he-IL", "sh": "sr-Latn", "sh-Cyrl": "sr-Cyrl", "cnr": "sr-ME", "en-BU": "en-MM", "hy-SU": "hy-AM", "zz-ZZ": "zz-ZZ", "en-u-nu-latn-ca-islamicc-kn-true": "en-u-ca-islamic-civil-kn-nu-latn", "de-u-co-phonebk-co-pinyin": "de-u-co-phonebk", "en-u-foo-foo-ca-gregory": "en-u-foo-ca-gregory", "en-x-u-a": "en-x-u-a", "en-t-de-DE-h0-hybrid": "en-t-de-de-h0-hybrid", "en-1996-fonipa": "en-1996-fonipa"} {
		id, ok := ParseLocale(input)
		if !ok || id.String() != want {
			t.Errorf("%q: %q %v want %q", input, id.String(), ok, want)
		}
	}
	for _, input := range []string{"", "root", "abcd", "en_US", "i-klingon", "en-GB-oed", "x-private", "en-u", "en-u-a1-foo", "en-t-de-1996-1996", "en-t-h0", "en-u-ca-gregory-u-nu-latn", "de-1996-1996", "en--US", "en-ä"} {
		if _, ok := ParseLocale(input); ok {
			t.Errorf("accepted invalid tag %q", input)
		}
	}
}
func TestLocaleLikelySubtags(t *testing.T) {
	for input, want := range map[string]string{"en": "en-Latn-US", "zh-TW": "zh-Hant-TW", "sr-Latn": "sr-Latn-RS", "und-Arab": "ar-Arab-EG", "zz": "zz-Latn-US"} {
		id, ok := ParseLocale(input)
		if !ok || id.Maximize().BaseName() != want {
			t.Errorf("maximize %s: %s", input, id.Maximize().BaseName())
		}
	}
	for input, want := range map[string]string{"en-Latn-US": "en", "en-Latn-GB": "en-GB", "zh-Hant-TW": "zh-TW", "sr-Latn-RS": "sr-Latn"} {
		id, _ := ParseLocale(input)
		if id.Minimize().BaseName() != want {
			t.Errorf("minimize %s: %s", input, id.Minimize().BaseName())
		}
	}
}

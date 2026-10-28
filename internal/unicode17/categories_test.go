package unicode17

import "testing"

func TestPinnedCategories(t *testing.T) {
	for code, want := range map[rune]string{'A': "Lu", 'a': "Ll", 0x10d50: "Lu", 0x10d70: "Ll", 0x10d40: "Nd", 0xd800: "Cs", 0x1f600: "So", 0x110000: "Cn", -1: "Cn"} {
		if actual := Category(code); actual != want {
			t.Errorf("%U: %s want %s", code, actual, want)
		}
	}
	for _, code := range []rune{'a', '1', '世', 0x10d50, 0x10d40} {
		if !IsWordLike(code) {
			t.Errorf("%U is not word-like", code)
		}
	}
	for _, code := range []rune{' ', '.', '_', 0x1f600, 0x301} {
		if IsWordLike(code) {
			t.Errorf("%U is word-like", code)
		}
	}
}

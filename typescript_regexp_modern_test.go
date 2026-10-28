package goya

import (
	"github.com/totoer/goya/internal/unicode17"
	"testing"
)

func TestCheckedRegExpUnicodeSetsAndModifiers(t *testing.T) {
	for name, source := range map[string]string{
		"intersection subtraction":    `const a=/^[\p{ASCII}&&\p{Letter}]+$/v,b=/^[[a-z]--[aeiou]]+$/v;a.test("abcZ")&&!a.test("é")&&b.test("bcdf")&&!b.test("cat");`,
		"nested complement":           `const f=/^[^[[a-z]&&[aeiou]]]$/v;f.test("b")&&f.test("😀")&&!f.test("a")&&/[^]/v.test(String.fromCharCode(0xd800));`,
		"ranges and escapes":          `const f=/^[\u{1f600}-\u{1f64f}\d\-\&]+$/v;f.test("😀1-&")&&!f.test("A")&&new RegExp("\\p{sc=Gara}","v").test("\u{10d50}")&&new RegExp("\\p{sc=Tayo}","u").test("\u{1e6c0}");`,
		"strings longest":             `const f=/[\q{a|abc|ab}]/v;f.exec("abcx")![0]==="abc"&&/[\q{|ab}]/v.exec("x")![0]===""&&/[\q{😀a|😀}]/v.exec("😀a")![0]==="😀a";`,
		"strings set operations":      `const a=/^[\q{ab|cd|a}&&\q{cd|ef|a}]$/v,b=/^[\q{ab|cd|a}--\q{cd|a}]$/v;a.test("cd")&&a.test("a")&&!a.test("ab")&&b.test("ab")&&!b.test("cd")&&/^[^[\q{ab|a}&&[a]]]$/v.test("b");`,
		"emoji families":              `const f=/^\p{RGI_Emoji}$/v;f.test("👩‍🚀")&&f.test("👨‍👩‍👧‍👦")&&f.test("🇺🇸")&&f.test("👍🏽")&&f.test("1️⃣")&&!f.test("😀😀")&&/^\p{Emoji_Keycap_Sequence}$/v.test("1️⃣")&&/^\p{RGI_Emoji_ZWJ_Sequence}$/v.test("👩‍🚀");`,
		"emoji subtraction":           `const f=/^[\p{RGI_Emoji}--\p{RGI_Emoji_Flag_Sequence}]$/v;f.test("👩‍🚀")&&!f.test("🇺🇸");`,
		"case fold before complement": `const f=/^[\P{Lowercase_Letter}]$/iv;!f.test("a")&&!f.test("A")&&f.test("1")&&/\P{Lowercase_Letter}/iu.test("a")&&/^[K]$/iv.test("K")&&/^[\p{Lowercase_Letter}&&[A-Z]]$/iv.test("A");`,
		"case folded strings":         `const f=/^[\q{ab|cd}&&\q{AB|EF}]$/iv;f.test("aB")&&!f.test("cd")&&/^[\q{\u{10d50}a}]$/iv.test("\u{10d70}A");`,
		"inline modifiers":            `const f=/^(?i:a)(?-i:b)$/;f.test("Ab")&&!f.test("AB")&&!f.ignoreCase&&/^(?s:a.b)$/.test("a\nb")&&/(?m:^b$)/.test("a\nb\nc");`,
		"scoped v folding":            `const f=/^(?i:[K])(?-i:[K])$/v;f.test("KK")&&!f.test("Kk")&&/^(?-i:[A-Z])$/iv.test("A")&&!/^(?-i:[A-Z])$/iv.test("a");`,
		"flags and copies":            `const f=/[a]/dgiv;const copy=new RegExp(f);f.unicodeSets&&!f.unicode&&f.hasIndices&&f.flags==="dgiv"&&copy.unicodeSets&&copy.flags===f.flags&&copy.source==="[a]"&&f.toString()==="/[a]/dgiv";`,
		"unicode advancement":         `const f=/(?:)/gv;const a=[..."😀".matchAll(f)];a.length===2&&a[0].index===0&&a[1].index===2&&"😀".split(/(?:)/v).length===1;`,
		"UTF16 string atoms":          `const text=String.fromCharCode(0xd800)+"a";const f=/[\q{\ud800a|\ud800}]/v;f.exec(text)![0]===text&&/[\ud800]/v.test(text);`,
	} {
		t.Run(name, func(t *testing.T) {
			value, err := New().RunString(source)
			if err != nil || !value.ToBoolean() {
				t.Fatalf("modern RegExp: %v %v", value, err)
			}
		})
	}
}
func TestCheckedRegExpDuplicateCaptures(t *testing.T) {
	for name, source := range map[string]string{
		"alternatives":         `const f=/(?<x>a)|(?<x>b)/d;const a=f.exec("a")!,b=f.exec("b")!;a.length===3&&a[1]==="a"&&a[2]===undefined&&a.groups!.x==="a"&&b[1]===undefined&&b[2]==="b"&&b.groups!.x==="b"&&a.indices!.groups!.x===a.indices![1]&&b.indices!.groups!.x===b.indices![2];`,
		"backreferences":       `const f=/^(?:(?<x>a)|(?<x>b))\k<x>$/;f.test("aa")&&f.test("bb")&&!f.test("ab");`,
		"capture clearing":     `const f=/^(?:(?<x>a)|(?<x>b))*\k<x>$/;const m=f.exec("abb")!;m[1]===undefined&&m[2]==="b"&&m.groups!.x==="b"&&/^(?:(a)|(b))*\1$/.test("ab");`,
		"replacement":          `"ab".replace(/(?<x>a)|(?<x>b)/g,"<$<x>>")==="<a><b>"&&"b".replace(/(?<x>a)|(?<x>b)/,(_match,a,b,_index,_input,groups)=>groups.x)==="b";`,
		"groups order":         `const f=/(?<x>a)|(?<y>b)(?<x>c)/;const m=f.exec("bc")!;Object.keys(m.groups!).join(",")==="x,y"&&m.groups!.x==="c"&&m.groups!.y==="b";`,
		"invalid early errors": `let errors=0;for(const pattern of ["(?<x>a)(?<x>b)","(?:(?<x>a)|b)(?<x>c)","(?x:a)","(?ii:a)","(?i-i:a)","[a&&b--c]","[^\\q{ab}]","\\P{RGI_Emoji}","[a--]"]){try{new RegExp(pattern,"v")}catch(e){if(e instanceof SyntaxError)errors++}}errors===9;`,
	} {
		t.Run(name, func(t *testing.T) {
			value, err := New().RunString(source)
			if err != nil || !value.ToBoolean() {
				t.Fatalf("RegExp captures: %v %v", value, err)
			}
		})
	}
}
func TestRegExpUnicode17StringProperties(t *testing.T) {
	for _, name := range []string{"Basic_Emoji", "Emoji_Keycap_Sequence", "RGI_Emoji_Modifier_Sequence", "RGI_Emoji_Flag_Sequence", "RGI_Emoji_Tag_Sequence", "RGI_Emoji_ZWJ_Sequence", "RGI_Emoji"} {
		t.Run(name, func(t *testing.T) {
			values, ok := unicode17.EmojiStrings(name)
			if !ok || len(values) == 0 {
				t.Fatal("missing official property")
			}
			pattern, err := compileRegexp("^\\p{"+name+"}$", "v")
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range values {
				input := newStringValue(value)
				match := pattern.findSubmatchIndex(input, 0)
				if len(match.indexes) == 0 || match.indexes[0] != 0 || match.indexes[1] != input.Length() {
					t.Fatalf("%U: %v", []rune(value), match.indexes)
				}
			}
			t.Logf("%d official sequences", len(values))
		})
	}
}

package goya

import (
	"bufio"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestIntlSegmenterUnicode17Conformance(t *testing.T) {
	for _, family := range []struct{ file, granularity string }{{"Grapheme", "grapheme"}, {"Word", "word"}, {"Sentence", "sentence"}} {
		t.Run(family.file, func(t *testing.T) {
			input, err := os.Open("testdata/unicode17/" + family.file + "BreakTest.txt")
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			scanner := bufio.NewScanner(input)
			cases := 0
			line := 0
			for scanner.Scan() {
				line++
				text, _, _ := strings.Cut(scanner.Text(), "#")
				fields := strings.Fields(text)
				if len(fields) == 0 {
					continue
				}
				var builder StringBuilder
				var ends []int
				length := 0
				for _, field := range fields {
					switch field {
					case "÷":
						if length > 0 {
							ends = append(ends, length)
						}
					case "×":
					default:
						code, err := strconv.ParseInt(field, 16, 32)
						if err != nil {
							t.Fatal(err)
						}
						builder.WriteRune(rune(code))
						length++
						if code > 0xffff {
							length++
						}
					}
				}
				spans := intlSegmentBoundaries(builder.String(), family.granularity, func() {})
				actual := make([]int, len(spans))
				for i, span := range spans {
					actual[i] = span.end
				}
				if !reflect.DeepEqual(actual, ends) {
					t.Fatalf("line %d: boundaries %v want %v", line, actual, ends)
				}
				cases++
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			if cases < 500 {
				t.Fatalf("incomplete official suite: %d", cases)
			}
			t.Logf("%d official Unicode 17 cases", cases)
		})
	}
}
func TestCheckedIntlSegmenter(t *testing.T) {
	for name, source := range map[string]string{
		"graphemes utf16":          `const text="A😀e\u0301👨‍👩‍👧‍👦\ud800Z";const s=new Intl.Segmenter("en").segment(text);const p=[...s];p.map(x=>x.index).join(",")==="0,1,3,5,16,17"&&p.map(x=>x.segment).join("")===text&&p.every(x=>x.input===text&&!("isWordLike" in x))&&p[4].segment==="\ud800";`,
		"word like":                `const text="Hello, 42 世界 👩‍🚀";const p=[...new Intl.Segmenter("en",{granularity:"word"}).segment(text)];p.filter(x=>x.isWordLike).map(x=>x.segment).join("|")==="Hello|42|世|界"&&p.filter(x=>!x.isWordLike).some(x=>x.segment==="👩‍🚀")&&p.map(x=>x.segment).join("")===text;`,
		"modern unicode letters":   `const p=[...new Intl.Segmenter("en",{granularity:"word"}).segment("\u{10d50}\u{10d51} \u{10d40}\u{10d41}")];p[0].isWordLike&&p[0].segment.length===4&&p[2].isWordLike;`,
		"sentences":                `const text="Hello! How are you? Fine.";const p=[...new Intl.Segmenter("en",{granularity:"sentence"}).segment(text)];p.map(x=>x.segment).join("|")==="Hello! |How are you? |Fine."&&p.map(x=>x.index).join(",")==="0,7,20";`,
		"containing":               `const s=new Intl.Segmenter().segment("A😀e\u0301");s.containing()!.segment==="A"&&s.containing(NaN)!.index===0&&s.containing(-0.3)!.index===0&&s.containing(2)!.segment==="😀"&&s.containing(4.9)!.index===3&&s.containing(-1)===undefined&&s.containing(Infinity)===undefined&&s.containing(5)===undefined&&s.containing(3)!==s.containing(3);`,
		"independent iterators":    `const s=new Intl.Segmenter().segment("AB");const a=s[Symbol.iterator](),b=s[Symbol.iterator]();a.next().value!.segment==="A"&&a.next().value!.segment==="B"&&a.next().done&&a.next().done&&b.next().value!.segment==="A"&&a[Symbol.iterator]()===a&&Object.prototype.toString.call(a)==="[object Segmenter String Iterator]";`,
		"iterator helpers":         `new Intl.Segmenter().segment("ABC")[Symbol.iterator]().map(x=>x.segment.toLowerCase()).toArray().join("")==="abc";`,
		"brands":                   `const f=new Intl.Segmenter(),s=f.segment("a"),i=s[Symbol.iterator]();let errors=0;for(const call of [()=>(Intl.Segmenter as any)(),()=>Intl.Segmenter.prototype.segment.call({},"a"),()=>s.containing.call(new Proxy(s,{}),0),()=>i.next.call({}),()=>s[Symbol.iterator].call({})]){try{call()}catch(e){if(e instanceof TypeError)errors++}}errors===5;`,
		"validation options order": `const seen:string[]=[];const opts=new Proxy({granularity:"word" as const},{get(o,k){seen.push(String(k));return Reflect.get(o,k)}});class S extends Intl.Segmenter{}const f=new S("DE-u-co-phonebk",opts);const o=f.resolvedOptions();let errors=0;for(const call of [()=>new Intl.Segmenter("en",{granularity:"letter" as any}),()=>new Intl.Segmenter("en",1 as any),()=>new Intl.Segmenter("bad_tag"),()=>f.segment(Symbol() as any)]){try{call()}catch(e){if(e instanceof TypeError||e instanceof RangeError)errors++}}f instanceof S&&o.locale==="de"&&o.granularity==="word"&&seen.join(",")==="localeMatcher,granularity"&&errors===4;`,
		"empty and coercion":       `const f=new Intl.Segmenter();[...f.segment("")].length===0&&f.segment("").containing(0)===undefined&&[...f.segment(123 as any)].map(x=>x.segment).join("")==="123"&&[...f.segment(undefined as any)].map(x=>x.segment).join("")==="undefined";`,
	} {
		t.Run(name, func(t *testing.T) {
			value, err := New().RunString(source)
			if err != nil || !value.ToBoolean() {
				t.Fatalf("Segmenter: %v %v", value, err)
			}
		})
	}
}

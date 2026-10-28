package goya

import "testing"

func TestCheckedIntlLocaleAndCollator(t *testing.T) {
	for name, source := range map[string]string{
		"locale compare delegation": `"item2".localeCompare("item10","en",{numeric:true})<0&&"ä".localeCompare("z","sv")>0;`,
		"locale casing":             `"Iİ".toLocaleLowerCase("tr")==="ıi"&&"iı".toLocaleUpperCase("tr")==="İI"&&"I\u0301".toLocaleLowerCase("lt")==="i\u0307\u0301"&&"ΟΣ".toLocaleLowerCase("el")==="ος";`,
		"locale casing surrogates":  `"I\ud800".toLocaleLowerCase("tr")==="ı\ud800"&&"\udc00i".toLocaleUpperCase("tr")==="\udc00İ";`,
		"casing locale errors":      `let rejected=false;try{"I".toLocaleLowerCase(["tr","en_US"])}catch(e){rejected=e instanceof RangeError}rejected;`,
		"canonical locales":         `Intl.getCanonicalLocales(["EN-us","en-US","iw-IL","sh"]).join(",")==="en-US,he-IL,sr-Latn";`,
		"unknown valid locale":      `Intl.getCanonicalLocales("zz-ZZ")[0]==="zz-ZZ"&&new Intl.Collator("zz-ZZ").resolvedOptions().locale==="en-US";`,
		"sparse locale list":        `const calls:string[]=[];const locales={length:3,0:{toString(){calls.push("zero");return "DE-de"}},2:"fr"};Intl.getCanonicalLocales(locales as any).join(",")==="de-DE,fr"&&calls.join(",")==="zero";`,
		"invalid locales":           `let errors=0;for(const x of ["","root","en_US","abcd","en-u","en-u-ca-gregory-u-kn","en-1996-1996"]){try{Intl.getCanonicalLocales(x)}catch(e){if(e instanceof RangeError)errors++}}try{Intl.getCanonicalLocales([42] as any)}catch(e){if(e instanceof TypeError)errors++}try{Intl.getCanonicalLocales(null as any)}catch(e){if(e instanceof TypeError)errors++}errors===9;`,
		"supported locales":         `Intl.Collator.supportedLocalesOf(["zz-ZZ","de-AT","ru-u-kn-true","sr-Latn-RS"]).join(",")==="de-AT,ru-u-kn,sr-Latn-RS";`,
		"base and accent":           `new Intl.Collator("de",{sensitivity:"base"}).compare("ä","a")===0&&new Intl.Collator("de",{sensitivity:"accent"}).compare("A","a")===0&&new Intl.Collator("de",{sensitivity:"accent"}).compare("ä","a")!==0;`,
		"swedish tailoring":         `new Intl.Collator("sv").compare("ä","z")>0&&new Intl.Collator("de").compare("ä","z")<0;`,
		"numeric":                   `const c=Intl.Collator("en-u-kn");c.compare("item2","item10")<0&&c.resolvedOptions().numeric&&new Intl.Collator("en",{numeric:false}).compare("item2","item10")>0;`,
		"modern unicode casing":     `const upper=String.fromCodePoint(0x10d50),lower=String.fromCodePoint(0x10d70);new Intl.Collator("en",{sensitivity:"base"}).compare(upper,lower)===0&&new Intl.Collator("en",{sensitivity:"accent"}).compare(upper,lower)===0&&new Intl.Collator("en",{caseFirst:"upper"}).compare(upper,lower)<0&&new Intl.Collator("en",{caseFirst:"lower"}).compare(upper,lower)>0;`,
		"case first":                `new Intl.Collator("en",{caseFirst:"upper"}).compare("A","a")<0&&new Intl.Collator("en",{caseFirst:"lower"}).compare("A","a")>0&&new Intl.Collator("en",{sensitivity:"case"}).compare("a","á")===0;`,
		"punctuation":               `new Intl.Collator("en",{ignorePunctuation:true}).compare("a-b c","abc")===0;`,
		"normalization":             `new Intl.Collator("fr").compare("é","e\u0301")===0;`,
		"phonebook":                 `const c=new Intl.Collator("de-u-co-phonebk");c.resolvedOptions().collation==="phonebk"&&c.compare("ä","ae")>0&&new Intl.Collator("de",{sensitivity:"base",collation:"phonebk"}).compare("ä","ae")===0;`,
		"extension override":        `const c=new Intl.Collator("en-u-kn-kf-upper",{numeric:false,caseFirst:"lower"});const o=c.resolvedOptions();o.locale==="en"&&!o.numeric&&o.caseFirst==="lower";`,
		"bound compare":             `const c=new Intl.Collator("en");const compare=c.compare;compare===c.compare&&compare.call(null,"a","b")<0&&compare.name===""&&compare.length===2&&Object.prototype.toString.call(c)==="[object Intl.Collator]";`,
		"subclass and brand":        `class C extends Intl.Collator{}const c=new C("en");let errors=0;for(const value of [{},new Proxy(c,{})]){try{Intl.Collator.prototype.resolvedOptions.call(value)}catch(e){if(e instanceof TypeError)errors++}}c instanceof C&&errors===2&&Intl.Collator.length===0;`,
		"options conversion":        `let count=0;const c=new Intl.Collator("en",{numeric:{valueOf(){count++;return 0}} as any});c.resolvedOptions().numeric&&count===0&&new Intl.Collator("en",42 as any).resolvedOptions().usage==="sort";`,
		"option errors":             `let errors=0;for(const options of [{sensitivity:"wrong"},{usage:"wrong"},{localeMatcher:"wrong"},{caseFirst:"wrong"},{collation:"invalid_thing"},null]){try{new Intl.Collator("en",options as any)}catch(e){if(e instanceof TypeError||e instanceof RangeError)errors++}}errors===6;`,
		"conversion order":          `const calls:string[]=[];const options=new Proxy({},{get(_,name){calls.push(String(name));return undefined}});new Intl.Collator({length:1,0:{toString(){calls.push("locale");return "en"}}} as any,options);calls.join(",")==="locale,usage,localeMatcher,collation,numeric,caseFirst,sensitivity,ignorePunctuation";`,
	} {
		t.Run(name, func(t *testing.T) {
			value, err := New().RunString(source)
			if err != nil || !value.ToBoolean() {
				t.Fatalf("Intl: %v %v", value, err)
			}
		})
	}
}

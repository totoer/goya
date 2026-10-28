package goya

import "testing"

func TestCheckedIntlLocale(t *testing.T) {
	for name, source := range map[string]string{
		"canonical":                   `const l=new Intl.Locale("SH-rs-u-nu-arab-ca-islamicc-kn-yes");l.toString()==="sr-Latn-RS-u-ca-islamic-civil-kn-nu-arab"&&l.baseName==="sr-Latn-RS"&&l.language==="sr"&&l.script==="Latn"&&l.region==="RS"&&l.calendar==="islamic-civil"&&l.numeric&&l.numberingSystem==="arab";`,
		"compound aliases":            `new Intl.Locale("ja-Latn-heploc-hepburn-fonipa").baseName==="ja-Latn-alalc97-fonipa"&&Intl.getCanonicalLocales("ja-hepburn-heploc")[0]==="ja-alalc97";`,
		"options":                     `const l=new Intl.Locale("en-US-u-ca-gregory-kn",{language:"sh",region:"me",calendar:"islamicc",numeric:false,hourCycle:"h23",caseFirst:"upper",numberingSystem:"arab"});l.toString()==="sr-Latn-ME-u-ca-islamic-civil-hc-h23-kf-upper-kn-false-nu-arab"&&!l.numeric&&l.hourCycle==="h23";`,
		"maximize minimize":           `const l=new Intl.Locale("zh-TW-u-ca-gregory-x-test");const m=l.maximize();m.toString()==="zh-Hant-TW-u-ca-gregory-x-test"&&m.minimize().toString()==="zh-TW-u-ca-gregory-x-test"&&l.toString()==="zh-TW-u-ca-gregory-x-test"&&m!==l;`,
		"internal identifier":         `const l=new Intl.Locale("de-u-co-phonebk");l.toString=()=>{throw Error("override")};new Intl.Locale(l).baseName==="de"&&Intl.getCanonicalLocales(l as any)[0]==="de-u-co-phonebk"&&Intl.NumberFormat.supportedLocalesOf([l])[0]==="de-u-co-phonebk";`,
		"unknown well formed":         `const l=new Intl.Locale("zz-Zzzz-ZZ-u-ca-unknown-co-other-hc-abc-kf-other-kn-unknown-nu-foobar");l.calendar==="unknown"&&l.hourCycle===("abc" as any)&&!l.numeric&&l.numberingSystem==="foobar";`,
		"invalid":                     `let errors=0;for(const f of [()=>new Intl.Locale("en_US"),()=>new Intl.Locale("en",{language:"en-US"}),()=>new Intl.Locale("en",{script:"Latin"}),()=>new Intl.Locale("en",{region:"USA"}),()=>new Intl.Locale("en",{hourCycle:"H23" as any}),()=>new Intl.Locale("en",{calendar:"ab"}),()=>new Intl.Locale(1 as any),()=>new Intl.Locale("bad_tag",null as any)]){try{f()}catch(e){if(e instanceof RangeError||e instanceof TypeError)errors++}}errors===8;`,
		"getter order":                `const seen:string[]=[];const options=new Proxy({},{get(_,key){seen.push(String(key));return undefined}});new Intl.Locale("en",options);seen.join(",")==="language,script,region,calendar,collation,hourCycle,caseFirst,numeric,numberingSystem";`,
		"locale info":                 `const us=new Intl.Locale("en-US") as any,gb=new Intl.Locale("en-GB") as any;const a=us.getWeekInfo(),b=gb.getWeekInfo();a.firstDay===7&&a.minimalDays===1&&a.weekend.join(",")==="6,7"&&b.firstDay===1&&b.minimalDays===4&&(new Intl.Locale("en-US-u-fw-mon") as any).getWeekInfo().firstDay===1&&us.getTimeZones().includes("America/New_York")&&(new Intl.Locale("en") as any).getTimeZones()===undefined&&(new Intl.Locale("ar") as any).getTextInfo().direction==="rtl";`,
		"fresh info arrays":           `const l=new Intl.Locale("de") as any;const a=l.getCollations();a.length=0;const w=l.getWeekInfo();w.weekend.length=0;l.getCollations().includes("phonebk")&&l.getWeekInfo().weekend.length===2&&l.getCalendars().includes("gregory")&&l.getHourCycles().includes("h23")&&l.getNumberingSystems().includes("latn");`,
		"brands subclass descriptors": `class L extends Intl.Locale{}const l=new L("en");let errors=0;for(const f of [()=>(Intl.Locale as any)("en"),()=>Intl.Locale.prototype.toString.call({}),()=>Intl.Locale.prototype.maximize.call(new Proxy(l,{})),()=>Object.getOwnPropertyDescriptor(Intl.Locale.prototype,"baseName")!.get!.call({})]){try{f()}catch(e){if(e instanceof TypeError)errors++}}l instanceof L&&errors===4&&Object.prototype.toString.call(l)==="[object Intl.Locale]"&&!Object.getOwnPropertyDescriptor(Intl.Locale.prototype,"language")!.enumerable&&!("supportedLocalesOf" in Intl.Locale);`,
	} {
		t.Run(name, func(t *testing.T) {
			value, err := New().RunString(source)
			if err != nil || !value.ToBoolean() {
				t.Fatalf("Locale: %v %v", value, err)
			}
		})
	}
}
func TestCheckedIntlDisplayNames(t *testing.T) {
	for name, source := range map[string]string{
		"languages dialect":           `const f=new Intl.DisplayNames("en",{type:"language"});f.of("en-US")==="American English"&&f.of("de")==="German"&&f.of("iw")==="Hebrew"&&f.of("sr-Latn-RS")==="Serbian (Latin, Serbia)";`,
		"languages standard":          `const f=new Intl.DisplayNames("en",{type:"language",languageDisplay:"standard"});f.of("en-US")==="English (United States)"&&f.resolvedOptions().languageDisplay==="standard";`,
		"localized names":             `new Intl.DisplayNames("ru",{type:"region"}).of("de")==="Германия"&&new Intl.DisplayNames("fr",{type:"language"}).of("de")==="allemand"&&new Intl.DisplayNames("en",{type:"script"}).of("lAtN")==="Latin";`,
		"styles":                      `new Intl.DisplayNames("en",{type:"region",style:"short"}).of("US")==="US"&&new Intl.DisplayNames("en",{type:"region",style:"long"}).of("US")==="United States";`,
		"currencies calendars fields": `new Intl.DisplayNames("en",{type:"currency"}).of("usd")==="US Dollar"&&new Intl.DisplayNames("en",{type:"calendar"}).of("gregory")==="Gregorian Calendar"&&new Intl.DisplayNames("en",{type:"dateTimeField"}).of("weekOfYear")==="week"&&new Intl.DisplayNames("en",{type:"dateTimeField",style:"short"}).of("second")==="sec.";`,
		"fallback":                    `const none=new Intl.DisplayNames("en",{type:"currency",fallback:"none"}),code=new Intl.DisplayNames("en",{type:"currency"});none.of("QQQ")===undefined&&code.of("qQq")==="QQQ"&&new Intl.DisplayNames("en",{type:"language",fallback:"none"}).of("zz-US")===undefined&&new Intl.DisplayNames("en",{type:"language"}).of("zz-US")==="zz-US";`,
		"validation":                  `let errors=0;for(const f of [()=>new (Intl.DisplayNames as any)("en"),()=>new Intl.DisplayNames("en",{} as any),()=>new Intl.DisplayNames("en",1 as any),()=>new Intl.DisplayNames("en",{type:"region"}).of("USA"),()=>new Intl.DisplayNames("en",{type:"script"}).of("latin"),()=>new Intl.DisplayNames("en",{type:"language"}).of("en-u-ca-gregory"),()=>new Intl.DisplayNames("en",{type:"calendar"}).of("gregorian"),()=>new Intl.DisplayNames("en",{type:"dateTimeField"}).of("Year")]){try{f()}catch(e){if(e instanceof TypeError||e instanceof RangeError)errors++}}errors===8;`,
		"coercion and options order":  `const seen:string[]=[];const f=new Intl.DisplayNames("en",new Proxy({type:"region" as const},{get(o,k){seen.push(String(k));return Reflect.get(o,k)}}));f.of({toString(){seen.push("code");return "us"}} as any)==="United States"&&seen.join(",")==="localeMatcher,style,type,fallback,languageDisplay,code";`,
		"brands and result":           `class D extends Intl.DisplayNames{}const d=new D("en",{type:"region"});let errors=0;for(const f of [()=>(Intl.DisplayNames as any)("en",{type:"region"}),()=>Intl.DisplayNames.prototype.of.call({},"US"),()=>Intl.DisplayNames.prototype.of.call(new Proxy(d,{}),"US")]){try{f()}catch(e){if(e instanceof TypeError)errors++}}d instanceof D&&errors===3&&Object.prototype.toString.call(d)==="[object Intl.DisplayNames]"&&Object.keys(d.resolvedOptions()).join(",")==="locale,style,type,fallback";`,
	} {
		t.Run(name, func(t *testing.T) {
			value, err := New().RunString(source)
			if err != nil || !value.ToBoolean() {
				t.Fatalf("DisplayNames: %v %v", value, err)
			}
		})
	}
}
func TestCheckedIntlListFormat(t *testing.T) {
	for name, source := range map[string]string{
		"english":                   `const f=new Intl.ListFormat("en");f.format([])===""&&f.format(["A"])==="A"&&f.format(["A","B"])==="A and B"&&f.format(["A","B","C"])==="A, B, and C"&&f.format(["A","B","C","D"])==="A, B, C, and D";`,
		"styles and types":          `new Intl.ListFormat("en",{style:"short"}).format(["A","B"])==="A & B"&&new Intl.ListFormat("en",{type:"disjunction"}).format(["A","B"])==="A or B"&&new Intl.ListFormat("en",{type:"unit",style:"narrow"}).format(["A","B","C"])==="A B C";`,
		"locales":                   `new Intl.ListFormat("de").format(["A","B"])==="A und B"&&new Intl.ListFormat("ru").format(["А","Б"])==="А и Б"&&new Intl.ListFormat("ja").format(["A","B"])==="A、B";`,
		"context forms":             `new Intl.ListFormat("es").format(["a","iglesia"])==="a e iglesia"&&new Intl.ListFormat("es").format(["a","hielo"])==="a y hielo"&&new Intl.ListFormat("es",{type:"disjunction"}).format(["a","ocho"])==="a u ocho"&&new Intl.ListFormat("he").format(["א","B"])==="א ו-B";`,
		"parts":                     `const f=new Intl.ListFormat("en");const p=f.formatToParts(["A","B","C"]);p.map(x=>x.type).join(",")==="element,literal,element,literal,element"&&p.map(x=>x.value).join("")===f.format(["A","B","C"])&&p[3].value===", and "&&Object.keys(p[0]).join(",")==="type,value";`,
		"utf16":                     `const f=new Intl.ListFormat("en");const p=f.formatToParts(["","\ud800","\udc00","😀"]);p.filter(x=>x.type==="element").map(x=>x.value).join("")==="\ud800\udc00😀"&&f.format(["\ud800","X"])==="\ud800 and X";`,
		"iterable":                  `const f=new Intl.ListFormat("en");f.format(new Set(["A","B"]))==="A and B"&&f.format("😀x")==="😀 and x"&&(f.format as any)()===""&&(f.formatToParts as any)().length===0;`,
		"iterator close type error": `let closed=0,converted=0;const input={*[Symbol.iterator](){try{yield "A";yield {toString(){converted++;return "B"}}}finally{closed++}}};let caught=false;try{new Intl.ListFormat("en").format(input as any)}catch(e){caught=e instanceof TypeError}caught&&closed===1&&converted===0;`,
		"next error no close":       `let closed=0;const marker={};const input={[Symbol.iterator](){return {next(){throw marker},return(){closed++;return {done:true}}}}};let caught=false;try{new Intl.ListFormat("en").format(input as any)}catch(e){caught=e===marker}caught&&closed===0;`,
		"options and brands":        `class L extends Intl.ListFormat{}const l=new L("en",{type:"unit",style:"short"});let errors=0;for(const f of [()=>(Intl.ListFormat as any)("en"),()=>new Intl.ListFormat("en",1 as any),()=>Intl.ListFormat.prototype.format.call({},[]),()=>Intl.ListFormat.prototype.format.call(new Proxy(l,{}),[]),()=>l.format(["a",1] as any)]){try{f()}catch(e){if(e instanceof TypeError)errors++}}l instanceof L&&errors===5&&Object.prototype.toString.call(l)==="[object Intl.ListFormat]"&&Object.keys(l.resolvedOptions()).join(",")==="locale,type,style";`,
	} {
		t.Run(name, func(t *testing.T) {
			value, err := New().RunString(source)
			if err != nil || !value.ToBoolean() {
				t.Fatalf("ListFormat: %v %v", value, err)
			}
		})
	}
}
func TestCheckedIntlNamesListAllLocales(t *testing.T) {
	source := `const locales=["en","en-GB","de","fr","ru","sr","sr-Latn","ar","ja","zh","zh-Hant","hi","es","tr","ko","pt","pt-PT","it","nl","sv","pl","uk","he","id","th"];for(const locale of locales){for(const style of ["long","short","narrow"] as const){for(const type of ["conjunction","disjunction","unit"] as const){const f=new Intl.ListFormat(locale,{style,type});const parts=f.formatToParts(["A","B","C","D"]);if(parts.map(p=>p.value).join("")!==f.format(["A","B","C","D"])||parts.filter(p=>p.type==="element").map(p=>p.value).join("")!=="ABCD")throw Error(locale+":"+type+":"+style)}for(const type of ["language","script","region","currency","calendar","dateTimeField"] as const){const code={language:"en-US",script:"Latn",region:"US",currency:"USD",calendar:"gregory",dateTimeField:"year"}[type];const text=new Intl.DisplayNames(locale,{style,type,fallback:"none"}).of(code);if(!text||text.includes("{"))throw Error(locale+":"+type+":"+style)}}}true;`
	value, err := New().RunString(source)
	if err != nil || !value.ToBoolean() {
		t.Fatalf("locale data: %v %v", value, err)
	}
}

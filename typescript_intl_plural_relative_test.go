package goya

import "testing"

func TestCheckedIntlPluralRules(t *testing.T) {
	for name, source := range map[string]string{
		"cardinal locales":            `const en=new Intl.PluralRules("en"),ru=new Intl.PluralRules("ru"),ar=new Intl.PluralRules("ar");en.select(1)==="one"&&en.select(2)==="other"&&ru.select(2)==="few"&&ru.select(11)==="many"&&ru.select(21)==="one"&&ar.select(0)==="zero"&&ar.select(2)==="two"&&ar.select(3)==="few";`,
		"ordinal":                     `const p=new Intl.PluralRules("en",{type:"ordinal"});p.select(1)==="one"&&p.select(2)==="two"&&p.select(3)==="few"&&p.select(11)==="other";`,
		"visible fraction":            `new Intl.PluralRules("en",{minimumFractionDigits:1}).select(1)==="other"&&new Intl.PluralRules("ru",{maximumFractionDigits:0}).select(1.2)==="one";`,
		"exact number rounding":       `new Intl.PluralRules("en",{minimumFractionDigits:0,maximumFractionDigits:2}).select(1.005)==="one";`,
		"non finite":                  `const p=new Intl.PluralRules("en") as Intl.PluralRules & {selectRange(start:number,end:number):Intl.LDMLPluralRule};p.select(NaN)==="other"&&p.select(Infinity)==="other"&&p.select(-Infinity)==="other";`,
		"rounding options":            `const p=new Intl.PluralRules("en",{minimumFractionDigits:2,maximumFractionDigits:2,roundingIncrement:5,roundingMode:"halfEven",trailingZeroDisplay:"stripIfInteger"} as Intl.PluralRulesOptions & Intl.NumberFormatOptions);const o=p.resolvedOptions() as Intl.ResolvedPluralRulesOptions & Intl.ResolvedNumberFormatOptions;p.select(1.001)==="one"&&o.roundingIncrement===5&&o.roundingMode==="halfEven"&&o.trailingZeroDisplay==="stripIfInteger";`,
		"range categories":            `const p=new Intl.PluralRules("en") as Intl.PluralRules & {selectRange(start:number,end:number):Intl.LDMLPluralRule};p.selectRange(1,1)==="one"&&p.selectRange(1,2)==="other"&&p.selectRange(1.0001,1.0002)==="one"&&p.selectRange(Infinity,Infinity)==="other";`,
		"range validation":            `const p=new Intl.PluralRules("en") as Intl.PluralRules & {selectRange(start:number,end:number):Intl.LDMLPluralRule};let errors=0;for(const f of [()=>p.selectRange(NaN,1),()=>p.selectRange(1,NaN),()=>(p.selectRange as any)(undefined,1),()=>p.select(1n as any)]){try{f()}catch(e){if(e instanceof RangeError||e instanceof TypeError)errors++}}errors===4;`,
		"resolved categories copies":  `const p=new Intl.PluralRules("ru");const first=p.resolvedOptions();first.pluralCategories.length=0;p.resolvedOptions().pluralCategories.join(",")==="one,few,many,other"&&p.resolvedOptions().locale==="ru";`,
		"subclass brands constructor": `class P extends Intl.PluralRules{}const p=new P("en");let errors=0;for(const f of [()=>(Intl.PluralRules as any)("en"),()=>Intl.PluralRules.prototype.select.call({},1),()=>Intl.PluralRules.prototype.select.call(new Proxy(p,{}),1)]){try{f()}catch(e){if(e instanceof TypeError)errors++}}p instanceof P&&errors===3&&Object.prototype.toString.call(p)==="[object Intl.PluralRules]";`,
	} {
		t.Run(name, func(t *testing.T) {
			value, err := New().RunString(source)
			if err != nil || !value.ToBoolean() {
				t.Fatalf("PluralRules: %v %v", value, err)
			}
		})
	}
}
func TestCheckedIntlRelativeTimeFormat(t *testing.T) {
	for name, source := range map[string]string{
		"basic":                   `const f=new Intl.RelativeTimeFormat("en");f.format(-1,"day")==="1 day ago"&&f.format(2,"days")==="in 2 days"&&f.format(-0,"second")==="0 seconds ago";`,
		"auto":                    `const f=new Intl.RelativeTimeFormat("en",{numeric:"auto"});f.format(-1,"day")==="yesterday"&&f.format(0,"day")==="today"&&f.format(1,"day")==="tomorrow"&&f.format(1,"year")==="next year"&&f.format(-2,"day")==="2 days ago";`,
		"locale plurals":          `const f=new Intl.RelativeTimeFormat("ru");f.format(2,"day")==="через 2 дня"&&f.format(5,"day")==="через 5 дней"&&new Intl.RelativeTimeFormat("de",{numeric:"auto"}).format(-1,"day")==="gestern";`,
		"short narrow":            `new Intl.RelativeTimeFormat("en",{style:"short"}).format(2,"quarter")==="in 2 qtrs."&&new Intl.RelativeTimeFormat("en",{style:"narrow"}).resolvedOptions().style==="narrow";`,
		"parts":                   `const f=new Intl.RelativeTimeFormat("en");const p=f.formatToParts(1234.5,"days");p.map(x=>x.value).join("")===f.format(1234.5,"day")&&p.filter(x=>x.type!=="literal").every(x=>"unit" in x&&x.unit==="day")&&p.filter(x=>x.type==="literal").every(x=>!("unit" in x));`,
		"auto parts":              `const p=new Intl.RelativeTimeFormat("en",{numeric:"auto"}).formatToParts(-1,"day");p.length===1&&p[0].type==="literal"&&p[0].value==="yesterday"&&!("unit" in p[0]);`,
		"numbering":               `const f=new Intl.RelativeTimeFormat("en-u-nu-arab");f.format(2,"day")==="in ٢ days"&&f.resolvedOptions().numberingSystem==="arab";`,
		"all units":               `const f=new Intl.RelativeTimeFormat("en");["year","quarter","month","week","day","hour","minute","second"].every(unit=>f.format(2,unit as Intl.RelativeTimeFormatUnit).startsWith("in 2 "));`,
		"validation":              `const f=new Intl.RelativeTimeFormat("en");let errors=0;for(const call of [()=>f.format(Infinity,"day"),()=>f.format(NaN,"day"),()=>f.format(1,"Day" as any),()=>f.format(1,"millisecond" as any),()=>f.format(1,Symbol() as any)]){try{call()}catch(e){if(e instanceof TypeError||e instanceof RangeError)errors++}}errors===5;`,
		"conversion order":        `const seen:string[]=[];const result=new Intl.RelativeTimeFormat("en").format({valueOf(){seen.push("value");return 1}} as any,{toString(){seen.push("unit");return "day"}} as any);result==="in 1 day"&&seen.join(",")==="value,unit";`,
		"brands and constructors": `class F extends Intl.RelativeTimeFormat{}const f=new F("en");let errors=0;for(const call of [()=>(Intl.RelativeTimeFormat as any)("en"),()=>Intl.RelativeTimeFormat.prototype.format.call({},1,"day"),()=>Intl.RelativeTimeFormat.prototype.format.call(new Proxy(f,{}),1,"day")]){try{call()}catch(e){if(e instanceof TypeError)errors++}}f instanceof F&&errors===3&&Object.prototype.toString.call(f)==="[object Intl.RelativeTimeFormat]";`,
	} {
		t.Run(name, func(t *testing.T) {
			value, err := New().RunString(source)
			if err != nil || !value.ToBoolean() {
				t.Fatalf("RelativeTimeFormat: %v %v", value, err)
			}
		})
	}
}

func TestCheckedIntlAdvertisedLocaleData(t *testing.T) {
	value, err := New().RunString(`const locales: string[] = ["en", "en-GB", "de", "fr", "ru", "sr", "sr-Latn", "ar", "ja", "zh", "zh-Hant", "hi", "es", "tr", "ko", "pt", "pt-PT", "it", "nl", "sv", "pl", "uk", "he", "id", "th"];
    const units: Intl.RelativeTimeFormatUnit[]=["year","quarter","month","week","day","hour","minute","second"];
    for(const locale of locales){for(const style of ["long","short","narrow"] as const){const f=new Intl.RelativeTimeFormat(locale,{style});for(const unit of units){for(const n of [-2,-1,0,1,2,11,1234.5]){const text=f.format(n,unit);if(text.length===0||text.includes("{0}"))throw Error(locale+":"+unit+":"+style)}}}}
    true;`)
	if err != nil || !value.ToBoolean() {
		t.Fatalf("advertised locale data: %v %v", value, err)
	}
}

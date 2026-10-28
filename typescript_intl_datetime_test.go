package goya

import (
	"testing"
	"time"
)

func TestCheckedIntlDateTimeFormat(t *testing.T) {
	for name, source := range map[string]string{
		"default and numeric widths":        `const f=Intl.DateTimeFormat("en",{timeZone:"UTC"});const g=new Intl.DateTimeFormat("en",{timeZone:"UTC",year:"numeric",month:"2-digit",day:"2-digit"});f.format(0)==="1/1/1970"&&g.format(0)==="01/01/1970"&&new Intl.DateTimeFormat("en-GB",{timeZone:"UTC"}).format(0)==="01/01/1970";`,
		"locale month names":                `const d=Date.UTC(2020,6,2);new Intl.DateTimeFormat("en",{timeZone:"UTC",year:"numeric",month:"long",day:"numeric"}).format(d)==="July 2, 2020"&&new Intl.DateTimeFormat("ru",{timeZone:"UTC",month:"long",day:"numeric"}).format(d)==="2 июля";`,
		"month standalone":                  `new Intl.DateTimeFormat("ru",{timeZone:"UTC",month:"long"}).format(Date.UTC(2020,6,2))==="июль";`,
		"weekday and era":                   `const f=new Intl.DateTimeFormat("en",{timeZone:"UTC",weekday:"long",era:"short",year:"numeric",month:"long",day:"numeric"});const p=f.formatToParts(0);p.some(x=>x.type==="weekday"&&x.value==="Thursday")&&p.some(x=>x.type==="era"&&x.value==="AD");`,
		"24 hour and fraction parts":        `const f=new Intl.DateTimeFormat("en",{timeZone:"UTC",hourCycle:"h23",hour:"2-digit",minute:"2-digit",second:"2-digit",fractionalSecondDigits:3});const p=f.formatToParts(123);f.format(123)==="00:00:00.123"&&p.some(x=>x.type==="fractionalSecond"&&x.value==="123")&&p.map(x=>x.value).join("")===f.format(123);`,
		"hour cycles":                       `const hour=(hourCycle:Intl.DateTimeFormatOptions["hourCycle"])=>new Intl.DateTimeFormat("en",{timeZone:"UTC",hourCycle,hour:"numeric"}).formatToParts(0).find(x=>x.type==="hour")!.value;hour("h11")==="0"&&hour("h12")==="12"&&Number(hour("h23"))===0&&Number(hour("h24"))===24;`,
		"hour12 override":                   `const f=new Intl.DateTimeFormat("en-u-hc-h23",{timeZone:"UTC",hour12:true,hour:"numeric"});const o=f.resolvedOptions();o.hour12===true&&o.hourCycle==="h12"&&o.locale==="en";`,
		"day periods":                       `const f=new Intl.DateTimeFormat("en",{timeZone:"UTC",hour:"numeric",dayPeriod:"long"});f.formatToParts(Date.UTC(2020,0,1,9)).some(x=>x.type==="dayPeriod"&&x.value==="in the morning")&&f.formatToParts(Date.UTC(2020,0,1,12)).some(x=>x.type==="dayPeriod"&&x.value==="noon");`,
		"timezone transitions":              `const f=new Intl.DateTimeFormat("en",{timeZone:"America/New_York",hourCycle:"h23",hour:"2-digit",minute:"2-digit"});f.format(Date.UTC(2020,0,1,12))==="07:00"&&f.format(Date.UTC(2020,6,1,12))==="08:00";`,
		"timezone names":                    `const f=new Intl.DateTimeFormat("en",{timeZone:"America/New_York",timeZoneName:"long"});const part=(d:number)=>f.formatToParts(d).find(x=>x.type==="timeZoneName")!.value;part(Date.UTC(2020,0,1))==="Eastern Standard Time"&&part(Date.UTC(2020,6,1))==="Eastern Daylight Time";`,
		"offset identifiers":                `const f=new Intl.DateTimeFormat("en",{timeZone:"+0530",hourCycle:"h23",hour:"2-digit",minute:"2-digit",timeZoneName:"longOffset"});f.resolvedOptions().timeZone==="+05:30"&&f.formatToParts(0).find(x=>x.type==="timeZoneName")!.value==="GMT+05:30"&&f.format(0).includes("05:30");`,
		"canonical zones":                   `new Intl.DateTimeFormat("en",{timeZone:"us/eastern"}).resolvedOptions().timeZone==="America/New_York"&&new Intl.DateTimeFormat("en",{timeZone:"Etc/GMT"}).resolvedOptions().timeZone==="UTC";`,
		"calendar negotiation":              `const f=new Intl.DateTimeFormat("en-u-ca-iso8601",{timeZone:"UTC"});f.resolvedOptions().calendar==="iso8601"&&new Intl.DateTimeFormat("en",{calendar:"notreal"}).resolvedOptions().calendar==="gregory";`,
		"numbering":                         `const f=new Intl.DateTimeFormat("en-u-nu-arab",{timeZone:"UTC",year:"numeric"});f.format(0)==="١٩٧٠"&&f.resolvedOptions().numberingSystem==="arab";`,
		"styles":                            `const f=new Intl.DateTimeFormat("en",{timeZone:"UTC",dateStyle:"full"});const o=f.resolvedOptions();f.format(0)==="Thursday, January 1, 1970"&&o.dateStyle==="full"&&!("year" in o)&&new Intl.DateTimeFormat("en",{timeZone:"UTC",timeStyle:"short",hourCycle:"h23"}).format(0)==="00:00";`,
		"ranges":                            `const f=new Intl.DateTimeFormat("en",{timeZone:"UTC"});const p=f.formatRangeToParts(0,86400000);p.map(x=>x.value).join("")===f.formatRange(0,86400000)&&p.some(x=>x.source==="startRange")&&p.some(x=>x.source==="endRange")&&f.formatRange(0,1)===f.format(0)&&f.formatRangeToParts(0,1).every(x=>x.source==="shared");`,
		"range errors and conversion order": `const f=new Intl.DateTimeFormat("en");const log:string[]=[];let errors=0;for(const call of [()=>f.formatRange(2,1),()=>f.formatRange(NaN,{valueOf(){log.push("end");return 0}} as any),()=>(f.formatRange as any)(undefined,0),()=>f.format(Infinity)]){try{call()}catch(e){if(e instanceof RangeError||e instanceof TypeError)errors++}}errors===4&&log.join(",")==="end";`,
		"bound format brands subclass":      `class F extends Intl.DateTimeFormat{}const f=new F("en",{timeZone:"UTC"});const format=f.format;let rejected=false;try{Intl.DateTimeFormat.prototype.formatToParts.call(new Proxy(f,{}),0)}catch(e){rejected=e instanceof TypeError}f instanceof F&&format===f.format&&format.call(null,0)==="1/1/1970"&&format.name===""&&format.length===1&&rejected&&Object.prototype.toString.call(f)==="[object Intl.DateTimeFormat]";`,
		"option errors":                     `let errors=0;for(const options of [{timeZone:"Local"},{timeZone:"../etc/localtime"},{timeZone:"+24:00"},{calendar:""},{numberingSystem:""},{hourCycle:"wrong"},{weekday:"numeric"},{fractionalSecondDigits:4},{dateStyle:"long",year:"numeric"},{timeStyle:"wrong"}]){try{new Intl.DateTimeFormat("en",options as any)}catch(e){if(e instanceof TypeError||e instanceof RangeError)errors++}}errors===10;`,
		"Date locale integration":           `const d=new Date(0);d.toLocaleDateString("en",{timeZone:"UTC"})==="1/1/1970"&&d.toLocaleTimeString("en",{timeZone:"UTC",hourCycle:"h23"})==="00:00:00"&&d.toLocaleString("en",{timeZone:"UTC",hourCycle:"h23"})==="1/1/1970, 00:00:00";`,
		"Date snapshot before options":      `const d=new Date(0);const result=d.toLocaleDateString("en",{get timeZone(){d.setTime(86400000);return "UTC"}});result==="1/1/1970"&&d.getTime()===86400000;`,
		"Date invalid skips options":        `let effects=0;const value=new Date(NaN).toLocaleString("en",{get timeZone():string{effects++;throw Error("not reached")}});value==="Invalid Date"&&effects===0;`,
		"Date incompatible styles":          `let errors=0;for(const call of [()=>new Date(0).toLocaleDateString("en",{timeStyle:"short"}),()=>new Date(0).toLocaleTimeString("en",{dateStyle:"short"})]){try{call()}catch(e){if(e instanceof TypeError)errors++}}errors===2;`,
		"supported values":                  `const zones=Intl.supportedValuesOf("timeZone");const calendars=Intl.supportedValuesOf("calendar");zones.includes("UTC")&&zones.includes("America/New_York")&&!zones.includes("US/Eastern")&&calendars.includes("iso8601")&&Intl.supportedValuesOf("numberingSystem").includes("arab")&&Intl.supportedValuesOf("currency").includes("USD")&&Intl.supportedValuesOf("unit").includes("meter");`,
	} {
		t.Run(name, func(t *testing.T) {
			value, err := New().RunString(source)
			if err != nil || !value.ToBoolean() {
				t.Fatalf("DateTimeFormat: %v %v", value, err)
			}
		})
	}
}
func TestRuntimeClockAndTimeZone(t *testing.T) {
	first, second := New(), New()
	first.SetTimeSource(func() time.Time { return time.Unix(0, 0) })
	if err := first.SetTimeZone("America/New_York"); err != nil {
		t.Fatal(err)
	}
	value, err := first.RunString(`new Date(0).getHours()===19&&new Date(1969,11,31,19).getTime()===0&&Date.parse("1969-12-31T19:00:00")===0&&new Intl.DateTimeFormat("en").resolvedOptions().timeZone==="America/New_York"&&new Intl.DateTimeFormat("en").format()==="12/31/1969";`)
	if err != nil || !value.ToBoolean() {
		t.Fatalf("configured clock/zone: %v %v", value, err)
	}
	if err = first.SetTimeZone("bad/zone"); err == nil {
		t.Fatal("unknown Go timezone accepted")
	}
	value, err = second.RunString(`new Date(0).getHours()===0&&new Intl.DateTimeFormat("en").resolvedOptions().timeZone==="UTC";`)
	if err != nil || !value.ToBoolean() {
		t.Fatalf("isolated default zone: %v %v", value, err)
	}
}

func TestCheckedDateTimeAdvertisedLocaleData(t *testing.T) {
	value, err := New().RunString(`const locales:string[]=["en", "en-GB", "de", "fr", "ru", "sr", "sr-Latn", "ar", "ja", "zh", "zh-Hant", "hi", "es", "tr", "ko", "pt", "pt-PT", "it", "nl", "sv", "pl", "uk", "he", "id", "th"];for(const locale of locales){for(const style of ["full","long","medium","short"] as const){const f=new Intl.DateTimeFormat(locale,{timeZone:"America/New_York",dateStyle:style,timeStyle:style});const parts=f.formatToParts(Date.UTC(2020,6,2,13,14,15));if(parts.length===0||parts.some(p=>p.value.length===0)||parts.map(p=>p.value).join("")!==f.format(Date.UTC(2020,6,2,13,14,15)))throw Error(locale+":"+style)}}true;`)
	if err != nil || !value.ToBoolean() {
		t.Fatalf("datetime locale data: %v %v", value, err)
	}
}

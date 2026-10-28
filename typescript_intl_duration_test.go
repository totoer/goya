package goya

import "testing"

func TestCheckedIntlDurationFormat(t *testing.T) {
	for name, source := range map[string]string{
		"text styles":                           `const d={hours:1,minutes:2,seconds:3};new Intl.DurationFormat("en",{style:"long"}).format(d)==="1 hour, 2 minutes, 3 seconds"&&new Intl.DurationFormat("en",{style:"short"}).format(d)==="1 hr, 2 min, 3 sec"&&new Intl.DurationFormat("en",{style:"narrow"}).format(d)==="1h 2m 3s";`,
		"digital":                               `const f=new Intl.DurationFormat("en",{style:"digital"});f.format({hours:1,minutes:2,seconds:3})==="1:02:03"&&f.format({seconds:0})==="0:00:00"&&f.format({minutes:90})==="0:90:00";`,
		"numeric localized separators":          `new Intl.DurationFormat("sr",{style:"digital"}).format({hours:1,minutes:2,seconds:3})==="1.02.03"&&new Intl.DurationFormat("id",{style:"digital"}).format({hours:1,minutes:2,seconds:3})==="1.02.03";`,
		"fractional seconds":                    `const f=new Intl.DurationFormat("en",{style:"digital",fractionalDigits:9});f.format({seconds:1,milliseconds:2,microseconds:3,nanoseconds:4})==="0:00:01.002003004"&&new Intl.DurationFormat("en",{style:"digital",fractionalDigits:2}).format({seconds:1,milliseconds:999})==="0:00:01.99";`,
		"exact unsafe integer":                  `new Intl.DurationFormat("en",{style:"long",milliseconds:"numeric",fractionalDigits:3}).format({milliseconds:2**62})==="4,611,686,018,427,387.904 seconds";`,
		"smaller fractional units":              `new Intl.DurationFormat("en",{style:"long",microseconds:"numeric",fractionalDigits:3}).format({milliseconds:1,microseconds:234,nanoseconds:567})==="1.234 milliseconds"&&new Intl.DurationFormat("en",{style:"long",nanoseconds:"numeric",fractionalDigits:3}).format({microseconds:1,nanoseconds:234})==="1.234 microseconds";`,
		"negative sign once":                    `const f=new Intl.DurationFormat("en",{style:"digital"});f.format({minutes:-2,seconds:-3})==="-0:02:03"&&new Intl.DurationFormat("en",{style:"long"}).format({hours:-1,minutes:-2})==="-1 hour, 2 minutes"&&new Intl.DurationFormat("en",{style:"long",yearsDisplay:"always"}).format({days:-2})==="-0 years, 2 days";`,
		"no negative zero sign":                 `new Intl.DurationFormat("en",{style:"digital"}).format({seconds:-0})==="0:00:00";`,
		"auto zeros":                            `new Intl.DurationFormat("en",{style:"long"}).format({seconds:0})===""&&new Intl.DurationFormat("en",{style:"digital",hoursDisplay:"auto",minutesDisplay:"auto",secondsDisplay:"auto"}).format({seconds:0})==="";`,
		"bridge minutes":                        `const f=new Intl.DurationFormat("en",{style:"digital",hoursDisplay:"auto",minutesDisplay:"auto",secondsDisplay:"auto"});f.format({hours:1,seconds:2})==="1:00:02"&&f.format({seconds:2})==="02"&&f.format({minutes:2})==="02";`,
		"numeric starts minutes":                `new Intl.DurationFormat("en",{style:"long",minutes:"numeric"}).format({hours:1,minutes:2,seconds:3})==="1 hour, 2:03";`,
		"fractional carry unbalanced":           `new Intl.DurationFormat("en",{style:"digital"}).format({milliseconds:61000})==="0:00:61";`,
		"parts units":                           `const f=new Intl.DurationFormat("en",{style:"digital"}),d={days:1,hours:2,minutes:3,seconds:4,milliseconds:5};const p=f.formatToParts(d);p.map(x=>x.value).join("")===f.format(d)&&p.some(x=>x.type==="integer"&&x.unit==="day")&&p.some(x=>x.type==="fraction"&&x.unit==="second"&&x.value==="005")&&p.filter(x=>x.value===":"||x.value===", ").every(x=>!("unit" in x));`,
		"numbering system":                      `const f=new Intl.DurationFormat("en-u-nu-arab",{style:"digital"});f.format({hours:1,minutes:2,seconds:3})==="١:٠٢:٠٣"&&f.resolvedOptions().numberingSystem==="arab";`,
		"resolved options":                      `const f=new Intl.DurationFormat("en",{style:"digital"});const o=f.resolvedOptions();o.hours==="numeric"&&o.minutes==="2-digit"&&o.seconds==="2-digit"&&o.milliseconds==="numeric"&&o.millisecondsDisplay==="auto"&&o.hoursDisplay==="always"&&!("fractionalDigits" in o)&&Object.keys(o).slice(0,5).join(",")==="locale,numberingSystem,style,years,yearsDisplay";`,
		"option restrictions":                   `let errors=0;for(const options of [{hours:"numeric",minutes:"long"},{milliseconds:"numeric",microseconds:"long"},{milliseconds:"numeric",millisecondsDisplay:"always"},{style:"digital",fractionalDigits:10},{seconds:"2-digit",nanoseconds:"long"},{years:"numeric"},{numberingSystem:"ab"}]){try{new Intl.DurationFormat("en",options as any)}catch(e){if(e instanceof RangeError)errors++}}errors===7;`,
		"duration restrictions":                 `const f=new Intl.DurationFormat("en");let errors=0;for(const value of [{},{seconds:NaN},{seconds:Infinity},{seconds:1.2},{hours:1,seconds:-1},{years:2**32},{months:2**32},{weeks:2**32},{seconds:2**53},{seconds:2**53-1,nanoseconds:1000000000},null,1,"PT1S",{seconds:1n}]){try{f.format(value as any)}catch(e){if(e instanceof RangeError||e instanceof TypeError)errors++}}errors===14;`,
		"boundary exact validation":             `new Intl.DurationFormat("en",{style:"digital",hoursDisplay:"auto",minutesDisplay:"auto"}).format({seconds:2**53-1,nanoseconds:999999999})==="9007199254740991.999999999";`,
		"input getter conversion order":         `const seen:string[]=[];const input=new Proxy({seconds:{valueOf(){seen.push("number");return "2"}}},{get(o,k){seen.push(String(k));return Reflect.get(o,k)}});const text=new Intl.DurationFormat("en",{style:"long"}).format(input as any);text==="2 seconds"&&seen.join(",")==="days,hours,microseconds,milliseconds,minutes,months,nanoseconds,seconds,number,weeks,years";`,
		"option getter order":                   `const seen:string[]=[];new Intl.DurationFormat("en",new Proxy({},{get(_,k){seen.push(String(k));return undefined}}));seen.join(",")==="localeMatcher,numberingSystem,style,years,yearsDisplay,months,monthsDisplay,weeks,weeksDisplay,days,daysDisplay,hours,hoursDisplay,minutes,minutesDisplay,seconds,secondsDisplay,milliseconds,millisecondsDisplay,microseconds,microsecondsDisplay,nanoseconds,nanosecondsDisplay,fractionalDigits";`,
		"brands subclass and intrinsic helpers": `class D extends Intl.DurationFormat{}const d=new D("en",{style:"digital"});Intl.NumberFormat=(()=>{throw Error("override")}) as any;(Intl as any).ListFormat=(()=>{throw Error("override")}) as any;let errors=0;for(const f of [()=>(Intl.DurationFormat as any)(),()=>Intl.DurationFormat.prototype.format.call({},{}),()=>Intl.DurationFormat.prototype.format.call(new Proxy(d,{}),{}),()=>new Intl.DurationFormat("en",1 as any)]){try{f()}catch(e){if(e instanceof TypeError)errors++}}d instanceof D&&errors===4&&d.format({seconds:1})==="0:00:01"&&Object.prototype.toString.call(d)==="[object Intl.DurationFormat]";`,
	} {
		t.Run(name, func(t *testing.T) {
			value, err := New().RunString(source)
			if err != nil || !value.ToBoolean() {
				t.Fatalf("DurationFormat: %v %v", value, err)
			}
		})
	}
}
func TestCheckedIntlDurationAllLocales(t *testing.T) {
	source := `const locales=["en","en-GB","de","fr","ru","sr","sr-Latn","ar","ja","zh","zh-Hant","hi","es","tr","ko","pt","pt-PT","it","nl","sv","pl","uk","he","id","th"];const units=["years","months","weeks","days","hours","minutes","seconds","milliseconds","microseconds","nanoseconds"] as const;for(const locale of locales){for(const style of ["long","short","narrow","digital"] as const){const f=new Intl.DurationFormat(locale,{style});for(const unit of units){const duration={[unit]:2};const parts=f.formatToParts(duration);const text=f.format(duration);if(!text||text.includes("{")||parts.map(p=>p.value).join("")!==text)throw Error(locale+":"+style+":"+unit)}}}true;`
	value, err := New().RunString(source)
	if err != nil || !value.ToBoolean() {
		t.Fatalf("duration locale data: %v %v", value, err)
	}
}

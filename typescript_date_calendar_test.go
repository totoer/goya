package goya

import (
	"fmt"
	"testing"
)

func TestCheckedDateCalendar(t *testing.T) {
	for name, source := range map[string]string{
		"boundaries":                 `Date.UTC(275760,8,13)===8640000000000000&&Date.UTC(-271821,3,20)===-8640000000000000&&Number.isNaN(Date.UTC(275760,8,13,0,0,0,1))&&Number.isNaN(Date.UTC(-271821,3,19,23,59,59,999));`,
		"component overflow":         `const values=[Number.MAX_VALUE,1e30,2**63,-(2**63),-(2**100),Infinity,-Infinity,NaN];values.every(n=>Number.isNaN(Date.UTC(1970,0,1,0,0,0,n)))&&values.every(n=>Number.isNaN(Date.UTC(n,0,1)))&&values.every(n=>Number.isNaN(Date.UTC(1970,n,1)))&&values.every(n=>Number.isNaN(new Date(0).setUTCFullYear(n)))&&values.every(n=>Number.isNaN(new Date(0).setUTCMilliseconds(n)));`,
		"normalization":              `Date.UTC(2000,-1,0,25,-60,120,-1)===Date.UTC(1999,10,30,24,0,119,999)&&Date.UTC(1970,12*100000,1)===Date.UTC(101970,0,1)&&Number.isNaN(Date.UTC(1001970,0,1));`,
		"large cancellation":         `Date.UTC(1970,0,1,2**50,-(2**50)*60)===0&&Date.UTC(1970,0,1,0,0,2**50,-(2**50)*1000)===0&&Date.UTC(2**52,-12*(2**52),1)===Date.parse("0000-01-01T00:00:00Z")&&Number.isNaN(Date.UTC(1970,0,1,Number.MAX_VALUE,-Number.MAX_VALUE));`,
		"pre-epoch milliseconds":     `new Date(-1).setUTCMilliseconds(42)===-958&&new Date(-1001).setUTCMilliseconds(42)===-1958&&new Date(-1001).setUTCSeconds(0,42)===-59958;`,
		"expanded years":             `const d=new Date(0);d.setUTCFullYear(-1);const n=new Date(0);n.setUTCFullYear(10000);d.toISOString()==="-000001-01-01T00:00:00.000Z"&&n.toISOString()==="+010000-01-01T00:00:00.000Z"&&new Date(-8640000000000000).toISOString()==="-271821-04-20T00:00:00.000Z"&&new Date(8640000000000000).toISOString()==="+275760-09-13T00:00:00.000Z";`,
		"conversion order":           `const log:number[]=[];const v=(i:number,n:number)=>({valueOf(){log.push(i);return n}});Date.UTC(v(0,NaN) as any,v(1,0) as any,v(2,1) as any,v(3,0) as any,v(4,0) as any,v(5,0) as any,v(6,0) as any);const a=log.join(",");log.length=0;new Date(v(0,NaN) as any,v(1,0) as any,v(2,1) as any,v(3,0) as any,v(4,0) as any,v(5,0) as any,v(6,0) as any);a==="0,1,2,3,4,5,6"&&log.join(",")==="0,1,2,3,4,5,6";`,
		"setter snapshots":           `const a=new Date(0);a.setUTCSeconds({valueOf(){a.setTime(86400000);return 10}} as any);const b=new Date(NaN);const result=b.setUTCSeconds({valueOf(){b.setTime(42);return NaN}} as any,{valueOf(){return 1}} as any);a.getTime()===10000&&Number.isNaN(result)&&b.getTime()===42;`,
		"setter arguments after NaN": `const log:number[]=[];const d=new Date(0);d.setUTCFullYear({valueOf(){log.push(1);return NaN}} as any,{valueOf(){log.push(2);return 0}} as any,{valueOf(){log.push(3);return 1}} as any);log.join(",")==="1,2,3"&&Number.isNaN(d.getTime());`,
		"invalid setter recovery":    `const d=new Date(NaN);const before=d.setUTCSeconds(42);const restored=d.setUTCFullYear(2000,0,1);Number.isNaN(before)&&restored===946684800000;`,
		"time clipping":              `const d=new Date(1);Object.is(new Date(-0).getTime(),0)&&Object.is(d.setTime(-0.5),0)&&new Date(0.9).getTime()===0&&new Date(-0.9).getTime()===0&&Number.isNaN(new Date(Infinity).getTime());`,
	} {
		t.Run(name, func(t *testing.T) {
			value, err := New().RunString(source)
			if err != nil || !value.ToBoolean() {
				t.Fatalf("Date calendar: %v %v", value, err)
			}
		})
	}
}

func TestCheckedDateLocalTransitionsAndBounds(t *testing.T) {
	for _, test := range []struct{ zone, source string }{
		{"America/New_York", `const gap=new Date(2017,2,12,2,30);const fold=new Date(2017,10,5,1,30);const secondFold=new Date("2017-11-05T06:30:00Z");const changed=secondFold.setMilliseconds(0);gap.toISOString()==="2017-03-12T07:30:00.000Z"&&fold.toISOString()==="2017-11-05T05:30:00.000Z"&&new Date("2017-03-12T02:30:00").getTime()===gap.getTime()&&changed===Date.parse("2017-11-05T05:30:00Z");`},
		{"Australia/Lord_Howe", `new Date(2024,9,6,2,15).toISOString()==="2024-10-05T15:45:00.000Z"&&new Date(2024,3,7,1,45).toISOString()==="2024-04-06T14:45:00.000Z";`},
		{"Pacific/Apia", `new Date(2011,11,30,12).toISOString()==="2011-12-30T22:00:00.000Z";`},
	} {
		t.Run(test.zone, func(t *testing.T) {
			r := New()
			if err := r.SetTimeZone(test.zone); err != nil {
				t.Fatal(err)
			}
			value, err := r.RunString(test.source)
			if err != nil || !value.ToBoolean() {
				t.Fatalf("Date transition: %v %v", value, err)
			}
		})
	}
	for _, offset := range []int{-3600, 3600, 13 * 3600} {
		r := New()
		sign := "+"
		if offset < 0 {
			sign = "-"
		}
		zone := fmt.Sprintf("%s%02d:00", sign, max(offset, -offset)/3600)
		if err := r.SetTimeZone(zone); err != nil {
			t.Fatal(err)
		}
		value, err := r.RunString(`const min=new Date(-8640000000000000),max=new Date(8640000000000000);const copy=(d:Date)=>new Date(d.getFullYear(),d.getMonth(),d.getDate(),d.getHours(),d.getMinutes(),d.getSeconds(),d.getMilliseconds());copy(min).getTime()===min.getTime()&&copy(max).getTime()===max.getTime();`)
		if err != nil || !value.ToBoolean() {
			t.Fatalf("Date local bounds %d: %v %v", offset, value, err)
		}
	}
}

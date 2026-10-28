package goya_test

import (
	"context"
	"testing"

	"github.com/totoer/goya"
)

func TestTypeScriptCollectionAndPromiseFamilies(t *testing.T) {
	for name, source := range map[string]string{
		"arrays":                        `const original=[3,1,2];const sorted=original.toSorted();const spliced=original.toSpliced(1,1,42);const replaced=original.with(-1,42);const reversed=original.toReversed();export const result=original.join(",")==="3,1,2"&&sorted.join(",")==="1,2,3"&&spliced.join(",")==="3,42,2"&&replaced.join(",")==="3,1,42"&&reversed.join(",")==="2,1,3"&&[1,[2,[3]]].flat(2).join(",")==="1,2,3"&&[1,2].flatMap(v=>[v,v]).join(",")==="1,1,2,2";`,
		"sort signed zero and NaN":      `const a=[2,1],b=[2,1];a.sort(()=>-0);b.sort(()=>-NaN);export const result=a.join(",")==="2,1"&&b.join(",")==="2,1"&&[2,1].toSorted(()=>-0).join(",")==="2,1";`,
		"locale argument propagation":   `const locale="en";const options={useGrouping:false};let calls=0;const value={toLocaleString(...args:unknown[]){if(this!==value||args.length!==2||args[0]!==locale||args[1]!==options)throw Error("locale arguments");calls++;return "x"}};const text=[value,[value]].toLocaleString(locale,options);export const result=text==="x,x"&&calls===2;`,
		"locale reads current elements": `const a:any[]=[{toLocaleString(){a.length=1;return "first"}},"old"];export const result=a.toLocaleString()==="first,";`,
		"typed locale arguments":        `const original=Number.prototype.toLocaleString;const locale="en";const options={useGrouping:false};let calls=0;Number.prototype.toLocaleString=function(this:number,...args:any[]){if(args.length!==2||args[0]!==locale||args[1]!==options)throw Error("locale arguments");calls++;return String(this)};const text=new Uint8Array([1,2]).toLocaleString(locale,options);Number.prototype.toLocaleString=original;export const result=text==="1,2"&&calls===2;`,
		"array holes":                   `const a:number[]=[];a[2]=42;const b=a.toReversed();const c=a.with(2,1);const d=a.toSpliced(1,0,2);export const result=!(0 in a)&&(0 in b)&&(2 in b)&&b[0]===42&&(0 in c)&&c[2]===1&&(0 in d)&&d.length===4;`,
		"map grouping insertion":        `const groups=Map.groupBy([1,2,3,4],v=>v%2);const map=new Map<string,number>();let calls=0;const a=map.getOrInsert("x",42);const b=map.getOrInsertComputed("x",()=>{calls++;return 0});const c=map.getOrInsertComputed("y",key=>{calls++;return key.length});export const result=groups.get(1)!.join(",")==="1,3"&&a===42&&b===42&&c===1&&calls===1&&[...map.keys()].join(",")==="x,y";`,
		"set operations":                `const a=new Set([1,2,3]);const b=new Set([2,3,4]);export const result=[...a.intersection(b)].join(",")==="2,3"&&[...a.difference(b)].join(",")==="1"&&[...a.symmetricDifference(b)].join(",")==="1,4"&&a.union(b).size===4&&a.isSupersetOf(new Set([1,2]))&&new Set([1,2]).isSubsetOf(a)&&a.isDisjointFrom(new Set([4,5]));`,
		"weak keys":                     `const key=Symbol("key");const registered=Symbol.for("registered");const map=new WeakMap<symbol,number>();map.set(key,42);const set=new WeakSet<symbol>();set.add(key);let rejected=false;try{map.set(registered,1)}catch(e){rejected=e instanceof TypeError}export const result=map.get(key)===42&&set.has(key)&&rejected&&!map.has(registered)&&map.delete(key)&&!map.has(key);`,
		"iterator closure":              `let closed=0;function* input(){try{yield 1;yield 2;yield 3}finally{closed++}}const values=input().map((v,i)=>v+i).filter(v=>v>1).take(1).toArray();export const result=values.join(",")==="3"&&closed===1&&Iterator.from([1,2,3]).drop(1).reduce((a,b)=>a+b,0)===5&&Iterator.from([1,2]).flatMap(v=>[v,v]).toArray().join(",")==="1,1,2,2"&&Iterator.from([1,2]).every(v=>v>0)&&Iterator.from([1,2]).some(v=>v===2)&&Iterator.from([1,2]).find(v=>v===2)===2;`,
		"promise combinators":           `const all=await Promise.all([Promise.resolve(1),2]);const settled=await Promise.allSettled([Promise.resolve(1),Promise.reject(2)]);const any=await Promise.any([Promise.reject(0),Promise.resolve(42)]);const race=await Promise.race([Promise.resolve(42)]);let aggregate=false;try{await Promise.any([Promise.reject(1),Promise.reject(2)])}catch(e){aggregate=e instanceof AggregateError&&e.errors.join(",")==="1,2"}const capability=Promise.withResolvers<number>();capability.resolve(42);export const result=all.join(",")==="1,2"&&settled[0].status==="fulfilled"&&settled[1].status==="rejected"&&any===42&&race===42&&aggregate&&(await capability.promise)===42&&(await Promise.try((a:number,b:number)=>a+b,20,22))===42;`,
		"error brands and cause":        `const cause={};const error=new Error("outer",{cause});const aggregate=new AggregateError([1,2],"many",{cause});const suppressed=new SuppressedError(1,2,"cleanup");export const result=Error.isError(error)&&Error.isError(aggregate)&&Error.isError(suppressed)&&!Error.isError({[Symbol.toStringTag]:"Error"})&&error.cause===cause&&aggregate.cause===cause&&aggregate.errors.join(",")==="1,2"&&suppressed.error===1&&suppressed.suppressed===2&&!Object.getOwnPropertyDescriptor(error,"cause")!.enumerable;`,
	} {
		t.Run(name, func(t *testing.T) {
			p, ds, err := goya.Compile(context.Background(), goya.Project{Files: map[string]string{"/main.ts": source}}, nil, goya.CompileOptions{"lib": []string{"esnext"}})
			if err != nil || p == nil {
				t.Fatalf("compile collection: %v %+v", err, ds)
			}
			value, err := goya.New().RunProgram(p)
			if err != nil {
				t.Fatal(err)
			}
			if promise, ok := value.Export().(*goya.Promise); ok {
				if promise.State() != goya.PromiseStateFulfilled {
					t.Fatalf("collection initialization: %v %v", promise.State(), promise.Result())
				}
				value = promise.Result()
			}
			if !hostObject(t, value).Get("result").ToBoolean() {
				t.Fatal("collection family semantics")
			}
		})
	}
}

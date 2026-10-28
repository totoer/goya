package goya_test

import (
	"context"
	"testing"

	"github.com/totoer/goya"
)

func TestTypeScriptArrayFromAsync(t *testing.T) {
	for name, source := range map[string]string{
		"sync iterable":                    `const values=await Array.fromAsync([Promise.resolve(20),22]);export const result=values.join(",")==="20,22";`,
		"array like":                       `const log:string[]=[];const values=await Array.fromAsync({length:2,get 0(){log.push("get0");return Promise.resolve(20)},get 1(){log.push("get1");return 22}},async (v,i)=>{log.push("map"+i);await Promise.resolve();log.push("mapped"+i);return v+1});export const result=values.join(",")==="21,23"&&log.join(",")==="get0,map0,mapped0,get1,map1,mapped1";`,
		"async iterable values":            `const promised=Promise.resolve(42);let n=0;const input={ [Symbol.asyncIterator](){return this},async next(){return {done:n++>0,value:promised}}};const values=await Array.fromAsync(input);export const result=(values[0] as unknown)===promised;`,
		"async preference":                 `const input={*[Symbol.iterator](){yield 1},async *[Symbol.asyncIterator](){yield 42}};export const result=(await Array.fromAsync(input))[0]===42;`,
		"mapper receiver":                  `const receiver={offset:40};const values=await Array.fromAsync([1,2],function(this:typeof receiver,v,i){return this.offset+v+i},receiver);export const result=values.join(",")==="41,43";`,
		"constructor":                      `const lengths:(number|undefined)[]=[];class Target{[index:number]:unknown;length=0;constructor(n?:number){lengths.push(n)}}const from=Array.fromAsync as Function;const iterable=await from.call(Target,[1,2]);const arrayLike=await from.call(Target,{0:42,length:1});export const result=iterable instanceof Target&&iterable[0]===1&&iterable.length===2&&arrayLike[0]===42&&arrayLike.length===1&&lengths.length===2&&lengths[0]===undefined&&lengths[1]===1;`,
		"sparse strings":                   `const a=await Array.fromAsync({length:2,1:42});const b=await Array.fromAsync("😀a");export const result=0 in a&&a[0]===undefined&&a[1]===42&&b.join(",")==="😀,a";`,
		"reject mapper closes":             `const log:string[]=[];const failure={};let i=0;const input={ [Symbol.asyncIterator](){return this},async next(){return {done:false,value:i++}},async return(){log.push("close");await Promise.resolve();log.push("closed");throw new Error("cleanup")}};let reason:unknown;try{await Array.fromAsync(input,()=>Promise.reject(failure))}catch(e){reason=e}export const result=reason===failure&&log.join(",")==="close,closed"&&i===1;`,
		"sync throw mapper closes":         `let closed=0;function* input(){try{yield 42;yield 43}finally{closed++}}const failure={};let reason:unknown;try{await Array.fromAsync(input(),()=>{throw failure})}catch(e){reason=e}export const result=reason===failure&&closed===1;`,
		"sync rejected value closes":       `let closed=0;function* input(){try{yield Promise.reject(42)}finally{closed++}}let reason:unknown;try{await Array.fromAsync(input())}catch(e){reason=e}export const result=reason===42&&closed===1;`,
		"next rejection does not close":    `let closed=0;const input={ [Symbol.asyncIterator](){return this},next(){return Promise.reject(42)},async return(){closed++;return {done:true,value:0}}};let reason:unknown;try{await Array.fromAsync(input)}catch(e){reason=e}export const result=reason===42&&closed===0;`,
		"define failure closes":            `let closed=0;function* input(){try{yield 42}finally{closed++}}class Target{constructor(){return Object.preventExtensions(this)}}let rejected=false;try{await (Array.fromAsync as Function).call(Target,input())}catch(e){rejected=e instanceof TypeError}export const result=rejected&&closed===1;`,
		"invalid arguments reject promise": `let syncThrow=false;let promise:Promise<unknown>|undefined;try{promise=Array.fromAsync(null as any)}catch{syncThrow=true}let rejected=false;try{await promise}catch(e){rejected=e instanceof TypeError}export const result=!syncThrow&&promise instanceof Promise&&rejected;`,
		"mapper checked first":             `let got=false;const input={get [Symbol.asyncIterator](){got=true;return undefined}};let rejected=false;try{await Array.fromAsync(input as any,1 as any)}catch(e){rejected=e instanceof TypeError}export const result=!got&&rejected;`,
	} {
		t.Run(name, func(t *testing.T) {
			p, ds, err := goya.Compile(context.Background(), goya.Project{Files: map[string]string{"/main.ts": source}}, nil, goya.CompileOptions{"lib": []string{"esnext"}})
			if err != nil || p == nil {
				t.Fatalf("compile fromAsync: %v %+v", err, ds)
			}
			value, err := goya.New().RunProgram(p)
			if err != nil {
				t.Fatal(err)
			}
			promise := value.Export().(*goya.Promise)
			if promise.State() != goya.PromiseStateFulfilled {
				t.Fatalf("fromAsync: %v %v", promise.State(), promise.Result())
			}
			if !hostObject(t, promise.Result()).Get("result").ToBoolean() {
				t.Fatal("fromAsync semantics")
			}
		})
	}
}

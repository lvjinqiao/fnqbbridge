import assert from 'node:assert/strict';
import fs from 'node:fs';
import {pickerPath} from '../web/picker-result.js';
let complete;const timers=[];
globalThis.window={};globalThis.__pickerPath=pickerPath;
globalThis.__TrimApp=class{isStandaloneWeb=false;ready(){return Promise.resolve()}pickUserFile(){return new Promise(resolve=>complete=resolve)}};
const original=setTimeout;globalThis.setTimeout=(fn,ms)=>{timers.push({fn,ms});return timers.length};
try{
 const source=fs.readFileSync(new URL('../web/sdk.js',import.meta.url),'utf8').replace("import { pickerPath } from './picker-result.js';","const pickerPath=globalThis.__pickerPath;").replace("import { TrimApp } from './trim-sdk.js';","const TrimApp=globalThis.__TrimApp;");
 await import('data:text/javascript,'+encodeURIComponent(source));
 const pending=window.pickFnosDirectory();for(let i=0;i<10;i++)await Promise.resolve();assert.equal(typeof complete,'function');assert.ok(!timers.some(x=>x.ms===15000),'interactive picker must not time out after 15s');
 // Fire the initialization timeout after ready already settled: it must not cancel selection.
 timers.forEach(x=>x.fn());await Promise.resolve();complete(JSON.stringify({code:0,data:{paths:['/vol2/1000/手机选择']}}));assert.equal(await pending,'/vol2/1000/手机选择');console.log('PASS native picker survives delayed confirmation and decodes serialized response');
}finally{globalThis.setTimeout=original}

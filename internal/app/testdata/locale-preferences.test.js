'use strict';
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const {test}=require('node:test');
const {boot,root}=require('./i18n-test-helpers');
const cases=JSON.parse(fs.readFileSync(path.join(root,'../i18n/testdata/locale-cases.json'),'utf8'));
for(const {input,expected} of cases) test('locale fixture '+JSON.stringify(input),()=>{
 const app=boot({languages:[input,'zh-CN']});
 assert.equal(app.api.getLocale(),expected||'zh-CN');
 app.api.setLocale('zh-CN');
 assert.equal(app.api.setLocale(input),Boolean(expected));
 assert.equal(app.api.getLocale(),expected||'zh-CN');
});
test('valid cookie agrees with server and repairs conflicting storage',()=>{
 const app=boot({cookie:'iosbk_lang=zh-CN',stored:{iosbk_lang:'en'}});
 assert.equal(app.api.getLocale(),'zh-CN');assert.equal(app.values.iosbk_lang,'zh-CN');
});
test('legacy preference migrates once and unchanged preferences do not rewrite',()=>{
 const app=boot({stored:{iosbk_guide_lang:'zh'}});
 assert.equal(app.api.getLocale(),'zh-CN');assert.equal(app.values.iosbk_lang,'zh-CN');
 const writes=app.storageWrites.length,cookies=app.cookieWrites.length;
 app.api.setLocale('zh-CN');app.window.dispatchEvent({type:'focus'});
 assert.equal(app.storageWrites.length,writes);assert.equal(app.cookieWrites.length,cookies);
});
test('storage and cookie failures remain independent',()=>{
 assert.equal(boot({cookie:'iosbk_lang=zh-CN',storageReadThrows:true}).api.getLocale(),'zh-CN');
 const app=boot({stored:{iosbk_lang:'zh-CN'},cookieReadThrows:true,cookieWriteThrows:true});
 assert.equal(app.api.getLocale(),'zh-CN');app.api.setLocale('en');assert.equal(app.values.iosbk_lang,'en');
 const blocked=boot({cookie:'iosbk_lang=en',cookieWriteThrows:true});
 blocked.api.setLocale('zh-CN');blocked.window.dispatchEvent({type:'focus'});
 assert.equal(blocked.api.getLocale(),'zh-CN','unchanged unwritable cookie must not undo current choice');
 const noStorage=boot({storageWriteThrows:true});noStorage.api.setLocale('zh-CN');
 assert.match(noStorage.document.cookie,/iosbk_lang=zh-CN/);
});
test('same-origin storage synchronization changes locale once without a write loop',()=>{
 const app=boot();const writes=app.storageWrites.length;
 app.values.iosbk_lang='zh-CN';app.document.cookie='iosbk_lang=zh-CN';
 app.window.dispatchEvent({type:'storage',key:'iosbk_lang',newValue:'zh-CN',storageArea:app.window.localStorage});
 assert.equal(app.api.getLocale(),'zh-CN');assert.equal(app.storageWrites.length,writes);
 assert.equal(app.events.filter(e=>e.type==='iosbk:localechange').length,1);
 app.window.dispatchEvent({type:'storage',key:'iosbk_lang',newValue:'zh-CN'});
 assert.equal(app.events.filter(e=>e.type==='iosbk:localechange').length,1);
 app.api.fetch('/api/status');assert.equal(new Headers(app.requests[0].opts.headers).get('Accept-Language'),'zh-CN');
});
test('invalid, unrelated, stale and session-storage events preserve a valid choice',()=>{
 const app=boot({cookie:'iosbk_lang=zh-CN'});
 for(const event of [
  {key:'other',newValue:'en'}, {key:'iosbk_lang',newValue:'en-<script>'},
  {key:null,newValue:null},{key:'iosbk_lang',newValue:'en',storageArea:{}},
  {key:'iosbk_lang',newValue:'en'}
 ]) app.window.dispatchEvent({type:'storage',...event});
 assert.equal(app.api.getLocale(),'zh-CN');
});
test('returning page adopts changed shared cookie without reviving stale preference',()=>{
 const app=boot({stored:{iosbk_lang:'en'}});
 app.document.cookie='iosbk_lang=zh-CN';app.window.dispatchEvent({type:'pageshow'});
 assert.equal(app.api.getLocale(),'zh-CN');assert.equal(app.values.iosbk_lang,'zh-CN');
 app.document.cookie='iosbk_lang=en';app.window.dispatchEvent({type:'focus'});
 assert.equal(app.api.getLocale(),'en');
});
test('a removed preference cannot validate an older queued storage event',()=>{
 const app=boot();app.values.iosbk_lang='zh-CN';app.api.setLocale('en');
 delete app.values.iosbk_lang;
 app.window.dispatchEvent({type:'storage',key:'iosbk_lang',newValue:'zh-CN'});
 assert.equal(app.api.getLocale(),'en');assert.match(app.document.cookie,/iosbk_lang=en/);
});
test('unreadable storage requires the shared cookie to corroborate an event',()=>{
 const app=boot();app.values.iosbk_lang='zh-CN';app.api.setLocale('en');
 app.window.localStorage.getItem=()=>{throw Error('blocked');};
 app.window.dispatchEvent({type:'storage',key:'iosbk_lang',newValue:'zh-CN'});
 assert.equal(app.api.getLocale(),'en');
 app.document.cookie='iosbk_lang=zh-CN';
 app.window.dispatchEvent({type:'storage',key:'iosbk_lang',newValue:'zh-CN'});
 assert.equal(app.api.getLocale(),'zh-CN');
});

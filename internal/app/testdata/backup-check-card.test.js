'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const {boot,catalogs,root}=require('./i18n-test-helpers');
function fixture(locale){
 const app=boot();app.api.register(catalogs);app.api.setLocale(locale);
 function node(tag='div'){
  const n={tagName:tag.toUpperCase(),children:[],dataset:{},className:'',textContent:'',open:false,attrs:{},listeners:{}};
  n.appendChild=c=>{n.children.push(c);return c};n.replaceChildren=()=>{n.children=[]};n.setAttribute=(k,v)=>n.attrs[k]=String(v);n.getAttribute=k=>n.attrs[k];n.addEventListener=(k,f)=>n.listeners[k]=f;n.querySelector=s=>walk(n).find(c=>c!==n&&c.tagName===s.toUpperCase())||null;
  n.classList={toggle(k,on){n.className=n.className.split(' ').filter(x=>x!==k).concat(on?[k]:[]).join(' ')}};return n;
 }
 function walk(n){return [n,...n.children.flatMap(walk)]}
 app.document.createElement=node;app.document.getElementById=()=>null;app.document.addEventListener=()=>{};
 vm.runInContext(fs.readFileSync(path.join(root,'static/backup-checks.js'),'utf8'),app.context);
 return {api:app.window.IOSBKChecks,host:node(),node,walk,text:n=>walk(n).map(x=>x.textContent).join('\n')};
}
const quick={kind:'quick',state:'passed',metadata_complete:true,freshness:'current',completed_at:'2026-11-02T15:26:00Z'};
const manifest={...quick,kind:'manifest',manifest_complete:true,records_checked:145905,objects_checked:120602,missing_objects:0,completed_at:'2026-11-03T14:46:00Z'};
for(const locale of ['en','zh-CN']){
 test('single check card shows one timestamp and only counts in its disclosure '+locale,()=>{
  const f=fixture(locale);f.api.card(f.host,{result:manifest,previous:quick});
  const more=f.host.querySelector('details');assert.ok(more);assert.equal(more.open,false);
  assert.ok(f.text(more).includes('145,905'));assert.ok(!f.text(more).includes('2026'));
  assert.equal(f.walk(f.host).filter(n=>n.tagName==='TIME').length,1);
  assert.ok(!f.text(f.host).includes(catalogs[locale]['history.automaticCheck']));
  more.open=true;f.api.card(f.host,{result:{...manifest,objects_checked:120603}});assert.equal(f.host.querySelector('details').open,true);
 });
 test('encrypted unsupported scan collapses to metadata evidence, without file counters '+locale,()=>{
  const f=fixture(locale);f.api.card(f.host,{result:{...manifest,state:'unavailable',reason:'encrypted_manifest'},previous:quick,encrypted:true});
  assert.ok(f.text(f.host).includes(catalogs[locale]['check.card.quickPassed']));assert.ok(f.text(f.host).includes(catalogs[locale]['check.card.encrypted']));
  assert.equal(f.host.querySelector('details'),null);assert.ok(!f.text(f.host).includes('145,905'));assert.ok(!f.text(f.host).includes(catalogs[locale]['check.state.unavailable']));
  f.api.card(f.host,{result:{...quick,state:'issues',reason:'metadata_missing'},encrypted:true});assert.ok(f.text(f.host).includes(catalogs[locale]['check.reason.metadata_missing']),'encryption must not hide genuine errors');
 });
 test('missing and stale encrypted evidence cannot turn into a current pass '+locale,()=>{
  const f=fixture(locale);f.api.card(f.host,{result:{...manifest,state:'unavailable',reason:'encrypted_manifest'},encrypted:true});assert.ok(f.text(f.host).includes(catalogs[locale]['check.none']));
  f.api.card(f.host,{result:{...manifest,state:'unavailable',reason:'encrypted_manifest'},previous:{...quick,freshness:'stale'},encrypted:true});assert.ok(f.text(f.host).includes(catalogs[locale]['check.card.stale']));assert.ok(!f.text(f.host).includes(catalogs[locale]['check.card.quickPassed']));
 });
 test('cancelled, partial and unsaved attempts preserve their meaning '+locale,()=>{
  const f=fixture(locale);f.api.card(f.host,{result:{...manifest,state:'unavailable',reason:'cancelled',manifest_complete:false,records_checked:11},previous:manifest,saveFailed:true});
  assert.ok(f.text(f.host).includes(catalogs[locale]['check.reason.cancelled']));assert.ok(f.text(f.host).includes(catalogs[locale]['check.card.partial']));assert.ok(f.text(f.host).includes(catalogs[locale]['history.reviewSaveFailed']));
  assert.ok(f.text(f.host).includes('2026'));assert.ok(f.text(f.host).includes(catalogs[locale]['check.state.passed']));
 });
}

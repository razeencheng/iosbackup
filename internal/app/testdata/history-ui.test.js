'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const {test}=require('node:test');
const {boot,catalogs,root}=require('./i18n-test-helpers');
function fixture(locale='en'){
 const app=boot(),nodes=new Map(),listeners={};app.api.register(catalogs);app.api.setLocale(locale);
 function node(tag='div'){
  const attrs={},n={tagName:tag.toUpperCase(),children:[],dataset:{},style:{},className:'',textContent:'',value:'',checked:false,disabled:false,open:false,isConnected:true};
  Object.defineProperty(n,'id',{get(){return attrs.id||'';},set(v){attrs.id=v;nodes.set(v,n)}});
  n.classList={contains:k=>n.className.split(' ').includes(k),add(k){if(!this.contains(k))n.className+=' '+k},remove(k){n.className=n.className.split(' ').filter(c=>c!==k).join(' ')},toggle(k,on){if(on===undefined)on=!this.contains(k);on?this.add(k):this.remove(k)}};
  n.setAttribute=(k,v)=>attrs[k]=String(v);n.getAttribute=k=>attrs[k]??null;n.removeAttribute=k=>delete attrs[k];n.appendChild=child=>{n.children.push(child);return child};n.append=(...children)=>children.forEach(n.appendChild);n.replaceChildren=()=>n.children=[];n.closest=()=>null;n.querySelector=selector=>n.children.find(c=>c.tagName===selector.toUpperCase())||null;n.querySelectorAll=()=>[];n.addEventListener=()=>{};n.focus=()=>{};return n;
 }
 app.document.createElement=node;app.document.getElementById=id=>nodes.get(id)||null;app.document.querySelector=()=>null;app.document.querySelectorAll=()=>[];app.document.addEventListener=(k,fn)=>listeners[k]=fn;app.document.body=node('body');app.document.activeElement=app.document.body;
 const get=id=>nodes.get(id);for(const prefix of ['historySettings','historyList','historyDetail','historyLayout','historyMore','historyWarning','historyError','consoleCount','consoleSize','consoleFree','consoleStorageNotice','consoleStorageHint','consoleFreeHint','historySummaryText','historyPolicySummary']){const n=node();n.id=prefix+'-A';}
 let data={policy:{enabled:false,count:2},points:[],unavailable_stores:[],total:0,can_edit_policy:true,bytes_known:false,logical_bytes:10,storage:{state:'unavailable'},current_source:{kind:'missing',available:false}};
 app.window.setTimeout=()=>1;app.window.clearTimeout=()=>{};app.window.formatBackupBytes=x=>x+' B';app.window.fetch=async()=>({ok:true,json:async()=>({jobs:[]})});app.window.csrfFetch=async()=>({ok:true,json:async()=>data});
 Object.assign(app.context,{AbortController,console});for(const file of ['backup-console.js','backup-history.js'])vm.runInContext(fs.readFileSync(path.join(root,'static',file),'utf8'),app.context);
 function flatten(n){return [n,...n.children.flatMap(flatten)]}
 return {...app,get,flatten,listeners,setData:d=>data={...data,...d},async open(){app.window.IOSBKHistory.section('A','backups');await app.window.IOSBKHistory.refresh()},async refresh(){await app.window.IOSBKHistory.refresh()}};
}
for(const locale of ['en','zh-CN'])test('retention settings use saved count, disable off input, discard drafts '+locale,async()=>{
 const f=fixture(locale);await f.open();
 assert.equal(String(f.get('historyCount-A').value),'2');assert.equal(f.get('historyCount-A').disabled,true);assert.equal(f.get('historyEnabled-A').checked,false);
 assert.equal(f.get('historyPolicyRules-A').open,false,'extra storage explanation starts collapsed');
 f.get('historyEnabled-A').checked=true;f.listeners.input({target:{dataset:{historyField:'enabled'}}});assert.equal(f.get('historyCount-A').disabled,false);
 f.get('historyCount-A').value='0';f.listeners.input({target:{dataset:{historyField:'count'}}});assert.equal(f.get('historyPolicySave-A').disabled,true);
 f.window.IOSBKHistory.discard('A');assert.equal(String(f.get('historyCount-A').value),'2');assert.equal(f.get('historyCount-A').disabled,true);
 f.setData({policy:{enabled:true,count:7}});await f.refresh();assert.equal(String(f.get('historyCount-A').value),'7','explicit saved count survives default initialization');
});
for(const locale of ['en','zh-CN'])test('list highlights only actual current version and uses concise check details '+locale,async()=>{
 const f=fixture(locale);const check={state:'passed',scope:'quick',checked_at:'2026-11-01T10:00:00Z'};
 f.setData({policy:{enabled:true,count:2},total:2,current_source:{kind:'point',available:true,point_id:'latest'},points:[{id:'old',baseline:true,state:'normal',check,completed_at:'2026-10-31T10:00:00Z'},{id:'latest',state:'normal',check,completed_at:'2026-11-01T10:00:00Z',review_attempt:{state:'unavailable',scope:'manifest',reason:'cancelled',checked_at:'2026-11-02T10:00:00Z'}}]});await f.open();
 const list=f.flatten(f.get('historyList-A')),current=list.filter(n=>n.classList.contains('console-current-point'));assert.equal(current.length,1);
 assert.equal(list.filter(n=>n.dataset.historyAction==='downloadPoint').length,0,'downloads only in backup details');
 const checks=list.filter(n=>n.classList.contains('console-check-pass'));assert.equal(checks.length,2);assert.ok(checks[0].getAttribute('aria-label'));assert.match(checks[0].getAttribute('data-hint'),/2026/);
 assert.ok(list.some(n=>n.textContent.includes(catalogs[locale]['check.reason.cancelled'])),'cancelled attempt remains visible alongside last pass');
 assert.ok(f.get('consoleStorageNotice-A').textContent.includes(catalogs[locale]['history.sizePartial']),'partial totals remain visible');
 assert.ok(f.get('consoleFree-A').getAttribute('data-hint'),'capacity measurement details remain accessible');
 assert.ok(!f.get('consoleFree-A').textContent.includes('0 B'),'unavailable free space must not become zero');
 f.setData({current_source:{kind:'point',available:false,point_id:'latest'}});await f.refresh();assert.equal(f.flatten(f.get('historyList-A')).filter(n=>n.classList.contains('console-current-point')).length,0);
});

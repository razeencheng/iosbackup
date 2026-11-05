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
  n.setAttribute=(k,v)=>attrs[k]=String(v);n.getAttribute=k=>attrs[k]??null;n.removeAttribute=k=>delete attrs[k];n.appendChild=child=>{child.parentNode=n;n.children.push(child);return child};n.append=(...children)=>children.forEach(n.appendChild);n.replaceChildren=()=>{n.children.forEach(c=>c.parentNode=null);n.children=[]};n.closest=()=>null;n.querySelector=selector=>flatten(n).find(c=>c!==n&&c.tagName===selector.toUpperCase())||null;n.querySelectorAll=()=>[];n.addEventListener=()=>{};n.focus=()=>{};return n;
 }
 app.document.createElement=node;app.document.getElementById=id=>nodes.get(id)||null;app.document.querySelector=()=>null;app.document.querySelectorAll=()=>[];app.document.addEventListener=(k,fn)=>listeners[k]=fn;app.document.body=node('body');app.document.activeElement=app.document.body;
 const get=id=>nodes.get(id);for(const prefix of ['historySettings','historyList','historyDetail','historyLayout','historyMore','historyWarning','historyError','consoleCount','consoleSize','consoleFree','consoleStorageNotice','consoleStorageHint','consoleFreeHint','historySummaryText','historyPolicySummary']){const n=node();n.id=prefix+'-A';}
 let data={policy:{enabled:false,count:2},points:[],unavailable_stores:[],total:0,can_edit_policy:true,bytes_known:false,logical_bytes:10,storage:{state:'unavailable'},current_source:{kind:'missing',available:false}},poll;const responses=new Map();
 app.window.setTimeout=fn=>{poll=fn;return 1};app.window.clearTimeout=()=>{};app.window.formatBackupBytes=x=>x+' B';app.window.fetch=async()=>({ok:true,json:async()=>({jobs:[]})});app.window.csrfFetch=async url=>({ok:true,json:async()=>responses.get(url)||data});
 Object.assign(app.context,{AbortController,console});for(const file of ['backup-checks.js','backup-console.js','backup-history.js'])vm.runInContext(fs.readFileSync(path.join(root,'static',file),'utf8'),app.context);
 function flatten(n){return [n,...n.children.flatMap(flatten)]}
 return {...app,get,flatten,listeners,responses,setData:d=>data={...data,...d},async poll(){await poll()},async act(action){const button=flatten(get('historyDetail-A')).find(n=>n.dataset.historyAction===action);assert.ok(button);listeners.click({target:{closest:()=>button}});await new Promise(setImmediate)},async open(){app.window.IOSBKHistory.section('A','backups');await app.window.IOSBKHistory.refresh()},async refresh(){await app.window.IOSBKHistory.refresh()},async select(id){app.window.IOSBKConsole.pointDrawer=()=>{};await app.window.IOSBKHistory.navigate('A',id)}};
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
for(const locale of ['en','zh-CN'])test('backup details prioritize completed checks, preserve exceptions and collapse evidence '+locale,async()=>{
 const f=fixture(locale),quick={state:'passed',scope:'quick',metadata_complete:true,checked_at:'2026-11-02T15:26:00Z'},review={state:'passed',scope:'manifest',metadata_complete:true,manifest_complete:true,records_checked:145905,objects_checked:120602,missing_objects:0,checked_at:'2026-11-02T15:31:00Z'};
 let point={id:'latest',state:'normal',reason:'baseline',encryption:'unencrypted',bytes_known:true,bytes:20500,completed_at:quick.checked_at,check:quick,can_check:true,can_export:true};
 const show=async changes=>{point={...point,...changes};f.setData({points:[point],total:1});await f.open();await f.select(point.id);return f.get('historyDetail-A')};
 let detail=await show({}),text=n=>f.flatten(n).map(c=>c.textContent).join('\n');
 assert.ok(detail.children.find(n=>n.classList.contains('console-point-hero')).textContent.includes(catalogs[locale]['history.reason.baseline']));
 assert.ok(text(detail).includes(catalogs[locale]['check.card.quickPassed']));
 assert.ok(!text(detail).includes(catalogs[locale]['history.restoreHint']));
 assert.ok(!text(detail).includes(catalogs[locale]['check.limitations']));
 assert.ok(!text(detail).includes(catalogs[locale]['history.downloadHint']));
 assert.equal(detail.children.some(n=>n.textContent.includes('2026-11-02 ·')),false);
 assert.equal(detail.children.some(n=>n.textContent.includes(catalogs[locale]['jobs.state.succeeded'])),false);
 detail=await show({review,review_attempt:review});
 let more=detail.querySelector('details');assert.ok(more);assert.equal(more.open,false);
 const frontText=n=>n.tagName==='DETAILS'?'':n.textContent+'\n'+n.children.map(frontText).join('\n'),visible=frontText(detail);
 assert.ok(visible.includes(catalogs[locale]['check.state.passed']));assert.ok(!visible.includes(catalogs[locale]['check.card.quickPassed']));assert.ok(!visible.includes('145,905'));
 assert.ok(text(more).includes('145,905'));assert.ok(!text(more).includes('2026'),'expanded details do not repeat timestamps or results');
 more.open=true;point={...point,revision:2};f.setData({points:[point]});await f.refresh();assert.equal(f.get('historyDetail-A').querySelector('details').open,true,'poll refresh preserves disclosure');
 for(const reason of ['cancelled','timeout','read_failed']){
  detail=await show({review_attempt:{scope:'manifest',state:'unavailable',reason,checked_at:'2026-11-02T16:00:00Z'}});
  assert.ok(detail.children.filter(n=>n.tagName!=='DETAILS').map(text).join('\n').includes(catalogs[locale]['check.reason.'+reason]));
  assert.ok(text(detail).includes(catalogs[locale]['check.state.passed']),'last completed evidence is still accessible');
 }
 detail=await show({review_attempt:{...review,state:'issues',reason:'missing_objects',missing_objects:1,checked_at:'2026-11-02T17:00:00Z'},review_save_failed:true});
 const front=frontText(detail);assert.ok(front.includes(catalogs[locale]['check.reason.missing_objects']));assert.ok(front.includes(catalogs[locale]['history.reviewSaveFailed']));assert.ok(!front.includes(catalogs[locale]['check.state.passed']));
 detail=await show({state:'pending',completion_unknown:true,review:null,review_attempt:null,review_save_failed:false});assert.ok(text(detail).includes(catalogs[locale]['history.state.pending']));assert.ok(text(detail).includes(catalogs[locale]['history.unknownDateHint']));
 detail=await show({review:{scope:'manifest',state:'unavailable',reason:'cancelled',checked_at:'2026-11-02T18:00:00Z'}});assert.ok(text(detail).includes(catalogs[locale]['check.reason.cancelled']),'older review records still expose their incomplete attempt');
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
for(const locale of ['en','zh-CN'])test('finished checks are shown once and export guidance appears with the download '+locale,async()=>{
 const f=fixture(locale),quick={scope:'quick',state:'passed',checked_at:'2026-11-02T15:00:00Z'},point={id:'latest',state:'normal',encryption:'unencrypted',check:quick,can_check:true,can_export:true};
 f.setData({points:[point],total:1});await f.open();await f.select(point.id);
 f.responses.set('/api/backup-point/latest/check',{id:'check-1'});await f.act('check');assert.ok(f.flatten(f.get('historyCheck-A')).some(n=>n.dataset.historyAction==='cancelCheck'));
 const review={scope:'manifest',state:'passed',metadata_complete:true,manifest_complete:true,checked_at:'2026-11-02T15:31:00Z'};
 f.setData({points:[{...point,review,review_attempt:review}]});f.responses.set('/api/backup-point/latest/check',{saved:true,result:{...review,kind:'manifest',completed_at:'2026-11-02T23:31:00+08:00'}});await f.poll();assert.equal(f.flatten(f.get('historyDetail-A')).filter(n=>n.classList.contains('check-card')).length,1,'completed result appears once even when timestamp offsets differ');assert.equal(f.flatten(f.get('historyDetail-A')).filter(n=>n.tagName==='TIME').length,1);
 f.setData({points:[{...point,encryption:'encrypted',can_check:false,check_unavailable_reason:'encrypted_manifest'}]});await f.refresh();assert.ok(!f.flatten(f.get('historyCheck-A')).some(n=>n.dataset.historyAction==='check'));assert.equal(f.get('historyCheck-A').querySelector('details'),null);
 f.responses.set('/api/backup-point/latest/exports',{id:'export-1',point_id:'latest',state:'ready',can_download:true});await f.act('export');
 const text=f.flatten(f.get('historyDownload-A')).map(n=>n.textContent).join('\n');assert.ok(text.includes(catalogs[locale]['history.downloadHint']));assert.ok(text.includes(catalogs[locale]['history.passwordHint']));assert.ok(text.includes(catalogs[locale]['history.exportInstructions']));
});

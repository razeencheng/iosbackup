'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const window={document:{addEventListener(){}},addEventListener(){}};
vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../static/backup-history.js'),'utf8'),{window,AbortController,console});
const api=window.IOSBKHistory;
(async()=>{
 for(const [canDownload,leave] of [[true,false],[false,false],[true,true]]){
  const listeners={},frame={src:''},point='a'.repeat(32),requests=[];let finishExport;
  const host={document:{activeElement:{isConnected:true},addEventListener(type,fn){listeners[type]=fn;},getElementById:id=>id==='historyDownloadFrame'?frame:null,querySelector(){return null;}},addEventListener(){},setTimeout(){return 1;},clearTimeout(){},IOSBKI18n:{t:key=>key,has:()=>true,message:()=>''},IOSBKConsole:{updateHistory(){},pointDrawer(){}},csrfFetch:async(url,options)=>{requests.push(url);if(leave&&url.endsWith('/exports'))await new Promise(resolve=>{finishExport=resolve;});return {ok:true,json:async()=>url.endsWith('/exports')?{id:'export-1',point_id:point,state:'ready',can_download:canDownload}:{policy:{enabled:true,days:3},points:[{id:point,can_export:true}],unavailable_stores:[],total:1}};}};
  vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../static/backup-history.js'),'utf8'),{window:host,AbortController,console});
  host.IOSBKHistory.section('A','backups');await host.IOSBKHistory.refresh();
  listeners.click({target:{closest(){return {dataset:{historyAction:'downloadPoint',point}};}}});
  if(leave){host.IOSBKHistory.hide();finishExport();}
  await new Promise(setImmediate);
  assert.ok(requests.includes('/api/backup-point/'+point+'/exports'));
  assert.equal(frame.src,canDownload&&!leave?'/api/backup-export/export-1/download':'','row download starts only when a transfer can start and the selected view is still active');
  host.IOSBKHistory.hide();
 }
 console.log('row downloads start directly and respect the transfer limit');
})().catch(e=>{console.error(e);process.exitCode=1});
(async()=>{
 const listeners={},notices=[];let state='interrupted';
 const host={document:{addEventListener(type,fn){listeners[type]=fn;},getElementById(){return null;},querySelector(){return null;}},addEventListener(){},setTimeout(){return 1;},clearTimeout(){},toast:key=>notices.push(key),IOSBKI18n:{t:key=>key,has:()=>true,message:()=>''},csrfFetch:async(url)=>({ok:true,json:async()=>url.endsWith('/policy-retry')?{operation:{id:'policy-1',state:'running'}}:{policy:{enabled:true,days:3},policy_operation:{id:'policy-1',state},can_retry_policy:state==='interrupted',points:[],unavailable_stores:[],total:0}})};
 vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../static/backup-history.js'),'utf8'),{window:host,AbortController,console});
 host.IOSBKHistory.section('A','settings');await host.IOSBKHistory.refresh();
 listeners.click({target:{closest(){return {dataset:{historyAction:'retryPolicy'}};}}});
 await new Promise(setImmediate);state='succeeded';await host.IOSBKHistory.refresh();await host.IOSBKHistory.refresh();
 assert.equal(notices.filter(key=>key==='bset.saved').length,1,'completion must be acknowledged once, not just task acceptance');
 host.IOSBKHistory.hide();
 console.log('history policy reports saved after background completion');
})().catch(e=>{console.error(e);process.exitCode=1});
(async()=>{
 const listeners={},requests=[];
 const host={document:{addEventListener(type,listener){listeners[type]=listener;},getElementById(){return null;},querySelector(){return null;}},addEventListener(){},setTimeout(){return 1;},clearTimeout(){},
  IOSBKI18n:{t:key=>key,has:()=>true,message:()=>''},
  csrfFetch(url,options){requests.push([url,options.method,options.body]);return Promise.resolve({ok:true,json:async()=>({policy:{enabled:true,days:3},policy_operation:{state:'interrupted'},can_retry_policy:true,points:[],unavailable_stores:[],total:0})});}
 };
 vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../static/backup-history.js'),'utf8'),{window:host,AbortController,console});
 host.IOSBKHistory.section('A','settings');await host.IOSBKHistory.refresh();
 listeners.click({target:{closest(){return {dataset:{historyAction:'retryPolicy'}};}}});
 await new Promise(setImmediate);
 assert.ok(requests.some(([url,method,body])=>url==='/api/backup-history/A/policy-retry'&&method==='POST'&&body==='{}'),'retry resumes the saved policy without creating a new preview');
 host.IOSBKHistory.hide();
 console.log('history policy retry resumes only saved settings');
})().catch(e=>{console.error(e);process.exitCode=1});
(async()=>{
 async function deleteAfterNavigation(destination){
  const listeners={},closed=[],dialogs=[],first='a'.repeat(32),second='b'.repeat(32);
  let resolveDelete,deleted=false;
  const document={
   activeElement:{isConnected:true,focus(){}},
   addEventListener(type,listener){listeners[type]=listener;},
   getElementById(){return null;},querySelector(){return null;},
   createElement(tag){return {tag,dataset:{},children:[],classList:{add(){}},setAttribute(){},addEventListener(){},appendChild(child){this.children.push(child);},showModal(){dialogs.push(this);},close(){},remove(){},focus(){}};},
   body:{appendChild(){}}
  };
  const host={document,addEventListener(){},setTimeout(){return 1;},clearTimeout(){},
   IOSBKI18n:{t:key=>key,has:()=>true,message:()=>'',formatDate:value=>value},
   IOSBKConsole:{updateHistory(){},pointDrawer(){},closePoint(u){closed.push(['point',u]);},closeLayers(){closed.push(['all']);}},
   csrfFetch(url,options){
    if(options.method==='DELETE')return new Promise(resolve=>{resolveDelete=resolve;});
    return Promise.resolve({ok:true,json:async()=>({policy:{enabled:true,days:3},points:(deleted?[second]:[first,second]).map(id=>({id,revision:1})),unavailable_stores:[],logical_bytes:0,bytes_known:true,total:deleted?1:2})});
   }
  };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../static/backup-history.js'),'utf8'),{window:host,AbortController,console});
  const history=host.IOSBKHistory;
  function click(action,point){const button={dataset:{historyAction:action,point},closest(){return this;}};listeners.click({target:button});}
  history.section('A','backups');await history.refresh();await history.navigate('A',first);
  click('delete');
  const confirmation=dialogs.at(-1);
  assert.ok(confirmation,'deletion must require confirmation');
  confirmation.children.at(-1).children.find(button=>button.dataset.historyAction==='dialogConfirm').onclick();
  await new Promise(setImmediate);
  assert.equal(typeof resolveDelete,'function','deletion must still be in flight while navigating');
  history.back();
  if(destination==='settings')history.section('A','settings');
  else click('select',second);
  deleted=true;resolveDelete({ok:true,json:async()=>({})});
  await new Promise(setImmediate);
  assert.deepEqual(closed,[],destination==='settings'?'a late deletion cannot close a settings window':'a late deletion cannot close another point on the same device');
  assert.equal(history.isPending(),false);
  history.hide();
 }
 await deleteAfterNavigation('settings');
 console.log('late deletion preserves the new settings window');
 await deleteAfterNavigation('point');
 console.log('late deletion preserves another point on the same device');
})().catch(e=>{console.error(e);process.exitCode=1});
(async()=>{
 const pending=[],drawers=[];
 const host={document:{addEventListener(){},getElementById(){return null}},addEventListener(){},setTimeout(){return 1},clearTimeout(){},IOSBKI18n:{t:k=>k,has:()=>true,message:()=>''},IOSBKConsole:{updateHistory(){},pointDrawer(u){drawers.push(u)}},csrfFetch(url,opts){return new Promise((resolve,reject)=>{pending.push({url,resolve});opts.signal.addEventListener('abort',()=>reject(Object.assign(Error('aborted'),{name:'AbortError'})),{once:true})})}};
 vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../static/backup-history.js'),'utf8'),{window:host,AbortController,console});
 host.IOSBKHistory.section('A','backups');const nav=host.IOSBKHistory.navigate('A','target');
 host.IOSBKHistory.section('A','settings');await new Promise(setImmediate);
 pending.at(-1).resolve({ok:true,json:async()=>({policy:{enabled:true,days:3},points:[{id:'target'}],unavailable_stores:[],logical_bytes:0,bytes_known:true,total:1})});
 await nav;assert.deepEqual(drawers,[],'late navigation cannot open a backup drawer over settings');
 console.log('same-device cross-section navigation remains cancelled');
})().catch(e=>{console.error(e);process.exitCode=1});
(async()=>{
 const views=[];const c=api.createController({get(){return Promise.resolve({policy:{enabled:true,days:3},points:[]})},render(v){views.push(v)},error(e){throw e}});
 await c.open('A');c.edit({enabled:false,days:12});c.discard('A');
 assert.equal(views.at(-1).draft.days,3);assert.equal(views.at(-1).draft.enabled,true);assert.equal(c.hasDirty(),false,'cancel discards the unsaved policy without changing the saved policy');
 c.close();console.log('history settings cancellation restores saved policy');
})().catch(e=>{console.error(e);process.exitCode=1});
(async()=>{
 let resolveA,calls=0;const views=[],errors=[];
 const c=api.createController({get(url){calls++;if(url.endsWith('/A'))return new Promise(r=>{resolveA=r});return Promise.resolve({udid:'B',policy:{enabled:true,days:3},points:[{id:'b'.repeat(32)},{id:'c'.repeat(32)}],total:2})},render(v){views.push(v)},error(e){errors.push(e)}});
 const old=c.open('A');c.refresh();assert.equal(calls,1,'deduplicate concurrent reads');await c.open('B');resolveA({udid:'A',policy:{days:1},points:[]});await old;assert.equal(views.at(-1).udid,'B');
 c.select('c'.repeat(32));c.edit({enabled:true,days:12});c.repaint();assert.equal(views.at(-1).selected,'c'.repeat(32));assert.equal(views.at(-1).draft.days,12);
 await c.refresh();assert.equal(views.at(-1).draft.days,12,'refresh cannot discard unsaved policy');c.close();await c.open('B');assert.equal(views.at(-1).selected,'c'.repeat(32));assert.equal(views.at(-1).draft.days,12,'return keeps draft');
 assert.equal(errors.length,0);console.log('history navigation ignores late results, deduplicates, preserves selection and unsaved edits');
})().catch(e=>{console.error(e);process.exitCode=1});
(async()=>{
 const views=[];const c=api.createController({get(url){return Promise.resolve({udid:url.split('/').pop(),policy:{enabled:true,days:3},points:[],next_offset:0})},render(v){views.push(v)},error(e){throw e}});
 await c.open('A');c.edit({enabled:true,days:7});await c.open('B');c.edit({enabled:true,days:12});c.saved('A');await c.refresh();assert.equal(views.at(-1).draft.days,12,'saving A cannot clear B draft');assert.equal(views.at(-1).dirty,true);await c.open('A');assert.equal(views.at(-1).dirty,false);console.log('policy completion is bound to the originating device');
})().catch(e=>{console.error(e);process.exitCode=1});
(async()=>{
 let shifted=false;const views=[],target='a'.repeat(32),first='b'.repeat(32);
 const c=api.createController({get(url){if(url.startsWith('/api/backup-point/'))return Promise.resolve({id:target});return Promise.resolve({policy:{days:3},points:shifted?[{id:first}]:[{id:target}],next_offset:0})},render(v){views.push(v)},error(e){throw e}});
 await c.open('A');c.select(target);shifted=true;await c.refresh();assert.equal(views.at(-1).selected,target,'new points cannot evict the selected point');console.log('selected point survives changes to the list window');
})().catch(e=>{console.error(e);process.exitCode=1});

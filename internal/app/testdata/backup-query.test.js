'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const root = path.resolve(__dirname, '..');

function boot() {
  const nodes = new Map(), listeners = {};
  function node(tag='div') {
    return {tagName:tag,textContent:'',value:'',style:{},dataset:{},attributes:{},children:[],hidden:false,disabled:false,
      classList:{add(){},remove(){},toggle(){},contains(){return false;}},
      setAttribute(key,value){this.attributes[key]=String(value);},getAttribute(key){return this.attributes[key]??null;},
      querySelector(selector){return this.querySelectorAll(selector)[0]||null;},
      querySelectorAll(selector){return this.children.flatMap(child=>[(selector[0]==='.'?(child.className||'').split(' ').includes(selector.slice(1)):child.tagName===selector)?child:null,...child.querySelectorAll(selector)]).filter(Boolean);},
      appendChild(child){this.children.push(child);return child;},replaceChildren(){this.children=[];this.textContent='';},
      addEventListener(type,fn){this[type]=fn;},focus(){document.activeElement=this;},select(){this.selected=true;},remove(){},click(){this.clicked=true;}};
  }
  const document={cookie:'',documentElement:{setAttribute(){}},body:node(),createElement:node,
    getElementById(id){if(!nodes.has(id))nodes.set(id,node());return nodes.get(id);},querySelector(){return null;},querySelectorAll(){return [];},addEventListener(){}};
  const window={document,navigator:{languages:['en']},localStorage:{getItem(){return null;},setItem(){}},
    addEventListener(type,fn){(listeners[type]||=[]).push(fn);},dispatchEvent(event){(listeners[event.type]||[]).forEach(fn=>fn(event));}};
  const context=vm.createContext({window,document,navigator:window.navigator,Intl,Date,Headers,AbortController,console,setTimeout,clearTimeout,Blob,Uint8Array,URL,
    CustomEvent:class{constructor(type,options){this.type=type;this.detail=options.detail;}}});
  vm.runInContext(fs.readFileSync(path.join(root,'static/i18n.js'),'utf8'),context);
  context.IOSBKI18n=window.IOSBKI18n;
  context.IOSBKI18n.register(JSON.parse(fs.readFileSync(path.join(root,'static/ui-messages.json'),'utf8')));
  context.IOSBKI18n.register(JSON.parse(fs.readFileSync(path.join(root,'../i18n/messages.json'),'utf8')));
  const helper=path.join(root,'static/backup-query.js');
  if(fs.existsSync(helper)) vm.runInContext(fs.readFileSync(helper,'utf8'),context);
  const html=fs.readFileSync(path.join(root,'templates/index.html'),'utf8');
  const script=[...html.matchAll(/<script>([\s\S]*?)<\/script>/g)].at(-1)[1].replace('{{.InitialStatus}}','{"devices":[]}');
  vm.runInContext(script,context);
  return {context,window,document,get:document.getElementById,api:context.IOSBKI18n};
}

async function run() {
  const app=boot(),calls=[];
  const failure={error:'failed',code:'api.backup_list_failed',message_code:'api.backup_query_device_error',params:{device_code:207},detail:'工具原始原因 <script>secret-free</script>',diagnostic:{operation:'list',reason:'device_error',device_error_code:207}};
  app.window.fetch=async url=>{calls.push(url);return {ok:!url.includes('backup-list'),json:async()=>url.includes('backup-info')?{size_human:'26 GB',info_status:'unavailable',info_error:failure}:failure};};
  app.get('bkCount-PHONE').textContent='123';
  vm.runInContext("loadBackups('PHONE')",app.context);
  assert.equal(app.get('bkDownload-PHONE').disabled,true,'querying and exporting the same device must not overlap in the UI');
  await new Promise(resolve=>setImmediate(resolve));
  assert.equal(calls.length,2,'Info failure must still load List');
  assert.equal(app.get('bkCount-PHONE').textContent,'—','a failed query must clear the previous count');
  assert.ok(app.get('bkInfoStatus-PHONE').children.length,'partial Info failure needs visible details');
  const details=app.get('bkListDiagnostic-PHONE');
  assert.ok(details.children.length,'List failure needs expandable diagnostic details');
  assert.equal(app.get('bkSearch-PHONE').disabled,true,'unavailable list must disable search');
  assert.equal(app.get('bkPrev-PHONE').disabled,true,'unavailable list must disable pagination');
  assert.equal(app.get('list-PHONE').textContent,'','failure must only appear once in the list surface');
  const toggle=details.querySelector('.bk-query-disclosure');
  assert.ok(toggle,'details need a keyboard-operable disclosure button');
  assert.equal(toggle.getAttribute('aria-expanded'),'false');
  const panel=details.querySelector('.bk-query-details');
  assert.equal(toggle.getAttribute('aria-controls'),panel.id);
  assert.equal(panel.hidden,true);
  toggle.click();
  assert.equal(toggle.getAttribute('aria-expanded'),'true');
  assert.equal(panel.hidden,false);
  assert.equal(panel.querySelector('pre').textContent,failure.detail,'raw evidence must remain literal text rather than JSON or HTML');
  assert.ok(panel.querySelectorAll('dd').some(field=>field.textContent==='207'));
  assert.ok(!panel.querySelectorAll('dd').some(field=>field.textContent==='49'),'absent exit codes must not be invented');
  const before=JSON.stringify(details.children);
  app.api.setLocale('zh-CN');
  assert.ok(JSON.stringify(details.children).includes('工具原始原因'),'language changes must preserve raw evidence');
  assert.notEqual(JSON.stringify(details.children),before,'diagnostic labels should follow the selected language');
  assert.equal(details.querySelector('.bk-query-disclosure').getAttribute('aria-expanded'),'true','language changes must preserve expansion');
  assert.equal(app.get('bkInfoStatus-PHONE').querySelector('.bk-query-disclosure').getAttribute('aria-expanded'),'false','Info and List expansions are independent');
  const helper=app.window.IOSBKBackupQuery;
  const downloader=helper.downloadCSV;
  const container=app.document.createElement('div');
  const copied=await helper.copyDiagnostic(container,failure,{document:app.document,navigator:app.window.navigator,t:app.api.t});
  assert.equal(copied,false,'HTTP clipboard fallback should remain selectable');
  assert.ok(container.children.some(child=>child.selected&&child.value.includes('207')),'fallback must expose selectable diagnostic text');

  let finishExport;
  app.window.IOSBKBackupQuery.downloadCSV=()=>new Promise((_,reject)=>{finishExport=reject;});
  const exporting=vm.runInContext("downloadBackupCSV('PHONE',document.getElementById('bkDownload-PHONE'))",app.context);
  assert.equal(details.querySelector('.bk-query-retry').disabled,true,'export must disable the inline retry too');
  finishExport({payload:failure});await exporting;
  assert.equal(details.querySelector('.bk-query-retry').disabled,false);
  app.window.fetch=async url=>({ok:true,json:async()=>url.includes('backup-info')?{info:'Encrypted: No',size_human:'26 GB'}:{entries:[{fields:['sample.txt','HomeDomain']}],count:1}});
  await details.querySelector('.bk-query-retry').click();
  assert.equal(app.get('bkSearch-PHONE').disabled,false,'retry should restore search after success');
  const rows=app.get('list-PHONE').textContent;
  app.window.IOSBKBackupQuery.downloadCSV=async()=>{throw {payload:failure};};
  await vm.runInContext("downloadBackupCSV('PHONE',document.getElementById('bkDownload-PHONE'))",app.context);
  assert.equal(app.get('list-PHONE').textContent,rows,'export failure must preserve loaded rows');
  assert.equal(app.get('bkCount-PHONE').textContent,'1');

  let finishCopy;
  const exportBox=app.get('bkDownloadStatus-PHONE');
  app.window.navigator.clipboard={writeText:()=>new Promise(resolve=>{finishCopy=resolve;})};
  exportBox.querySelector('.bk-query-disclosure').click();
  const copying=exportBox.querySelector('.bk-query-copy').click();
  const nextFailure={...failure,detail:'new diagnostic evidence'};
  app.window.IOSBKBackupQuery.downloadCSV=async()=>{throw {payload:nextFailure};};
  await vm.runInContext("downloadBackupCSV('PHONE',document.getElementById('bkDownload-PHONE'))",app.context);
  finishCopy();await copying;
  assert.equal(exportBox.querySelector('pre').textContent,nextFailure.detail,'late clipboard completion must not replace a newer export failure');
  assert.equal(exportBox.querySelector('.bk-query-copy-status').textContent,'','new errors must not inherit old copy feedback');
  assert.ok(app.get('bkQueryLive-export-PHONE').textContent.includes('207'),'export failures need a persistent live announcement');

  const fresh=boot();let release;
  const old=new Promise(resolve=>{release=resolve;});let count=0;
  fresh.window.fetch=async url=>{count++;if(count===1)return old;return {ok:true,json:async()=>url.includes('backup-info')?{info:'',size_human:'new'}:{entries:[],count:0}};};
  vm.runInContext("loadBackups('PHONE');loadBackups('PHONE')",fresh.context);
  await new Promise(resolve=>setImmediate(resolve));
  release({ok:true,json:async()=>({size_human:'old'})});
  await new Promise(resolve=>setImmediate(resolve));
  assert.equal(fresh.get('bkSize-PHONE').textContent,'new','stale responses must not overwrite newer results');
  assert.match(fs.readFileSync(path.join(root,'templates/index.html'),'utf8'),/onclick="downloadBackupCSV\(/,'CSV downloads must handle failure on the current page');
  let saved=0,revoked=0,cancelled=0;
  const downloadOptions={document:app.document,Blob,URL:{createObjectURL(){saved++;return 'blob:test';},revokeObjectURL(){revoked++;}},setTimeout:fn=>fn(),limit:10};
  const response=(chunks,headers={},status=200)=>({ok:status===200,status,headers:new Headers({'Content-Type':'text/csv',...headers}),json:async()=>failure,body:{async cancel(){cancelled++;},getReader(){return {async read(){if(!chunks.length)return {done:true};const next=chunks.shift();if(next instanceof Error)throw next;return {done:false,value:new Uint8Array(next)};},async cancel(){cancelled++;},releaseLock(){}};}}});
  for(const bad of [response([],{},409),response([],{"Content-Type":"text/html"}),response([[1,2]],{'Content-Length':'4'}),response([Array(11).fill(0)]),response([new Error('connection reset')])]) {
    await assert.rejects(downloader(async()=>bad,'/download',downloadOptions));
    assert.equal(saved,0,'failed downloads must never save a file');
  }
  await downloader(async()=>response([[1,2]],{'Content-Length':'2'}),'/download',downloadOptions);
  assert.equal(saved,1);assert.equal(revoked,1);assert.ok(cancelled>=3,'failed streams should be cancelled');
  await downloader(async()=>response([[1,2]],{'Content-Length':'4','Content-Encoding':'gzip'}),'/download',downloadOptions);
  assert.equal(saved,2,'decoded CSV must not be compared against compressed transfer length');
  console.log('backup query details, clipboard fallback and request state passed');
}
module.exports=run;
if(require.main===module) run().catch(error=>{console.error(error);process.exitCode=1;});

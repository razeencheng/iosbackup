'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const window = { document: { addEventListener() {} }, addEventListener() {} };
vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../static/backup-console.js'), 'utf8'), { window });
const api = window.IOSBKConsole;
const config = { start_time: '22:00', end_time: '06:00', backup_interval: 24, min_battery_level: 20, only_when_charging: true, auto_backup_enabled: false, backup_directory: '/backups', network_address: '' };
assert.deepEqual(Object.keys(api.configPayload('connection', config)).sort(), ['backup_directory', 'network_address']);
assert.equal(api.configPayload('schedule', config).backup_directory, undefined, 'schedule save cannot overwrite another group');
assert.equal(api.configPayload('schedule', config).auto_backup_enabled, false, 'false must survive partial updates');
assert.throws(() => api.configPayload('unknown', config));
const jobs = [
  { id: 'a', operation: 'backup', state: 'succeeded', history_state: 'failed' },
  { id: 'b', operation: 'check', state: 'succeeded', check: { state: 'unavailable' } },
  { id: 'c', state: 'failed' },
  { id: 'd', operation: 'backup', state: 'succeeded', history_state: 'saved', check: { state: 'passed' } },
];
assert.equal(api.filterJobs(jobs, 'check', 'attention')[0].id, 'b', 'successful execution is not the same as a completed check');
assert.equal(api.filterJobs(jobs, 'backup', 'attention').length, 2, 'history failure must remain visible');
assert.equal(api.filterJobs(jobs, 'all', 'succeeded').length, 3, 'execution filter does not silently redefine tool success');
assert.equal(api.filterJobs([{state:'starting'},{state:'running'},{state:'stopping'},{state:'stopped'}], 'all', 'running').length,3,'active task filter includes preparation and stopping');
assert.equal(api.isCurrentPoint({ current_source: { kind: 'point', point_id: 'new', available: true } }, { id: 'old', baseline: true }), false, 'an old-generation baseline is not the current source');
assert.equal(api.isCurrentPoint({ current_source: { kind: 'current', available: true } }, { id: 'new', baseline: true }), false);
assert.equal(api.isCurrentPoint({ current_source: { kind: 'point', point_id: 'new', available: true } }, { id: 'new' }), true);
assert.equal(api.isBackupRunning({backing_up:true,active_operation:'check'}),false,'a read-only check must not display phone backup progress');
assert.equal(api.isBackupRunning({backing_up:true,active_operation:'local'}),false);
assert.equal(api.isBackupRunning({backing_up:true,active_operation:'backup'}),true);
assert.equal(!!api.needsAttention({operation:'check',state:'stopped',check:{state:'unavailable',reason:'cancelled'}}),false,'a user-cancelled check is neutral');
assert.equal(!!api.needsAttention({operation:'check',state:'failed',error_code:'storage_unavailable',check:{state:'unavailable',reason:'cancelled'}}),true,'save failures remain attention-worthy after cancellation');
assert.equal(api.busyLabel({active_operation:'check'}),'console.checkingBackup');
assert.equal(api.busyLabel({active_operation:'local'}),'console.deviceBusy');
assert.equal(api.canDeleteCurrent({phase:'active',current_source:{kind:'point',available:true}}),false);
assert.equal(api.canDeleteCurrent({phase:'enabling',current_source:{kind:'current',available:true}}),false);
assert.equal(api.canDeleteCurrent({phase:'disabled',current_source:{kind:'current',available:true}}),true,'an isolated ordinary current copy retains its delete action');
assert.equal(api.canDeleteCurrent({current_source:{kind:'current',available:true}}),true,'legacy current backups retain their delete action');
console.log('console group saves, task filtering and current-source identity passed');

// 执行实际卡片／页签切换代码，只模拟 DOM、存储和数据读取边界。
function pageStateFixture({values={},devices=['A','B'],readFails=false,writeFails=false}={}) {
  const nodes=new Map(),reads=[],writes=[];
  function node(id='',classes=''){
    const names=new Set(classes.split(' ').filter(Boolean));
    const n={id,dataset:{},value:'',textContent:'',attributes:{},classList:{contains:k=>names.has(k),add:k=>names.add(k),remove:k=>names.delete(k),toggle(k,on){if(on===undefined)on=!names.has(k);on?names.add(k):names.delete(k);}},setAttribute(k,v){this.attributes[k]=String(v);},removeAttribute(k){delete this.attributes[k];},querySelectorAll(){return []}};
    if(id)nodes.set(id,n);return n;
  }
  const cards=devices.map(u=>{
    const card=node();card.dataset.udidCard=u;card.head=node();card.head.dataset.cardToggle=u;
    card.querySelector=s=>s==='[data-card-toggle]'?card.head:null;
    node('panel-'+u,'hidden');node('chev-'+u);
    card.tabs=['backups','jobs','settings'].map(sec=>{const n=node('',sec==='backups'?'snav active':'snav');n.dataset={udid:u,sec};return n});
    card.sections=['backups','jobs','settings'].map(sec=>{const n=node('',sec==='backups'?'':'hidden');n.dataset={udid:u,secPanel:sec};return n});
    for(const prefix of ['startTime','endTime','backupInterval','minBattery','chargingToggle','autoBackupToggle','backupDir','networkAddress'])node(prefix+'-'+u);
    return card;
  });
  const document={addEventListener(){},getElementById:id=>nodes.get(id)||null,
    querySelectorAll(s){
      if(s==='[data-udid-card]')return cards;
      if(s==='[data-card-toggle]')return cards.map(c=>c.head);
      if(s==='[id^="panel-"]')return cards.map(c=>nodes.get('panel-'+c.dataset.udidCard));
      if(s==='[id^="chev-"]')return cards.map(c=>nodes.get('chev-'+c.dataset.udidCard));
      const u=s.match(/\[data-udid="([^"]+)"\]/)?.[1],card=cards.find(c=>c.dataset.udidCard===u);
      if(s.startsWith('.snav'))return card?.tabs||[];
      if(s.startsWith('[data-sec-panel]'))return card?.sections||[];
      return [];
    },querySelector(s){if(s==='[data-udid-card]')return cards[0]||null;return this.querySelectorAll(s).find(n=>!s.includes('.active')||n.classList.contains('active'))||null;}};
  const jobs={hide(){},open:u=>reads.push(['jobs',u])},history={hide(){},section:(u,sec)=>reads.push([sec,u])};
  const window={document,addEventListener(){},IOSBKI18n:{t:k=>k},IOSBKJobs:jobs,IOSBKHistory:history,localStorage:{getItem(k){if(readFails)throw Error('blocked');return values[k]||null},setItem(k,v){if(writeFails)throw Error('full');values[k]=v;writes.push(v)}}};
  const context=vm.createContext({window,document,IOSBKJobs:jobs,IOSBKHistory:history,setSVGIcon(){},backupBrowser:{visibilityChanged(){},close(){}},loadEncStatus:u=>reads.push(['encryption',u]),maybeReload(){},console});
  vm.runInContext(fs.readFileSync(path.join(__dirname,'../static/backup-console.js'),'utf8'),context);
  context.IOSBKConsole=window.IOSBKConsole;
  const template=fs.readFileSync(path.join(__dirname,'../templates/index.html'),'utf8');
  vm.runInContext(template.slice(template.indexOf('const expandedCards='),template.indexOf('function openBackupFiles')),context);
  window.toggleCard=context.toggleCard;window.switchSection=context.switchSection;
  window.IOSBKConsole.init();
  return {values,reads,writes,toggle:context.toggleCard,select:context.switchSection,
    expanded:()=>cards.filter(c=>!nodes.get('panel-'+c.dataset.udidCard).classList.contains('hidden')).map(c=>c.dataset.udidCard),
    section:u=>cards.find(c=>c.dataset.udidCard===u).tabs.find(n=>n.classList.contains('active')).dataset.sec};
}

{
  const f=pageStateFixture();
  assert.deepEqual(f.expanded(),[],'first visit must leave every device collapsed');
  assert.deepEqual(f.reads,[],'collapsed cards must not start panel requests');
  f.toggle('B');f.select('B','jobs');f.toggle('A');f.select('A','settings');
  const restored=pageStateFixture({values:f.values,devices:['B','A']});
  assert.deepEqual(restored.expanded(),['A'],'restore by device identity, not list position');
  assert.equal(restored.section('A'),'settings');assert.equal(restored.section('B'),'jobs');
  assert.deepEqual(restored.reads,[['settings','A'],['encryption','A']],'restore only loads the expanded panel once');
  restored.toggle('A');assert.deepEqual(restored.expanded(),[]);
  const collapsed=pageStateFixture({values:f.values});assert.deepEqual(collapsed.expanded(),[],'all-collapsed state must survive refresh');
  assert.equal(collapsed.section('B'),'jobs','collapsed devices retain their tab');
  collapsed.toggle('B');assert.equal(collapsed.section('B'),'jobs');
  const removed=pageStateFixture({values:f.values,devices:['A']});assert.deepEqual(removed.expanded(),[],'removed device must not expand a different one');
  assert.deepEqual(removed.reads,[]);
  const stored=JSON.parse(Object.values(f.values)[0]);
  assert.deepEqual(Object.keys(stored).sort(),['expanded','sections','version'],'only navigation is retained');
  const key=Object.keys(f.values)[0];
  for(const bad of ['{', 'null','[]',JSON.stringify({...stored,version:999}),JSON.stringify({...stored,expanded:'UNKNOWN'})]) {
    assert.deepEqual(pageStateFixture({values:{[key]:bad}}).expanded(),[],'invalid or obsolete state falls back to collapsed');
  }
  const unknown=pageStateFixture({values:{[key]:JSON.stringify({...stored,expanded:'A',sections:{A:'<script>'}})}});
  assert.deepEqual(unknown.expanded(),['A']);assert.equal(unknown.section('A'),'backups','unknown tab uses safe default');
  const denied=pageStateFixture({values:f.values,readFails:true,writeFails:true});assert.deepEqual(denied.expanded(),[]);assert.doesNotThrow(()=>{denied.toggle('A');denied.select('A','jobs');denied.toggle('A')});
  assert.deepEqual(denied.expanded(),[],'blocked storage cannot break manual navigation');
  console.log('device expansion and tabs survive reload without restoring dialogs or settings');
}

{
  const dialogs=[],submitted=[];
  function node(){return {children:[],classList:{add(){},remove(){}},setAttribute(){},addEventListener(){},append(...children){this.children.push(...children);},querySelector(){return this.children.at(-1);},showModal(){dialogs.push(this);},close(){},remove(){},focus(){}};}
  const primary=node(),page={document:{addEventListener(){},getElementById:()=>primary,createElement:node,body:node()},addEventListener(){},IOSBKI18n:{t:key=>key},submitBackup(...args){submitted.push(args);}};
  vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../static/backup-console.js'),'utf8'),{window:page});
  page.IOSBKConsole.backup('A');
  assert.equal(submitted.length,0,'advanced entry must require confirmation');
  dialogs.at(-1).children[1].children.at(-1).children[0].onclick();
  assert.equal(submitted.length,0,'cancel must not start a full backup');
  page.IOSBKConsole.backup('A');
  dialogs.at(-1).children[1].children.at(-1).children[1].onclick();
  assert.equal(submitted.length,1);
  assert.equal(submitted[0][0],'A');assert.equal(submitted[0][2],'full');
  console.log('force-full remains behind its own confirmation');
}

(async()=>{
  const messages=JSON.parse(fs.readFileSync(path.join(__dirname,'../static/ui-messages.json'),'utf8')).en;
  for(const [job,expected] of [
    [{state:'succeeded',history_state:'failed',history_phase:'publish'},/History could not be saved/],
    [{state:'succeeded',history_state:'pending',check:{state:'unavailable',reason:'encrypted_manifest'}},/Encrypted manifest/],
    [{state:'failed',error_code:'unknown_vendor'},/failed/i]
  ]){
    const host={classList:{toggle(){}},replaceChildren(){this.children=[];},append(...children){this.children=children;}};
    const page={document:{addEventListener(){},getElementById:id=>id==='consoleRecent-A'?host:null,createElement:()=>({})},addEventListener(){},formatBackupBytes:()=>'',IOSBKI18n:{t:(key,params={})=>(messages[key]||key).replace(/\{(\w+)\}/g,(_,key)=>params[key]),has:key=>key in messages,fetch:async url=>({ok:true,json:async()=>({jobs:url.includes('type=backup')?[{id:'task',...job}]:Array.from({length:5},(_,i)=>({id:'check-'+i,operation:'check',state:'succeeded'}))})})}};
    vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../static/backup-console.js'),'utf8'),{window:page});
    page.IOSBKConsole.updateHistory({udid:'A',points:[],data:{policy:{enabled:false},current_source:{kind:'missing'},storage:{},total:0,logical_bytes:0,bytes_known:true}});
    await new Promise(setImmediate);
    assert.ok(host.children?.length,'recent backup issues must remain visible after five newer checks');
    assert.match(host.children[0].textContent,expected,'the banner must show a concrete reason');
    assert.doesNotMatch(host.children[0].textContent,/needs attention: Needs attention/i);
  }
})().catch(error=>{console.error(error);process.exitCode=1});

(async()=>{
  let locale='en',fetches=0;const listeners={};
  const host={classList:{toggle(){}},replaceChildren(){this.children=[];},append(...children){this.children=children;}};
  const page={document:{addEventListener(){},getElementById:id=>id==='consoleRecent-A'?host:null,createElement:()=>({})},addEventListener(name,fn){listeners[name]=fn;},formatBackupBytes:()=>'',IOSBKI18n:{t:key=>locale+':'+key,has:()=>true,fetch:async()=>{fetches++;return {ok:true,json:async()=>({jobs:[{id:'task',state:'failed',error_code:'busy'}]})};}}};
  vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../static/backup-console.js'),'utf8'),{window:page});
  page.IOSBKConsole.updateHistory({udid:'A',points:[],data:{policy:{enabled:false},current_source:{kind:'missing'},storage:{},total:0,logical_bytes:0,bytes_known:true}});
  await new Promise(setImmediate);assert.equal(host.children[1].textContent,'en:console.viewJob');
  locale='zh-CN';listeners['iosbk:localechange']();
  assert.equal(host.children[1].textContent,'zh-CN:console.viewJob','the recent issue translates immediately');
  assert.equal(fetches,1,'language changes reuse retained task data without another request');
})().catch(error=>{console.error(error);process.exitCode=1});

// A connection result belongs to the draft that started it, including after
// cancel restores the same value and a second request is started.
(async () => {
  const pending = [], nodes = {}, results = {};
  ['backupDir-A','networkAddress-A','dirResult-A','ipResult-A','testIPBtn-A'].forEach(id => {
    nodes[id] = {id, value:'', textContent:'', disabled:false, style:{}, classList:{add(){},remove(){}}};
  });
  const context = {
    document:{getElementById:id=>nodes[id]}, inlineResults:results,
    showInlineResult(node,ok,payload){results[node.id]={ok,payload};node.textContent=payload;},
    postJSON(){return new Promise(resolve=>pending.push(resolve));}, applyStatus(){}
  };
  const template=fs.readFileSync(path.join(__dirname,'../templates/index.html'),'utf8');
  vm.runInNewContext(template.slice(template.indexOf('/* ============== backup settings'),template.indexOf('function pairingStatusText')),context);
  nodes['backupDir-A'].value='/draft';context.checkBackupDir('A');
  assert.equal(typeof context.clearConnectionResults,'function','cancel must invalidate pending diagnostics');
  context.clearConnectionResults('A');nodes['backupDir-A'].value='/backups';
  pending.shift()({ok:true});await new Promise(setImmediate);
  assert.equal(results['dirResult-A'],undefined,'cancelled directory check cannot reappear');
  nodes['networkAddress-A'].value='10.0.0.1';context.testDeviceIP('A');
  context.clearConnectionResults('A');context.testDeviceIP('A');
  pending.shift()({ok:true});await new Promise(setImmediate);
  assert.equal(nodes['testIPBtn-A'].disabled,true,'old completion cannot unlock the new request');
  pending.shift()({ok:true});await new Promise(setImmediate);
  assert.equal(results['ipResult-A'].payload,'bset.testOk');
  assert.equal(nodes['testIPBtn-A'].disabled,false);
  console.log('cancelled connection checks cannot publish stale results');
})().catch(error=>{console.error(error);process.exitCode=1});

// Closing details must restore focus even when selection and polling replace the row.
{
  let focused='';
  function node(){return {children:[],classList:{add(){},remove(){},contains(){return false;}},setAttribute(){},addEventListener(){},append(...children){this.children.push(...children);},querySelector(){return this.children.at(-1);},showModal(){},close(){},remove(){},focus(){focused=this.name||'header';},getClientRects(){return [1];},isConnected:true};}
  const source={...node(),name:'old',isConnected:false,dataset:{point:'point-a',historyAction:'select'}},replacement={...node(),name:'replacement',dataset:{point:'point-a',historyAction:'select'}},content={...node(),closest(){return null;}};
  const page={document:{activeElement:source,addEventListener(){},createElement:node,body:node(),getElementById(id){if(id==='historyDetail-A')return content;if(id==='historyList-A')return {querySelectorAll(){return [replacement];}};return null;}},addEventListener(){},IOSBKI18n:{t:key=>key},IOSBKHistory:{back(){}}};
  vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../static/backup-console.js'),'utf8'),{window:page});
  page.IOSBKConsole.pointDrawer('A',source);page.IOSBKConsole.closeLayers();
  assert.equal(focused,'replacement','details must return keyboard focus to the matching replacement row');
}

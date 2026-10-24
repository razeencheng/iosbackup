'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const runtime = path.resolve(__dirname, '../static/backup-browser.js');
assert.ok(fs.existsSync(runtime), 'production offline backup browser controller must exist');
const browser = require(runtime);
function snapshot(overrides={}) {
  return {...{source_key:'source-a',revision:'r1',last_backup_at:'2026-10-17T01:00:00Z',refresh:{state:'idle'},overview:{status:'available',freshness:'current',size_bytes:123456,encrypted:false,read_at:'2026-10-17T01:01:00Z'},file_list:{status:'available',freshness:'current',complete:true,csv_available:true,entry_count:5010,preview_count:5000,read_at:'2026-10-17T01:01:00Z',entries:Array.from({length:5000},(_,i)=>({domain:'HomeDomain',relative_path:'Library/file-'+i,flags:1}))}},...overrides};
}
const emptyPart={status:'missing',freshness:'unknown'};
for (const [state,data,error] of [
 ['offlineReady',snapshot()],
 ['first',snapshot({overview:emptyPart,file_list:emptyPart,refresh:{state:'running'}})],
 ['firstFailed',snapshot({overview:{status:'unavailable'},file_list:{status:'unavailable'},refresh:{state:'failed'}})],
 ['refreshing',snapshot({refresh:{state:'queued'}})],
 ['failed',snapshot({refresh:{state:'failed'}})],
 ['encrypted',snapshot({overview:{status:'available',encrypted:true},file_list:{status:'unsupported'},refresh:{state:'failed'}})],
 ['firstFailed',snapshot({overview:{status:'unavailable',encrypted:true,message_code:'api.backup_index_refresh_failed'},file_list:{status:'unsupported',message_code:'api.backup_index_encrypted'},refresh:{state:'failed'}})],
 ['encryptedCached',snapshot({overview:{status:'available',encrypted:true},file_list:{...snapshot().file_list,freshness:'stale',message_code:'api.backup_index_encrypted'}})],
 ['unavailable',snapshot({overview:{...snapshot().overview,message_code:'api.backup_index_unavailable'},refresh:{state:'failed'}})],
 ['empty',snapshot({overview:{...emptyPart,message_code:'api.backup_index_missing'},file_list:emptyPart,refresh:{state:'failed',message_code:'api.backup_index_missing'}})],
 ['failed',snapshot(),true],
]) assert.equal(browser.classify(data,error),state,state);
assert.equal(browser.csvURL('PHONE',snapshot()),'/api/backup-index/PHONE?format=csv&revision=r1');
assert.equal(browser.csvURL('PHONE',snapshot({file_list:{status:'available',complete:false,csv_available:true}})),'');
assert.equal(browser.csvURL('PHONE',snapshot({revision:''})), '');
assert.equal(browser.view({snapshot:snapshot({refresh:{state:'running'}}),requestError:true}).busy,false,'failed HTTP request must allow retry even after a running snapshot');
assert.equal(browser.view({snapshot:snapshot(),posting:true}).scenario,'refreshing','POST immediately reports refreshing while retaining the snapshot');
assert.equal(browser.validDate('0001-01-01T00:00:00Z'),false);
assert.equal(browser.validDate('2026-10-17T01:01:00Z'),true);
function deferred(){let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b;});return {promise,resolve,reject};}
function setup(){
 const requests=[],renders=[],timers=new Map();let timerID=0,visible=true;
 const controller=browser.create({fetch(url,options){const d=deferred();requests.push({...d,url,options});return d.promise;},render(id,state){renders.push({id,state});},isVisible(){return visible;},setTimeout(fn){timers.set(++timerID,fn);return timerID;},clearTimeout(id){timers.delete(id);}});
 function reply(i,data){requests[i].resolve({ok:true,json:()=>Promise.resolve(data)});}
 return {controller,requests,renders,timers,reply,setVisible(v){visible=v;}};
}
async function tick(){for(let i=0;i<8;i++)await Promise.resolve();}
(async()=>{
 const initial=setup();
 const firstRead=initial.controller.open('PHONE');await tick();
 initial.controller.notify('PHONE',{source_key:'new-source',revision:'new',refresh_state:'running'});await tick();
 initial.reply(0,snapshot());await firstRead;
 assert.ok(initial.controller.state('PHONE').snapshot === null,'source change during first request discards unknown-source response');
 const revision=setup();let read=revision.controller.open('PHONE');await tick();revision.reply(0,snapshot());await read;
 read=revision.controller.load('PHONE');await tick();revision.controller.notify('PHONE',{source_key:'source-a',revision:'r3',refresh_state:'idle'});
 revision.reply(1,snapshot({revision:'r2'}));await read;
 assert.equal(revision.controller.state('PHONE').snapshot.revision,'r1','response predating a new SSE revision stays unpublished until recheck');
 const h=setup(),c=h.controller;
 const first=c.open('PHONE');await tick();assert.equal(h.requests.length,1);h.reply(0,snapshot());await first;
 c.search('PHONE','Library/file');c.page('PHONE',2);assert.equal(c.state('PHONE').page,2);
 const refresh=c.refresh('PHONE');await tick();assert.equal(h.requests[1].options.method,'POST');assert.equal(c.state('PHONE').snapshot.revision,'r1');
 h.requests[1].reject(Error('offline'));await refresh;
 assert.equal(c.state('PHONE').query,'Library/file');assert.equal(c.state('PHONE').page,2);assert.equal(c.state('PHONE').snapshot.file_list.entries.length,5000);
 assert.equal(browser.view(c.state('PHONE')).scenario,'failed');
 c.rerender();assert.equal(c.state('PHONE').page,2,'locale rerender preserves page');
 const pending=c.load('PHONE');const newer=c.refresh('PHONE');await tick();h.reply(3,snapshot({revision:'r3'}));await newer;h.reply(2,snapshot({revision:'r2'}));await pending;
 assert.equal(c.state('PHONE').snapshot.revision,'r3','late HTTP response cannot replace newer response');
 const old=c.load('PHONE');c.notify('PHONE',{source_key:'source-b',revision:'b1',refresh_state:'running'});await tick();
 assert.equal(c.state('PHONE').snapshot,null);assert.equal(c.state('PHONE').query,'');
 h.reply(4,snapshot({revision:'old-source'}));await old;await tick();
 assert.equal(c.state('PHONE').snapshot,null,'late old-source response must stay discarded');
 const last=h.requests.length-1;h.reply(last,snapshot({source_key:'source-b',revision:'b1',refresh:{state:'running'}}));await tick();
 assert.equal(h.timers.size,1,'only active reads poll');c.close('PHONE');assert.equal(h.timers.size,0,'closed panel must stop polling');
 const reopen=c.open('PHONE');await tick();h.reply(h.requests.length-1,snapshot({source_key:'source-b',revision:'b2'}));await reopen;
 assert.equal(h.timers.size,0,'terminal reads must stop polling');
 c.notify('PHONE',{source_key:'source-b',revision:'b3',refresh_state:'running'});await tick();const count=h.requests.length;
 c.notify('PHONE',{source_key:'source-b',revision:'b3',refresh_state:'running'});assert.equal(h.requests.length,count,'duplicate SSE messages coalesce');
 h.reply(count-1,snapshot({source_key:'source-b',revision:'b3',refresh:{state:'running'}}));await tick();h.setVisible(false);c.visibilityChanged();assert.equal(h.timers.size,0);
 assert.equal(browser.view({...c.state('PHONE'),query:'notfound'}).rows.length,0);
 const unknown=browser.view({snapshot:snapshot({overview:{status:'unavailable',size_bytes:null,encrypted:null},file_list:{status:'unavailable',entry_count:null}}),query:'',page:0});
 assert.equal(unknown.size,null);assert.equal(unknown.count,null,'missing counts cannot become zero');
 // 路径和域名通过文本节点输出，即使它们含有 HTML。
 const nodes=new Map();function node(){return {style:{},dataset:{},children:[],textContent:'',classList:{toggle(){}},setAttribute(k,v){this[k]=String(v);},removeAttribute(k){delete this[k];},appendChild(n){this.children.push(n);},replaceChildren(){this.children=[];}};}
 const document={getElementById(id){if(!nodes.has(id))nodes.set(id,node());return nodes.get(id);},createElement:node};
 const tr=(k,p)=>k+JSON.stringify(p||{});const renderer=browser.domRenderer({document,tr,formatDate:s=>s,locale:()=> 'en'});
 renderer('PHONE',{snapshot:snapshot({file_list:{...snapshot().file_list,entries:[{relative_path:'<img src=x onerror=alert(1)>',domain:'<script>bad</script>',flags:1}]}}),query:'',page:0});
 assert.equal(nodes.get('list-PHONE').children[0].children[0].textContent,'<img src=x onerror=alert(1)>');
 assert.equal(nodes.get('list-PHONE').children[0].children[1].textContent,'<script>bad</script>');
 assert.ok(!('innerHTML' in nodes.get('list-PHONE').children[0]));
 renderer('PHONE',{snapshot:snapshot({overview:{status:'unavailable',encrypted:true},file_list:{status:'unsupported',message_code:'api.backup_index_encrypted'},refresh:{state:'failed'}}),query:'',page:0});
 assert.equal(nodes.get('bkStatusTitle-PHONE').textContent,'bindex.firstFailedTitle{}','encrypted-list limitation must not claim unreadable overview is available');
 assert.equal(nodes.get('bkSize-PHONE').textContent,'—');
 assert.equal(nodes.get('bkCSV-PHONE')['aria-disabled'],'true');
 console.log('offline backup browser states, requests, pagination and safe rendering passed');
})().catch(error=>{console.error(error);process.exitCode=1;});

'use strict';
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const window={document:{addEventListener(){}},addEventListener(){}};
const context=vm.createContext({window,console,AbortController,setTimeout,clearTimeout});
vm.runInContext(fs.readFileSync(path.join(__dirname,'../static/backup-jobs.js'),'utf8'),context);
const api=window.IOSBKJobs;
assert.equal(typeof api.durationText,'function');
window.IOSBKI18n={t:(key,p={})=>key+':'+JSON.stringify(p)};
for(const [seconds,key] of [[0,'seconds'],[59,'seconds'],[60,'minutes'],[3599,'minutes'],[3600,'hours']]){
 const start='2026-10-30T00:00:00Z',end=new Date(Date.parse(start)+seconds*1000).toISOString();
 assert.ok(api.durationText(start,end).startsWith('console.duration.'+key+':'));
}
assert.ok(api.durationText('2026-10-30T00:00:00Z','').startsWith('console.inProgress'));
assert.ok(api.durationText('bad','bad').startsWith('jobs.unknown'));
assert.ok(api.durationText('2026-10-31T00:00:00Z','2026-10-30T00:00:00Z').startsWith('jobs.unknown'));
const events=api.mergeEvents([{cursor:1},{cursor:2}],[{cursor:2},{cursor:3}],2);
assert.deepEqual(Array.from(events,e=>e.cursor),[2,3],'repeated pulls must not duplicate events, and the view must stay bounded');
console.log('backup task timeline is bounded and deduplicated');

(async()=>{
 const views=[],existing='bkp-000000000004',missing='bkp-000000000005';
 const controller=api.createController({
  get(url){if(url.startsWith('/api/backup-jobs/'))return Promise.resolve({jobs:[{id:existing,last_cursor:0}],recording_status:'available'});if(url.includes(missing))return Promise.reject(Object.assign(new Error('expired'),{status:410}));return Promise.resolve({job:{id:existing},events:[]});},
  render(view){views.push(view)},error(error){assert.equal(error.status,410)},schedule(){return 1},cancel(){}
 });
 await controller.open('PHONE');await controller.select(missing);
 assert.equal(views.at(-1).job,null,'following an expired link must not silently open the latest task');
 assert.equal(views.at(-1).missing,true);
 controller.close();console.log('expired task links do not open unrelated jobs');
})().catch(error=>{console.error(error);process.exitCode=1;});

(async()=>{
 const views=[],requests=[],errors=[],ids=Array.from({length:12},(_,i)=>'bkp-'+String(12-i).padStart(12,'0'));
 let fail=false,newRecord=false,release;
 const controller=api.createController({
  get(url){
   requests.push(url);const u=new URL(url,'http://test');
   if(u.pathname.startsWith('/api/backup-job/'))return Promise.resolve({job:{id:u.pathname.split('/')[3]},events:[]});
   if(fail)return Promise.reject(new Error('offline'));
   if(u.searchParams.get('result')==='failed')return new Promise(resolve=>{release=resolve});
   const cursor=u.searchParams.get('cursor')||'p0',index=Number(cursor.slice(1));
   return Promise.resolve({jobs:ids.slice(index,index+5).map(id=>({id})),page_cursor:cursor,next_cursor:index<10?'p'+(index+5):'',has_more:index<10,has_new:newRecord,recording_status:'available'});
  },render(v){views.push(v)},error(e){errors.push(e)},schedule(){return 1},cancel(){}
 });
 await controller.open('PHONE');assert.equal(views.at(-1).jobs.length,5);
 assert.equal(typeof controller.next,'function','task list needs real pagination');
 await controller.next();assert.equal(views.at(-1).jobs[0].id,ids[5]);assert.equal(views.at(-1).hasPrevious,true);
 await controller.select(ids[11]);assert.equal(views.at(-1).job.id,ids[11],'linked details remain available outside the page');assert.ok(!views.at(-1).missing);
 assert.equal(views.at(-1).jobs[0].id,ids[5],'details must preserve the page');
 fail=true;await controller.next();assert.equal(views.at(-1).jobs[0].id,ids[5],'failed navigation must keep the current page');fail=false;
 await controller.next();assert.equal(views.at(-1).jobs.length,2);assert.equal(views.at(-1).hasMore,false);
 await controller.previous();assert.equal(views.at(-1).jobs[0].id,ids[5]);
 newRecord=true;await controller.select(ids[6]);assert.equal(views.at(-1).jobs[0].id,ids[5]);assert.equal(views.at(-1).hasNew,true);
 await controller.latest();assert.equal(views.at(-1).hasPrevious,false);assert.equal(views.at(-1).jobs[0].id,ids[0]);
 await controller.next();await controller.filter({type:'check',result:'attention'});
 assert.equal(views.at(-1).hasPrevious,false);assert.ok(requests.some(url=>url.includes('type=check')&&url.includes('result=attention')&&!url.includes('cursor=')),'filters are applied server-side and reset the page');
 const stale=controller.filter({type:'check',result:'failed'});
 await controller.filter({type:'backup',result:'all'});release({jobs:[],page_cursor:'stale'});await stale;
 assert.equal(views.at(-1).filter.type,'backup','slow filter response cannot overwrite a newer filter');assert.equal(views.at(-1).jobs.length,5);
 assert.equal(errors.length,1);controller.close();console.log('task pagination preserves pages, filters, linked details and request ordering');
})().catch(error=>{console.error(error);process.exitCode=1;});

(async()=>{
 let releaseA;
 const views=[],errors=[];
 const bID='bkp-000000000002';
 const controller=api.createController({
  get(url){
   if(url==='/api/backup-jobs/A')return new Promise(resolve=>{releaseA=resolve;});
   if(url==='/api/backup-jobs/B')return Promise.resolve({jobs:[{id:bID,last_cursor:2}],recording_status:'available'});
   assert.match(url,/\/api\/backup-job\/bkp-000000000002\/events/);
   return Promise.resolve({job:{id:bID},events:[{cursor:1},{cursor:2}],next_cursor:2,has_more:false});
  },render(view){views.push(view);},error(error){errors.push(error);},schedule(){return 1;},cancel(){}
 });
 const old=controller.open('A');
 await controller.open('B');
 releaseA({jobs:[{id:'bkp-000000000001',last_cursor:1}],recording_status:'available'});
 await old;
 assert.equal(views.at(-1).udid,'B','late responses cannot replace the selected device');
 assert.equal(views.at(-1).job.id,bID);
 assert.deepEqual(Array.from(views.at(-1).events,e=>e.cursor),[1,2]);
 assert.equal(errors.length,0);
 controller.close();
 console.log('task selection ignores stale asynchronous responses');
})().catch(error=>{console.error(error);process.exitCode=1;});

(async()=>{
 let removed=false;
 const id='bkp-000000000003',views=[],errors=[];
 const controller=api.createController({
  get(url){
   if(url.startsWith('/api/backup-jobs/'))return Promise.resolve({jobs:[{id,last_cursor:1}],recording_status:'available'});
   if(removed){const error=new Error('expired');error.status=410;return Promise.reject(error);}
   return Promise.resolve({job:{id},events:[{cursor:1}],next_cursor:1});
  },render(view){views.push(view);},error(error){errors.push(error);},schedule(){return 1;},cancel(){}
 });
 await controller.open('PHONE');
 assert.equal(views.at(-1).job.id,id);
 removed=true;
 await controller.select(id);
 assert.equal(views.at(-1).selected,'','expired selection must be cleared instead of retrying it forever');
 assert.equal(views.at(-1).job,null);
 assert.equal(views.at(-1).events.length,0);
 assert.equal(views.at(-1).missing,true,'410 must preserve the expired-task state across future refreshes');
 assert.equal(errors.length,1);
 controller.close();
 console.log('expired task selection is cleared');
})().catch(error=>{console.error(error);process.exitCode=1;});

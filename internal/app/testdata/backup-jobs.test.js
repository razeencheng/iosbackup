'use strict';
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const vm=require('node:vm');
const window={document:{addEventListener(){}},addEventListener(){}};
const context=vm.createContext({window,console,AbortController,setTimeout,clearTimeout});
vm.runInContext(fs.readFileSync(path.join(__dirname,'../static/backup-jobs.js'),'utf8'),context);
const api=window.IOSBKJobs;
const events=api.mergeEvents([{cursor:1},{cursor:2}],[{cursor:2},{cursor:3}],2);
assert.deepEqual(Array.from(events,e=>e.cursor),[2,3],'repeated pulls must not duplicate events, and the view must stay bounded');
console.log('backup task timeline is bounded and deduplicated');

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
 assert.equal(errors.length,1);
 controller.close();
 console.log('expired task selection is cleared');
})().catch(error=>{console.error(error);process.exitCode=1;});

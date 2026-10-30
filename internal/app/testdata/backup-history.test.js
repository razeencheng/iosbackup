'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const window={document:{addEventListener(){}},addEventListener(){}};
vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../static/backup-history.js'),'utf8'),{window,AbortController,console});
const api=window.IOSBKHistory;
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

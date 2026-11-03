'use strict';
const assert=require('node:assert/strict'),vm=require('node:vm');
const {boot,dom,pageScript,catalogs}=require('./i18n-test-helpers');
const app=boot(),ui=dom(app);
vm.runInContext(pageScript('index.html'),app.context);
ui.get('card-A').setAttribute('data-udid-card','A');
const initialKeys={'title':'progress.preparingTitle','subtitle':'progress.preparingSubtitle','status-title':'progress.preparingTitle','status-hint':'progress.preparingHint'};
Object.entries(initialKeys).forEach(([suffix,key])=>ui.get('bkprogress-'+suffix+'-A').setAttribute('data-i18n',key));
const writes=[];
for(const suffix of ['title','subtitle','status-title','status-hint','activity']) {
 const node=ui.get('bkprogress-'+suffix+'-A');let text=node.textContent;
 Object.defineProperty(node,'textContent',{get:()=>text,set:value=>{writes.push({suffix,value});text=value;}});
}
const status=ui.get('bkprogress-status-A');let statusClass='';
Object.defineProperty(status,'className',{get:()=>statusClass,set:value=>{writes.push({suffix:'class',value});statusClass=value;}});
const icon=ui.get('bkprogress-status-icon-A'),setAttribute=icon.setAttribute;
icon.setAttribute=function(key,value){writes.push({suffix:'icon',value});setAttribute.call(this,key,value);};
const base={udid:'A',name:'Phone',conn:'wifi',online:true,backing_up:true,active_operation:'backup',backup_job_active:true,backup_job_id:'job-a',backup_state:'running'};
function render(progress,extra={}) {const device={...base,...extra,backup_progress:progress};vm.runInContext('applyStatus('+JSON.stringify({devices:[device],backup_in_progress:device.backing_up?1:0,timezone:'Asia/Shanghai'})+')',app.context);return device;}
const receiving={state:'running',phase:'receiving',overall_percent:3,current_bytes:5,current_total_bytes:10,last_activity_at:'2026-11-02T12:00:01Z'};
render(receiving);
vm.runInContext('var progressRenderCount=0,originalProgressRenderer=renderBackupProgress;renderBackupProgress=function(d){progressRenderCount++;return originalProgressRenderer(d);}',app.context);
writes.length=0;
render({...receiving});
assert.equal(vm.runInContext('progressRenderCount',app.context),1,'each status snapshot renders progress once');
assert.deepEqual(writes,[],'identical snapshots must not rewrite progress copy, tone or icon');
render({...receiving,current_bytes:7,last_activity_at:'2026-11-02T12:00:20Z'});
assert.deepEqual(writes,[],'byte updates and activity within the displayed minute must not rewrite status');
assert.match(ui.get('bkprogress-current-value-A').textContent,/7 B/);
app.api.setLocale('zh-CN');
assert.equal(ui.get('bkprogress-status-title-A').textContent,catalogs['zh-CN']['progress.runningTitle']);
assert.ok(!writes.some(w=>w.value===catalogs['zh-CN']['progress.preparingTitle']),'language switch must not flash the initial preparing copy');
render({...receiving,phase:'waiting_device'});
assert.equal(ui.get('bkprogress-current-A').classList.contains('hidden'),false,'short protocol waits must keep the file row in the layout');
assert.doesNotMatch(ui.get('bkprogress-current-value-A').textContent,/5 B/,'waiting must not present stale file bytes as active transfer');
render({...receiving,phase:'receiving',current_bytes:null,current_total_bytes:null});
assert.equal(ui.get('bkprogress-current-A').classList.contains('hidden'),false,'file boundaries retain the established slot');
render({...receiving,phase:'sending'});
assert.equal(ui.get('bkprogress-status-title-A').textContent,catalogs['zh-CN']['progress.sendingTitle']);
for(const phase of ['preparing','waiting_authorization','waiting_device']) {
 render({state:'running',phase},{backup_job_id:'job-'+phase});
 assert.equal(ui.get('bkprogress-value-A').textContent,'','unknown progress must not show a dash or a made-up percent');
 assert.equal(ui.get('bkprogress-track-A').getAttribute('aria-valuenow'),null);
 if(phase==='waiting_authorization')assert.equal(ui.get('bkprogress-track-A').classList.contains('indeterminate'),false);
}
for(const percent of [0,3]) {
 render({...receiving,overall_percent:percent});
 assert.equal(ui.get('bkprogress-value-A').textContent,percent+'%');
 assert.equal(ui.get('bkprogress-track-A').getAttribute('aria-valuenow'),String(percent));
}
const local={state:'preparing',phase:'local_copying',local:{phase:'copying',bytes:50,total_bytes:100,totals_known:true}};
render(local,{backup_job_id:'job-local'});
assert.equal(ui.get('bkprogress-value-A').textContent,'50%');
assert.equal(ui.get('bkprogress-track-A').getAttribute('aria-label'),catalogs['zh-CN']['progress.localAria']);
assert.equal(ui.get('bkprogress-status-hint-A').textContent,catalogs['zh-CN']['progress.localHint']);
assert.equal(ui.get('bkprogress-current-A').classList.contains('hidden'),true,'new tasks must not inherit the previous file slot');
for(const state of ['stopping','stopped','failed','interrupted','completed']) {
 render({...receiving,state},{backup_state:state,backing_up:state==='stopping'});
 assert.equal(ui.get('bkbar-A').getAttribute('data-progress-mode'),state,'terminal/stop state is immediate');
}
for(const state of ['stopped','failed','interrupted']) {
 render({...local,state,phase:state},{backup_state:state,backing_up:false,backup_job_active:false});
 assert.equal(ui.get('bkprogress-value-A').textContent,'50%','ending during local copying must retain the last measured local progress');
 assert.equal(ui.get('bkprogress-track-A').getAttribute('aria-label'),catalogs['zh-CN']['progress.localAria'],'local progress must not become phone progress after stopping');
 assert.equal(ui.get('bkprogress-track-A').classList.contains('indeterminate'),false,'terminal progress must never animate');
 assert.equal(ui.get('bkprogress-current-A').classList.contains('hidden'),true,'terminal summaries do not retain an empty current-file slot');
 render({state},{backup_state:state,backing_up:false,backup_job_active:false});
 assert.equal(ui.get('bkprogress-subtitle-A').classList.contains('hidden'),true,'without a reported percentage the terminal summary must not retain a progress subtitle');
 assert.equal(ui.get('bkprogress-activity-A').classList.contains('hidden'),true,'terminal summaries omit an empty activity placeholder');
 render({...receiving,state,phase:state},{backup_state:state,backing_up:false,backup_job_active:false});
 assert.equal(ui.get('bkprogress-value-A').textContent,'3%','terminal phone progress remains the measured value, not 100%');
 assert.equal(ui.get('bkprogress-track-A').classList.contains('empty'),false,'a known terminal percentage keeps its track');
 assert.equal(ui.get('bkprogress-subtitle-A').textContent,catalogs['zh-CN']['progress.endedOverallSubtitle']);
}
render(receiving);
assert.equal(ui.get('bkbar-A').getAttribute('data-progress-terminal'),null,'a new active snapshot restores the live layout');
assert.equal(ui.get('bkprogress-subtitle-A').classList.contains('hidden'),false);
render({state:'interrupted'},{backup_state:'stopped',backing_up:false,backup_job_active:false,auto_backup_hold:true});
assert.equal(ui.get('bkbar-A').classList.contains('hidden'),true,'the hold notice replaces the redundant stopped panel without measured progress');
render({...receiving,state:'interrupted'},{backup_state:'stopped',backing_up:false,backup_job_active:false,auto_backup_hold:true});
assert.equal(ui.get('bkbar-A').classList.contains('hidden'),false,'measured progress remains available after stopping');
assert.equal(ui.get('bkprogress-status-A').classList.contains('hidden'),true,'manual stop must not show a second yellow notice');
render({...receiving,state:'failed'},{backup_state:'failed',backing_up:false,backup_job_active:false,auto_backup_hold:true});
assert.equal(ui.get('bkprogress-status-A').classList.contains('hidden'),false,'actual failures must remain visible even while held');
console.log('progress snapshots update only changed values; waits, locale changes and local/phone scopes remain distinct');

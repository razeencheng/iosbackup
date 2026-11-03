'use strict';
const assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs'),path=require('node:path');
const {boot,dom,pageScript,catalogs,root}=require('./i18n-test-helpers');
const {test}=require('node:test');
function setup(){const app=boot(),nodes=dom(app);vm.runInContext(pageScript('index.html'),app.context);return {...app,...nodes,run:code=>vm.runInContext(code,app.context)};}
function label(app,button){const key=button.innerHTML.match(/data-i18n="([^"]+)"/);return button.textContent||(key?app.api.t(key[1]):'');}
const idle={udid:'PHONE',name:'Phone',online:true,conn:'wifi',operations_available:true,backup_state:'idle',backing_up:false};
function patch(app,extra){app.run('patchDevice('+JSON.stringify({...idle,...extra})+')');}
for(const language of ['en','zh-CN'])test('one stable primary action across task states '+language,()=>{
 const app=setup();app.api.setLocale(language);const button=app.get('backupBtn-PHONE');
 patch(app,{});assert.equal(label(app,button),catalogs[language]['device.backupNow']);assert.equal(button.disabled,false);
 patch(app,{backing_up:true,active_operation:'backup',backup_job_active:true,backup_job_id:'job-1',backup_state:'running'});
 assert.equal(label(app,button),catalogs[language]['control.stop']);assert.equal(button.disabled,false);
 assert.equal(app.get('backupAdvanced-PHONE').disabled,true,'the full-backup action in settings cannot run during a backup');
 assert.equal(app.get('backupAdvanced-PHONE').classList.contains('hidden'),false,'advanced actions remain discoverable while busy');
 app.run("backupAction('PHONE',document.getElementById('backupBtn-PHONE'))");
 assert.equal(app.run('backupStopTarget.jobID'),'job-1','action reads current task identity');
 patch(app,{conn:'offline',online:false,operations_available:false,backing_up:true,active_operation:'backup',backup_job_active:true,backup_job_id:'job-2',backup_state:'running'});
 assert.equal(button.disabled,false,'device disconnect must not disable server-side cancellation');
 patch(app,{backing_up:true,active_operation:'backup',backup_job_active:true,backup_job_id:'job-2',backup_state:'stopping'});
 assert.equal(label(app,button),catalogs[language]['control.stopping']);assert.equal(button.disabled,true);
 patch(app,{backup_state:'stopped',auto_backup_hold:true});assert.equal(label(app,button),catalogs[language]['device.backupNow']);
 assert.equal(app.get('bkbar-PHONE').classList.contains('hidden'),true,'an unmeasured stop is summarized in the single hold notice');
 for(const operation of ['check','local']){patch(app,{backing_up:true,active_operation:operation});assert.equal(button.disabled,true);assert.notEqual(label(app,button),catalogs[language]['control.stop']);}
 patch(app,{conn:'offline'});assert.equal(button.disabled,true);assert.equal(app.get('backupAdvanced-PHONE').disabled,true);
 patch(app,{});assert.strictEqual(app.get('backupBtn-PHONE'),button);assert.equal(app.get('backupAdvanced-PHONE').disabled,false);
 const html=fs.readFileSync(path.join(root,'templates/index.html'),'utf8');assert.ok(!html.includes('id="stopBackup-'),'there is no second backup action in the template');
 assert.ok(!html.includes('backupSplit-'),'the header must not retain a split backup button');
 const advanced=html.match(/<details class="console-advanced">[\s\S]*?<\/details>/)[0];
 assert.ok(advanced.includes('id="backupAdvanced-'),'the full-backup action belongs to device settings advanced actions');
});
test('start submission stays disabled across status and language refreshes',async()=>{
 const app=setup();patch(app,{backup_storage_mode:'history'});let resolve,calls=0;
 app.window.fetch=()=>{calls++;return new Promise(r=>{resolve=r;});};
 const promise=app.run("startBackup('PHONE',document.getElementById('backupBtn-PHONE'))");
 app.run("startBackup('PHONE',document.getElementById('backupBtn-PHONE'))");
 patch(app,{});app.api.setLocale('zh-CN');assert.equal(calls,1);assert.equal(app.get('backupBtn-PHONE').disabled,true);assert.equal(label(app,app.get('backupBtn-PHONE')),catalogs['zh-CN']['control.starting']);
 assert.equal(app.get('backupAdvanced-PHONE').disabled,true,'pending auto submission also blocks full backup');
 resolve({ok:true,json:()=>Promise.resolve({backup_job_id:'job-new',backup_storage_mode:'in_place',backup_state:'starting',requested_mode:'auto'})});await promise;
 assert.equal(label(app,app.get('backupBtn-PHONE')),catalogs['zh-CN']['control.stop']);assert.equal(app.get('backupBtn-PHONE').disabled,false);
 app.run("backupAction('PHONE',document.getElementById('backupBtn-PHONE'))");assert.equal(app.run('backupStopTarget.jobID'),'job-new');
 assert.equal(app.get('stopBackupRisk').textContent,catalogs['zh-CN']['control.stopRiskInPlace'],'new task response supplies its own storage mode');
});
test('late accepted response cannot revive an already finished task',async()=>{
 const app=setup();patch(app,{});let resolve;
 app.window.fetch=()=>new Promise(r=>{resolve=r;});
 const promise=app.run("startBackup('PHONE',document.getElementById('backupBtn-PHONE'))");
 patch(app,{backup_job_id:'job-fast',backup_state:'succeeded',backup_job_active:false});
 resolve({ok:true,json:()=>Promise.resolve({backup_job_id:'job-fast',backup_state:'starting'})});await promise;
 assert.equal(app.run('latestDeviceStatuses.PHONE.backup_state'),'succeeded');assert.equal(label(app,app.get('backupBtn-PHONE')),catalogs.en['device.backupNow']);
});
for(const language of ['en','zh-CN'])test('stop warning follows the selected task storage mode '+language,()=>{
 const app=setup();app.api.setLocale(language);
 const running={backing_up:true,backup_job_active:true,backup_job_id:'job-history',backup_state:'running'};
 patch(app,{...running,backup_storage_mode:'history'});app.run("openStopBackup('PHONE')");
 assert.equal(app.get('stopBackupRisk').textContent,catalogs[language]['control.stopRiskHistory']);
 assert.equal(app.get('stopBackupDetail').textContent,catalogs[language]['control.stopDetailHistory']);
 patch(app,{...running,backup_job_id:'job-current',backup_storage_mode:'in_place'});
 app.api.setLocale(language==='en'?'zh-CN':'en');app.api.setLocale(language);
 assert.equal(app.get('stopBackupRisk').textContent,catalogs[language]['control.stopRiskHistory'],'a new task must not change the old confirmation');
 app.run("closeStopBackup();openStopBackup('PHONE')");
 assert.equal(app.get('stopBackupRisk').textContent,catalogs[language]['control.stopRiskInPlace']);
 assert.equal(app.get('stopBackupDetail').textContent,catalogs[language]['control.stopDetail']);
 app.run('closeStopBackup()');patch(app,{...running,backup_storage_mode:undefined});app.run("openStopBackup('PHONE')");
 assert.equal(app.get('stopBackupRisk').textContent,catalogs[language]['control.stopRisk'],'missing information must not promise protection');
 patch(app,{...running,backup_storage_mode:'history'});
 assert.equal(app.get('stopBackupRisk').textContent,catalogs[language]['control.stopRiskHistory'],'same-task status resolves an initially unknown mode');
});

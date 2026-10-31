'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const {boot,dom,pageScript,root} = require('./i18n-test-helpers');
const runtime = path.join(root, 'static/i18n.js');
assert.ok(fs.existsSync(runtime), 'four pages need a shared locale runtime');
assert.equal(boot({stored:{iosbk_lang:'zh'}}).api.getLocale(),'zh-CN');
assert.equal(boot({stored:{iosbk_lang:'en',iosbk_guide_lang:'zh'},languages:['zh-CN']}).api.getLocale(),'en');
assert.equal(boot({stored:{iosbk_guide_lang:'zh'}}).api.getLocale(),'zh-CN');
assert.equal(boot({languages:['fr-FR','zh-TW']}).api.getLocale(),'en');
assert.equal(boot({languages:['zh-Hant','zh-HK','en-GB']}).api.getLocale(),'en');
assert.equal(boot({languages:['zh-TW','zh-Hans-SG','en']}).api.getLocale(),'zh-CN');
assert.equal(boot({stored:{iosbk_lang:'xx'},languages:['en-GB']}).api.getLocale(),'en');
assert.equal(boot({storageThrows:true,languages:['zh-CN']}).api.getLocale(),'zh-CN');
assert.equal(boot({cookie:'iosbk_lang=en',languages:['zh-CN'],storageThrows:true}).api.getLocale(),'en');
const app=boot();
app.api.register({en:{'greeting':'Hello {name}', 'fallback':'Please retry.', 'api.busy':'{device} is busy.', 'ui.only':'Fallback'},'zh-CN':{'greeting':'你好，{name}', 'fallback':'请重试。','api.busy':'{device} 正忙。'}});
assert.equal(app.api.t('greeting',{name:'iPhone'}),'Hello iPhone');
assert.equal(app.api.message({code:'api.busy',params:{device:'Phone'}},'fallback'),'Phone is busy.');
assert.equal(app.api.message({code:'busy',message_code:'api.busy',params:{device:'Phone'}},'fallback'),'Phone is busy.');
assert.equal(app.api.message({code:'fallback',message_code:'api.busy',params:{device:'Phone'}},'fallback'),'Phone is busy.');
assert.equal(app.api.message({code:'api.busy',message_code:'unknown',params:{device:'Phone'}},'fallback'),'Phone is busy.');
assert.equal(app.api.message({error:'服务器硬编码中文',code:'unknown'},'fallback'),'Please retry.');
app.api.setLocale('zh');
assert.equal(app.api.getLocale(),'zh-CN');
assert.equal(app.values.iosbk_lang,'zh-CN');
assert.match(app.document.cookie,/iosbk_lang=zh-CN/);
assert.equal(app.document.documentElement.lang,'zh-CN');
assert.equal(app.events.at(-1).type,'iosbk:localechange');
assert.equal(app.api.t('ui.only'),'Fallback');
assert.equal(app.api.t('greeting',{name:'<script>'}),'你好，<script>');
assert.equal(app.api.formatDate('2026-10-11T00:30:00Z'),new Intl.DateTimeFormat('zh-CN',{timeZone:'Asia/Shanghai',year:'numeric',month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',hour12:false}).format(new Date('2026-10-11T00:30:00Z')));
const utcApp=boot({timezone:'UTC'});
const expectedUTC=new Intl.DateTimeFormat('en',{timeZone:'UTC',year:'numeric',month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',hour12:false}).format(new Date('2026-10-11T00:30:00Z'));
assert.equal(utcApp.api.formatDate('2026-10-11T00:30:00Z'),expectedUTC,'dates must use the deployment timezone');
assert.equal(utcApp.api.formatDate('2026-10-11 08:30'),expectedUTC,'legacy values remain Beijing wall clocks');
utcApp.api.setLocale('zh-CN');
assert.equal(utcApp.api.getTimezone(),'UTC','browser language cannot alter the timezone');
utcApp.api.setTimezone('Mars/Olympus');
assert.equal(utcApp.api.getTimezone(),'UTC','invalid status values cannot break date rendering');
app.api.fetch('/api/example',{headers:{'X-Test':'value'}});
assert.equal(new Headers(app.requests[0].opts.headers).get('Accept-Language'),'zh-CN');
assert.equal(new Headers(app.requests[0].opts.headers).get('X-Test'),'value');
const ui=path.join(root,'static/ui-messages.json');
assert.ok(fs.existsSync(ui),'UI resources must be shared with server rendering');
assert.ok(!fs.existsSync(path.join(root,'static/ui-messages.js')),'the script route must use the JSON source without a duplicate catalog');
const catalogs=JSON.parse(fs.readFileSync(ui,'utf8'));
assert.deepEqual(Object.keys(catalogs.en).sort(),Object.keys(catalogs['zh-CN']).sort(),'locale keys must match');
for(const [key,value] of Object.entries(catalogs.en)) {
 assert.ok(typeof value==='string' && value.trim(),key+' English resource must be nonempty');
 assert.ok(typeof catalogs['zh-CN'][key]==='string' && catalogs['zh-CN'][key].trim(),key+' Chinese resource must be nonempty');
 const params=s=>[...s.matchAll(/\{([\w]+)\}/g)].map(m=>m[1]).sort();
 assert.deepEqual(params(value),params(catalogs['zh-CN'][key]),key+' placeholders must match');
}
console.log('shared i18n behaviors passed');

// A small DOM adapter executes real page scripts; state and translations remain production code.

const login=boot(), loginDOM=dom(login);
vm.runInContext(pageScript('login.html'),login.context);
loginDOM.get('password').value='unsaved secret';
loginDOM.get('togglePassword').handlers.click();
assert.equal(loginDOM.get('password').type,'text');
assert.equal(loginDOM.get('togglePassword').textContent,'Hide');
login.api.setLocale('zh-CN');
assert.equal(loginDOM.get('togglePassword').textContent,'隐藏');
assert.equal(loginDOM.get('password').value,'unsaved secret');
const consoleApp=boot(), consoleDOM=dom(consoleApp);
vm.runInContext(pageScript('index.html'),consoleApp.context);
const backupPasswordButton=fs.readFileSync(path.join(root,'templates/index.html'),'utf8').match(/<button\b[^>]*onclick="changeBackupPw\('[^']+'\)"[^>]*>/)[0];
assert.match(backupPasswordButton,/data-connection-action="\{\{\$d\.UDID\}\}"/,'backup password changes must participate in live device-operation gating');
const passwordAction=consoleDOM.get('changeBackupPw-PHONE');passwordAction.setAttribute('data-connection-action','PHONE');
for(const [conn,available,backingUp,disabled] of [['usb',true,false,false],['offline',false,false,true],['usb',true,true,true],['usb',true,false,false]]) {
 vm.runInContext('patchDevice('+JSON.stringify({udid:'PHONE',conn,operations_available:available,backing_up:backingUp})+')',consoleApp.context);
 assert.equal(passwordAction.disabled,disabled,'password action gating for '+conn+' / backing_up='+backingUp);
}
for(const operation of ['check','local','backup']) {
 vm.runInContext('patchDevice('+JSON.stringify({udid:'PHONE',conn:'usb',operations_available:true,backing_up:true,active_operation:operation})+')',consoleApp.context);
 assert.equal(passwordAction.disabled,true,'busy operation must retain device action protection');
 assert.equal(consoleDOM.get('bkbar-PHONE').classList.contains('hidden'),operation!=='backup','only phone backups show progress/authorization hints');
 assert.match(consoleDOM.get('backupBtn-PHONE').innerHTML,new RegExp(operation==='check'?'console.checkingBackup':operation==='local'?'console.deviceBusy':'device.backingUp'));
}

vm.runInContext(`latestEncryptionStatuses.PHONE={online:true,will_encrypt:true};backupEncryptionStates.PHONE=false;listState.PHONE={all:[],filtered:[],page:0,pageSize:200,total:0};showInlineResult(document.getElementById('ipResult-PHONE'),true,'bset.testOk');dirtyCards.add('PHONE');`,consoleApp.context);
consoleDOM.get('networkAddress-PHONE').value='192.0.2.1';
consoleApp.api.setLocale('zh-CN');
assert.equal(consoleDOM.get('encState-PHONE').querySelector('span').textContent,catalogs['zh-CN']['enc.stateOn']);
assert.equal(consoleDOM.get('bkEnc-PHONE').textContent,catalogs['zh-CN']['enc.off']);
assert.equal(consoleDOM.get('list-PHONE').textContent,catalogs['zh-CN']['action.renderBackupList.noMatches']);
consoleApp.api.setLocale('en');
assert.equal(consoleDOM.get('encState-PHONE').querySelector('span').textContent,catalogs.en['enc.stateOn']);
assert.equal(consoleDOM.get('networkAddress-PHONE').value,'192.0.2.1');
assert.equal(vm.runInContext(`dirtyCards.has('PHONE')`,consoleApp.context),true);
async function verifyNotificationDrafts(){
const notifications=boot(), notificationDOM=dom(notifications);
vm.runInContext(pageScript('notifications.html'),notifications.context);
notifications.window.fetch=()=>Promise.resolve({ok:true,json:()=>Promise.resolve({enabled:true,language:'zh-CN',email_configs:[{name:'Original',password:'',enabled:true}]})});
await vm.runInContext('loadConfig()',notifications.context);
vm.runInContext(`setField('email_configs',0,'name','Unsaved name',false);setTemplate('backup_success','My custom {device_name}');`,notifications.context);
notifications.api.setLocale('en');
assert.equal(vm.runInContext('cfg.language',notifications.context),'zh-CN');
assert.equal(vm.runInContext('cfg.email_configs[0].name',notifications.context),'Unsaved name');
assert.equal(vm.runInContext('cfg.notification_templates.backup_success',notifications.context),'My custom {device_name}');
assert.equal(vm.runInContext('notifDirty',notifications.context),true);
}
console.log('page language switching preserves live state and unsaved inputs');
const accessible=boot(), accessibleDOM=dom(accessible);
const heading=accessibleDOM.get('title'), image=accessibleDOM.get('image'), languageButton=accessibleDOM.get('languageButton');
heading.setAttribute('data-i18n','page.login');
image.setAttribute('data-i18n-alt','guide.wifiAlt');image.setAttribute('data-i18n-aria','guide.homeAria');
languageButton.setAttribute('data-lang','en');
accessible.api.apply();
assert.equal(heading.textContent,catalogs.en['page.login']);
assert.equal(image.getAttribute('alt'),catalogs.en['guide.wifiAlt']);
assert.equal(image.getAttribute('aria-label'),catalogs.en['guide.homeAria']);
assert.equal(languageButton.getAttribute('aria-pressed'),'true');
accessible.api.setLocale('zh-CN');accessible.api.apply();
assert.equal(image.getAttribute('alt'),catalogs['zh-CN']['guide.wifiAlt']);
assert.equal(languageButton.getAttribute('aria-pressed'),'false');
assert.equal(accessible.api.tree('guide').stepTitles[0],catalogs['zh-CN']['guide.stepTitles.0']);
for(const filename of ['index.html','notifications.html','login.html','onboarding.html']) {
 const source=fs.readFileSync(path.join(root,'templates',filename),'utf8');
 for(const match of source.matchAll(/(?:data-i18n(?:-ph|-aria|-alt|-title|-href)?="|\btr\(')([a-zA-Z][\w.]*)(?:"|')/g)) {
  assert.ok(catalogs.en[match[1]],filename+' uses missing key '+match[1]);
 }
 for(const match of source.matchAll(/\{\{t \$\.Locale "([\w.]+)"/g)) {
  assert.ok(catalogs.en[match[1]],filename+' server template uses missing key '+match[1]);
 }
}
console.log('accessible text and all referenced UI resources are translated');
const guide=boot(),guideDOM=dom(guide);
const apiCatalog=JSON.parse(fs.readFileSync(path.join(root,'../i18n/messages.json'),'utf8'));
guide.api.register(apiCatalog);
const guideSource=fs.readFileSync(path.join(root,'static/onboarding.js'),'utf8');
const guideStart=guideSource.indexOf('  function pairingMessage(device)'),guideEnd=guideSource.indexOf('  function render()',guideStart);
vm.runInContext(`function t(){return IOSBKI18n.tree('guide');}function tr(key,params){return IOSBKI18n.t(key,params);}function text(id,value){document.getElementById(id).textContent=value;}function connectionProblem(){return '';}`+guideSource.slice(guideStart,guideEnd),guide.context);
guide.document.querySelector=()=>guideDOM.get('password-grid');
vm.runInContext(`pairingMessage({pairing_state:'failed',pairing_error:'硬编码中文不得显示',pairing_error_code:'device_unavailable'});backupMessage({backup_state:'failed',last_backup_error:'原始错误',last_backup_error_code:'backup_stalled'});`,guide.context);
assert.equal(guideDOM.get('pairError').textContent,apiCatalog.en['api.device_unavailable']);
assert.equal(guideDOM.get('backupError').textContent,apiCatalog.en['api.backup_stalled']);
guide.api.setLocale('zh-CN');
vm.runInContext(`pairingMessage({pairing_state:'failed',pairing_error_code:'device_unavailable'});backupMessage({backup_state:'failed',last_backup_error_code:'unknown_vendor_failure'});`,guide.context);
assert.equal(guideDOM.get('pairError').textContent,apiCatalog['zh-CN']['api.device_unavailable']);
assert.equal(guideDOM.get('backupError').textContent,catalogs['zh-CN']['guide.failedBackup']);
vm.runInContext(`backupMessage({backup_state:'stopping',backing_up:true,pairing_state:'paired'});`,guide.context);
assert.equal(guideDOM.get('startBackup').disabled,true,'onboarding must remain busy while the task stops');
vm.runInContext(`backupMessage({backup_state:'stopped',backing_up:false,pairing_state:'paired'});`,guide.context);
assert.equal(guideDOM.get('backupTitle').textContent,catalogs['zh-CN']['control.stopped']);
console.log('onboarding errors use translated machine codes and safe unknown fallbacks');

async function verifyOperationStateAcrossLanguageChanges(){
 const pendingApp=boot(),pendingDOM=dom(pendingApp);
 pendingApp.api.register(apiCatalog);
 vm.runInContext(pageScript('index.html'),pendingApp.context);
 pendingApp.window.fetch=()=>new Promise(()=>{});
 const inlineText=id=>pendingDOM.get(id).children.list.at(-1).children.list.at(-1).textContent;
 pendingDOM.get('networkAddress-PHONE').value='192.0.2.55';
 pendingDOM.get('backupDir-PHONE').value='/backups/new';
 vm.runInContext("showInlineResult(document.getElementById('ipResult-PHONE'),true,'bset.testOk');showInlineResult(document.getElementById('dirResult-PHONE'),true,'bset.dirOk');testDeviceIP('PHONE');checkBackupDir('PHONE');",pendingApp.context);
 pendingApp.api.setLocale('zh-CN');
 assert.equal(inlineText('ipResult-PHONE'),catalogs['zh-CN']['bset.testing'],'an unfinished IP check must not restore an old success');
 assert.equal(inlineText('dirResult-PHONE'),catalogs['zh-CN']['bset.dirChecking'],'an unfinished directory check must not restore an old success');

 const deletedApp=boot(),deletedDOM=dom(deletedApp);
 deletedApp.api.register(apiCatalog);
 vm.runInContext(pageScript('index.html'),deletedApp.context);
 deletedApp.context.prompt=()=> 'Phone';
 deletedApp.window.fetch=()=>Promise.resolve({ok:true,status:200,json:()=>Promise.resolve({code:'api.backup_deleted',message:'Deleted'})});
 vm.runInContext("listState.PHONE={all:['OLD-BACKUP-FILE'],filtered:['OLD-BACKUP-FILE'],page:0,pageSize:200,total:1};confirmDeleteBackup('PHONE','Phone');",deletedApp.context);
 await new Promise(resolve=>setImmediate(resolve));
 deletedApp.api.setLocale('zh-CN');
 assert.equal(deletedDOM.get('list-PHONE').textContent,catalogs['zh-CN']['bmgmt.noBackup'],'a deleted backup must not reappear after rerender');

 const deliveryApp=boot(),deliveryDOM=dom(deliveryApp);
 deliveryApp.api.register(apiCatalog);
 vm.runInContext(pageScript('notifications.html'),deliveryApp.context);
 deliveryApp.window.fetch=()=>Promise.resolve({ok:true,status:200,json:()=>Promise.resolve({enabled:true,language:'en'})});
 await vm.runInContext('loadConfig()',deliveryApp.context);
 for(const result of [
   {status:409,data:{attempted:0,succeeded:0,failed:0},key:'notif.testNoTargets'},
   {status:502,data:{attempted:1,succeeded:0,failed:1},key:'notif.testFailed'},
   {status:207,data:{attempted:2,succeeded:1,failed:1},key:'notif.testPartial',params:{succeeded:1,failed:1}},
   {status:200,data:{attempted:1,succeeded:1,failed:0},key:'notif.testSent'}
 ]) {
   deliveryApp.window.fetch=()=>Promise.resolve({ok:result.status>=200&&result.status<300,status:result.status,json:()=>Promise.resolve(result.data)});
   vm.runInContext('testNotification()',deliveryApp.context);
   await new Promise(resolve=>setImmediate(resolve));
   assert.equal(deliveryDOM.get('toastMsg').textContent,deliveryApp.api.t(result.key,result.params),'notification delivery status '+result.status+' must match the actual result');
 }
 console.log('pending checks, deleted backups and notification delivery results survive localization correctly');
}
async function verifyBackupControlBehavior(){
 const controls=boot(),nodes=dom(controls);
 controls.api.register(apiCatalog);
 vm.runInContext(pageScript('index.html'),controls.context);
 assert.equal(vm.runInContext('typeof openStopBackup',controls.context),'function','console must expose the approved stop confirmation');
 vm.runInContext(`patchDevice({udid:'PHONE',name:'Phone',conn:'usb',online:true,backup_job_id:'job-1',backup_state:'running',backing_up:true});openStopBackup('PHONE');`,controls.context);
 assert.equal(nodes.get('stopBackupModal').classList.contains('hidden'),false);
 controls.api.setLocale('zh-CN');
 assert.equal(nodes.get('stopBackupRisk').textContent,catalogs['zh-CN']['control.stopRisk']);
 vm.runInContext(`patchDevice({udid:'PHONE',conn:'usb',online:true,backup_job_id:'job-2',backup_state:'running',backing_up:true});`,controls.context);
 const requests=[];
 let release;
 controls.window.fetch=(url,opts)=>{
  requests.push({url,opts});
  if(url==='/api/status')return Promise.resolve({json:()=>Promise.resolve({devices:[{udid:'PHONE',conn:'usb',online:true,backup_job_id:'job-2',backup_state:'running',backing_up:true}]})});
  return new Promise(resolve=>{release=resolve;});
 };
 const pending=vm.runInContext('submitStopBackup()',controls.context);
 vm.runInContext('submitStopBackup()',controls.context);
 assert.equal(requests.length,1,'double confirmation must not send two stops');
 assert.equal(JSON.parse(requests[0].opts.body).job_id,'job-1','confirmation must keep the originally selected task');
 release({status:409,json:()=>Promise.resolve({error:'stale',code:'api.backup_job_stale'})});
 await pending;
 assert.equal(vm.runInContext('latestDeviceStatuses.PHONE.backup_job_id',controls.context),'job-2');
 assert.equal(vm.runInContext('latestDeviceStatuses.PHONE.backup_state',controls.context),'running');
 vm.runInContext(`patchDevice({udid:'PHONE',conn:'usb',online:true,backup_job_id:'job-2',backup_state:'stopping',backing_up:true,auto_backup_hold:true,auto_backup_hold_version:'hold-2',auto_backup_hold_persistence:'saved'});`,controls.context);
 assert.equal(nodes.get('resumeBackup-PHONE').disabled,true,'cannot resume while stopping');
 assert.equal(nodes.get('stopBackup-PHONE').disabled,true);
 assert.equal(nodes.get('bkprogress-status-title-PHONE').textContent,catalogs['zh-CN']['control.stopping']);
 vm.runInContext(`patchDevice({udid:'PHONE',conn:'usb',online:true,backup_job_id:'job-2',backup_state:'stopped',backing_up:false,auto_backup_hold:true,auto_backup_hold_version:'hold-2',auto_backup_hold_persistence:'error'});`,controls.context);
 assert.equal(nodes.get('retryHold-PHONE').classList.contains('hidden'),false,'save error needs a retry action');
 assert.equal(nodes.get('backupBtn-PHONE').disabled,false,'manual once remains available while held');
 controls.api.setLocale('en');
 assert.equal(nodes.get('backupHoldMessage-PHONE').textContent,catalogs.en['control.holdSaveFailed']);
 assert.equal(nodes.get('bkprogress-status-title-PHONE').textContent,catalogs.en['control.stopped']);
 vm.runInContext(`patchDevice({udid:'PHONE',conn:'usb',online:true,backup_job_id:'job-3',backup_state:'running',backing_up:true});openStopBackup('PHONE');`,controls.context);
 controls.window.fetch=(url)=>url==='/api/status'?Promise.reject(Error('offline')):Promise.resolve({status:500,json:()=>Promise.resolve({error:'save',code:'api.backup_hold_save_failed',backup_job_id:'job-3',backup_state:'stopping',backing_up:true,auto_backup_hold:true,auto_backup_hold_version:'job-3',auto_backup_hold_persistence:'error'})});
 await vm.runInContext('submitStopBackup()',controls.context);
 assert.equal(nodes.get('backupHoldMessage-PHONE').textContent,catalogs.en['control.holdSaveFailed'],'partial result must remain visible even when the follow-up refresh fails');
 assert.equal(nodes.get('stopBackup-PHONE').disabled,true,'accepted stop must not revert to running after a save error');
 console.log('backup controls preserve task identity, prevent duplicate requests and distinguish stopping, holding and save failures');
}
async function runAllBehaviorChecks(){
 await verifyNotificationDrafts();
 await verifyOperationStateAcrossLanguageChanges();
 await verifyBackupControlBehavior();
 require('./context-i18n.test');
 require('./locale-preferences.test');
 require('./console-i18n.test');
 await require('./notifications-i18n.test')();
}
runAllBehaviorChecks().catch(error=>{console.error(error);process.exitCode=1;});

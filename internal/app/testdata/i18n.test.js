'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const root = path.resolve(__dirname, '..');
const runtime = path.join(root, 'static/i18n.js');
assert.ok(fs.existsSync(runtime), 'four pages need a shared locale runtime');
function boot({stored={},cookie='',languages=['en-US'],storageThrows=false}={}) {
  const values={...stored}, events=[], requests=[], listeners={};
  const document={cookie,documentElement:{lang:'',setAttribute(k,v){this[k]=v;}},querySelectorAll(){return [];}};
  const window={document,navigator:{languages,language:languages[0]||''},localStorage:{getItem(k){if(storageThrows)throw Error('blocked');return values[k]||null;},setItem(k,v){if(storageThrows)throw Error('blocked');values[k]=v;},removeItem(k){delete values[k];}},dispatchEvent(e){events.push(e);(listeners[e.type]||[]).forEach(fn=>fn(e));},addEventListener(type,fn){(listeners[type]||=[]).push(fn);},fetch(url,opts){requests.push({url,opts});return Promise.resolve({});}};
  const context=vm.createContext({...window,window,CustomEvent:class{constructor(type,opts){this.type=type;this.detail=opts.detail;}},Intl,Date,Headers});
  vm.runInContext(fs.readFileSync(runtime,'utf8'),context);
  context.IOSBKI18n=window.IOSBKI18n;
  return {api:window.IOSBKI18n,values,document,events,requests,context,window};
}
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
app.api.fetch('/api/example',{headers:{'X-Test':'value'}});
assert.equal(new Headers(app.requests[0].opts.headers).get('Accept-Language'),'zh-CN');
assert.equal(new Headers(app.requests[0].opts.headers).get('X-Test'),'value');
const ui=path.join(root,'static/ui-messages.json');
assert.ok(fs.existsSync(ui),'UI resources must be shared with server rendering');
assert.ok(!fs.existsSync(path.join(root,'static/ui-messages.js')),'the script route must use the JSON source without a duplicate catalog');
const catalogs=JSON.parse(fs.readFileSync(ui,'utf8'));
assert.deepEqual(Object.keys(catalogs.en).sort(),Object.keys(catalogs['zh-CN']).sort(),'locale keys must match');
for(const [key,value] of Object.entries(catalogs.en)) {
 const params=s=>[...s.matchAll(/\{([\w]+)\}/g)].map(m=>m[1]).sort();
 assert.deepEqual(params(value),params(catalogs['zh-CN'][key]),key+' placeholders must match');
}
console.log('shared i18n behaviors passed');

// A small DOM adapter executes real page scripts; state and translations remain production code.
function dom(app) {
 const nodes=new Map();
 function node(id='') {
  const attributes={}, children={}, handlers={}, classes=new Set();
  const result={id,value:'',type:'password',textContent:'',innerHTML:'',hidden:false,style:{},dataset:{},attributes,children,handlers,
   classList:{toggle(k,v){if(v===undefined)v=!classes.has(k);v?classes.add(k):classes.delete(k);},add(k){classes.add(k);},remove(k){classes.delete(k);},contains(k){return classes.has(k);}},
   setAttribute(k,v){attributes[k]=String(v);},getAttribute(k){return attributes[k]??null;},removeAttribute(k){delete attributes[k];},
   querySelector(k){return children[k]||(children[k]=node());},querySelectorAll(){return [];},insertAdjacentHTML(){},appendChild(child){(children.list||=[]).push(child);},addEventListener(type,fn){handlers[type]=fn;},focus(){},getClientRects(){return [1];},replaceChildren(){children.list=[];}};
  return result;
 }
 app.document.getElementById=id=>nodes.get(id)||(nodes.set(id,node(id)),nodes.get(id));
 app.document.querySelector=()=>null;
 app.document.querySelectorAll=selector=>{
  const match=selector.match(/^\[([^\]]+)\]$/);
  return match?[...nodes.values()].filter(n=>Object.hasOwn(n.attributes,match[1])):[];
 };
 app.document.addEventListener=()=>{};
 app.document.createElement=()=>node();
 app.document.body=node('body');app.document.body.dataset={};
 Object.assign(app.context,{setTimeout:()=>1,clearTimeout(){},AbortController,console});
 app.window.setTimeout=()=>1;app.window.clearTimeout=()=>{};
 app.api.register(catalogs);
 return {get:app.document.getElementById,nodes,node};
}
function pageScript(name) {
 const html=fs.readFileSync(path.join(root,'templates',name),'utf8');
 const scripts=[...html.matchAll(/<script>([\s\S]*?)<\/script>/g)];
 return (name==='index.html'?fs.readFileSync(path.join(root,'static/backup-browser.js'),'utf8')+'\n':'')+scripts.at(-1)[1].replace('{{.InitialStatus}}','{"devices":[]}');
}
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
const notifications=boot(), notificationDOM=dom(notifications);
vm.runInContext(pageScript('notifications.html'),notifications.context);
vm.runInContext(`cfg.language='zh-CN';cfg.email_configs=[{name:'Original',password:'',enabled:true}];setField('email_configs',0,'name','Unsaved name',false);setTemplate('backup_success','My custom {device_name}');`,notifications.context);
notifications.api.setLocale('en');
assert.equal(vm.runInContext('cfg.language',notifications.context),'zh-CN');
assert.equal(vm.runInContext('cfg.email_configs[0].name',notifications.context),'Unsaved name');
assert.equal(vm.runInContext('cfg.notification_templates.backup_success',notifications.context),'My custom {device_name}');
assert.equal(vm.runInContext('notifDirty',notifications.context),true);
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
verifyOperationStateAcrossLanguageChanges().catch(error=>{console.error(error);process.exitCode=1;});

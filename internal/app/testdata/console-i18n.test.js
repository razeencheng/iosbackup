'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const {test} = require('node:test');
const {boot,dom,pageScript,catalogs,apiCatalog,root} = require('./i18n-test-helpers');

const tick = () => new Promise(resolve => setImmediate(resolve));
function deferred() { let resolve,reject; const promise=new Promise((a,b)=>{resolve=a;reject=b;}); return {promise,resolve,reject}; }
function response(data,ok=true,status=ok?200:409) { return {ok,status,json:()=>Promise.resolve(data)}; }
function page(locale='en',guide=false) {
  const app=boot(),nodes=dom(app),created=[],timers=new Map(),callbacks=[];
  let now=1000,nextTimer=0;
  app.api.register(apiCatalog); app.api.setLocale(locale);
  app.document.contains=()=>false;
  const get=nodes.get;
  app.document.getElementById=id=>created.findLast(node=>node.id===id)||get(id);
  app.document.createElement=tag=>{
    const node=nodes.node(); node.tagName=tag.toUpperCase();
    node.querySelector=selector=>(node.children.list||[]).find(child=>child.tagName===selector.toUpperCase())||nodes.node();
    created.push(node); return node;
  };
  app.context.location={reload(){},assign(){}};
  app.context.Date=class extends Date { static now(){return now;} };
  function setTimer(fn,delay){const id=++nextTimer;timers.set(id,{fn,at:now+delay});callbacks.push(fn);return id;}
  function clearTimer(id){timers.delete(id);}
  Object.assign(app.context,{setTimeout:setTimer,clearTimeout:clearTimer});
  Object.assign(app.window,{setTimeout:setTimer,clearTimeout:clearTimer});
  const events=[];
  app.context.EventSource=class { constructor(){events.push(this);}close(){} };
  if(guide){
    app.document.querySelector=selector=>get(selector);
    vm.runInContext(fs.readFileSync(path.join(root,'static/onboarding.js'),'utf8'),app.context);
  }else{
    vm.runInContext(pageScript('index.html'),app.context);
    const source=fs.readFileSync(path.join(root,'templates/index.html'),'utf8');
    const button=source.match(/<button[^>]*id="removeDeviceConfirm"[^>]*>/)[0];
    const key=button.match(/data-i18n="([^"]+)"/);if(key)get('removeDeviceConfirm').setAttribute('data-i18n',key[1]);
  }
  const env={...app,nodes,created,events,callbacks,run:code=>vm.runInContext(code,app.context),get:id=>app.document.getElementById(id),advance(ms){now+=ms;for(const [id,timer] of [...timers])if(timer.at<=now){timers.delete(id);timer.fn();}}};
  return env;
}

for(const [from,to] of [['en','zh-CN'],['zh-CN','en']]){
  test('remove-pending-locale '+from,async()=>{
    const app=page(from),request=deferred();let calls=0;
    app.window.fetch=()=>{calls++;return request.promise;};
    app.run("openRemoveDeviceModal('PHONE','Phone','iPhone');submitRemoveDevice();");
    app.api.setLocale(to);app.run('applyStatus({devices:[]})');
    assert.equal(app.get('removeDeviceConfirm').textContent,catalogs[to]['remove.removing']);
    assert.equal(app.get('removeDeviceConfirm').disabled,true);
    app.run("closeRemoveDeviceModal();openRemoveDeviceModal('PHONE','Phone','iPhone');submitRemoveDevice();");
    assert.equal(calls,1);
    request.reject(Error('network'));await tick();
    assert.equal(app.get('removeDeviceConfirm').disabled,false);
    assert.equal(app.get('removeDeviceConfirm').textContent,catalogs[to]['remove.confirm']);
  });
  test('restore-managed-device-pending-locale '+from,async()=>{
    const app=page(from),request=deferred();let calls=0;
    app.window.fetch=()=>{calls++;return request.promise;};
    app.run("removedDevicesCache=[{udid:'PHONE',name:'Phone'}];renderRemovedDevices(removedDevicesCache,'ready');");
    const button=()=>app.get('removedDevicesList').children.list.at(-1).children.list.at(-1).children.list.at(-1);
    app.context.clickedButton=button();app.run("restoreRemovedDeviceFromDrawer('PHONE',clickedButton)");
    app.api.setLocale(to);
    assert.equal(button().disabled,true);
    assert.equal(button().querySelector('span').textContent,catalogs[to]['removed.restoring']);
    app.context.clickedButton=button();app.run("restoreRemovedDeviceFromDrawer('PHONE',clickedButton)");assert.equal(calls,1);
    request.resolve(response({error:'busy',message_code:'api.busy'},false));await tick();
    assert.equal(button().disabled,false);
    assert.equal(button().querySelector('span').textContent,catalogs[to]['removed.restoreAction']);
  });
  for(const success of [true,false])test('delete-backup-pending-locale '+from+' '+success,async()=>{
    const app=page(from),request=deferred();let calls=0;
    app.window.fetch=()=>{calls++;return request.promise;};app.context.prompt=()=> 'Phone';
    app.get('delBtn-PHONE').innerHTML='<span data-i18n="bmgmt.delDataBtn">'+catalogs[from]['bmgmt.delDataBtn']+'</span>';
    app.run("confirmDeleteBackup('PHONE','Phone')");app.api.setLocale(to);app.run('applyStatus({devices:[]})');
    assert.ok(app.get('delBtn-PHONE').innerHTML.includes(catalogs[to]['bmgmt.deleting']));
    assert.equal(app.get('delBtn-PHONE').disabled,true);
    app.run("confirmDeleteBackup('PHONE','Phone')");assert.equal(calls,1);
    request.resolve(response(success?{message_code:'api.backup_deleted'}:{error:'busy',message_code:'api.busy'},success));await tick();
    assert.equal(app.get('delBtn-PHONE').disabled,false);
    assert.ok(app.get('delBtn-PHONE').innerHTML.includes(catalogs[to]['bmgmt.delDataBtn']));
  });
}

test('console toast relocalizes without extending its deadline',()=>{
  const app=page();app.run("toast('bset.saved')");app.advance(1000);app.api.setLocale('zh-CN');
  assert.equal(app.get('toastMsg').textContent,catalogs['zh-CN']['bset.saved']);
  assert.equal(app.get('toast').classList.contains('hidden'),false);
  app.advance(1601);assert.equal(app.get('toast').classList.contains('hidden'),true);
  app.api.setLocale('en');assert.equal(app.get('toast').classList.contains('hidden'),true);
});
test('old toast timer cannot hide a newer message',()=>{
  const app=page();app.run("toast('bset.saved')");const oldTimer=app.callbacks.at(-1);
  app.advance(500);app.run("toast({message_code:'api.low_battery',params:{battery:9,min_battery:20}},true)");
  oldTimer();app.api.setLocale('zh-CN');
  assert.equal(app.get('toast').classList.contains('hidden'),false);
  assert.equal(app.get('toastMsg').textContent,app.api.message({message_code:'api.low_battery',params:{battery:9,min_battery:20}}));
});
test('pair rejection preserves its message without changing actual pairing state',async()=>{
  const app=page();app.run("renderPairingResult({udid:'PHONE',pairing_state:'paired'})");
  app.window.fetch=()=>Promise.resolve(response({error:'raw diagnostic',code:'busy',message_code:'api.busy'},false));
  await app.run("pairDevice('PHONE')");
  assert.equal(app.get('pairingResult-PHONE').textContent,apiCatalog.en['api.busy']);
  assert.equal(app.run("latestPairingStatuses.PHONE.pairing_state"),'paired');
  app.api.setLocale('zh-CN');app.run("renderPairingResult({udid:'PHONE',pairing_state:'paired'})");
  assert.equal(app.get('pairingResult-PHONE').textContent,apiCatalog['zh-CN']['api.busy']);
  assert.equal(app.get('toastMsg').textContent,apiCatalog['zh-CN']['api.busy']);
});
test('stale pairing poll cannot replace a newer attempt',async()=>{
  const app=page(),oldStatus=deferred();let statusCalls=0;
  app.window.fetch=url=>Promise.resolve(url.startsWith('/api/pair/')?response({}):(++statusCalls===1?oldStatus.promise:response({devices:[{udid:'PHONE',pairing_state:'paired'}]})));
  const old=app.run("pairDevice('PHONE')");await tick();
  await app.run("pairDevice('PHONE')");
  oldStatus.resolve(response({devices:[{udid:'PHONE',pairing_state:'failed',pairing_error_code:'pairing_required'}]}));await old;
  assert.equal(app.run('latestPairingStatuses.PHONE.pairing_state'),'paired');
  assert.equal(app.get('pairingResult-PHONE').textContent,catalogs.en['action.pairingStatusText.devicePairingIsValid']);
});
test('pair tracking failures keep an actionable relocalizable message',async()=>{
  const app=page();app.window.fetch=url=>url.startsWith('/api/pair/')?Promise.resolve(response({})):Promise.reject(Error('raw private diagnostic'));
  await app.run("pairDevice('PHONE')");app.api.setLocale('zh-CN');
  assert.equal(app.get('pairingResult-PHONE').textContent,catalogs['zh-CN']['action.trackPairingResult.cannotReadThePairingResult']);
  assert.ok(!app.get('toastMsg').textContent.includes('private'));
});
for(const locale of ['en','zh-CN']){
  test('pair tracking shows the trust instruction before completion '+locale,async()=>{
    const app=page(locale),states=['checking','waiting_for_trust','paired'];
    app.window.fetch=url=>Promise.resolve(response(url.startsWith('/api/pair/')?{}:{devices:[{udid:'PHONE',pairing_state:states.shift()}]}));
    const pending=app.run("pairDevice('PHONE')");await tick();
    assert.equal(app.get('pairingResult-PHONE').textContent,catalogs[locale]['action.pairingStatusText.checkingDevicePairing']);
    app.advance(1000);await tick();
    assert.equal(app.get('pairingResult-PHONE').textContent,catalogs[locale]['action.pairingStatusText.tapTrustThisComputerAnd']);
    assert.equal(app.get('pairBtn-PHONE').disabled,true);
    app.advance(1000);await pending;
    assert.equal(app.get('pairingResult-PHONE').textContent,catalogs[locale]['action.pairingStatusText.devicePairingIsValid']);
    assert.equal(app.get('pairBtn-PHONE').disabled,false);
  });
  test('pair tracking error survives unchanged status but clears on progress '+locale,async()=>{
    const app=page(locale);app.run("patchDevice({udid:'PHONE',pairing_state:'checking'})");
    app.window.fetch=url=>url.startsWith('/api/pair/')?Promise.resolve(response({})):Promise.reject(Error('offline'));
    await app.run("pairDevice('PHONE')");
    const other=locale==='en'?'zh-CN':'en';app.api.setLocale(other);
    app.run("patchDevice({udid:'PHONE',pairing_state:'checking',pairing_error_code:''})");
    assert.equal(app.get('pairingResult-PHONE').textContent,catalogs[other]['action.trackPairingResult.cannotReadThePairingResult']);
    assert.equal(app.get('pairingResult-PHONE').style.color,'#9A6B66');
    app.run("patchDevice({udid:'PHONE',pairing_state:'waiting_for_trust'})");
    assert.equal(app.get('pairingResult-PHONE').textContent,catalogs[other]['action.pairingStatusText.tapTrustThisComputerAnd']);
    assert.equal(app.get('pairingResult-PHONE').style.color,'#718090');
    app.run("patchDevice({udid:'PHONE',pairing_state:'paired'})");app.api.setLocale(locale);
    assert.equal(app.get('pairingResult-PHONE').textContent,catalogs[locale]['action.pairingStatusText.devicePairingIsValid']);
    assert.equal(app.get('pairingResult-PHONE').style.color,'#0B7565');
  });
  test('new external pairing progress replaces a rejected request '+locale,async()=>{
    const app=page(locale);app.run("patchDevice({udid:'PHONE',pairing_state:'paired'})");
    app.window.fetch=()=>Promise.resolve(response({error:'busy',message_code:'api.busy'},false));
    await app.run("pairDevice('PHONE')");
    app.run("patchDevice({udid:'PHONE',pairing_state:'paired'})");
    assert.equal(app.get('pairingResult-PHONE').textContent,apiCatalog[locale]['api.busy']);
    app.run("patchDevice({udid:'PHONE',pairing_state:'checking'})");
    assert.equal(app.get('pairingResult-PHONE').textContent,catalogs[locale]['action.pairingStatusText.checkingDevicePairing']);
    app.run("patchDevice({udid:'PHONE',pairing_state:'waiting_for_trust'})");
    assert.equal(app.get('pairingResult-PHONE').textContent,catalogs[locale]['action.pairingStatusText.tapTrustThisComputerAnd']);
    app.run("patchDevice({udid:'PHONE',pairing_state:'paired'})");
    assert.equal(app.get('pairingResult-PHONE').textContent,catalogs[locale]['action.pairingStatusText.devicePairingIsValid']);
    assert.equal(app.get('pairBtn-PHONE').disabled,false);
  });
}
test('backup terminal metadata relocalizes and unknown codes retain legacy fallback',()=>{
  const app=page();
  app.run("patchDevice({udid:'PHONE',backup_state:'failed',last_backup_error_code:'backup_failed',last_backup_error:'raw private text',last_backup_message_code:'api.low_battery',last_backup_message_params:{battery:9,min_battery:20},backup_progress:{state:'failed'}})");
  assert.equal(app.get('bkprogress-status-hint-PHONE').textContent,app.api.message({message_code:'api.low_battery',params:{battery:9,min_battery:20}}));
  app.api.setLocale('zh-CN');
  assert.equal(app.get('bkprogress-status-hint-PHONE').textContent,app.api.message({message_code:'api.low_battery',params:{battery:9,min_battery:20}}));
  app.run("patchDevice({udid:'PHONE',last_backup_error_code:'backup_stalled',last_backup_message_code:'vendor.unknown',backup_progress:{state:'failed'}})");
  assert.equal(app.get('bkprogress-status-hint-PHONE').textContent,apiCatalog['zh-CN']['api.backup_stalled']);
});
test('onboarding toast and backup terminal use live language without raw diagnostics',()=>{
  const app=page('en',true);
  app.get('startBackup').handlers.click();app.advance(1000);app.api.setLocale('zh-CN');
  assert.equal(app.get('toastText').textContent,catalogs['zh-CN']['guide.selectFirst']);assert.equal(app.get('toast').hidden,false);
  app.advance(1801);assert.equal(app.get('toast').hidden,true);app.api.setLocale('en');assert.equal(app.get('toast').hidden,true);
  app.events[0].onmessage({data:JSON.stringify({devices:[{udid:'PHONE',online:true,conn:'usb',pairing_state:'paired',backup_state:'failed',last_backup_error_code:'backup_failed',last_backup_error:'raw private text',last_backup_message_code:'api.low_battery',last_backup_message_params:{battery:9,min_battery:20}}]})});
  assert.equal(app.get('backupError').textContent,app.api.message({message_code:'api.low_battery',params:{battery:9,min_battery:20}}));
  app.api.setLocale('zh-CN');assert.equal(app.get('backupError').textContent,app.api.message({message_code:'api.low_battery',params:{battery:9,min_battery:20}}));
});

test('restoring a managed device survives drawer reopening and a network failure',async()=>{
  const app=page(),request=deferred(),device={udid:'PHONE',name:'Phone'};
  app.window.fetch=url=>url==='/api/removed-devices'?Promise.resolve(response({devices:[device]})):request.promise;
  app.context.removedFixture=device;app.run("removedDevicesCache=[removedFixture];renderRemovedDevices(removedDevicesCache,'ready')");
  app.run("restoreRemovedDeviceFromDrawer('PHONE',document.getElementById('restoreManagedDevice-PHONE'));closeRemovedDevicesDrawer();openRemovedDevicesDrawer()");
  await tick();app.api.setLocale('zh-CN');
  assert.equal(app.get('restoreManagedDevice-PHONE').disabled,true);
  request.reject(Error('network'));await tick();
  assert.equal(app.get('restoreManagedDevice-PHONE').disabled,false);
  assert.equal(app.get('restoreManagedDevice-PHONE').querySelector('span').textContent,catalogs['zh-CN']['removed.restoreAction']);
});
test('deleting a backup releases pending state after a network failure',async()=>{
  const app=page(),request=deferred();app.window.fetch=()=>request.promise;app.context.prompt=()=> 'Phone';
  app.run("confirmDeleteBackup('PHONE','Phone')");app.api.setLocale('zh-CN');request.reject(Error('network'));await tick();
  assert.equal(app.get('delBtn-PHONE').disabled,false);
  assert.ok(app.get('delBtn-PHONE').innerHTML.includes(catalogs['zh-CN']['bmgmt.delDataBtn']));
  assert.equal(app.get('toastMsg').textContent,catalogs['zh-CN']['common.operationFailed']);
});
test('removal success does not close a different device confirmation',async()=>{
  const app=page(),request=deferred();app.window.fetch=()=>request.promise;
  app.run("openRemoveDeviceModal('PHONE','Phone','iPhone');submitRemoveDevice();closeRemoveDeviceModal();openRemoveDeviceModal('OTHER','Other','iPhone')");
  request.resolve(response({}));await tick();
  assert.equal(app.run('removeDeviceUdid'),'OTHER');
  assert.equal(app.get('removeDeviceConfirm').disabled,false);
});
test('a missing pairing device and a polling timeout retain distinct reasons',async()=>{
  const missing=page();missing.window.fetch=url=>Promise.resolve(response(url.startsWith('/api/pair/')?{}:{devices:[]}));
  await missing.run("pairDevice('PHONE')");missing.api.setLocale('zh-CN');
  assert.equal(missing.get('pairingResult-PHONE').textContent,catalogs['zh-CN']['action.trackPairingResult.theDeviceIsNoLonger']);
  const timeout=page();timeout.window.fetch=url=>Promise.resolve(response(url.startsWith('/api/pair/')?{}:{devices:[{udid:'PHONE',pairing_state:'checking'}]}));
  const pending=timeout.run("pairDevice('PHONE')");await tick();timeout.advance(240001);await pending;timeout.api.setLocale('zh-CN');
  assert.equal(timeout.get('pairingResult-PHONE').textContent,catalogs['zh-CN']['action.trackPairingResult.pairingHasNotFinishedRefresh']);
});
test('onboarding stale timer and old backup schemas remain compatible',()=>{
  const app=page('en',true);app.get('startBackup').handlers.click();const oldTimer=app.callbacks.at(-1);
  app.advance(500);app.events[0].onmessage({data:'invalid JSON'});oldTimer();app.api.setLocale('zh-CN');
  assert.equal(app.get('toast').hidden,false);assert.equal(app.get('toastText').textContent,catalogs['zh-CN']['guide.invalidStatus']);
  const device={udid:'PHONE',online:true,conn:'usb',pairing_state:'paired',backup_state:'failed',last_backup_error_code:'backup_stalled',last_backup_message_code:'vendor.unknown',last_backup_error:'private'};
  app.events[0].onmessage({data:JSON.stringify({devices:[device]})});
  assert.equal(app.get('backupError').textContent,apiCatalog['zh-CN']['api.backup_stalled']);
  app.events[0].onmessage({data:JSON.stringify({devices:[{...device,backup_state:'succeeded',last_backup:'2026-10-31 12:00:00'}]})});
  assert.equal(app.get('backupError').hidden,true);assert.ok(!app.get('backupDetail').textContent.includes('private'));
});

for(const scenario of [
  {name:'resume',action:'resume',key:'control.resumed'},
  {name:'hold persistence retry',action:'hold-retry',key:'control.holdSaved'},
  {name:'stale task rejection',action:'stop',key:'api.backup_job_stale',error:true},
  {name:'request failure',action:'stop',key:'control.unknownResult',requestFails:true},
  {name:'refresh failure',action:'resume',key:'control.refreshFailed',refreshFails:true}
])test('backup control feedback relocalizes after '+scenario.name,async()=>{
  const app=page();
  app.window.fetch=url=>{
    if(url==='/api/status') return scenario.refreshFails?Promise.reject(Error('offline')):Promise.resolve(response({devices:[]}));
    if(scenario.requestFails) return Promise.reject(Error('offline'));
    return Promise.resolve(response(scenario.error?{error:'raw diagnostic',message_code:scenario.key}:{}));
  };
  await app.run("sendBackupControl('PHONE',"+JSON.stringify(scenario.action)+",{})");
  assert.equal(app.get('toastMsg').textContent,app.api.t(scenario.key));
  app.advance(1000);app.api.setLocale('zh-CN');
  assert.equal(app.get('toastMsg').textContent,app.api.t(scenario.key));
  assert.equal(app.get('toast').classList.contains('hidden'),false);
});

test('deleting an indexed backup invalidates its cache while keeping pending localization',async()=>{
  const app=page(),request=deferred();app.context.prompt=()=> 'Phone';
  app.window.fetch=()=>request.promise;
  app.run("backupBrowser.state('PHONE').sequence=1;backupBrowser.state('PHONE').snapshot={source_key:'old',overview:{status:'available',size_bytes:1024},file_list:{status:'available',entries:[{relative_path:'old-file'}]}};listState.PHONE={all:['old-file'],filtered:['old-file'],page:0,pageSize:200,total:1};");
  const pending=app.run("confirmDeleteBackup('PHONE','Phone')");
  app.api.setLocale('zh-CN');
  assert.equal(app.get('delBtn-PHONE').disabled,true);
  assert.ok(app.get('delBtn-PHONE').innerHTML.includes(catalogs['zh-CN']['bmgmt.deleting']));
  request.resolve(response({message_code:'api.backup_deleted'}));await pending;
  assert.equal(app.run("backupBrowser.state('PHONE').snapshot"),null);
  assert.equal(app.run("Object.hasOwn(listState,'PHONE')"),false);
  assert.equal(app.get('delBtn-PHONE').disabled,false);
  app.api.setLocale('en');
  assert.ok(!app.get('list-PHONE').textContent.includes('old-file'));
});

for(const locale of ['en','zh-CN'])test('completed deletion preserves backup availability across locale changes '+locale,async()=>{
  const app=page(locale);app.context.prompt=()=> 'Phone';
  app.window.fetch=()=>Promise.resolve(response({message_code:'api.backup_deleted'}));
  await app.run("confirmDeleteBackup('PHONE','Phone')");
  for(const state of ['running','stopping']){
    app.run("patchDevice({udid:'PHONE',conn:'usb',operations_available:true,backing_up:true,backup_state:"+JSON.stringify(state)+"})");
    assert.equal(app.get('delBtn-PHONE').disabled,true);
    app.api.setLocale(app.api.getLocale()==='en'?'zh-CN':'en');
    app.run('applyI18n()');
    assert.equal(app.get('delBtn-PHONE').disabled,true,'localizing a completed deletion cannot enable deletion during '+state);
  }
  app.run("patchDevice({udid:'PHONE',conn:'offline',presence_known:false,operations_available:false,backing_up:false,backup_state:'stopped'});applyI18n()");
  assert.equal(app.get('delBtn-PHONE').disabled,false,'local backup deletion remains available while the device is offline');
});

test('status patches and delete completion retain all active deletion blockers',async()=>{
  const app=page(),request=deferred();app.context.prompt=()=> 'Phone';app.window.fetch=()=>request.promise;
  const pending=app.run("confirmDeleteBackup('PHONE','Phone')");
  app.run("patchDevice({udid:'PHONE',conn:'offline',operations_available:false,backing_up:false})");
  assert.equal(app.get('delBtn-PHONE').disabled,true,'a status patch cannot release a pending deletion');
  app.run("patchDevice({udid:'PHONE',conn:'usb',operations_available:true,backing_up:true,backup_state:'running'})");
  request.resolve(response({error:'busy',message_code:'api.busy'},false));await pending;
  assert.equal(app.get('delBtn-PHONE').disabled,true,'finishing a deletion cannot release the active backup guard');
  app.run("patchDevice({udid:'PHONE',conn:'usb',operations_available:true,backing_up:false,backup_state:'succeeded'})");
  assert.equal(app.get('delBtn-PHONE').disabled,false);
});

for(const [from,to] of [['en','zh-CN'],['zh-CN','en']])test('delete buttons relocalize before any delete operation '+from,()=>{
  const app=page(from);
  app.run("patchDevice({udid:'PHONE',conn:'offline',operations_available:false,backing_up:false})");
  assert.equal(app.run('Object.keys(deviceOperations).length'),0);
  assert.ok(app.get('delBtn-PHONE').innerHTML.includes(catalogs[from]['bmgmt.delDataBtn']));
  app.api.setLocale(to);
  assert.ok(app.get('delBtn-PHONE').innerHTML.includes(catalogs[to]['bmgmt.delDataBtn']),'an untouched delete button follows the current locale');
  assert.equal(app.get('delBtn-PHONE').disabled,false);
  app.run("patchDevice({udid:'PHONE',conn:'usb',operations_available:true,backing_up:true,backup_state:'running'})");
  app.api.setLocale(from);
  assert.ok(app.get('delBtn-PHONE').innerHTML.includes(catalogs[from]['bmgmt.delDataBtn']));
  assert.equal(app.get('delBtn-PHONE').disabled,true);
  assert.equal(app.run('Object.keys(deviceOperations).length'),0);
});

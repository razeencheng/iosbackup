(function(global){
 'use strict';
 var doc=global.document,devices={},pending={};
 function tr(key,params){return global.IOSBKI18n.t(key,params);}
 function label(key,value){var name='check.'+key+'.'+value;return global.IOSBKI18n.has(name)?tr(name):tr('check.unknown');}
 function summary(result,options){
  if(!result)return tr('check.none');
  var text=label('kind',result.kind)+(options&&options.detailsOnly?'':' · '+label('state',result.state));
  if(result.reason)text+=' · '+label('reason',result.reason);
  text+='\n'+tr(result.kind==='quick'?'check.quickCoverage':'check.coverage',{metadata:tr(result.metadata_complete?'check.yes':'check.no'),manifest:tr(result.manifest_complete?'check.yes':'check.no'),records:result.records_checked||0,objects:result.objects_checked||0,missing:result.missing_objects||0});
  if(result.completed_at&&!result.completed_at.startsWith('0001-'))text+='\n'+global.IOSBKI18n.formatDate(result.completed_at);
  if(result.freshness==='stale'||result.freshness==='unknown')text+='\n'+label('freshness',result.freshness);
  if(result.last_completed)text+='\n\n'+tr('check.lastCompleted')+'\n'+summary(Object.assign({},result.last_completed,{last_completed:null}));
  return text;
 }
 function render(d){
  devices[d.udid]=d;
  var status=doc.getElementById('backupCheckResult-'+d.udid);if(status){status.removeAttribute('data-i18n');status.textContent=summary(d.backup_check);}
  var start=doc.getElementById('backupCheckStart-'+d.udid);if(start)start.disabled=!!pending[d.udid]||!!d.backing_up;
  var cancel=doc.getElementById('backupCheckCancel-'+d.udid);if(cancel){cancel.classList.toggle('hidden',!d.backup_check_active);cancel.disabled=!!pending[d.udid];}
 }
 async function act(button){
  var u=button.dataset.udid,action=button.dataset.checkAction,d=devices[u]||{};
  if(pending[u])return;
  if(action==='cancel'&&(!d.backup_check_active||!d.backup_check))return;
  pending[u]=true;render(Object.assign({udid:u},d));
  try{
   var response=await global.csrfFetch('/api/backup-check/'+encodeURIComponent(u),{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({action:action,id:action==='cancel'?d.backup_check.id:undefined})});
   var payload=await response.json();
   if(!response.ok)throw new Error(global.IOSBKI18n.message(payload,'common.operationFailed'));
   await global.refreshBackupControlStatus();
  }catch(error){if(global.toast)global.toast(error.message,true);}
  finally{delete pending[u];render(devices[u]||{udid:u});}
 }
 doc.addEventListener('click',function(event){var button=event.target.closest('[data-check-action]');if(button)act(button);});
 global.addEventListener('iosbk:localechange',function(){Object.keys(devices).forEach(function(u){render(devices[u]);});});
 global.IOSBKChecks={summary:summary,render:render};
})(window);

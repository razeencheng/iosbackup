(function(global){
 'use strict';
 var doc=global.document,devices={},pending={},sources={};
 function tr(key,params){return global.IOSBKI18n.t(key,params);}
 function label(key,value){var name='check.'+key+'.'+value;return global.IOSBKI18n.has(name)?tr(name):tr('check.unknown');}
 function reason(result){
  if(result.reason==='resource_limit'&&result.plist_file&&result.plist_limit_mib)return tr('check.plistLimit',{file:result.plist_file,limit:result.plist_limit_mib});
  return label('reason',result.reason||'read_failed');
 }
 function summary(result,options){
  if(!result)return tr('check.none');
  var text=label('kind',result.kind)+(options&&options.detailsOnly?'':' · '+label('state',result.state));
  if(result.reason)text+=' · '+reason(result);
  text+='\n'+tr(result.kind==='quick'?'check.quickCoverage':'check.coverage',{metadata:tr(result.metadata_complete?'check.yes':'check.no'),manifest:tr(result.manifest_complete?'check.yes':'check.no'),records:result.records_checked||0,objects:result.objects_checked||0,missing:result.missing_objects||0});
  if(result.completed_at&&!result.completed_at.startsWith('0001-'))text+='\n'+global.IOSBKI18n.formatDate(result.completed_at);
  if(result.freshness==='stale'||result.freshness==='unknown')text+='\n'+label('freshness',result.freshness);
  if(result.last_completed)text+='\n\n'+tr('check.lastCompleted')+'\n'+summary(Object.assign({},result.last_completed,{last_completed:null}));
  return text;
 }
 function node(tag,text,cls){var n=doc.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;}
 function date(value){return value&&!String(value).startsWith('0001-')?global.IOSBKI18n.formatDate(value):tr('history.unknown');}
 function historical(c){return c&&(c.freshness==='stale'||c.freshness==='unknown');}
 function card(host,options){
  var result=options.result,previous=options.previous,encrypted=options.encrypted;
  // 能力限制不伪装成检查失败，也不能凭“已加密”推断状态检查通过。
  if(encrypted&&result&&result.state==='unavailable'&&result.reason==='encrypted_manifest'){result=previous;previous=null;}
  var signature=JSON.stringify([result,previous,encrypted,options.saveFailed,options.actionKey,global.IOSBKI18n.getLocale()]);
  if(host.dataset.checkSignature===signature)return;
  var old=host.querySelector('details'),open=old?old.open:options.open,focused=doc.activeElement&&host.contains&&host.contains(doc.activeElement),focusTag=focused&&doc.activeElement.tagName;
  host.dataset.checkSignature=signature;host.replaceChildren();host.classList.toggle('check-card',true);
  var attention=result&&['issues','unavailable'].indexOf(result.state)>=0;host.classList.toggle('check-card-attention',!!attention);
  var top=node('div',undefined,'check-card-top'),icon=node('span',undefined,'check-card-icon');
  var iconName=!result||historical(result)?'info':result.state==='passed'?'check-circle':result.state==='running'?'loader':'alert';
  icon.innerHTML='<svg class="ui-icon" aria-hidden="true"><use href="/static/ui-icons.svg#icon-'+iconName+'"></use></svg>';top.appendChild(icon);
  var copy=node('div',undefined,'check-card-copy'),title=!result?tr('check.none'):historical(result)?tr('check.card.'+(result.freshness==='stale'?'stale':'historical')):result.state==='passed'?tr(result.kind==='quick'?'check.card.quickPassed':'check.state.passed'):result.state==='unavailable'&&result.reason?reason(result):label('state',result.state);
  copy.appendChild(node('strong',title,'check-card-title'));
  if(result){
   var meta=node('div',undefined,'check-card-meta');meta.appendChild(node('span',tr('check.card.kind.'+result.kind)));
   var info=node('button','i','check-card-info');info.type='button';info.setAttribute('aria-label',tr('check.card.scope'));info.setAttribute('data-hint',tr(result.kind==='quick'?'check.card.quickScope':'check.card.manifestScope'));meta.appendChild(info);
   if(result.completed_at&&!String(result.completed_at).startsWith('0001-')){meta.appendChild(node('span','·'));var time=node('time',date(result.completed_at));time.setAttribute('datetime',result.completed_at);meta.appendChild(time);}
   copy.appendChild(meta);
  }
  top.appendChild(copy);if(options.action)top.appendChild(options.action);host.appendChild(top);
  if(result&&result.reason&&(result.state!=='unavailable'||historical(result)))host.appendChild(node('p',reason(result),'check-card-note console-amber'));
  if(historical(result))host.appendChild(node('p',label('freshness',result.freshness),'check-card-note'));
  if(encrypted)host.appendChild(node('p',tr('check.card.encrypted'),'check-card-note'));
  if(previous&&result&&['passed','issues'].indexOf(result.state)<0){var prior=tr('check.card.previous',{kind:tr('check.card.kind.'+previous.kind),state:label('state',previous.state),time:date(previous.completed_at)});if(historical(previous))prior+=' · '+tr('check.card.'+(previous.freshness==='stale'?'stale':'historical'));host.appendChild(node('p',prior,'check-card-note'));}
  if(options.saveFailed)host.appendChild(node('p',tr('history.reviewSaveFailed'),'check-card-note console-amber'));
  if(result&&result.kind==='manifest'&&!encrypted&&result.state!=='running'&&(result.manifest_complete||result.records_checked>0||result.objects_checked>0)){
   var details=node('details',undefined,'check-card-details');details.open=!!open;var toggle=node('summary',tr(open?'check.card.less':'check.card.more'));details.appendChild(toggle);
   details.addEventListener('toggle',function(){toggle.textContent=tr(details.open?'check.card.less':'check.card.more');});
   if(!result.manifest_complete)details.appendChild(node('p',tr('check.card.partial'),'check-card-note'));
   var counts=node('dl',undefined,'check-card-counts');[['records',result.records_checked],['objects',result.objects_checked],['missing',result.missing_objects]].forEach(function(pair){var item=node('div');item.appendChild(node('dt',tr('check.card.'+pair[0])));item.appendChild(node('dd',new Intl.NumberFormat(global.IOSBKI18n.getLocale()).format(pair[1]||0),pair[0]==='missing'&&pair[1]>0?'console-amber':''));counts.appendChild(item);});details.appendChild(counts);host.appendChild(details);
  }
  if(focused){var target=focusTag==='SUMMARY'?host.querySelector('summary'):options.action;if(target&&!target.disabled&&target.focus)target.focus();}
 }
 function encrypted(d){
  var c=d.backup_check;if(c&&c.freshness==='current'&&c.reason==='encrypted_manifest')return true;
  if(c&&c.freshness==='current'&&c.kind==='manifest'&&c.state==='passed')return false;
  var source=sources[d.udid],brief=d.backup_index,o=source&&source.overview;
  // 同源概况中的加密标记只约束操作能力；旧检查仍按自身 freshness 显示，不能升级为当前通过。
  if(o&&o.status==='available'&&brief&&brief.source_key===source.source_key&&typeof o.encrypted==='boolean')return o.encrypted;
  return false;
 }
 function render(d){
  devices[d.udid]=d;
  var host=doc.getElementById('currentCheck-'+d.udid);if(!host)return;
  var c=d.backup_check,locked=encrypted(d),active=!!d.backup_check_active,action;
  if(active||!locked){action=node('button',tr(active?'check.cancel':c&&c.kind==='manifest'?'check.card.retry':'check.start'),'check-card-action');action.type='button';action.dataset.udid=d.udid;action.dataset.checkAction=active?'cancel':'start';action.disabled=!!pending[d.udid]||(!active&&!!d.backing_up);}
  card(host,{result:c,previous:c&&c.last_completed,encrypted:locked,action:action,actionKey:[active,!!pending[d.udid],!!d.backing_up]});
 }
 async function act(button){
  var u=button.dataset.udid,action=button.dataset.checkAction,d=devices[u]||{};
  if(pending[u])return;
  if(action==='start'&&encrypted(d))return;
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
 global.IOSBKChecks={summary:summary,reason:reason,card:card,render:render,source:function(u,snapshot){sources[u]=snapshot;if(devices[u])render(devices[u]);}};
})(window);

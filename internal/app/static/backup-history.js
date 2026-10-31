(function(global){
 'use strict';
 var doc=global.document,visible='',mode='',latest=null,timer=null,pending=false,exportSession=null,reviewSession=null,archiveDialog=null,archiveOriginal=null,pollEpoch=0,pollAbort=null,pollBusy=false;
 function createController(options){
  var states={},current='',generation=0,inflight=null,abort=null;
  function state(u){return states[u]||(states[u]={udid:u,selected:'',points:[],data:null,draft:null,dirty:false,loading:false});}
  function publish(){if(current)options.render(Object.assign({},state(current)));}
  async function refresh(more){
   if(!current||inflight)return inflight;var u=current,epoch=generation,v=state(u);abort=new AbortController();v.loading=true;publish();
   var work=(async function(){try{
    var result=await options.get('/api/backup-history/'+encodeURIComponent(u)+(more&&v.data&&v.data.next_offset?'?offset='+v.data.next_offset:''),abort.signal);
    if(epoch!==generation)return;
    if(!more){var target=v.points.length,collected=(result.points||[]).slice();while(result.next_offset&&collected.length<target){var next=await options.get('/api/backup-history/'+encodeURIComponent(u)+'?offset='+result.next_offset,abort.signal);if(epoch!==generation)return;collected=collected.concat(next.points||[]);result.next_offset=next.next_offset;}result.points=collected;}
    if(!more&&v.selected&&!result.points.some(function(p){return p.id===v.selected;})){try{var selected=await options.get('/api/backup-point/'+encodeURIComponent(v.selected)+'?udid='+encodeURIComponent(u),abort.signal);if(epoch!==generation)return;result.points.push(selected);}catch(e){if(epoch!==generation)return;if(e.status!==404)throw e;}}
    v.points=more?v.points.concat(result.points||[]):result.points||[];v.points=Array.from(new Map(v.points.map(function(p){return [p.id,p];})).values());v.data=result;
    if(!v.dirty)v.draft=Object.assign({},result.policy);
    if(v.selected&&!v.points.some(function(p){return p.id===v.selected;}))v.selected='';
   }catch(e){if(epoch===generation&&e.name!=='AbortError')options.error(e);}finally{if(epoch===generation){inflight=null;v.loading=false;publish();}}})();
   inflight=work;return work;
  }
  return {open:function(u){if(current===u)return refresh();generation++;if(abort)abort.abort();inflight=null;current=u;publish();return refresh();},refresh:refresh,select:function(id){var v=state(current);if(v.points.some(function(p){return p.id===id;})){v.selected=id;publish();}},back:function(){if(current){state(current).selected='';publish();}},edit:function(draft){if(current){var v=state(current);v.draft=Object.assign({},draft);v.dirty=true;publish();}},discard:function(u){var v=states[u];if(v&&v.data){v.dirty=false;v.draft=Object.assign({},v.data.policy);publish();}},navigate:function(id){if(!current)return;state(current).selected=id;return refresh();},saved:function(u){if(u&&states[u])states[u].dirty=false;},close:function(){generation++;if(abort)abort.abort();inflight=null;current='';},repaint:publish,hasDirty:function(){return Object.keys(states).some(function(u){return states[u].dirty;});}};
 }
 function tr(key,params){return global.IOSBKI18n.t(key,params);}
 function label(group,value){var key='history.'+group+'.'+value;return global.IOSBKI18n.has(key)?tr(key):tr('history.unknown');}
 function date(value){return value&&!String(value).startsWith('0001-')?global.IOSBKI18n.formatDate(value):tr('history.unknown');}
 function pointDate(p){return p.completion_unknown?tr('history.timeUnknown'):date(p.completed_at);}
 function bytes(n){return global.formatBackupBytes?global.formatBackupBytes(n):String(n)+' B';}
 function el(tag,text,cls){var node=doc.createElement(tag);if(text!==undefined)node.textContent=text;if(cls)node.className=cls;return node;}
 function set(id,text){var n=doc.getElementById(id);if(n){n.removeAttribute('data-i18n');n.textContent=text;}return n;}
 function show(id,yes){var n=doc.getElementById(id);if(n)n.classList.toggle('hidden',!yes);return n;}
 function button(text,action,disabled){var b=el('button',text,'history-button');b.type='button';b.dataset.historyAction=action;b.disabled=!!disabled;return b;}
 function hint(term){var b=el('button','?','term-hint');b.type='button';b.setAttribute('data-term',term);b.setAttribute('aria-label',tr('history.explain',{term:tr('term.'+term+'.title')}));return b;}
 async function request(url,signal,method,body){var options={signal:signal};if(method){options.method=method;options.headers={'Content-Type':'application/json'};options.body=JSON.stringify(body||{});}var response=await global.csrfFetch(url,options),data=await response.json();if(!response.ok){var e=new Error(global.IOSBKI18n.message(data,'common.operationFailed'));e.status=response.status;throw e;}return data;}
 function error(e){if(e.name==='AbortError')return;var node=set('historyError-'+visible,e.message||tr('common.operationFailed'));if(node)node.classList.remove('hidden');if(global.toast)global.toast(e.message||tr('common.operationFailed'),true);}
 var controller=createController({get:request,render:render,error:error});
 function policyFields(v){
  var host=doc.getElementById('historySettings-'+v.udid);if(!host)return;
  var enabled=doc.getElementById('historyEnabled-'+v.udid),days=doc.getElementById('historyDays-'+v.udid);
  if(!enabled){
   var title=el('h3',tr('history.title'));title.dataset.i18n='history.title';host.appendChild(title);
   var fields=el('div',undefined,'history-policy-fields'),toggle=el('label');enabled=el('input');enabled.type='checkbox';enabled.id='historyEnabled-'+v.udid;enabled.dataset.historyField='enabled';toggle.appendChild(enabled);var copy=el('span',tr('history.enable'));copy.dataset.i18n='history.enable';toggle.appendChild(copy);fields.appendChild(toggle);
   var daylabel=el('label');copy=el('span',tr('history.days'));copy.dataset.i18n='history.days';daylabel.appendChild(copy);days=el('input');days.type='number';days.min='1';days.max='365';days.id='historyDays-'+v.udid;days.dataset.historyField='days';days.className='iosbk-in';daylabel.appendChild(days);fields.appendChild(daylabel);host.appendChild(fields);
   ['cost','retention','disableHint'].forEach(function(key){var n=el('p',tr('history.'+key),'history-note');n.dataset.i18n='history.'+key;host.appendChild(n);});
   var clone=el('p',undefined,'history-note'),cloneLabel=el('span',tr('term.clone.title'));cloneLabel.dataset.i18n='term.clone.title';clone.appendChild(cloneLabel);clone.appendChild(hint('clone'));host.appendChild(clone);
   var save=button(tr('history.preview'),'policy');save.id='historyPolicySave-'+v.udid;save.dataset.i18n='history.preview';host.appendChild(save);var dirty=el('span','', 'history-note');dirty.id='historyDirty-'+v.udid;host.appendChild(dirty);
   var operation=el('p','','history-note');operation.id='historyPolicyOperation-'+v.udid;operation.setAttribute('role','status');host.appendChild(operation);
   var retry=button(tr('history.policyRetry'),'retryPolicy');retry.id='historyPolicyRetry-'+v.udid;retry.dataset.i18n='history.policyRetry';host.appendChild(retry);
  }
  if(v.draft){enabled.checked=!!v.draft.enabled;if(days.value!==String(v.draft.days))days.value=v.draft.days;}
  enabled.disabled=days.disabled=pending||!v.data||!v.data.can_edit_policy;
  var save=doc.getElementById('historyPolicySave-'+v.udid);save.disabled=pending||!v.data||!v.data.can_edit_policy||!v.draft||!Number.isInteger(Number(v.draft.days))||Number(v.draft.days)<1||Number(v.draft.days)>365;
  set('historyDirty-'+v.udid,v.dirty?tr('bset.unsaved'):'');
  set('historyPolicyOperation-'+v.udid,policyNotice(v.data));
  var retry=show('historyPolicyRetry-'+v.udid,!!(v.data&&v.data.can_retry_policy));if(retry)retry.disabled=pending;
 }
 function policyNotice(data){var state=data&&data.policy_operation&&data.policy_operation.state;return ['running','failed','interrupted'].indexOf(state)>=0?tr('history.policyOperation.'+state):'';}
 function render(v){
  latest=v;var u=v.udid;policyFields(v);if(!v.data)return;
  var data=v.data;if(global.IOSBKConsole)global.IOSBKConsole.updateHistory(v);set('historySummaryText-'+u,tr(data.policy.enabled?'history.summary':'history.summaryOff',{days:data.policy.days,count:data.total})+(policyNotice(data)?'\n'+policyNotice(data):''));
  set('historyStorage-'+u,tr('history.storage',{size:bytes(data.logical_bytes||0)})+(data.bytes_known?'':' · '+tr('history.sizePartial')));
  var warning=[];if(data.unavailable_stores.length)warning.push(tr('history.unavailable'));if(data.busy)warning.push(tr('history.busy'));if(data.phase==='enabling'||data.phase==='disabling')warning.push(tr('history.transition'));if(data.phase==='disabled'&&data.total)warning.push(tr('history.manualHint'));
  set('historyWarning-'+u,warning.join(' '));show('historyWarning-'+u,warning.length>0);
  var moreButton=doc.getElementById('historyMore-'+u);if(moreButton)moreButton.disabled=v.loading;var list=doc.getElementById('historyList-'+u);if(!list)return;var signature=JSON.stringify([v.points,v.selected,pending,data.current_source,global.IOSBKI18n.getLocale()]);if(list.dataset.signature===signature){renderSessions();return;}list.dataset.signature=signature;list.replaceChildren();
  if(!v.points.length)list.appendChild(el('p',tr(data.policy.enabled?'history.empty':'history.off'),'history-empty'));
  if(v.points.length){var head=el('div',undefined,'console-point-row console-table-head');['console.completed','console.sizeEncryption','console.lastCheck','console.retention','console.actions'].forEach(function(key){head.appendChild(el('span',tr(key)));});list.appendChild(head);}
  v.points.forEach(function(p){
   var row=el('div',undefined,'console-point-row'+(p.state==='pending'?' attention':''));
   var select=button(pointDate(p),'select');select.dataset.point=p.id;select.classList.add('history-point-select');
   var cell=el('div');cell.appendChild(select);cell.appendChild(el('small',tr(global.IOSBKConsole&&global.IOSBKConsole.isCurrentPoint(data,p)?'console.currentPoint':'console.historicalPoint'),'console-subtle'));row.appendChild(cell);
   cell=el('div');cell.appendChild(el('strong',p.bytes_known?bytes(p.bytes):tr('history.sizeUnknown')));cell.appendChild(el('small',label('encryption',p.encryption),'console-subtle'));row.appendChild(cell);
   var check=p.review&&p.review.scope?p.review:p.check;cell=el('div');cell.appendChild(el('span',tr('check.kind.'+(check.scope||'quick'))+' · '+label('check',check.state),check.state==='passed'?'console-good':'console-amber'));cell.appendChild(el('small',date(check.checked_at),'console-subtle'));row.appendChild(cell);
   cell=el('div',undefined,'console-retention');var term=p.pinned?'pinned':p.manual?'manual':p.state==='pending'?'pending':'';
   if(term){cell.appendChild(el('span',tr('term.'+term+'.title'),'history-badge'));cell.appendChild(hint(term));}else cell.appendChild(el('span',tr('console.byPolicy')));
   if(p.baseline){var baseline=el('small',tr('console.incrementalSource'),'console-subtle');baseline.appendChild(hint('baseline'));cell.appendChild(baseline);}row.appendChild(cell);
   var actions=el('div',undefined,'console-row-actions'),download=button(tr('console.download'),'downloadPoint',pending||!p.can_export);download.dataset.point=p.id;actions.appendChild(download);var details=button(tr('console.details'),'select');details.dataset.point=p.id;details.classList.add('console-text-button');actions.appendChild(details);row.appendChild(actions);list.appendChild(row);
  });
  show('historyMore-'+u,!!data.next_offset);var more=doc.getElementById('historyMore-'+u);if(more)more.disabled=v.loading;
  var p=v.points.find(function(p){return p.id===v.selected;}),detail=doc.getElementById('historyDetail-'+u);show('historyDetail-'+u,!!p);var layout=doc.getElementById('historyLayout-'+u);if(layout)layout.classList.toggle('has-selection',!!p);
  if(!p||!detail){if(detail&&detail.closest('dialog.console-drawer'))global.IOSBKConsole.closePoint(u);return;}detail.replaceChildren();detail.appendChild(el('h3',pointDate(p)));detail.appendChild(el('p',(p.bytes_known?bytes(p.bytes):tr('history.sizeUnknown'))+' · '+label('encryption',p.encryption),'console-point-hero'));

  detail.appendChild(el('p',tr('history.outcomes',{execution:p.completion_unknown?tr('history.unknown'):tr('jobs.state.succeeded'),history:label('state',p.state)}),'history-note'));
  var checked=el('p',tr('term.quick.title')+' · '+label('check',p.check.state),'history-note');checked.appendChild(hint('quick'));detail.appendChild(checked);detail.appendChild(el('p',storedCheck(p.check),'history-note'));
  detail.appendChild(el('p',tr('history.originalDate',{day:p.day,zone:p.timezone}),'history-note'));if(p.completion_unknown)detail.appendChild(el('p',tr('history.unknownDateHint'),'history-warning'));
  detail.appendChild(el('p',tr('history.keepReason',{reason:label('reason',p.reason||'eligible')}),'history-note'));
  if(p.review&&p.review.scope)detail.appendChild(el('p',tr('history.lastReview',{scope:tr('check.kind.'+p.review.scope),state:label('check',p.review.state),time:date(p.review.checked_at)})+'\n'+storedCheck(p.review),'history-note'));
  detail.appendChild(el('p',tr('history.restoreHint'),'history-warning'));if(p.encryption==='encrypted')detail.appendChild(el('p',tr('history.passwordHint'),'history-warning'));
  var actions=el('div',undefined,'history-actions');actions.appendChild(button(tr(p.pinned?'history.unpin':'history.pin'),'pin',pending||!p.can_pin));actions.appendChild(button(tr('history.check'),'check',pending||!p.can_check));actions.appendChild(button(tr('history.export'),'export',pending||!p.can_export));actions.appendChild(button(tr('history.delete'),'delete',pending||!p.can_delete));detail.appendChild(actions);
  var related=el('section',undefined,'console-related');related.appendChild(el('h4',tr('console.related')));
  if(p.job_id){var job=el('button',tr('console.openGeneratingJob'),'console-navigation-row');job.type='button';job.dataset.consoleAction='job';job.dataset.udid=u;job.dataset.job=p.job_id;related.appendChild(job);}
  if(global.IOSBKConsole&&global.IOSBKConsole.isCurrentPoint(data,p)){var files=el('button',tr('console.browseCurrent'),'console-navigation-row');files.type='button';files.dataset.consoleAction='current';files.dataset.udid=u;related.appendChild(files);}
  detail.appendChild(related);
  var review=el('div');review.id='historyReview-'+u;detail.appendChild(review);var download=el('div');download.id='historyDownload-'+u;detail.appendChild(download);renderSessions();
 }
 function storedCheck(c){return global.IOSBKChecks?global.IOSBKChecks.summary(Object.assign({},c,{kind:c.scope,completed_at:c.checked_at,freshness:'current'})):label('check',c.state);}
 function renderSessions(){
  if(!latest)return;var u=latest.udid,p=latest.selected,host=doc.getElementById('historyDownload-'+u),review=doc.getElementById('historyReview-'+u);
  if(host&&exportSession&&exportSession.point_id===p&&host.dataset.sessionSignature!==JSON.stringify([exportSession.id,exportSession.state,pending,global.IOSBKI18n.getLocale()])){host.dataset.sessionSignature=JSON.stringify([exportSession.id,exportSession.state,pending,global.IOSBKI18n.getLocale()]);host.replaceChildren();host.appendChild(el('p',label('export',exportSession.state),'history-warning'));if(exportSession.state==='ready'){var link=el('a',tr('history.download'),'history-button primary');link.href='/api/backup-export/'+exportSession.id+'/download';link.target='history-download';link.setAttribute('download','');host.appendChild(link);}
   if(exportSession.state==='ready'||exportSession.state==='streaming')host.appendChild(button(tr('history.cancelDownload'),'cancelExport',pending));
  }
  if(review&&reviewSession&&reviewSession.point_id===p&&review.dataset.sessionSignature!==JSON.stringify([reviewSession,pending,global.IOSBKI18n.getLocale()])){review.dataset.sessionSignature=JSON.stringify([reviewSession,pending,global.IOSBKI18n.getLocale()]);review.replaceChildren();review.appendChild(el('p',global.IOSBKChecks?global.IOSBKChecks.summary(reviewSession.result):label('check',reviewSession.result.state),'history-note'));if(reviewSession.result.state==='running')review.appendChild(button(tr('check.cancel'),'cancelCheck',pending));else if(!reviewSession.saved)review.appendChild(el('p',tr('history.reviewSaveFailed'),'history-warning'));}
 }
 function schedule(){if(timer)global.clearTimeout(timer);if(visible)timer=global.setTimeout(poll,4000);}
 async function poll(){if(pollBusy||!visible)return;pollBusy=true;var u=visible,epoch=pollEpoch;pollAbort=new AbortController();try{
  if(exportSession&&(exportSession.state==='ready'||exportSession.state==='streaming')){var id=exportSession.id;var result=await request('/api/backup-export/'+id,pollAbort.signal);if(epoch!==pollEpoch)return;if(exportSession&&exportSession.id===id)exportSession=result;}
  if(reviewSession&&reviewSession.result.state==='running'){var point=reviewSession.point_id;var result=await request('/api/backup-point/'+point+'/check',pollAbort.signal);if(epoch!==pollEpoch)return;if(reviewSession&&reviewSession.point_id===point)reviewSession=Object.assign(result,{point_id:point});}
  if(epoch!==pollEpoch)return;if(visible===u&&!pending)await controller.refresh();if(epoch===pollEpoch)renderSessions();
 }catch(e){if(epoch===pollEpoch&&visible===u&&e.name!=='AbortError'){error(e);if(e.status===410){if(exportSession)exportSession.state='expired';if(reviewSession)reviewSession.result.state='unavailable';}}}finally{if(epoch===pollEpoch){pollBusy=false;schedule();}}}
 function confirmAction(title,body){return new Promise(function(resolve){
  var prior=doc.activeElement,d=el('dialog',undefined,'history-dialog');d.setAttribute('aria-labelledby','historyConfirmTitle');d.setAttribute('aria-describedby','historyConfirmBody');var h=el('h3',title);h.id='historyConfirmTitle';d.appendChild(h);var p=el('p',body);p.id='historyConfirmBody';d.appendChild(p);var actions=el('div',undefined,'history-actions');var cancel=button(tr('history.cancel'),'dialogCancel'),ok=button(tr('history.confirm'),'dialogConfirm');ok.classList.add('primary');actions.appendChild(cancel);actions.appendChild(ok);d.appendChild(actions);doc.body.appendChild(d);
  function finish(yes){d.close();d.remove();if(prior&&prior.isConnected)prior.focus();resolve(yes);}cancel.onclick=function(){finish(false)};ok.onclick=function(){finish(true)};d.addEventListener('cancel',function(e){e.preventDefault();finish(false)});d.showModal();cancel.focus();
 });}
 async function act(action,buttonNode){
  if(action==='open'){open(buttonNode.dataset.udid);return;}if(action==='back'){close(true);return;}if(action==='settings'){var u=visible;close(false);global.switchSection(u,'backup');var n=doc.getElementById('historySettings-'+u);if(n)n.scrollIntoView({block:'start',behavior:'smooth'});return;}
  if(action==='select'||action==='downloadPoint'){controller.select(buttonNode.dataset.point);global.IOSBKConsole.pointDrawer(visible);if(action==='select')return;action='export';}if(action==='list'){global.IOSBKConsole.closeLayers();controller.back();return;}if(action==='more'){controller.refresh(true);return;}if(action==='refresh'){controller.refresh();return;}
  if(pending||!latest)return;var actionEpoch=pollEpoch,u=latest.udid,p=latest.points.find(function(p){return p.id===latest.selected;}),draft=Object.assign({},latest.draft),originalPoints=latest.points.slice();pending=true;controller.repaint();
  try{
   if(action==='policy'){
    draft.days=Number(draft.days);var preview=await request('/api/backup-history/'+encodeURIComponent(u)+'/policy-preview',null,'POST',{policy:draft});var text=tr(draft.enabled?'history.enableConfirm':'history.disableHint')+'\n\n'+tr('history.previewCount',{count:preview.affected_count});
    var affected=[];Object.keys(preview.preview.stores).forEach(function(s){(preview.preview.stores[s].delete||[]).forEach(function(id){var point=originalPoints.find(function(p){return p.id===id});affected.push(point?pointDate(point):id);});});if(affected.length)text+='\n'+affected.join('\n');
    if(!await confirmAction(tr('history.policyTitle'),text))return;
    await request('/api/backup-history/'+encodeURIComponent(u)+'/policy',null,'PUT',{policy:draft,token:preview.token});controller.saved(u);if(global.toast)global.toast('history.policyStarted');
   }else if(action==='retryPolicy'){
    await request('/api/backup-history/'+encodeURIComponent(u)+'/policy-retry',null,'POST',{});if(global.toast)global.toast('history.policyStarted');
   }else if(p){var base='/api/backup-point/'+p.id+'?udid='+encodeURIComponent(u);
    if(action==='pin'){if(p.pinned&&!await confirmAction(tr('console.unpinTitle'),tr('console.unpinHint')))return;await request('/api/backup-point/'+p.id+'/pin?udid='+encodeURIComponent(u),null,'POST',{revision:p.revision,pinned:!p.pinned});}
    else if(action==='delete'){if(!await confirmAction(tr('history.deleteTitle'),pointDate(p)+'\n'+tr('history.deleteConfirm')+(p.pinned?'\n'+tr('history.deletePin'):'')+(p.baseline?'\n'+tr('history.deleteBaseline'):'')))return;await request(base,null,'DELETE',{revision:p.revision,confirm:p.id});if(actionEpoch===pollEpoch&&visible===u&&latest&&latest.selected===p.id)global.IOSBKConsole.closePoint(u);}
    else if(action==='check'){var result=await request('/api/backup-point/'+p.id+'/check',null,'POST',{revision:p.revision,action:'start'});reviewSession={id:result.id,point_id:p.id,result:{kind:'manifest',state:'running',freshness:'current'}};}
    else if(action==='export'){exportSession=await request('/api/backup-point/'+p.id+'/exports',null,'POST',{});}
    else if(action==='cancelExport'&&exportSession)await request('/api/backup-export/'+exportSession.id,null,'DELETE',{});
    else if(action==='cancelCheck'&&reviewSession)await request('/api/backup-point/'+p.id+'/check',null,'POST',{action:'cancel',id:reviewSession.id});
   }
   if(visible===u)await controller.refresh();
  }catch(e){if(visible===u)error(e);else if(global.toast)global.toast(e.message,true);}finally{pending=false;controller.repaint();if(visible===u){var focus=doc.activeElement;if(!focus||focus===doc.body||!focus.isConnected){var target=action==='export'?doc.querySelector('#historyDownload-'+u+' a'):doc.querySelector('#historyDetail-'+u+' [data-history-action="'+action+'"]');if(target&&!target.disabled)target.focus();}schedule();}}
 }
 function open(u){if(global.IOSBKJobs)global.IOSBKJobs.hide();visible=u;mode='history';show('historyPanel-'+u,true);controller.open(u);schedule();var b=doc.getElementById('historyBack-'+u);if(b)b.focus();}
 function close(focus){var u=visible;if(!u)return;pollEpoch++;if(pollAbort)pollAbort.abort();pollBusy=false;if(archiveDialog){if(archiveOriginal){archiveOriginal.parent.appendChild(archiveOriginal.panel);archiveOriginal.panel.classList.add('hidden');archiveOriginal=null;}archiveDialog.close();archiveDialog.remove();archiveDialog=null;}show('historyPanel-'+u,false);controller.close();visible='';mode='';if(timer)global.clearTimeout(timer);timer=null;if(focus){var b=doc.getElementById('openHistory-'+u);if(b)b.focus();}}
 function openArchived(u){
  close(false);var dialog=el('dialog',undefined,'history-archive-dialog');archiveDialog=dialog;dialog.setAttribute('aria-label',tr('history.title'));
  var panel=doc.getElementById('historyPanel-'+u);
  if(panel){archiveOriginal={panel:panel,parent:panel.parentNode};dialog.appendChild(panel);}else{
   panel=el('section',undefined,'history-panel');panel.id='historyPanel-'+u;
   var toolbar=el('div',undefined,'history-toolbar');toolbar.appendChild(el('h3',tr('history.title')));var back=button(tr('history.close'),'back');back.id='historyBack-'+u;toolbar.appendChild(back);panel.appendChild(toolbar);
   ['Error','Warning'].forEach(function(suffix){var n=el('p','', 'history-warning hidden');n.id='history'+suffix+'-'+u;n.setAttribute('role',suffix==='Error'?'alert':'status');panel.appendChild(n);});
   var layout=el('div',undefined,'history-layout');layout.id='historyLayout-'+u;var column=el('div',undefined,'history-list-column'),list=el('div',undefined,'history-list');list.id='historyList-'+u;column.appendChild(list);var more=button(tr('history.more'),'more');more.id='historyMore-'+u;more.classList.add('hidden');column.appendChild(more);layout.appendChild(column);var detail=el('section',undefined,'history-detail hidden');detail.id='historyDetail-'+u;layout.appendChild(detail);panel.appendChild(layout);dialog.appendChild(panel);
  }
  doc.body.appendChild(dialog);panel.classList.remove('hidden');dialog.addEventListener('cancel',function(e){e.preventDefault();close(false);});dialog.showModal();visible=u;mode='archive';controller.open(u);schedule();
 }
 function section(u,sec){if(visible&&visible!==u)close(false);if(sec==='settings'||sec==='backups'){close(false);visible=u;mode=sec;show('historyPanel-'+u,sec==='backups');controller.open(u);schedule();}else close(false);}
 doc.addEventListener('click',function(e){var b=e.target.closest('[data-history-action]');if(b)act(b.dataset.historyAction,b);});
 doc.addEventListener('input',function(e){if(!e.target.dataset.historyField||!latest)return;var enabled=doc.getElementById('historyEnabled-'+latest.udid),days=doc.getElementById('historyDays-'+latest.udid);controller.edit({enabled:enabled.checked,days:days.value});});
 global.addEventListener('iosbk:localechange',function(){controller.repaint();});global.addEventListener('beforeunload',function(e){if(controller.hasDirty()){e.preventDefault();e.returnValue='';}});
 global.IOSBKHistory={isPending:function(){return pending;},refresh:function(){return controller.refresh();},back:function(){controller.back();},discard:function(u){controller.discard(u);},navigate:async function(u,id){var epoch=pollEpoch,targetMode=mode;if(visible!==u||(mode!=='backups'&&mode!=='archive'))return;await controller.open(u);if(epoch!==pollEpoch||visible!==u||mode!==targetMode)return;await controller.navigate(id);if(epoch!==pollEpoch||visible!==u||mode!==targetMode)return;if(latest&&latest.selected===id&&latest.points.some(function(p){return p.id===id;}))global.IOSBKConsole.pointDrawer(u);else global.toast(tr('console.pointMissing'),true);},createController:createController,open:open,openArchived:openArchived,close:close,section:section,hide:function(){close(false);}};
})(window);

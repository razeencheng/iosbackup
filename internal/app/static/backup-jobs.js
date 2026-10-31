(function(global){
  'use strict';
  var document=global.document, deviceStates={}, visible='', currentView=null;
  var filters={};
  var jobID=/^bkp-[0-9a-f]{12,32}$/;
  function mergeEvents(previous,incoming,limit){
    var byCursor=new Map();
    previous.concat(incoming).forEach(function(event){if(Number.isSafeInteger(event.cursor)&&event.cursor>0)byCursor.set(event.cursor,event);});
    return Array.from(byCursor.values()).sort(function(a,b){return a.cursor-b.cursor;}).slice(-limit);
  }
  function createController(options){
    var generation=0,timer=null,abort=null,busy=false,dirty=false;
    var view={udid:'',selected:'',jobs:[],events:[],cursor:0,job:null,status:'available',truncated:false};
    function publish(){options.render(Object.assign({},view,{events:view.events.slice(),jobs:view.jobs.slice()}));}
    function schedule(delay){if(timer!==null)options.cancel(timer);timer=options.schedule(function(){timer=null;poll();},delay);}
    async function poll(){
      if(!view.udid||busy)return;
      busy=true;dirty=false;var epoch=generation;abort=new AbortController();
      try{
        var list=await options.get('/api/backup-jobs/'+encodeURIComponent(view.udid),abort.signal);
        if(epoch!==generation)return;
        view.jobs=list.jobs||[];view.status=list.recording_status;
        if(view.selected&&!view.jobs.some(function(job){return job.id===view.selected;})){view.selected='';view.job=null;view.events=[];view.cursor=0;view.truncated=false;view.missing=true;}
        if(!view.selected&&!view.missing&&view.jobs.length){view.selected=view.jobs[0].id;view.cursor=Math.max(0,view.jobs[0].last_cursor-1000);}
        publish();
        if(view.selected&&jobID.test(view.selected)){
          var result=await options.get('/api/backup-job/'+encodeURIComponent(view.selected)+'/events?after='+view.cursor+'&limit=500',abort.signal);
          if(epoch!==generation)return;
          view.job=result.job;view.events=mergeEvents(view.events,result.events||[],1000);
          view.cursor=Math.max(view.cursor,result.next_cursor||0);view.truncated=!!result.truncated;
          publish();if(result.has_more)dirty=true;
        }
      }catch(error){if(epoch===generation&&error.name!=='AbortError'){if(error.status===410){view.selected='';view.job=null;view.events=[];view.cursor=0;view.truncated=false;view.missing=true;publish();}options.error(error);}}
      finally{if(epoch===generation){busy=false;schedule(dirty?100:5000);}}
    }
    function reset(){generation++;if(abort)abort.abort();if(timer!==null)options.cancel(timer);timer=null;busy=false;}
    return {
      open:function(udid){reset();view={udid:udid,selected:'',jobs:[],events:[],cursor:0,job:null,status:'available',truncated:false};publish();return poll();},
      select:function(id){if(!jobID.test(id))return;reset();view.selected=id;view.missing=false;view.events=[];view.job=null;var job=view.jobs.find(function(j){return j.id===id;});view.cursor=Math.max(0,(job&&job.last_cursor||0)-1000);publish();return poll();},
      close:function(){reset();view.udid='';},
      invalidate:function(){if(!view.udid)return;if(busy){dirty=true;return;}schedule(150);},
      repaint:publish
    };
  }
  function tr(key,params){return global.IOSBKI18n.t(key,params);}
  function text(id,value){var node=document.getElementById(id);if(node)node.textContent=value;}
  function show(id,yes){var node=document.getElementById(id);if(node)node.classList.toggle('hidden',!yes);return node;}
  function date(value){return value&&!String(value).startsWith('0001-')?global.IOSBKI18n.formatDate(value):tr('jobs.noActivity');}
  function label(kind,value){var key='jobs.'+kind+'.'+value;return global.IOSBKI18n.has(key)?tr(key):tr('jobs.unknown');}
  function historyLabel(job){var key='console.historyResult.'+(job.history_state||'unknown');return tr(global.IOSBKI18n.has(key)?key:'console.historyResult.unknown');}
  function formatEvent(event){
    var line=date(event.at)+'  '+label('event',event.code);
    if(event.phase)line+=' · '+label('phase',event.phase);
    if(typeof event.percent==='number')line+=' · '+event.percent.toLocaleString(global.IOSBKI18n.getLocale(),{maximumFractionDigits:1})+'%';
    if(typeof event.bytes==='number')line+=' · '+event.bytes.toLocaleString(global.IOSBKI18n.getLocale())+' B';
    if(event.error_code){var key='api.'+event.error_code;line+=' · '+(global.IOSBKI18n.has(key)?tr(key):tr('jobs.unknown'));}
    return line;
  }
  function notice(message,error){if(typeof global.toast==='function')global.toast(message,!!error);}
  async function request(url,signal,body){
    var options={signal:signal};
    if(body){var meta=document.querySelector('meta[name="iosbk-csrf"]');options.method='POST';options.headers={'Content-Type':'application/json','X-IOSBK-CSRF':meta?meta.content:''};options.body=JSON.stringify(body);}
    var response=await global.IOSBKI18n.fetch(url,options),payload=await response.json();
    if(!response.ok){var error=new Error('request failed');error.payload=payload;error.status=response.status;throw error;}
    return payload;
  }
  function render(view){
    currentView=view;if(!view.udid)return;
    var u=view.udid,list=document.getElementById('jobList-'+u);if(!list)return;
    var filter=filters[u]||{type:'all',result:'all'},jobs=global.IOSBKConsole.filterJobs(view.jobs,filter.type,filter.result);
    var signature=JSON.stringify([jobs,filter,global.IOSBKI18n.getLocale()]);
    if(list.dataset.signature!==signature){
      list.dataset.signature=signature;list.replaceChildren();
      jobs.forEach(function(job){
        var row=document.createElement('button');row.type='button';row.className='console-task-row';row.dataset.jobAction='select';row.dataset.udid=u;row.dataset.job=job.id;
        var when=document.createElement('span'),strong=document.createElement('strong'),small=document.createElement('small');strong.textContent=date(job.started_at);small.textContent=job.operation==='check'?tr('check.title'):label('source',job.source)+' · '+label('mode',job.requested_mode);when.append(strong,small);row.append(when);
        var outcome=document.createElement('span');strong=document.createElement('strong');strong.textContent=label('state',job.state);strong.className=global.IOSBKConsole.needsAttention(job)?'console-amber':'console-good';outcome.append(strong);
        small=document.createElement('small');small.textContent=job.operation==='check'?(job.check?tr('check.state.'+job.check.state):tr('check.none')):tr('jobs.outcomes',{execution:label('state',job.execution_state||job.state),history:historyLabel(job)});outcome.append(small);row.append(outcome);
        var duration=document.createElement('span'),start=Date.parse(job.started_at),end=Date.parse(job.completed_at);duration.textContent=Number.isFinite(start)&&Number.isFinite(end)&&end>=start?tr('console.duration',{seconds:Math.round((end-start)/1000)}):tr('console.inProgress');duration.className='console-duration';row.append(duration);
        var details=document.createElement('span');details.textContent=tr('console.details')+' →';details.className='console-task-link';row.append(details);list.append(row);
      });
    }
    show('jobEmpty-'+u,!jobs.length);show('jobDetails-'+u,!!document.getElementById('jobDetails-'+u).closest('dialog[open]'));
    var warnings=[];
    if(view.missing)warnings.push(tr('console.jobMissing'));
    if(view.status!=='available')warnings.push(tr('jobs.recordsUnavailable'));
    if(view.job&&view.job.persistence==='error')warnings.push(tr('jobs.saveError'));
    if(view.job&&view.job.persistence==='pending')warnings.push(tr('jobs.saving'));
    if(view.truncated)warnings.push(tr('jobs.truncated',{count:view.job&&view.job.dropped||0}));
    text('jobWarning-'+u,warnings.join(' '));show('jobWarning-'+u,!!warnings.length);text('jobDetailWarning-'+u,warnings.join(' '));show('jobDetailWarning-'+u,!!warnings.length);
    if(!view.job){var exportLink=document.getElementById('jobExport-'+u);if(exportLink)exportLink.removeAttribute('href');text('jobResult-'+u,tr(view.missing?'console.jobMissing':'history.loading'));['jobDevice-','jobMode-','jobSource-','jobActivity-','jobCheck-','jobEvents-','jobOutcomes-'].forEach(function(prefix){text(prefix+u,'');});var related=document.getElementById('jobPoint-'+u);if(related)related.replaceChildren();return;}
    var job=view.job;
    text('jobDevice-'+u,job.device_name||'');
    text('jobResult-'+u,job.operation==='check'?tr('check.title'):label('state',job.state));
    var outcomes=document.getElementById('jobOutcomes-'+u);if(outcomes){outcomes.replaceChildren();var values=job.operation==='check'?[['console.checkOutcome',job.check?tr('check.state.'+job.check.state):tr('console.checkNotRun')]]:[['console.execution',label('state',job.execution_state||job.state)],['console.checkOutcome',job.check?tr('check.state.'+job.check.state):tr('console.checkNotRun')],['console.historyOutcome',historyLabel(job)]];values.forEach(function(pair){var cell=document.createElement('div'),name=document.createElement('small'),value=document.createElement('strong');name.textContent=tr(pair[0]);value.textContent=pair[1];cell.append(name,value);outcomes.append(cell);});}

    text('jobCheck-'+u,(job.history_committed&&['failed','uncertain'].indexOf(job.history_state)>=0?tr('console.savedWithIssue',{phase:tr('console.historyPhase.'+(job.history_phase||'unknown'))})+'\n':'')+(global.IOSBKChecks&&job.check?global.IOSBKChecks.summary(job.check,{detailsOnly:true}):''));
    text('jobMode-'+u,job.operation==='check'?'':tr('jobs.modeSummary',{requested:label('mode',job.requested_mode),observed:label('mode',job.observed_mode)}));
    text('jobSource-'+u,tr('jobs.sourceSummary',{source:label('source',job.source),time:date(job.started_at)}));
    text('jobActivity-'+u,tr('jobs.lastActivity',{time:date(job.last_activity_at)}));
    text('jobEvents-'+u,view.events.map(formatEvent).join('\n')||tr('jobs.noEvents'));
    var related=document.getElementById('jobPoint-'+u);if(related){related.replaceChildren();var node=document.createElement(job.point_id?'button':'p');node.textContent=tr(job.point_id?'console.openLinkedBackup':'console.noLinkedBackup');node.className=job.point_id?'console-navigation-row':'history-note';if(job.point_id){node.type='button';node.dataset.consoleAction='point';node.dataset.udid=u;node.dataset.point=job.point_id;}related.append(node);}
    var link=document.getElementById('jobExport-'+u);if(link&&jobID.test(job.id))link.href='/api/backup-job/'+encodeURIComponent(job.id)+'/export';
  }
  var controller=createController({get:request,render:render,error:function(error){var message=global.IOSBKI18n.message(error.payload,'common.operationFailed');text('jobWarning-'+visible,message);show('jobWarning-'+visible,true);text('jobDetailWarning-'+visible,message);show('jobDetailWarning-'+visible,true);},schedule:function(fn,delay){return global.setTimeout(fn,delay);},cancel:function(id){global.clearTimeout(id);}});
  function open(udid){
    if(global.IOSBKHistory)global.IOSBKHistory.hide();
    if(visible&&visible!==udid)close(visible);
    visible=udid;show('jobsPanel-'+udid,true);return controller.open(udid);
  }
  function close(udid,returnFocus){if(visible!==udid)return;controller.close();show('jobsPanel-'+udid,false);visible='';}
  async function copy(){
    var job=currentView&&currentView.job;if(!job||!jobID.test(job.id))return;
    try{
      var response=await global.IOSBKI18n.fetch('/api/backup-job/'+encodeURIComponent(job.id)+'/export');
      if(!response.ok)throw Error('export failed');
      var raw=await response.text();
      var lines=raw.trim().split('\n').map(function(line){return JSON.parse(line);});
      var summary=lines.shift().summary;
      var heading=[summary.id+' · '+label('state',summary.state),tr('jobs.retention')];
      if(summary.check&&global.IOSBKChecks)heading.push(global.IOSBKChecks.summary(summary.check));
      if(summary.truncated)heading.push(tr('jobs.truncated',{count:summary.dropped||0}));
      if(summary.persistence!=='saved')heading.push(tr(summary.persistence==='error'?'jobs.saveError':'jobs.saving'));
      var content=heading.concat(lines.map(formatEvent)).join('\n');
      if(global.navigator.clipboard&&global.navigator.clipboard.writeText){await global.navigator.clipboard.writeText(content);}else{var field=document.createElement('textarea'),previous=document.activeElement;field.value=content;field.setAttribute('readonly','');field.style.cssText='position:fixed;left:0;top:0;opacity:0;width:1px;height:1px';var dialogs=document.querySelectorAll('dialog[open]');(dialogs.length?dialogs[dialogs.length-1]:document.body).appendChild(field);field.select();var copied=document.execCommand('copy');field.remove();if(previous&&previous.isConnected)previous.focus();if(!copied)throw Error('copy unavailable');}notice(tr('jobs.copied'));
    }catch(_){notice(tr('jobs.copyFailed'),true);}
  }
  document.addEventListener('click',function(event){
    var button=event.target.closest('[data-job-action]');if(!button)return;
    var action=button.dataset.jobAction,u=button.dataset.udid;
    if(action==='open')global.switchSection(u,'jobs');else if(action==='close')close(u);else if(action==='copy')copy();else if(action==='select'){controller.select(button.dataset.job);global.IOSBKConsole.taskDrawer(u);}
  });
  document.addEventListener('change',function(event){var f=event.target.dataset.jobFilter,u=event.target.dataset.udid;if(!f)return;filters[u]=filters[u]||{type:'all',result:'all'};filters[u][f]=event.target.value;controller.repaint();});
  global.addEventListener('iosbk:localechange',function(){controller.repaint();});
  var lastRevision;
  global.IOSBKJobs={select:function(id){return controller.select(id);},mergeEvents:mergeEvents,createController:createController,formatEvent:formatEvent,open:open,close:close,hide:function(){if(visible)close(visible,false);},onStatus:function(snapshot){if(snapshot.backup_jobs_revision!==lastRevision){lastRevision=snapshot.backup_jobs_revision;controller.invalidate();}}};
})(window);

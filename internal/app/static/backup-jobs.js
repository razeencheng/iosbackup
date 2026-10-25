(function(global){
  'use strict';
  var document=global.document, deviceStates={}, visible='', currentView=null;
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
        if(view.selected&&!view.jobs.some(function(job){return job.id===view.selected;})){view.selected='';view.job=null;view.events=[];view.cursor=0;view.truncated=false;}
        if(!view.selected&&view.jobs.length){view.selected=view.jobs[0].id;view.cursor=Math.max(0,view.jobs[0].last_cursor-1000);}
        publish();
        if(view.selected&&jobID.test(view.selected)){
          var result=await options.get('/api/backup-job/'+encodeURIComponent(view.selected)+'/events?after='+view.cursor+'&limit=500',abort.signal);
          if(epoch!==generation)return;
          view.job=result.job;view.events=mergeEvents(view.events,result.events||[],1000);
          view.cursor=Math.max(view.cursor,result.next_cursor||0);view.truncated=!!result.truncated;
          publish();if(result.has_more)dirty=true;
        }
      }catch(error){if(epoch===generation&&error.name!=='AbortError'){if(error.status===410){view.selected='';view.job=null;view.events=[];view.cursor=0;view.truncated=false;publish();}options.error(error);}}
      finally{if(epoch===generation){busy=false;schedule(dirty?100:5000);}}
    }
    function reset(){generation++;if(abort)abort.abort();if(timer!==null)options.cancel(timer);timer=null;busy=false;}
    return {
      open:function(udid){reset();view={udid:udid,selected:'',jobs:[],events:[],cursor:0,job:null,status:'available',truncated:false};publish();return poll();},
      select:function(id){if(!jobID.test(id))return;reset();view.selected=id;view.events=[];view.job=null;var job=view.jobs.find(function(j){return j.id===id;});view.cursor=Math.max(0,(job&&job.last_cursor||0)-1000);publish();return poll();},
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
    var u=view.udid,select=document.getElementById('jobSelect-'+u);if(!select)return;
    var existing=new Map(Array.from(select.options).map(function(option){return [option.value,option];}));
    view.jobs.forEach(function(job){var option=existing.get(job.id);if(!option){option=document.createElement('option');option.value=job.id;select.appendChild(option);}option.textContent=date(job.started_at)+' · '+(job.operation==='check'?tr('check.title'):label('state',job.state)+' · '+label('mode',job.requested_mode));existing.delete(job.id);});
    existing.forEach(function(option){option.remove();});select.value=view.selected;
    show('jobEmpty-'+u,!view.jobs.length);show('jobDetails-'+u,!!view.job);select.disabled=!view.jobs.length;
    var warnings=[];
    if(view.status!=='available')warnings.push(tr('jobs.recordsUnavailable'));
    if(view.job&&view.job.persistence==='error')warnings.push(tr('jobs.saveError'));
    if(view.job&&view.job.persistence==='pending')warnings.push(tr('jobs.saving'));
    if(view.truncated)warnings.push(tr('jobs.truncated',{count:view.job&&view.job.dropped||0}));
    text('jobWarning-'+u,warnings.join(' '));show('jobWarning-'+u,!!warnings.length);
    if(!view.job)return;
    var job=view.job;
    text('jobResult-'+u,job.operation==='check'?tr('check.title'):label('state',job.state));
 text('jobCheck-'+u,(job.operation==='check'?'':tr('jobs.outcomes',{execution:label('state',job.execution_state||job.state),history:label('state',job.history_state||'not_enabled')})+'\n')+(global.IOSBKChecks&&job.check?global.IOSBKChecks.summary(job.check):''));
    text('jobMode-'+u,job.operation==='check'?'':tr('jobs.modeSummary',{requested:label('mode',job.requested_mode),observed:label('mode',job.observed_mode)}));
    text('jobSource-'+u,tr('jobs.sourceSummary',{source:label('source',job.source),time:date(job.started_at)}));
    text('jobActivity-'+u,tr('jobs.lastActivity',{time:date(job.last_activity_at)}));
    text('jobEvents-'+u,view.events.map(formatEvent).join('\n')||tr('jobs.noEvents'));
    var link=document.getElementById('jobExport-'+u);if(link&&jobID.test(job.id))link.href='/api/backup-job/'+encodeURIComponent(job.id)+'/export';
  }
  var controller=createController({get:request,render:render,error:function(error){var message=global.IOSBKI18n.message(error.payload,'common.operationFailed');text('jobWarning-'+visible,message);show('jobWarning-'+visible,true);},schedule:function(fn,delay){return global.setTimeout(fn,delay);},cancel:function(id){global.clearTimeout(id);}});
  function open(udid){
    if(visible&&visible!==udid)close(visible);
    visible=udid;show('backupOverview-'+udid,false);show('jobsPanel-'+udid,true);controller.open(udid);
    var select=document.getElementById('jobSelect-'+udid);if(select)select.focus();
  }
  function close(udid,returnFocus){if(visible!==udid)return;controller.close();show('backupOverview-'+udid,true);show('jobsPanel-'+udid,false);visible='';var button=document.getElementById('openJobs-'+udid);if(button&&returnFocus!==false)button.focus();}
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
      await global.navigator.clipboard.writeText(content);notice(tr('jobs.copied'));
    }catch(_){notice(tr('jobs.copyFailed'),true);}
  }
  document.addEventListener('click',function(event){
    var button=event.target.closest('[data-job-action]');if(!button)return;
    var action=button.dataset.jobAction,u=button.dataset.udid;
    if(action==='open')open(u);else if(action==='close')close(u);else if(action==='copy')copy();
  });
  document.addEventListener('change',function(event){if(event.target.hasAttribute('data-job-select'))controller.select(event.target.value);});
  global.addEventListener('iosbk:localechange',function(){controller.repaint();});
  var lastRevision;
  global.IOSBKJobs={mergeEvents:mergeEvents,createController:createController,formatEvent:formatEvent,open:open,close:close,hide:function(){if(visible)close(visible,false);},onStatus:function(snapshot){if(snapshot.backup_jobs_revision!==lastRevision){lastRevision=snapshot.backup_jobs_revision;controller.invalidate();}}};
})(window);

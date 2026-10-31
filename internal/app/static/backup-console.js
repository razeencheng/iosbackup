(function (global) {
  'use strict';
  var doc = global.document;
  var groups = {
    schedule: ['start_time', 'end_time', 'backup_interval', 'min_battery_level', 'only_when_charging', 'auto_backup_enabled'],
    connection: ['backup_directory', 'network_address']
  };
  var fields = {start_time:'startTime',end_time:'endTime',backup_interval:'backupInterval',min_battery_level:'minBattery',only_when_charging:'chargingToggle',auto_backup_enabled:'autoBackupToggle',backup_directory:'backupDir',network_address:'networkAddress'};
  var saved = {}, history = {}, layers = [], sequence = 0, recent = {};
  function isBackupRunning(device) { return !!device.backing_up && (!device.active_operation || device.active_operation === 'backup'); }
  function busyLabel(device) { return device.active_operation === 'check' ? 'console.checkingBackup' : (device.active_operation === 'local' ? 'console.deviceBusy' : 'device.backingUp'); }
  function configPayload(group, config) {
    if (!groups[group]) throw new Error('Unknown settings group');
    var result = {};
    groups[group].forEach(function (key) { result[key] = config[key]; });
    return result;
  }
  function needsAttention(job) {
    return ['failed','interrupted'].indexOf(job.state) >= 0 || ['failed','uncertain','pending'].indexOf(job.history_state) >= 0 || (job.check && ['issues','unavailable','cancelled'].indexOf(job.check.state) >= 0);
  }
  function filterJobs(jobs, type, result) {
    return jobs.filter(function (job) {
      return (type === 'all' || (job.operation || 'backup') === type) &&
        (result === 'all' || (result === 'attention' ? needsAttention(job) : (result === 'running' ? ['starting','running','stopping'].indexOf(job.state) >= 0 : job.state === result)));
    });
  }
  function isCurrentPoint(data, point) {
    var source = data && data.current_source;
    return !!(source && source.kind === 'point' && source.available && source.point_id === point.id);
  }
  function tr(key, params) { return global.IOSBKI18n.t(key, params); }
  function el(tag, text, cls) { var n = doc.createElement(tag); if (text !== undefined) n.textContent = text; if (cls) n.className = cls; return n; }
  function button(text, fn, cls) { var b = el('button', text, cls || 'history-button'); b.type = 'button'; b.onclick = fn; return b; }
  function get(id) { return doc.getElementById(id); }
  function text(id, value) { var n = get(id); if (n) n.textContent = value; }
  function formatDate(value) { return value && !String(value).startsWith('0001-') ? global.IOSBKI18n.formatDate(value) : tr('history.unknown'); }
  function bytes(value) { return typeof value === 'number' ? global.formatBackupBytes(value) : tr('history.sizeUnknown'); }
  function closeLayers() { layers.slice().reverse().forEach(function (layer) { layer.close(); }); }
  // 使用原生 dialog 保持键盘焦点；移动现有面板而非复制，避免重复 ID 和状态分叉。
  function layer(content, title, options) {
    options = options || {};
    var prior = doc.activeElement, parent = content.parentNode, next = content.nextSibling;
    var dialog = el('dialog', undefined, options.drawer ? 'console-drawer' : 'console-dialog');
    var header = el('header', undefined, 'console-layer-head'), heading = el('h2', title);
    heading.id = 'console-layer-' + (++sequence); dialog.setAttribute('aria-labelledby', heading.id);
    var entry = {dialog: dialog, close: finish, busy: false};
    function finish() {
      if (entry.busy || (options.canClose && !options.canClose())) return;
      dialog.close();
      if (parent) { parent.insertBefore(content, next && next.parentNode === parent ? next : null); content.classList.add('hidden'); }
      dialog.remove(); layers = layers.filter(function (item) { return item !== entry; });
      if (options.close) options.close();
      if (prior && prior.isConnected && prior.getClientRects().length) prior.focus();
    }
    entry.closeButton=button(tr('console.close'),finish,'console-close');
    header.append(heading,entry.closeButton);
    dialog.append(header, content); content.classList.remove('hidden');
    dialog.addEventListener('cancel', function (event) { event.preventDefault(); finish(); });
    doc.body.append(dialog); layers.push(entry); dialog.showModal();
    header.querySelector('button').focus();
    return entry;
  }
  function readConfig(u) {
    var config = {};
    Object.keys(fields).forEach(function (key) {
      var n = get(fields[key] + '-' + u);
      config[key] = /Toggle$/.test(fields[key]) ? n.classList.contains('on') : n.value;
      if (key === 'backup_interval' || key === 'min_battery_level') config[key] = Number(config[key]);
    });
    config.network_address = config.network_address.trim();
    return config;
  }
  function restoreConfig(u, group) {
    groups[group].forEach(function (key) {
      var n = get(fields[key] + '-' + u), value = saved[u][key];
      if (/Toggle$/.test(fields[key])) { n.classList.toggle('on', value); n.setAttribute('aria-checked', String(value)); }
      else n.value = value;
    });
    text('minBatLabel-' + u, saved[u].min_battery_level + '%');
    get('autoNote-' + u).classList.toggle('hidden', !saved[u].auto_backup_enabled);
    if(group==='connection')global.clearConnectionResults(u);
    changed(get('setting-'+group+'-'+u));
  }
  function changed(target) {
    var content=target&&target.closest?target.closest('[data-settings-group]'):null;
    if(!content)return;
    var u=content.dataset.udid,group=content.dataset.settingsGroup,hint=content.querySelector('[data-group-dirty]');
    if(hint)hint.classList.toggle('hidden',JSON.stringify(configPayload(group,readConfig(u)))===JSON.stringify(configPayload(group,saved[u])));
  }
  function settingsSummary(u) {
    var config = saved[u]; if (!config) return;
    text('scheduleSummary-' + u, tr(config.auto_backup_enabled ? 'console.scheduleOn' : 'console.scheduleOff', {start:config.start_time,end:config.end_time,hours:config.backup_interval}));
    text('connectionSummary-' + u, (config.network_address || tr('console.autoAddress')) + ' · ' + config.backup_directory);
  }
  function settings(u, group) {
    var content = get('setting-' + group + '-' + u); if (!content) return;
    var entry = layer(content, tr('console.setting.' + group), {canClose: function () { return group !== 'history' || !global.IOSBKHistory.isPending(); },close: function () {
      if (groups[group]) restoreConfig(u, group);
      if (group === 'history') global.IOSBKHistory.discard(u);
      content.querySelectorAll('input[type="password"]').forEach(function (input) { input.value = ''; });
    }});
    if (group === 'encryption') global.loadEncStatus(u);
    if (groups[group]) {
      var save = content.querySelector('[data-group-save]'), cancel = content.querySelector('[data-group-cancel]');
      cancel.onclick = entry.close;
      save.onclick = async function () {
        if (entry.busy) return;
        var payload = configPayload(group, readConfig(u));
        entry.busy = true; save.disabled = cancel.disabled = entry.closeButton.disabled = true;
        var controls=Array.from(content.querySelectorAll('input,select,textarea,.tgl'));
        var disabled=controls.map(function(control){var before=control.disabled;control.disabled=true;return before;});
        var errorNode = content.querySelector('[data-group-error]'); errorNode.textContent = '';
        try {
          var response = await global.csrfFetch('/api/save-config/' + encodeURIComponent(u), {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});
          var result = await response.json();
          if (!response.ok || result.error) throw new Error(global.IOSBKI18n.message(result, 'common.operationFailed'));
          Object.assign(saved[u], payload); settingsSummary(u);
          entry.busy = false; entry.close(); global.toast('bset.saved');
          if (global.IOSBKHistory) global.IOSBKHistory.refresh();
        } catch (error) { errorNode.textContent = error.message || tr('common.operationFailed'); }
        finally { entry.busy = false; save.disabled = cancel.disabled = entry.closeButton.disabled = false;controls.forEach(function(control,index){control.disabled=disabled[index];}); }
      };
    }
  }
  function pointDrawer(u) {
    var content = get('historyDetail-' + u); if (!content || content.closest('dialog.console-drawer')) return;
    layer(content, tr('console.pointDetails'), {drawer:true,close:function(){global.IOSBKHistory.back();}});
  }
  function closePoint(u) {
    var content=get('historyDetail-'+u);
    if(content)layers.slice().forEach(function(entry){if(entry.dialog.contains(content))entry.close();});
  }
  function taskDrawer(u) {
    var content = get('jobDetails-' + u); if (!content || content.closest('dialog')) return;
    layer(content, tr('console.taskDetails'), {drawer:true,close:function(){if(!get('panel-'+u))global.IOSBKJobs.hide();}});
  }
  function ensureArchivedJobs(u) {
    if(get('jobsPanel-'+u))return;
    var template=get('archivedJobTemplate'),panel=template.content.firstElementChild.cloneNode(true);
    panel.id='jobsPanel-'+u;
    panel.querySelectorAll('[id]').forEach(function(node){node.id=node.id.replace(/-archived$/,'-'+u);});
    panel.querySelectorAll('[data-udid]').forEach(function(node){node.dataset.udid=u;});
    doc.body.appendChild(panel);
    global.IOSBKI18n.apply(panel);
  }
  function openJob(u, id) {
    if (!/^bkp-[0-9a-f]{12,32}$/.test(id || '')) { global.toast('console.jobMissing',true); return; }
    closeLayers();
    if(get('panel-'+u))global.switchSection(u,'jobs');
    else{ensureArchivedJobs(u);global.IOSBKJobs.open(u);}
    global.IOSBKJobs.select(id); taskDrawer(u);
  }
  function openPoint(u, id) {
    closeLayers();
    if(get('panel-'+u))global.switchSection(u,'backups');
    else{global.IOSBKJobs.hide();global.IOSBKHistory.openArchived(u);}
    global.IOSBKHistory.navigate(u,id);
  }
  function current(u) {
    var source = history[u] && history[u].current_source;
    if (!source || !source.available) { global.toast('console.sourceUnavailable', true); return; }
    closeLayers();
    var content = get('backupOverview-' + u), check = get('currentCheck-' + u);
    if(check)check.classList.toggle('hidden',source.kind==='point');
    layer(content, tr('console.currentDetails'), {drawer:true,close:function(){global.closeBackupFiles(u);}});
    global.openBackupFiles(u);
  }
  function storage(u) {
    var data = history[u], content = el('div', undefined, 'console-layer-body'); if (!data) return;
    var s = data.storage || {};
    content.append(el('p', tr('history.storage', {size:bytes(data.logical_bytes)})), el('p', tr('console.freeSpace', {size:bytes(s.available_bytes)})), el('p', tr('console.copy.' + (s.copy_strategy || 'unknown'))), el('p', tr('console.storageHint'), 'history-warning'), el('p', tr('console.checkedAt',{time:formatDate(s.checked_at)}), 'history-note'));
    if (!data.bytes_known || (data.unavailable_stores || []).length) content.append(el('p', tr('history.sizePartial'), 'history-warning'));
    if (s.state !== 'available') content.append(el('p',tr('console.storageUnavailable'),'history-warning'));
    layer(content, tr('console.storage'));
  }
  function updateHistory(view) {
    var u = view.udid, data = view.data; if (!data) return;
    history[u] = data;
    recentJob(u);
    var source = data.current_source || {}, currentPoint = view.points.find(function (p) { return isCurrentPoint(data,p); });
    text('consoleLatest-' + u, currentPoint ? (currentPoint.completion_unknown ? tr('history.timeUnknown') : formatDate(currentPoint.completed_at)) : tr(source.kind === 'current' ? 'console.currentSet' : 'history.unknown'));
    text('consoleCount-' + u, String(data.total));
    text('consoleSize-' + u, bytes(data.logical_bytes) + (data.bytes_known ? '' : ' + ?'));
    text('consoleFree-' + u, tr('console.freeSpace',{size:bytes((data.storage || {}).available_bytes)}));
    text('historyPolicySummary-' + u, tr(data.policy.enabled ? 'history.summary' : 'history.summaryOff',{days:data.policy.days,count:data.total}));
    var currentCard = get('currentBackup-' + u);
    if (currentCard) {
      currentCard.classList.toggle('hidden', source.kind === 'point' && source.available);
      text('currentBackupState-' + u, tr(source.kind === 'current' && source.available ? 'console.mutableHint' : (source.kind === 'missing' ? 'console.noCurrent' : 'console.sourceUnavailable')));
      currentCard.querySelector('button').disabled = !source.available;
    }
  }
  async function recentJob(u) {
    var state = recent[u] || (recent[u] = {});
    if (state.pending || (state.at && Date.now() - state.at < 10000)) return;
    state.pending = true;
    try {
      var response = await global.IOSBKI18n.fetch('/api/backup-jobs/' + encodeURIComponent(u));
      if (!response.ok) return;
      var result = await response.json();
      state.job = (result.jobs || []).find(function (j) { return j.operation !== 'check'; });
      renderRecent(u);
      state.at = Date.now();
    } catch (_) { /* 任务接口失败由任务页提示，不覆盖已读取的备份。 */ }
    finally { state.pending = false; }
  }
  function renderRecent(u) {
    var host=get('consoleRecent-'+u),job=recent[u]&&recent[u].job;if(!host)return;
    host.replaceChildren();host.classList.toggle('hidden',!job||!needsAttention(job));
    if(job&&needsAttention(job)){
      var stateText=job.error_code&&global.IOSBKI18n.has('api.'+job.error_code)?tr('api.'+job.error_code):tr('console.attention');
      host.append(el('span',tr('console.recentIssue',{state:stateText})),button(tr('console.viewJob'),function(){openJob(u,job.id);},'console-text-button history-button'));
    }
  }
  function backup(u) {
    var content = el('div', undefined, 'console-layer-body');
    content.append(el('p', tr('console.backupHint'), 'history-note'));
    var label = el('label',tr('jobs.requestedMode')), select = el('select',undefined,'iosbk-in');
    ['auto','full'].forEach(function (mode) { var option = el('option',tr('jobs.mode.'+mode));option.value=mode;select.append(option); });
    select.setAttribute('aria-label',tr('jobs.requestedMode')); label.append(select); content.append(label,el('p',tr('jobs.fullHint'),'history-warning'));
    var actions = el('div',undefined,'history-actions'), entry;
    actions.append(button(tr('history.cancel'),function(){entry.close();}),button(tr('device.backupNow'),function(){var mode=select.value;entry.close();global.submitBackup(u,get('backupBtn-'+u),mode);},'history-button primary'));
    content.append(actions); entry=layer(content,tr('device.backupNow'));
  }
  function init() {
    doc.querySelectorAll('[data-udid-card]').forEach(function (card) {
      var u = card.dataset.udidCard; saved[u] = readConfig(u); settingsSummary(u);
      card.querySelectorAll('.tgl').forEach(function (toggle) { toggle.setAttribute('role','switch'); toggle.setAttribute('aria-checked',String(toggle.classList.contains('on'))); });
      var head=card.querySelector('[data-card-toggle]'); if(head)head.setAttribute('aria-expanded','false');
    });
    var first = doc.querySelector('[data-udid-card]'); if (first) global.toggleCard(first.dataset.udidCard);
  }
  doc.addEventListener('click',function(event){
    if(event.target.closest('.tgl'))changed(event.target);
    var b=event.target.closest('[data-console-action]');if(!b)return;
    var u=b.dataset.udid,action=b.dataset.consoleAction;
    if(action==='settings')settings(u,b.dataset.group);
    if(action==='current')current(u);
    if(action==='storage')storage(u);
    if(action==='job')openJob(u,b.dataset.job);
    if(action==='point')openPoint(u,b.dataset.point);
    if(action==='deployment'){
      var body=el('div',undefined,'console-layer-body');
      body.append(el('p',tr('header.tz',{timezone:doc.documentElement.dataset.timezone})),el('p',tr('console.deploymentHint'),'history-note'));
      global.closeMenu();layer(body,tr('console.deployment'));
    }
  });
  doc.addEventListener('input',function(event){
    changed(event.target);
    var content=event.target.closest&&event.target.closest('[data-settings-group="connection"]');
    if(content)global.clearConnectionResults(content.dataset.udid);
  });
  doc.addEventListener('change',function(event){changed(event.target);});
  global.addEventListener('iosbk:localechange',function(){Object.keys(saved).forEach(settingsSummary);Object.keys(recent).forEach(renderRecent);});
  global.addEventListener('beforeunload',function(event){
    var dirty=Object.keys(saved).some(function(u){return Object.keys(groups).some(function(group){var content=get('setting-'+group+'-'+u);return content&&content.closest('dialog[open]')&&JSON.stringify(configPayload(group,readConfig(u)))!==JSON.stringify(configPayload(group,saved[u]));});});
    if(dirty){event.preventDefault();event.returnValue='';}
  });
  global.IOSBKConsole={isBackupRunning:isBackupRunning,busyLabel:busyLabel,configPayload:configPayload,filterJobs:filterJobs,needsAttention:needsAttention,isCurrentPoint:isCurrentPoint,init:init,settings:settings,backup:backup,pointDrawer:pointDrawer,closePoint:closePoint,taskDrawer:taskDrawer,openJob:openJob,openPoint:openPoint,current:current,updateHistory:updateHistory,closeLayers:closeLayers,layer:layer};
})(window);

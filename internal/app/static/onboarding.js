(function () {
  'use strict';

  var body = document.body;
  var csrf = body.dataset.csrf || '';

  var active = Number(IOSBKI18n.readStorage('iosbk_guide_step') || 0);
  var wifiConfirmed = IOSBKI18n.readStorage('iosbk_guide_wifi') === 'done';
  var selectedUDID = IOSBKI18n.readStorage('iosbk_guide_udid') || '';
  var platform = 'mac';
  var snapshot = { devices: [], version: body.dataset.version || '' };
  var eventSource = null;
  var reconnectTimer = null;
  var toastState = null, toastSequence = 0;


  function text(id, value) { var node = document.getElementById(id); if (node) node.textContent = value; }
  function t() { return IOSBKI18n.tree('guide'); }
  function tr(key,params){return IOSBKI18n.t(key,params);}
  function renderConnectionCopy() {
    var badge = document.getElementById('connectionBadge');
    var label = t().connecting;
    if (badge.classList.contains('online')) label = t().online;
    if (badge.classList.contains('offline')) label = t().offline;
    badge.querySelector('span').textContent = label;
    text('liveText', label);
  }
  function renderToast() {
    if (!toastState) return;
    if (Date.now() >= toastState.expiresAt) { document.getElementById('toast').hidden = true; toastState = null; return; }
    text('toastText', typeof toastState.payload === 'string' ? tr(toastState.payload) : IOSBKI18n.message(toastState.payload, 'common.operationFailed'));
  }
  function showToast(payload) {
    var toast = document.getElementById('toast'), sequence = ++toastSequence;
    toastState = {payload: payload, expiresAt: Date.now() + 2800, sequence: sequence};
    renderToast(); toast.hidden = false; window.clearTimeout(showToast.timer);
    showToast.timer = window.setTimeout(function () { if (toastState && toastState.sequence === sequence) { toast.hidden = true; toastState = null; } }, 2800);
  }
  function post(url) {
    return IOSBKI18n.fetch(url, { method: 'POST', headers: { 'X-IOSBK-CSRF': csrf } }).then(function (response) {
      return response.json().catch(function () { return {}; }).then(function (data) {
        if (!response.ok) { var error=new Error('request_failed'); error.payload=data; throw error; }
        return data;
      });
    });
  }

  function connectionProblem() {
    var service=(snapshot.connection_services||[]).find(function(item){return item.connection==='usb';});
    if(!service) return '';
    if(service.recovery==='restarting') return tr('guide.connectionProblem.restarting');
    if(service.recovery==='waiting_busy') return tr('guide.connectionProblem.waitingBusy');
    if(service.recovery==='manual_required') return tr('guide.connectionProblem.manualRequired');
    if(service.recovery==='backoff') return tr('guide.connectionProblem.backoff');
    if(service.health==='unknown') return tr('guide.connectionProblem.checking');
    if(service.health!=='healthy') return tr('guide.connectionProblem.unavailable');
    return '';
  }
  function selectedDevice() { return snapshot.devices.find(function (device) { return device.udid === selectedUDID; }) || null; }
  function usbDevices() { return snapshot.devices.filter(function (device) { return device.online && device.conn === 'usb'; }); }
  function completedCount() {
    if (!wifiConfirmed) return 0;
    var count = 1;
    var device = selectedDevice();
    if (device && device.conn === 'usb' && device.online) count = Math.max(count, 2);
    if (device && device.presence_known!==false && device.pairing_state === 'paired') count = Math.max(count, 3);
    if (device && device.backup_state === 'succeeded' && device.last_backup) count = 4;
    return count;
  }

  function selectDevice(udid) {
    selectedUDID = udid;
    IOSBKI18n.writeStorage('iosbk_guide_udid', udid);
    var device = selectedDevice();
    if (wifiConfirmed && device && active < 2) setActive(device.pairing_state === 'paired' ? 3 : 2);
    render();
  }

  function setActive(step) {
    active = Math.max(0, Math.min(3, step));
    IOSBKI18n.writeStorage('iosbk_guide_step', String(active));
    render();
  }

  function renderInstructions() {
    var instructions = t().wifiInstructions[platform];
    document.getElementById('wifiInstructions').innerHTML = instructions.map(function (instruction, index) {
      return '<div class="instruction-row"><b>' + (index + 1) + '</b><span>' + instruction + '</span></div>';
    }).join('');
  }

  function applyLanguage() {
    var c = t();
    IOSBKI18n.apply();
    text('brandSub', c.brandSub); text('backConsole', c.back); text('tzText', c.tz); text('heroTitle', c.heroTitle); text('heroLead', c.heroLead); text('progressLabel', c.progress); text('railNote', c.rail); text('optionalLabel', c.optional);
    text('s1Title', c.s1Title); text('s1Lead', c.s1Lead); text('s1Note', c.s1Note); text('skipWifi', c.skip); text('completeWifi', c.complete); text('itunesLabel', c.itunes);
    text('s2Title', c.s2Title); text('s2Lead', c.s2Lead); text('s2Note', c.s2Note); text('refreshDevices', c.refresh); text('backStep1', c.backStep1);
    text('s3Title', c.s3Title); text('s3Lead', c.s3Lead); text('correctTitle', c.correctTitle); text('correctBody', c.correctBody); text('wrongTitle', c.wrongTitle); text('wrongBody', c.wrongBody); text('backStep2', c.backStep2);
    text('s4Title', c.s4Title); text('s4Lead', c.s4Lead); text('backupAuth', c.auth); text('backupSuccessTitle', c.succeeded); text('backupSuccessBody', c.backupSuccessBody); text('backStep3', c.backStep3);
    text('wifiAfterTitle', c.wifiAfterTitle); text('wifiAfterLead', c.wifiAfterLead); text('wifiFlowDiscover', c.wifiFlow[0]); text('wifiFlowCheck', c.wifiFlow[1]); text('wifiFlowStart', c.wifiFlow[2]); text('wifiFlowDone', c.wifiFlow[3]);
    text('conditionVisible', c.conditions[0]); text('conditionNetwork', c.conditions[1]); text('conditionPairing', c.conditions[2]); text('conditionRules', c.conditions[3]); text('footerLocal', c.footerLocal); text('footerBack', c.back);
    c.stepTitles.forEach(function (value, index) { document.querySelector('[data-step-title="' + index + '"]').textContent = value; document.querySelector('[data-step-sub="' + index + '"]').textContent = c.stepSubs[index]; });

    renderConnectionCopy();
    renderInstructions();
  }

  function renderDeviceChoices(devices) {
    var node = document.getElementById('deviceChoices');
    if (devices.length < 2) { node.hidden = true; node.innerHTML = ''; return; }
    node.hidden = false;
    node.replaceChildren();
    devices.forEach(function (device) {
      var button = document.createElement('button');
      button.type = 'button';
      button.dataset.device = device.udid;
      button.classList.toggle('active', device.udid === selectedUDID);
      button.textContent = device.name || device.udid;
      node.appendChild(button);
    });
  }

  function pairingMessage(device) {
    var c = t();
    var state = device ? device.pairing_state : 'unknown';
    var title = c.pairWaiting, detail = c.pairWaitingDetail, badge = c.waitAction;
    if (state === 'checking') { title = c.pairChecking; badge = c.checking; }
    if (state === 'paired') { title = c.pairPaired; detail = device.name || ''; badge = c.paired; }
    if (state === 'failed') { title = c.pairFailed; detail = IOSBKI18n.message({code:'api.'+(device.pairing_error_code||'')},'guide.pairingFailed'); badge = c.failed; }
    var problem=connectionProblem();
    if(problem){ title=tr('guide.connectionUnavailable');detail=problem;badge=c.failed; }
    document.getElementById('checkPair').disabled=!!problem || !device || device.operations_available===false;
    document.querySelector('.password-grid').hidden=!!problem;
    text('s3Title',problem?title:c.s3Title);text('s3Lead',problem||c.s3Lead);
    text('pairTitle', title); text('pairDetail', detail); text('pairState', badge);
    var error = document.getElementById('pairError');
    error.hidden = !!problem || state !== 'failed';
    error.textContent = detail;
    text('checkPair', state === 'failed' ? c.retryPair : c.checkPair);
  }

  function backupFailureMessage(device) {
    var code = device.last_backup_message_code;
    if (code && IOSBKI18n.has(code)) return IOSBKI18n.message({message_code: code, params: device.last_backup_message_params}, 'guide.failedBackup');
    return IOSBKI18n.message({code: 'api.' + (device.last_backup_error_code || '')}, 'guide.failedBackup');
  }
  function backupMessage(device) {
    var c = t();
    var state = device ? device.backup_state : 'idle';
    var title = c.ready, detail = c.idle;
    if (state === 'starting') { title = c.starting; detail = c.starting; }
    if (state === 'running') { title = c.running; detail = c.running; }
    if (state === 'stopping') { title = tr('control.stopping'); detail = tr('control.stoppingHint'); }
    if (state === 'stopped') { title = tr('control.stopped'); detail = tr('control.stoppedHint'); }
    if (state === 'succeeded' && device && device.last_backup) { title = c.succeeded; detail = IOSBKI18n.formatDate(device.last_backup_at||device.last_backup); }
    if (state === 'failed') { title = c.failedBackup; detail = backupFailureMessage(device); }
    if (state === 'interrupted') { title = c.interrupted; detail = backupFailureMessage(device); }
    text('backupTitle', title); text('backupDetail', detail);
    document.getElementById('backupMotion').hidden = state !== 'starting' && state !== 'running';
    document.getElementById('backupAuth').hidden = state !== 'starting' && state !== 'running';
    document.getElementById('backupSuccess').hidden = !(state === 'succeeded' && device && device.last_backup);
    var error = document.getElementById('backupError');
    error.hidden = state !== 'failed' && state !== 'interrupted';
    error.textContent = detail;
    var button = document.getElementById('startBackup');
    button.disabled = state === 'starting' || state === 'running' || state === 'stopping' || !device || !!device.backing_up || device.presence_known===false || device.operations_available===false || device.pairing_state !== 'paired';
    button.textContent = state === 'succeeded' && device && device.last_backup ? c.finish : ((state === 'failed' || state === 'interrupted') ? c.retryBackup : c.start);
  }

  function render() {
    var c = t();
    var devices = usbDevices();
    if (!selectedUDID || !snapshot.devices.some(function (device) { return device.udid === selectedUDID; })) {
      if (devices.length === 1) selectDevice(devices[0].udid);
    }
    var device = selectedDevice();
    var completed = completedCount();
    if (wifiConfirmed && device && device.online && device.conn === 'usb') {
      if (active === 1) active = device.pairing_state === 'paired' ? 3 : 2;
      if (active === 2 && device.pairing_state === 'paired') active = 3;
      IOSBKI18n.writeStorage('iosbk_guide_step', String(active));
    }
    if (active > completed && !(active === 1 && wifiConfirmed)) active = Math.min(completed, 3);
    document.querySelectorAll('.step-panel').forEach(function (panel) { panel.classList.toggle('active', Number(panel.dataset.panel) === active); });
    document.querySelectorAll('.step-row').forEach(function (row) {
      var index = Number(row.dataset.step);
      row.classList.toggle('active', index === active);
      row.classList.toggle('complete', completed > index);
      row.querySelector('.step-index').innerHTML = completed > index ? '<svg class="ui-icon" aria-hidden="true"><use href="/static/ui-icons.svg#icon-check"></use></svg>' : String(index + 1);
      row.querySelector('em').textContent = completed > index ? c.done : (index === active ? c.current : c.todo);
      row.disabled = index > Math.min(completed, 3);
    });
    document.getElementById('progressFill').style.width = String(completed * 25) + '%';
    text('progressCount', completed >= 4 ? c.done : tr('guide.progressCount',{completed:completed,total:4}));

    renderDeviceChoices(devices);
    if (devices.length) {
      text('usbTitle', c.usbFound);
      text('usbDetail', devices.length > 1 ? c.choose : (devices[0].name || devices[0].udid));
      text('usbState', c.done);
    } else {
      text('usbTitle', c.usbWaiting); text('usbDetail', c.usbWaitingDetail); text('usbState', c.listening);
    }
    var problem=connectionProblem();
    if(problem){ text('usbTitle',tr('guide.connectionUnavailable'));text('usbDetail',problem);text('usbState',c.failed);text('s2Lead',problem); } else {text('s2Lead',c.s2Lead);}
    pairingMessage(device);
    text('deviceName', device ? (device.name || c.deviceFallback) : c.deviceFallback);
    text('deviceMeta', device ? ((device.device_type || 'iOS') + ' · ' + (device.battery || 0) + '%') : c.deviceWaiting);
    text('connectionPill',device?tr('status.'+({usb:'usb',wifi:'wifi',network:'wifi',unknown:'unknown'}[device.conn]||'offline')):'USB');
    backupMessage(device);
  }

  function connectEvents() {
    window.clearTimeout(reconnectTimer);
    if (eventSource) eventSource.close();
    eventSource = new EventSource('/api/events');
    eventSource.onopen = function () {
      var badge = document.getElementById('connectionBadge');
      badge.className = 'connection-badge online'; renderConnectionCopy();
    };
    eventSource.onmessage = function (event) {
      try { snapshot = JSON.parse(event.data); IOSBKI18n.setTimezone(snapshot.timezone); render(); } catch (_) { showToast('guide.invalidStatus'); }
    };
    eventSource.onerror = function () {
      var badge = document.getElementById('connectionBadge');
      badge.className = 'connection-badge offline'; renderConnectionCopy();
      eventSource.close(); reconnectTimer = window.setTimeout(connectEvents, 2500);
    };
  }

  document.querySelectorAll('[data-lang]').forEach(function(button){ button.addEventListener('click',function(){IOSBKI18n.setLocale(button.dataset.lang);}); });
  window.addEventListener('iosbk:localechange',function(){applyLanguage();render();renderToast();});
  document.querySelectorAll('[data-platform]').forEach(function (button) { button.addEventListener('click', function () { platform = button.dataset.platform; document.querySelectorAll('[data-platform]').forEach(function (item) { item.classList.toggle('active', item === button); }); renderInstructions(); }); });
  document.querySelectorAll('[data-back]').forEach(function (button) { button.addEventListener('click', function () { setActive(Number(button.dataset.back)); }); });
  document.querySelectorAll('.step-row').forEach(function (button) { button.addEventListener('click', function () { if (!button.disabled) setActive(Number(button.dataset.step)); }); });
  document.getElementById('deviceChoices').addEventListener('click', function (event) { var button = event.target.closest('[data-device]'); if (button) selectDevice(button.dataset.device); });
  document.getElementById('skipWifi').addEventListener('click', function () { wifiConfirmed = true; IOSBKI18n.writeStorage('iosbk_guide_wifi', 'done'); setActive(1); });
  document.getElementById('completeWifi').addEventListener('click', function () { wifiConfirmed = true; IOSBKI18n.writeStorage('iosbk_guide_wifi', 'done'); setActive(1); });
  document.getElementById('refreshDevices').addEventListener('click', function () { text('refreshDevices', t().refreshing); post('/api/refresh').then(function () { showToast('guide.refreshing'); }).catch(function (error) { showToast(error.payload); }).finally(function () { text('refreshDevices', t().refresh); }); });
  document.getElementById('checkPair').addEventListener('click', function () { if (!selectedUDID) { showToast('guide.selectFirst'); return; } post('/api/pair/' + encodeURIComponent(selectedUDID)).catch(function (error) { showToast(error.payload); }); });
  document.getElementById('startBackup').addEventListener('click', function () {
    var device = selectedDevice();
    if (device && device.backup_state === 'succeeded' && device.last_backup) { document.getElementById('wifiAfter').scrollIntoView({ behavior: 'smooth' }); return; }
    if (!selectedUDID) { showToast('guide.selectFirst'); return; }
    post('/api/backup/' + encodeURIComponent(selectedUDID)).catch(function (error) { showToast(error.payload); });
  });

  applyLanguage();
  render();
  connectEvents();
})();

(function () {
  'use strict';

  var body = document.body;
  var csrf = body.dataset.csrf || '';
  var lang = localStorage.getItem('iosbk_guide_lang') || 'zh';
  var active = Number(localStorage.getItem('iosbk_guide_step') || 0);
  var wifiConfirmed = localStorage.getItem('iosbk_guide_wifi') === 'done';
  var selectedUDID = localStorage.getItem('iosbk_guide_udid') || '';
  var platform = 'mac';
  var snapshot = { devices: [], version: body.dataset.version || '' };
  var eventSource = null;
  var reconnectTimer = null;

  var copy = {
    zh: {
      brandSub: '回到家连上 Wi-Fi，就自动备份你的 iPhone 和 iPad。', back: '返回控制台', connecting: '正在连接服务', online: '实时状态已连接', offline: '实时状态已断开，正在重连', tz: '北京时间 (UTC+8)',
      heroTitle: '让这台 iPhone 开始在家自动备份', heroLead: '第一次只需完成一次 USB 配对。系统会告诉你下一步做什么，并自动识别连接和备份状态。', progress: '首次设置进度', stepUnit: '步', current: '当前', todo: '待进行', done: '已完成', optional: '可选',
      stepTitles: ['准备 Wi-Fi 连接', '连接 iOS Backup', '信任并完成配对', '完成第一次备份'], stepSubs: ['可选，只用 USB 可跳过', '把设备插到 NAS / 主机', '在设备上确认一次', '确认整条链路可用'], rail: '这套设置只需做一次。完成后仍可从菜单重新打开向导。',
      s1Title: '准备以后通过 Wi-Fi 备份', s1Lead: 'Apple 要求先通过 Mac 或 Windows 打开无线连接。只准备使用 USB 备份，可以直接跳过。', s1Note: '这一项无法由 NAS 自动判断，需要你确认。以后想开也可以；不会影响今天先用 USB 备份。', skip: '暂时跳过，只用 USB', complete: '我已完成', itunes: '旧版 iTunes',
      wifiInstructions: {
        mac: ['用数据线把 iPhone / iPad 接到 Mac。', '打开 Finder，在侧边栏选择设备，然后进入“通用”。', '勾选“连接 Wi-Fi 时显示此 iPhone / iPad”，点击“应用”。'],
        windows: ['在 Windows 安装并打开 Apple Devices（Apple 设备）App。', '用数据线连接设备，在侧边栏选择它，然后进入“通用”。', '勾选“连接 Wi-Fi 时显示此 iPhone / iPad”，点击“应用”。'],
        itunes: ['用数据线连接设备，在 iTunes 中选择它。', '进入“摘要”。', '勾选“通过 Wi-Fi 与此设备同步”，点击“应用”。']
      },
      s2Title: '把设备连接到 iOS Backup 主机', s2Lead: '请使用支持数据传输的 USB 线，把已经解锁的 iPhone / iPad 插到运行 iOS Backup 的 NAS 或主机。', usbWaiting: '等待 USB 设备', usbWaitingDetail: '插好后，系统会自动识别；通常不需要手动刷新。', listening: '正在监听', usbFound: '已检测到 USB 设备', choose: '检测到多台 USB 设备，请选择要设置的一台。', s2Note: 'USB 连接状态由后端实时识别；检测到设备后，这一步会自动完成。', refresh: '我已插好，检查连接', refreshing: '正在检查设备…', backStep1: '返回上一步',
      s3Title: '在 iPhone 上确认“信任”', s3Lead: '这是与 iOS Backup 主机建立的独立信任关系。即使手机以前信任过 Mac 或 Windows，这里仍可能再询问一次。', pairWaiting: '等待设备确认', pairWaitingDetail: '在 iPhone / iPad 上点“信任”，并按提示输入设备锁屏密码。', pairChecking: '正在检查配对', pairPaired: '设备已完成配对', pairFailed: '配对未完成', waitAction: '等待操作', checking: '检查中', paired: '已配对', failed: '需要处理', correctTitle: '这里输入的是', correctBody: 'iPhone / iPad 的设备锁屏密码。', wrongTitle: '不是下面这些', wrongBody: '不是控制台登录密码，也不是加密本地备份密码。', checkPair: '我已确认，检查配对', retryPair: '重新检查配对', backStep2: '返回连接步骤',
      s4Title: '完成第一次备份', s4Lead: '先用 USB 完成一次备份，确认配对、存储路径和设备通信都正常；之后再开启自动备份。', deviceFallback: '这台 iPhone', deviceWaiting: '等待设备状态', ready: '可以开始第一次备份', idle: '尚未开始', starting: '正在准备备份', running: '正在备份设备数据', succeeded: '第一次备份已完成', failedBackup: '备份未完成', interrupted: '设备连接中断', auth: '如设备出现授权提示，请解锁并输入设备锁屏密码。', start: '立即备份', retryBackup: '重新备份', finish: '查看 Wi-Fi 自动备份', backStep3: '返回配对步骤', backupSuccessBody: '设备、配对和本地存储均已验证。现在可以拔掉数据线，并按需要开启自动备份。',
      wifiAfterTitle: '以后通过 Wi-Fi 备份', wifiAfterLead: '系统发现设备并满足你设置的条件后，会自动尝试发起备份；如 iOS 要求授权，请在手机上完成确认。', wifiFlow: ['发现设备', '检查时间与电量', '自动发起备份', '完成本地备份'], conditions: ['已打开 Wi-Fi 显示', '手机与 NAS 网络可互通', '配对记录仍然有效', '满足充电与时间条件'], footerLocal: '本地备份', requestFailed: '操作失败：', selectFirst: '请先连接并选择一台 USB 设备。'
    },
    en: {
      brandSub: 'Back home on Wi-Fi — your iPhone and iPad back up automatically.', back: 'Back to console', connecting: 'Connecting', online: 'Live status connected', offline: 'Live status disconnected — reconnecting', tz: 'Beijing time (UTC+8)',
      heroTitle: 'Get this iPhone backing up at home', heroLead: 'Complete one USB pairing the first time. The console guides each action and detects connection and backup states.', progress: 'First setup progress', stepUnit: 'steps', current: 'Now', todo: 'To do', done: 'Done', optional: 'Optional',
      stepTitles: ['Prepare Wi-Fi', 'Connect to iOS Backup', 'Trust and pair', 'Run the first backup'], stepSubs: ['Optional; skip for USB only', 'Plug into the NAS or host', 'Confirm once on the device', 'Verify the full path works'], rail: 'You only need to do this once. Reopen the guide from the menu whenever needed.',
      s1Title: 'Prepare future Wi-Fi backups', s1Lead: 'Apple requires a Mac or Windows PC to enable wireless visibility first. Skip this if you only plan to use USB.', s1Note: 'The NAS cannot verify this setting, so you confirm it manually. You can enable it later without blocking a USB backup today.', skip: 'Skip for now — use USB', complete: 'I’ve done this', itunes: 'Older iTunes',
      wifiInstructions: {
        mac: ['Connect the iPhone / iPad to the Mac with a cable.', 'Open Finder, select the device in the sidebar, then choose General.', 'Select “Show this device when on Wi-Fi”, then click Apply.'],
        windows: ['Install and open the Apple Devices app on Windows.', 'Connect the device by cable, select it in the sidebar, then choose General.', 'Select “Show this device when on Wi-Fi”, then click Apply.'],
        itunes: ['Connect the device by cable and select it in iTunes.', 'Open Summary.', 'Select “Sync with this device over Wi-Fi”, then click Apply.']
      },
      s2Title: 'Connect the device to the iOS Backup host', s2Lead: 'Use a data-capable USB cable to plug the unlocked iPhone / iPad into the NAS or host running iOS Backup.', usbWaiting: 'Waiting for a USB device', usbWaitingDetail: 'Once connected, it is detected automatically; manual refresh is normally unnecessary.', listening: 'Listening', usbFound: 'USB device detected', choose: 'Multiple USB devices were found. Choose the one to set up.', s2Note: 'USB connection is detected by the backend in real time, so this step completes automatically.', refresh: 'It’s connected — check again', refreshing: 'Checking devices…', backStep1: 'Back',
      s3Title: 'Confirm “Trust” on the iPhone', s3Lead: 'This trust belongs to the iOS Backup host. The device may ask again even if it already trusts another computer.', pairWaiting: 'Waiting for confirmation', pairWaitingDetail: 'Tap Trust on the iPhone / iPad and enter its device passcode if prompted.', pairChecking: 'Checking pairing', pairPaired: 'Pairing complete', pairFailed: 'Pairing is not complete', waitAction: 'Waiting', checking: 'Checking', paired: 'Paired', failed: 'Needs attention', correctTitle: 'Enter this', correctBody: 'The iPhone / iPad device passcode.', wrongTitle: 'Not these passwords', wrongBody: 'Not the console password or encrypted-backup password.', checkPair: 'I confirmed — check pairing', retryPair: 'Check pairing again', backStep2: 'Back to connection',
      s4Title: 'Complete the first backup', s4Lead: 'Run one backup over USB to verify pairing, storage, and device communication before enabling automatic backups.', deviceFallback: 'This iPhone', deviceWaiting: 'Waiting for device status', ready: 'Ready for the first backup', idle: 'Not started', starting: 'Preparing backup', running: 'Backing up device data', succeeded: 'First backup complete', failedBackup: 'Backup did not complete', interrupted: 'Device connection interrupted', auth: 'If the device asks for authorization, unlock it and enter its device passcode.', start: 'Back up now', retryBackup: 'Back up again', finish: 'See Wi-Fi automatic backup', backStep3: 'Back to pairing', backupSuccessBody: 'Device access, pairing, and local storage are verified. You can unplug the cable and enable automatic backups when ready.',
      wifiAfterTitle: 'Future backups over Wi-Fi', wifiAfterLead: 'When the device is found and your conditions are met, the system automatically attempts a backup. Confirm on the iPhone if iOS asks.', wifiFlow: ['Discover device', 'Check time and battery', 'Start backup', 'Store locally'], conditions: ['Wi-Fi visibility enabled', 'Phone and NAS can reach each other', 'Pairing record is still valid', 'Time and charging rules are met'], footerLocal: 'Local backup', requestFailed: 'Action failed: ', selectFirst: 'Connect and select a USB device first.'
    }
  };

  function text(id, value) { var node = document.getElementById(id); if (node) node.textContent = value; }
  function t() { return copy[lang]; }
  function renderConnectionCopy() {
    var badge = document.getElementById('connectionBadge');
    var label = t().connecting;
    if (badge.classList.contains('online')) label = t().online;
    if (badge.classList.contains('offline')) label = t().offline;
    badge.querySelector('span').textContent = label;
    text('liveText', label);
  }
  function showToast(message) { var toast = document.getElementById('toast'); text('toastText', message); toast.hidden = false; window.clearTimeout(showToast.timer); showToast.timer = window.setTimeout(function () { toast.hidden = true; }, 2800); }
  function post(url) {
    return fetch(url, { method: 'POST', headers: { 'X-IOSBK-CSRF': csrf } }).then(function (response) {
      return response.json().catch(function () { return {}; }).then(function (data) {
        if (!response.ok) throw new Error(data.error || data.error_code || response.statusText);
        return data;
      });
    });
  }

  function connectionProblem() {
    var service=(snapshot.connection_services||[]).find(function(item){return item.connection==='usb';});
    if(!service) return '';
    var zh=lang==='zh';
    if(service.recovery==='restarting') return zh?'正在恢复 USB 连接服务，请稍候。':'Restoring the USB connection service. Please wait.';
    if(service.recovery==='waiting_busy') return zh?'连接服务异常，等待当前设备任务结束后恢复。':'Waiting for current device tasks to finish before recovering the connection service.';
    if(service.recovery==='manual_required') return zh?'USB 连接服务自动恢复失败，请返回控制台查看状态。':'USB connection recovery failed. Return to the console to check its status.';
    if(service.recovery==='backoff') return zh?'USB 连接服务恢复失败，稍后自动重试。':'USB connection recovery failed. Another attempt is scheduled.';
    if(service.health==='unknown') return zh?'正在检查 USB 连接服务…':'Checking the USB connection service…';
    if(service.health!=='healthy') return zh?'USB 连接服务异常，设备状态暂时无法确认。':'USB connection service is unavailable. Device status is temporarily unknown.';
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
    localStorage.setItem('iosbk_guide_udid', udid);
    var device = selectedDevice();
    if (wifiConfirmed && device && active < 2) setActive(device.pairing_state === 'paired' ? 3 : 2);
    render();
  }

  function setActive(step) {
    active = Math.max(0, Math.min(3, step));
    localStorage.setItem('iosbk_guide_step', String(active));
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
    document.documentElement.lang = lang === 'zh' ? 'zh-CN' : 'en';
    text('brandSub', c.brandSub); text('backConsole', c.back); text('tzText', c.tz); text('heroTitle', c.heroTitle); text('heroLead', c.heroLead); text('progressLabel', c.progress); text('railNote', c.rail); text('optionalLabel', c.optional);
    text('s1Title', c.s1Title); text('s1Lead', c.s1Lead); text('s1Note', c.s1Note); text('skipWifi', c.skip); text('completeWifi', c.complete); text('itunesLabel', c.itunes);
    text('s2Title', c.s2Title); text('s2Lead', c.s2Lead); text('s2Note', c.s2Note); text('refreshDevices', c.refresh); text('backStep1', c.backStep1);
    text('s3Title', c.s3Title); text('s3Lead', c.s3Lead); text('correctTitle', c.correctTitle); text('correctBody', c.correctBody); text('wrongTitle', c.wrongTitle); text('wrongBody', c.wrongBody); text('backStep2', c.backStep2);
    text('s4Title', c.s4Title); text('s4Lead', c.s4Lead); text('backupAuth', c.auth); text('backupSuccessTitle', c.succeeded); text('backupSuccessBody', c.backupSuccessBody); text('backStep3', c.backStep3);
    text('wifiAfterTitle', c.wifiAfterTitle); text('wifiAfterLead', c.wifiAfterLead); text('wifiFlowDiscover', c.wifiFlow[0]); text('wifiFlowCheck', c.wifiFlow[1]); text('wifiFlowStart', c.wifiFlow[2]); text('wifiFlowDone', c.wifiFlow[3]);
    text('conditionVisible', c.conditions[0]); text('conditionNetwork', c.conditions[1]); text('conditionPairing', c.conditions[2]); text('conditionRules', c.conditions[3]); text('footerLocal', c.footerLocal); text('footerBack', c.back);
    c.stepTitles.forEach(function (value, index) { document.querySelector('[data-step-title="' + index + '"]').textContent = value; document.querySelector('[data-step-sub="' + index + '"]').textContent = c.stepSubs[index]; });
    document.querySelectorAll('[data-lang]').forEach(function (button) { button.classList.toggle('active', button.dataset.lang === lang); });
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
    if (state === 'failed') { title = c.pairFailed; detail = device.pairing_error || device.pairing_error_code || ''; badge = c.failed; }
    var problem=connectionProblem();
    if(problem){ title=lang==='zh'?'连接服务暂时不可用':'Connection service unavailable';detail=problem;badge=c.failed; }
    document.getElementById('checkPair').disabled=!!problem || !device || device.operations_available===false;
    document.querySelector('.password-grid').hidden=!!problem;
    text('s3Title',problem?title:c.s3Title);text('s3Lead',problem||c.s3Lead);
    text('pairTitle', title); text('pairDetail', detail); text('pairState', badge);
    var error = document.getElementById('pairError');
    error.hidden = !!problem || state !== 'failed';
    error.textContent = detail;
    text('checkPair', state === 'failed' ? c.retryPair : c.checkPair);
  }

  function backupMessage(device) {
    var c = t();
    var state = device ? device.backup_state : 'idle';
    var title = c.ready, detail = c.idle;
    if (state === 'starting') { title = c.starting; detail = c.starting; }
    if (state === 'running') { title = c.running; detail = c.running; }
    if (state === 'succeeded' && device && device.last_backup) { title = c.succeeded; detail = device.last_backup; }
    if (state === 'failed') { title = c.failedBackup; detail = device.last_backup_error || device.last_backup_error_code || ''; }
    if (state === 'interrupted') { title = c.interrupted; detail = device.last_backup_error || device.last_backup_error_code || ''; }
    text('backupTitle', title); text('backupDetail', detail);
    document.getElementById('backupMotion').hidden = state !== 'starting' && state !== 'running';
    document.getElementById('backupAuth').hidden = state !== 'starting' && state !== 'running';
    document.getElementById('backupSuccess').hidden = !(state === 'succeeded' && device && device.last_backup);
    var error = document.getElementById('backupError');
    error.hidden = state !== 'failed' && state !== 'interrupted';
    error.textContent = detail;
    var button = document.getElementById('startBackup');
    button.disabled = state === 'starting' || state === 'running' || !device || device.presence_known===false || device.operations_available===false || device.pairing_state !== 'paired';
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
      localStorage.setItem('iosbk_guide_step', String(active));
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
    text('progressCount', completed >= 4 ? c.done : (completed + ' / 4 ' + c.stepUnit));

    renderDeviceChoices(devices);
    if (devices.length) {
      text('usbTitle', c.usbFound);
      text('usbDetail', devices.length > 1 ? c.choose : (devices[0].name || devices[0].udid));
      text('usbState', c.done);
    } else {
      text('usbTitle', c.usbWaiting); text('usbDetail', c.usbWaitingDetail); text('usbState', c.listening);
    }
    var problem=connectionProblem();
    if(problem){ text('usbTitle',lang==='zh'?'连接服务暂时不可用':'Connection service unavailable');text('usbDetail',problem);text('usbState',c.failed);text('s2Lead',problem); } else {text('s2Lead',c.s2Lead);}
    pairingMessage(device);
    text('deviceName', device ? (device.name || c.deviceFallback) : c.deviceFallback);
    text('deviceMeta', device ? ((device.device_type || 'iOS') + ' · ' + (device.battery || 0) + '%') : c.deviceWaiting);
    text('connectionPill', device ? String(device.conn || 'offline').toUpperCase() : 'USB');
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
      try { snapshot = JSON.parse(event.data); render(); } catch (_) { showToast(t().requestFailed + 'invalid status'); }
    };
    eventSource.onerror = function () {
      var badge = document.getElementById('connectionBadge');
      badge.className = 'connection-badge offline'; renderConnectionCopy();
      eventSource.close(); reconnectTimer = window.setTimeout(connectEvents, 2500);
    };
  }

  document.querySelectorAll('[data-lang]').forEach(function (button) { button.addEventListener('click', function () { lang = button.dataset.lang; localStorage.setItem('iosbk_guide_lang', lang); document.getElementById('toast').hidden = true; applyLanguage(); render(); }); });
  document.querySelectorAll('[data-platform]').forEach(function (button) { button.addEventListener('click', function () { platform = button.dataset.platform; document.querySelectorAll('[data-platform]').forEach(function (item) { item.classList.toggle('active', item === button); }); renderInstructions(); }); });
  document.querySelectorAll('[data-back]').forEach(function (button) { button.addEventListener('click', function () { setActive(Number(button.dataset.back)); }); });
  document.querySelectorAll('.step-row').forEach(function (button) { button.addEventListener('click', function () { if (!button.disabled) setActive(Number(button.dataset.step)); }); });
  document.getElementById('deviceChoices').addEventListener('click', function (event) { var button = event.target.closest('[data-device]'); if (button) selectDevice(button.dataset.device); });
  document.getElementById('skipWifi').addEventListener('click', function () { wifiConfirmed = true; localStorage.setItem('iosbk_guide_wifi', 'done'); setActive(1); });
  document.getElementById('completeWifi').addEventListener('click', function () { wifiConfirmed = true; localStorage.setItem('iosbk_guide_wifi', 'done'); setActive(1); });
  document.getElementById('refreshDevices').addEventListener('click', function () { text('refreshDevices', t().refreshing); post('/api/refresh').then(function () { showToast(t().refreshing); }).catch(function (error) { showToast(t().requestFailed + error.message); }).finally(function () { text('refreshDevices', t().refresh); }); });
  document.getElementById('checkPair').addEventListener('click', function () { if (!selectedUDID) { showToast(t().selectFirst); return; } post('/api/pair/' + encodeURIComponent(selectedUDID)).catch(function (error) { showToast(t().requestFailed + error.message); }); });
  document.getElementById('startBackup').addEventListener('click', function () {
    var device = selectedDevice();
    if (device && device.backup_state === 'succeeded' && device.last_backup) { document.getElementById('wifiAfter').scrollIntoView({ behavior: 'smooth' }); return; }
    if (!selectedUDID) { showToast(t().selectFirst); return; }
    post('/api/backup/' + encodeURIComponent(selectedUDID)).catch(function (error) { showToast(t().requestFailed + error.message); });
  });

  applyLanguage();
  render();
  connectEvents();
})();

(function (global) {
  'use strict';
  function diagnosticText(payload) {
    var diagnostic = {}, source = payload.diagnostic || {};
    ['operation','stage','reason','connection','exit_code','device_error_code','duration_ms','output_truncated','summary'].forEach(function (key) {
      if (source[key] !== undefined) diagnostic[key] = source[key];
    });
    return JSON.stringify({code:payload.code || '', diagnostic:diagnostic, detail:payload.detail || ''}, null, 2);
  }

  async function copyDiagnostic(container, payload, options) {
    var text = diagnosticText(payload), nav = options.navigator;
    try {
      if (!nav.clipboard || !nav.clipboard.writeText) throw new Error('clipboard unavailable');
      await nav.clipboard.writeText(text);
      return true;
    } catch (_) {
      var input = options.document.createElement('textarea');
      input.readOnly = true; input.value = text; input.rows = 8;
      input.className = 'bk-query-copy-fallback';
      input.setAttribute('aria-label', options.t('query.copyFallback'));
      container.appendChild(input); input.focus(); input.select();
      return false;
    }
  }

  var nextDiagnosticID = 0;
  function renderDiagnostic(container, payload, options) {
    if (!container) return;
    var state = container.backupQueryView || {expanded:false, id:'backup-query-detail-'+(++nextDiagnosticID)};
    // 新一轮结果保留展开偏好，但不能继承旧复制状态或迟到的回调。
    if (state.payload !== payload || state.loading !== !!options.loading) {
      state = {expanded:state.expanded,id:state.id,payload:payload,loading:!!options.loading};
    }
    var active = options.document.activeElement;
    var focused = active && active === container.querySelector('.bk-query-disclosure');
    container.replaceChildren();
    container.hidden = !payload && !options.loading;
    var announcement = options.loading ? options.t('query.preparingDownload') : payload ? options.message(payload) : '';
    if (options.liveRegion && options.liveRegion.textContent !== announcement) options.liveRegion.textContent = announcement;
    if (container.hidden) { container.backupQueryView = null; return; }
    container.backupQueryView = state;
    var doc = options.document, t = options.t;
    var kind = options.kind || 'info', loading = options.loading;
    container.className = 'bk-query-notice bk-query-'+kind+(loading ? ' bk-query-pending' : '');
    function element(tag, className, text) {
      var el = doc.createElement(tag); el.className = className || '';
      if (text !== undefined) el.textContent = text;
      return el;
    }
    function button(className, text) { var el = element('button',className,text); el.type = 'button'; return el; }
    var row = element('div','bk-query-notice-row');
    var icon = element('span','bk-query-notice-icon'); icon.setAttribute('aria-hidden','true');
    // 图标名称为固定值，诊断内容只通过 textContent 展示。
    icon.innerHTML = '<svg class="ui-icon'+(loading ? ' spin' : '')+'"><use href="/static/ui-icons.svg#icon-'+(loading ? 'refresh' : 'info')+'"></use></svg>';
    row.appendChild(icon);
    var copy = element('div','bk-query-notice-copy');
    copy.appendChild(element('strong','',t(loading ? 'query.preparingTitle' : kind === 'list' ? 'query.listFailed' : kind === 'export' ? 'query.exportFailed' : 'query.infoIncomplete')));
    copy.appendChild(element('p','',loading ? t('query.preparingDownload') : options.message(payload)));
    row.appendChild(copy); container.appendChild(row);
    if (loading) return;
    var actions = element('div','bk-query-actions');
    if (options.onRetry) {
      var retry = button('bk-query-button bk-query-retry',t('query.retry'));
      retry.disabled = !!options.busy; retry.addEventListener('click',options.onRetry); actions.appendChild(retry);
    }
    var source = payload.diagnostic || {};
    if (!payload.diagnostic && !payload.detail) {
      if (options.onRetry) copy.appendChild(actions);
      return;
    }
    var toggle = button('bk-query-text-button bk-query-disclosure','');
    var details = element('div','bk-query-details'); details.id = state.id;
    function updateDisclosure() {
      toggle.textContent = t(state.expanded ? 'query.hideDetails' : 'query.details');
      toggle.setAttribute('aria-expanded',String(state.expanded)); details.hidden = !state.expanded;
    }
    toggle.setAttribute('aria-controls',details.id); updateDisclosure();
    toggle.addEventListener('click',function () { state.expanded = !state.expanded; updateDisclosure(); });
    actions.appendChild(toggle);
    if (kind === 'list') copy.appendChild(actions); else row.appendChild(actions);
    var head = element('div','bk-query-detail-head');
    head.appendChild(element('strong','',t('query.diagnostic')));
    var copyButton = button('bk-query-button bk-query-copy',t('query.copy'));
    head.appendChild(copyButton); details.appendChild(head);
    var fields = element('dl','bk-query-fields');
    var operations = {info:'query.operationInfo',list:'query.operationList',export:'query.operationExport'};
    var stages = {command:'query.stageCommand',parse:'query.stageParse',export_prepare:'query.stageExport'};
    function field(key, value) {
      if (value === undefined || value === null || value === '') return;
      var item = element('div',''); item.appendChild(element('dt','',t(key)));
      item.appendChild(element('dd','',String(value))); fields.appendChild(item);
    }
    field('query.operation',operations[source.operation] ? t(operations[source.operation]) : source.operation);
    field('query.connection',source.connection === 'network' ? 'Wi-Fi' : source.connection === 'usb' ? 'USB' : source.connection);
    field('query.deviceCode',source.device_error_code);
    field('query.exitCode',source.exit_code);
    if (source.stage && source.stage !== 'command') field('query.stage',stages[source.stage] ? t(stages[source.stage]) : source.stage);
    if (source.duration_ms !== undefined) field('query.duration',t('query.durationValue',{ms:source.duration_ms}));
    details.appendChild(fields);
    var raw = payload.detail || source.summary;
    if (raw) {
      details.appendChild(element('div','bk-query-raw-label',t('query.rawOutput')));
      details.appendChild(element('pre','bk-query-raw',raw));
    }
    if (payload.code) details.appendChild(element('code','bk-query-code',payload.code));
    if (source.output_truncated) details.appendChild(element('p','bk-query-footnote',t('query.truncated')));
    var status = element('p','bk-query-copy-status',state.copyStatus ? t(state.copyStatus) : ''); status.setAttribute('role','status');
    var fallback = element('div','');
    if (state.fallback) {
      var input = element('textarea','bk-query-copy-fallback'); input.readOnly = true; input.rows = 8;
      input.value = diagnosticText(payload); input.setAttribute('aria-label',t('query.copyFallback')); fallback.appendChild(input);
    }
    copyButton.addEventListener('click', async function () {
      if (copyButton.disabled) return;
      copyButton.disabled = true; fallback.replaceChildren();
      try {
        var copied = await copyDiagnostic(fallback, payload, options);
        if (container.backupQueryView !== state) return;
        state.copyStatus = copied ? 'query.copied' : 'query.copyFallback'; state.fallback = !copied;
        renderDiagnostic(container,payload,options);
        var target = container.querySelector(copied ? '.bk-query-copy' : 'textarea');
        if (target) { target.focus(); if (!copied) target.select(); }
      } finally { copyButton.disabled = false; }
    });
    details.appendChild(status); details.appendChild(fallback);
    container.appendChild(details);
    if (focused) toggle.focus();
  }
  async function downloadCSV(fetcher, url, options) {
    var response = await fetcher(url, {signal:options.signal});
    if (!response.ok) {
      var failure = new Error('CSV preparation failed');
      try { failure.payload = await response.json(); } catch (_) {}
      throw failure;
    }
    var limit = options.limit || 32 * 1024 * 1024;
    var length = response.headers.get('Content-Length');
    // Fetch 返回解码后的正文；压缩传输长度不能作为 CSV 字节数。
    var encoding = (response.headers.get('Content-Encoding') || '').trim().toLowerCase();
    var expected = length === null || (encoding && encoding !== 'identity') ? null : Number(length);
    if (!/^text\/csv(?:;|$)/i.test(response.headers.get('Content-Type') || '') ||
        (expected !== null && (!Number.isSafeInteger(expected) || expected < 0 || expected > limit)) || !response.body) {
      if (response.body) await response.body.cancel();
      throw new Error('Invalid CSV response');
    }
    var reader = response.body.getReader(), chunks = [], size = 0;
    try {
      for (;;) {
        var item = await reader.read();
        if (item.done) break;
        size += item.value.byteLength;
        if (size > limit) throw new Error('CSV exceeds limit');
        chunks.push(item.value);
      }
      if (expected !== null && size !== expected) throw new Error('Incomplete CSV');
    } catch (error) {
      try { await reader.cancel(); } catch (_) {}
      throw error;
    } finally { reader.releaseLock(); }
    var urls = options.URL || global.URL, BlobType = options.Blob || global.Blob;
    var objectURL = urls.createObjectURL(new BlobType(chunks, {type:'text/csv;charset=utf-8'}));
    var link = options.document.createElement('a');
    try {
      link.href = objectURL; link.download = options.filename || 'backup-list.csv';
      options.document.body.appendChild(link); link.click();
    } finally {
      link.remove();
      (options.setTimeout || global.setTimeout)(function () { urls.revokeObjectURL(objectURL); }, 1000);
    }
  }
  global.IOSBKBackupQuery = {renderDiagnostic:renderDiagnostic,copyDiagnostic:copyDiagnostic,diagnosticText:diagnosticText,downloadCSV:downloadCSV};
})(window);

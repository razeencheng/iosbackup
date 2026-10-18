/* 本地备份浏览器：读取状态与设备连接状态相互独立。 */
(function (root, factory) {
  var api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  else root.IOSBKBackupBrowser = api;
})(typeof window !== 'undefined' ? window : globalThis, function () {
  'use strict';
  var PAGE_SIZE = 200;
  function validDate(value) { return !!value && !/^0001-/.test(value) && Number.isFinite(new Date(value).getTime()); }
  function busy(data) { return !!data && ['queued', 'running'].indexOf((data.refresh || {}).state) >= 0; }
  function classify(data, requestError) {
    var o = (data || {}).overview || {}, l = (data || {}).file_list || {}, r = (data || {}).refresh || {};
    var hasData = o.status === 'available' || l.status === 'available';
    if (requestError) return hasData ? 'failed' : 'firstFailed';
    if (busy(data)) return hasData ? 'refreshing' : 'first';
    if ([o.message_code, l.message_code, r.message_code].indexOf('api.backup_index_unavailable') >= 0) return 'unavailable';
    if (l.status === 'unsupported') return o.status === 'available' ? 'encrypted' : 'firstFailed';
    if (o.encrypted === true && l.status === 'available' && l.freshness !== 'current') return 'encryptedCached';
    if (o.status === 'missing' && o.message_code === 'api.backup_index_missing' && l.status === 'missing') return 'empty';
    if (r.state === 'failed' || o.status === 'unavailable' || l.status === 'unavailable') return hasData ? 'failed' : 'firstFailed';
    return hasData ? 'offlineReady' : 'first';
  }
  function csvURL(udid, data) {
    var l = (data || {}).file_list || {};
    return l.status === 'available' && l.complete && l.csv_available && data.revision ? '/api/backup-index/' + encodeURIComponent(udid) + '?format=csv&revision=' + encodeURIComponent(data.revision) : '';
  }
  function recordText(record) { return record.fields ? record.fields.join(',  ') : (record.relative_path || '') + '\n' + (record.domain || '') + '\n' + (record.file_id || ''); }
  function view(state) {
    var data = state.snapshot || {}, o = data.overview || {}, l = data.file_list || {};
    var entries = l.status === 'available' ? (l.entries || []).slice(0, 5000) : [];
    var query = (state.query || '').trim().toLocaleLowerCase();
    var filtered = query ? entries.filter(function (r) { return recordText(r).toLocaleLowerCase().indexOf(query) >= 0; }) : entries;
    var pages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE));
    var page = Math.max(0, Math.min(state.page || 0, pages - 1));
    return {scenario: state.posting ? (o.status === 'available' || l.status === 'available' ? 'refreshing' : 'first') : classify(data, state.requestError), busy: !!state.posting || (!state.requestError && busy(data)), rows: filtered.slice(page * PAGE_SIZE, (page + 1) * PAGE_SIZE), loaded: entries.length, matches: filtered.length, page: page, pages: pages,
      size: o.status === 'available' && Number.isFinite(o.size_bytes) ? o.size_bytes : null,
      count: l.status === 'available' && Number.isFinite(l.entry_count) ? l.entry_count : null,
      encrypted: o.status === 'available' && typeof o.encrypted === 'boolean' ? o.encrypted : null,
      stale: (o.status === 'available' && o.freshness !== 'current') || (l.status === 'available' && l.freshness !== 'current')};
  }
  function create(options) {
    var states = Object.create(null);
    function state(id) { return states[id] || (states[id] = {snapshot: null, query: '', page: 0, open: false, sequence: 0, active: 0, timer: null, source: '', signal: '', pending: false, requestError: false, posting: false}); }
    function visible(id) { return state(id).open && (!options.isVisible || options.isVisible(id)); }
    function stop(s) { if (s.timer !== null) options.clearTimeout(s.timer); s.timer = null; }
    function render(id) { options.render(id, state(id)); }
    function schedule(id) {
      var s = state(id); stop(s);
      if (visible(id) && !s.requestError && busy(s.snapshot) && !s.active) s.timer = options.setTimeout(function () { s.timer = null; if (visible(id)) load(id); }, 3000);
    }
    function clearSource(id, source) {
      var s = state(id); stop(s); s.sequence++; s.active = 0; s.snapshot = null; s.query = ''; s.page = 0; s.source = source || ''; s.requestError = false; s.posting = false; s.pending = false; render(id);
    }
    function load(id, method) {
      var s = state(id); stop(s); var seq = ++s.sequence, source = s.source, signal = s.signal;
      s.active = seq; s.requestError = false; s.posting = method === 'POST'; render(id);
      return Promise.resolve().then(function () { return options.fetch('/api/backup-index/' + encodeURIComponent(id), {method: method || 'GET'}); }).then(function (response) {
        if (!response.ok) throw new Error('backup index request failed');
        return response.json();
      }).then(function (data) {
        if (s.sequence !== seq || (source && s.source !== source) || signal !== s.signal) return;
        if (!data || !data.overview || !data.file_list || !data.source_key) throw new Error('invalid backup index response');
        if (s.source && data.source_key !== s.source) { s.query = ''; s.page = 0; }
        s.source = data.source_key; s.snapshot = data; s.requestError = false; s.page = view(s).page;
      }).catch(function () { if (s.sequence === seq) s.requestError = true; }).finally(function () {
        if (s.sequence !== seq) return;
        s.active = 0; s.posting = false; render(id);
        if (s.pending && visible(id)) { s.pending = false; load(id); } else schedule(id);
      });
    }
    function notify(id, signal) {
      var s = state(id); if (!signal) return;
      var key = JSON.stringify(signal);
      if (key === s.signal) return;
      s.signal = key;
      if (signal.source_key && signal.source_key !== s.source) clearSource(id, signal.source_key);
      if (!visible(id)) return;
      if (s.active) s.pending = true; else load(id);
    }
    return {state: state, load: load, notify: notify,
      open: function (id) { var s = state(id); s.open = true; return load(id); },
      close: function (id) { var s = state(id); s.open = false; s.pending = false; stop(s); },
      refresh: function (id) { return load(id, 'POST'); },
      invalidate: function (id) { clearSource(id, ''); if (visible(id)) return load(id); },
      search: function (id, value) { var s = state(id); s.query = value || ''; s.page = 0; render(id); },
      page: function (id, delta) { var s = state(id); s.page += delta; s.page = view(s).page; render(id); },
      rerender: function () { Object.keys(states).forEach(function(id){var s=state(id);if(s.snapshot||s.open||s.sequence)render(id);}); },
      reconnect: function () { Object.keys(states).forEach(function (id) { if (visible(id)) { if (state(id).active) state(id).pending = true; else load(id); } }); },
      visibilityChanged: function () { Object.keys(states).forEach(function (id) { if (!visible(id)) stop(state(id)); else if (!state(id).active) load(id); }); }
    };
  }
  function domRenderer(options) {
    var doc = options.document, tr = options.tr;
    function node(id, name) { return doc.getElementById(name + '-' + id); }
    function set(id, name, value) { var n = node(id, name); if (n) n.textContent = value; }
    function date(value) { return validDate(value) ? options.formatDate(value) : tr('bindex.notRead'); }
    function number(value) { return new Intl.NumberFormat(options.locale()).format(value); }
    function size(value) { if (value === null) return '—'; var units = ['B', 'KB', 'MB', 'GB', 'TB'], n = 0; while (value >= 1024 && n < units.length - 1) { value /= 1024; n++; } return new Intl.NumberFormat(options.locale(), {maximumFractionDigits: n ? 1 : 0}).format(value) + ' ' + units[n]; }
    return function (id, state) {
      var vm = view(state), data = state.snapshot || {}, o = data.overview || {}, l = data.file_list || {};
      set(id, 'bkSize', size(vm.size)); set(id, 'bkCount', vm.count === null ? '—' : number(vm.count));
      set(id, 'bkEnc', vm.encrypted === null ? '—' : tr(vm.encrypted ? 'bindex.encryptedYes' : 'bindex.encryptedNo'));
      set(id, 'bkLatest', validDate(data.last_backup_at) ? options.formatDate(data.last_backup_at) : tr('bindex.noRecord'));
      set(id, 'bkStatusTitle', tr('bindex.' + vm.scenario + 'Title'));
      set(id, 'bkStatusBody', tr('bindex.' + vm.scenario + 'Body'));
      var status = node(id, 'bkStatus'); if (status) { status.dataset.state = vm.scenario; status.setAttribute('aria-busy', String(vm.busy)); }
      var times = [];
      if (validDate(o.read_at)) times.push(tr('bindex.overviewRead', {time: date(o.read_at)}));
      if (validDate(l.read_at)) times.push(tr('bindex.listRead', {time: date(l.read_at)}));
      set(id, 'bkReadAt', times.length ? times.join(' · ') : (vm.scenario === 'empty' ? '' : tr('bindex.notRead')));
      var detail = node(id, 'bkReadDetail');
      if (detail) {
        var messages = [];
        if (vm.stale) messages.push(tr('bindex.previousRead'));
        if (options.message && o.message_code && o.status !== 'missing') messages.push(tr('bindex.overviewDetail', {message: options.message({message_code: o.message_code}, 'common.operationFailed')}));
        if (options.message && l.message_code && l.status !== 'missing') messages.push(tr('bindex.listDetail', {message: options.message({message_code: l.message_code}, 'common.operationFailed')}));
        detail.textContent = messages.join(' '); detail.classList.toggle('hidden', !messages.length);
      }
      var refresh = node(id, 'bkRefresh'); if (refresh) refresh.disabled = vm.busy;
      set(id, 'bkRefreshLabel', tr(vm.busy ? 'bindex.refreshing' : ['failed', 'firstFailed', 'unavailable'].indexOf(vm.scenario) >= 0 ? 'bindex.retry' : 'bmgmt.infoRefresh'));
      var csv = node(id, 'bkCSV'), url = csvURL(id, data);
      if (csv) { if (url) csv.setAttribute('href', url); else csv.removeAttribute('href'); csv.setAttribute('aria-disabled', String(!url)); csv.setAttribute('tabindex', url ? '0' : '-1'); csv.setAttribute('title', tr(url ? 'bindex.fullCSV' : 'bindex.noCSV')); }
      var search = node(id, 'bkSearch'); if (search) { search.disabled = l.status !== 'available'; if (search.value !== state.query) search.value = state.query || ''; }
      set(id, 'bkPreview', l.status === 'available' ? tr('bindex.preview', {loaded: number(vm.loaded), total: vm.count === null ? '—' : number(vm.count)}) : '');
      var box = node(id, 'list'); if (box) {
        box.replaceChildren(); box.setAttribute('aria-busy', String(vm.busy && !vm.rows.length));
        if (vm.rows.length) {
          vm.rows.forEach(function (entry) {
            var row = doc.createElement('div'), path = doc.createElement('span'), domain = doc.createElement('span');
            row.className = 'backup-file-row'; path.className = 'backup-file-path'; domain.className = 'backup-file-domain';
            path.textContent = entry.fields ? entry.fields.join(',  ') : entry.relative_path || entry.file_id || '—'; domain.textContent = entry.fields ? '' : entry.domain || '';
            row.appendChild(path); row.appendChild(domain); box.appendChild(row);
          });
        } else {
          var empty = doc.createElement('div'); empty.className = 'backup-list-empty';
          empty.textContent = tr(l.status === 'available' ? (vm.loaded ? 'bindex.noMatches' : 'bindex.emptyManifest') : vm.scenario === 'first' ? 'bindex.readingList' : vm.scenario === 'encrypted' ? 'bindex.encryptedList' : vm.scenario === 'empty' ? 'bmgmt.noBackup' : 'bindex.noList'); box.appendChild(empty);
        }
      }
      set(id, 'listPage', l.status === 'available' ? (vm.page + 1) + ' / ' + vm.pages : '—');
      set(id, 'listMeta', l.status === 'available' ? tr(state.query ? 'bindex.matches' : 'bindex.range', {start: number(vm.matches ? vm.page * PAGE_SIZE + 1 : 0), end: number(Math.min((vm.page + 1) * PAGE_SIZE, vm.matches)), n: number(vm.matches)}) : '');
      var prev = node(id, 'bkPrev'), next = node(id, 'bkNext'); if (prev) prev.disabled = !vm.matches || vm.page === 0; if (next) next.disabled = !vm.matches || vm.page === vm.pages - 1;
    };
  }
  return {create: create, classify: classify, view: view, csvURL: csvURL, validDate: validDate, domRenderer: domRenderer};
});

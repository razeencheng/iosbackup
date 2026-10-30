(function (global) {
  'use strict';
  var catalogs = { en: {}, 'zh-CN': {} };
  function canonical(value) {
    value = String(value || '').trim().replace(/_/g, '-').toLowerCase();
    if (!/^[a-z]{2,8}(?:-[a-z0-9]{1,8})*$/.test(value)) return '';
    if (value === 'zh' || value === 'zh-cn' || value === 'zh-sg' || value === 'zh-hans' || value.indexOf('zh-hans-') === 0) return 'zh-CN';
    if (value === 'en' || value.indexOf('en-') === 0) return 'en';
    return '';
  }
  function storageValue(key) { try { return { available: true, value: global.localStorage.getItem(key) }; } catch (_) { return { available: false, value: null }; } }
  function readStorage(key) { return storageValue(key).value; }
  function writeStorage(key, value) { try { global.localStorage.setItem(key, value); } catch (_) {} }
  function readCookie() {
    try { var match = global.document.cookie.match(/(?:^|;\s*)iosbk_lang=([^;]*)/); return match ? decodeURIComponent(match[1]) : ''; } catch (_) { return ''; }
  }
  function resolveLocale() {
    var selected = canonical(readCookie()) || canonical(readStorage('iosbk_lang')) || canonical(readStorage('iosbk_guide_lang'));
    if (selected) return selected;
    var languages = global.navigator.languages || [global.navigator.language];
    for (var i = 0; i < languages.length; i++) { selected = canonical(languages[i]); if (selected) return selected; }
    return 'en';
  }
  var locale = resolveLocale(), observedCookie = '', observedStorage = null;
  function persist(writePreference) {
    if (writePreference !== false && readStorage('iosbk_lang') !== locale) writeStorage('iosbk_lang', locale);
    global.document.documentElement.lang = locale;
    if (readCookie() !== locale) {
      try { global.document.cookie = 'iosbk_lang=' + locale + '; Path=/; Max-Age=31536000; SameSite=Lax' + (global.location && global.location.protocol === 'https:' ? '; Secure' : ''); } catch (_) {}
    }
    // 写入失败也记录已观察值，避免页面恢复焦点时撤销本页刚作出的选择。
    observedCookie = readCookie(); observedStorage = readStorage('iosbk_lang');
  }
  function register(resources) {
    Object.keys(resources).forEach(function (language) {
      if (!catalogs[language]) catalogs[language] = {};
      Object.keys(resources[language]).forEach(function (key) { catalogs[language][key] = resources[language][key]; });
    });
  }
  function has(key) { return Object.prototype.hasOwnProperty.call(catalogs[locale] || {}, key) || Object.prototype.hasOwnProperty.call(catalogs.en, key); }
  function t(key, params) {
    var value = (catalogs[locale] || {})[key];
    if (value == null) value = catalogs.en[key];
    if (value == null) return key;
    return String(value).replace(/\{([\w]+)\}/g, function (match, name) { return params && params[name] != null ? String(params[name]) : match; });
  }
  function message(payload, fallbackKey) {
    var key = payload && (has(payload.message_code) ? payload.message_code : payload.code);
    var reason = t(key && has(key) ? key : (fallbackKey || 'common.operationFailed'), payload && payload.params);
    var context = payload && payload.message_context;
    if (context && context.kind === 'notification_channel' && typeof context.channel === 'string' && typeof context.name === 'string') {
      return t('api.notification_channel_context', { channel: context.channel, name: context.name, reason: reason });
    }
    return reason;
  }
  function tree(prefix) {
    var result = {};
    Object.keys(catalogs.en).forEach(function (key) {
      if (key.indexOf(prefix + '.') !== 0) return;
      var parts = key.slice(prefix.length + 1).split('.'), node = result;
      parts.forEach(function (part, index) {
        if (index === parts.length - 1) node[part] = t(key);
        else node = node[part] || (node[part] = /^\d+$/.test(parts[index + 1]) ? [] : {});
      });
    });
    return result;
  }
  function apply(root) {
    root = root || global.document;
    [['data-i18n', 'textContent'], ['data-i18n-ph', 'placeholder'], ['data-i18n-title', 'title'], ['data-i18n-href', 'href'], ['data-i18n-alt', 'alt'], ['data-i18n-aria', 'aria-label']].forEach(function (pair) {
      root.querySelectorAll('[' + pair[0] + ']').forEach(function (node) {
        var value = t(node.getAttribute(pair[0]));
        if (pair[1] === 'textContent') node.textContent = value;
        else node.setAttribute(pair[1], value);
      });
    });
    root.querySelectorAll('[data-lang]').forEach(function (button) {
      var selected = canonical(button.getAttribute('data-lang')) === locale;
      button.classList.toggle('active', selected); button.setAttribute('aria-pressed', selected ? 'true' : 'false');
    });
  }
  function selectLocale(selected, writePreference) {
    var changed = selected !== locale;
    locale = selected; persist(writePreference);
    if (changed) global.dispatchEvent(new CustomEvent('iosbk:localechange', { detail: { locale: locale } }));
    return true;
  }
  function setLocale(value) {
    var selected = canonical(value);
    return selected ? selectLocale(selected, true) : false;
  }
  function syncPreferences() {
    var cookie = readCookie(), stored = readStorage('iosbk_lang');
    var selected = cookie !== observedCookie ? canonical(cookie) : '';
    if (!selected && stored !== observedStorage) selected = canonical(stored);
    if (selected) selectLocale(selected, true);
    else { observedCookie = cookie; observedStorage = stored; }
  }
  global.addEventListener('storage', function (event) {
    if (event.key !== 'iosbk_lang') return;
    try { if (event.storageArea && event.storageArea !== global.localStorage) return; } catch (_) { return; }
    var selected = canonical(event.newValue), stored = storageValue('iosbk_lang');
    if (!selected) return;
    // 缺失键不能为旧事件作证；读取受阻时仅接受与共享 cookie 一致的事件。
    if (stored.available ? canonical(stored.value) !== selected : canonical(readCookie()) !== selected) return;
    // 事件的写入已经完成；只同步内存与 cookie，不向其他页面重复广播。
    selectLocale(selected, false);
  });
  global.addEventListener('pageshow', syncPreferences);
  global.addEventListener('focus', syncPreferences);
  function formatDate(value) {
    if (!value) return '';
    // Older API snapshots used a Beijing wall clock without an offset.
    var input = /^\d{4}-\d\d-\d\d \d\d:\d\d(?::\d\d)?$/.test(value) ? value.replace(' ', 'T') + '+08:00' : value;
    var date = new Date(input);
    if (Number.isNaN(date.getTime())) return String(value);
    return new Intl.DateTimeFormat(locale, { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(date);
  }
  function localeFetch(url, options) {
    options = Object.assign({}, options || {});
    var headers = new Headers(options.headers || {}); headers.set('Accept-Language', locale); options.headers = headers;
    return global.fetch(url, options);
  }
  global.IOSBKI18n = { register: register, t: t, has: has, message: message, tree: tree, apply: apply, getLocale: function () { return locale; }, setLocale: setLocale, formatDate: formatDate, fetch: localeFetch, readStorage: readStorage, writeStorage: writeStorage };
  persist();
})(window);

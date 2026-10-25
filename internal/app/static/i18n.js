(function (global) {
  'use strict';
  var catalogs = { en: {}, 'zh-CN': {} };
  function canonical(value) {
    value = String(value || '').replace(/_/g, '-').toLowerCase();
    if (value === 'zh' || value === 'zh-cn' || value === 'zh-sg' || value === 'zh-hans' || value.indexOf('zh-hans-') === 0) return 'zh-CN';
    if (value === 'en' || value.indexOf('en-') === 0) return 'en';
    return '';
  }
  function readStorage(key) { try { return global.localStorage.getItem(key); } catch (_) { return null; } }
  function writeStorage(key, value) { try { global.localStorage.setItem(key, value); } catch (_) {} }
  function readCookie() {
    try { var match = global.document.cookie.match(/(?:^|;\s*)iosbk_lang=([^;]*)/); return match ? decodeURIComponent(match[1]) : ''; } catch (_) { return ''; }
  }
  function resolveLocale() {
    var selected = canonical(readStorage('iosbk_lang')) || canonical(readCookie()) || canonical(readStorage('iosbk_guide_lang'));
    if (selected) return selected;
    var languages = global.navigator.languages || [global.navigator.language];
    for (var i = 0; i < languages.length; i++) { selected = canonical(languages[i]); if (selected) return selected; }
    return 'en';
  }
  var locale = resolveLocale();
  var timezone = 'Asia/Shanghai';
  function setTimezone(value) {
    if (!value) return;
    try { new Intl.DateTimeFormat('en', {timeZone: value}); timezone = value; } catch (_) {}
  }
  setTimezone((global.document.documentElement.dataset || {}).timezone);
  function persist() {
    writeStorage('iosbk_lang', locale);
    global.document.documentElement.lang = locale;
    try { global.document.cookie = 'iosbk_lang=' + locale + '; Path=/; Max-Age=31536000; SameSite=Lax' + (global.location && global.location.protocol === 'https:' ? '; Secure' : ''); } catch (_) {}
  }
  function register(resources) {
    Object.keys(resources).forEach(function (language) {
      if (!catalogs[language]) catalogs[language] = {};
      Object.keys(resources[language]).forEach(function (key) { catalogs[language][key] = resources[language][key]; });
    });
  }
  function has(key) { return Object.prototype.hasOwnProperty.call(catalogs[locale] || {}, key) || Object.prototype.hasOwnProperty.call(catalogs.en, key); }
  function t(key, params) {
    params = Object.assign({timezone: timezone}, params || {});
    var value = (catalogs[locale] || {})[key];
    if (value == null) value = catalogs.en[key];
    if (value == null) return key;
    return String(value).replace(/\{([\w]+)\}/g, function (match, name) { return params && params[name] != null ? String(params[name]) : match; });
  }
  function message(payload, fallbackKey) {
    var key = payload && (has(payload.message_code) ? payload.message_code : payload.code);
    return t(key && has(key) ? key : (fallbackKey || 'common.operationFailed'), payload && payload.params);
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
  function setLocale(value) {
    locale = canonical(value) || 'en'; persist();
    global.dispatchEvent(new CustomEvent('iosbk:localechange', { detail: { locale: locale } }));
  }
  function formatDate(value) {
    if (!value) return '';
    // 旧接口的无偏移时间仍按北京时间解析，显示时再转换到部署时区。
    var input = /^\d{4}-\d\d-\d\d \d\d:\d\d(?::\d\d)?$/.test(value) ? value.replace(' ', 'T') + '+08:00' : value;
    var date = new Date(input);
    if (Number.isNaN(date.getTime())) return String(value);
    return new Intl.DateTimeFormat(locale, { timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(date);
  }
  function localeFetch(url, options) {
    options = Object.assign({}, options || {});
    var headers = new Headers(options.headers || {}); headers.set('Accept-Language', locale); options.headers = headers;
    return global.fetch(url, options);
  }
  global.IOSBKI18n = { register: register, t: t, has: has, message: message, tree: tree, apply: apply, getLocale: function () { return locale; }, setLocale: setLocale, formatDate: formatDate, setTimezone: setTimezone, getTimezone: function () { return timezone; }, fetch: localeFetch, readStorage: readStorage, writeStorage: writeStorage };
  persist();
})(window);

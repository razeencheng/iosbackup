'use strict';
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');
const {boot, dom, pageScript, apiCatalog, root} = require('./i18n-test-helpers');

function deferred() { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no; }); return {promise, resolve, reject}; }
function response(data, ok = true) { return {ok, json: async () => data}; }
function setup(locale = 'en') {
  const app = boot({stored: {iosbk_lang: locale}}), nodes = dom(app), requests = [], timers = new Map();
  let now = 1000, timerId = 0;
  app.api.register(apiCatalog);
  app.window.fetch = (url, options) => { const pending = deferred(); requests.push({url, options, ...pending}); return pending.promise; };
  const setTimer = (fn, delay) => { const id = ++timerId; timers.set(id, {fn, at: now + delay}); return id; };
  const clearTimer = id => timers.delete(id);
  class ClockDate extends Date { constructor(...args) { super(...(args.length ? args : [now])); } static now() { return now; } }
  Object.assign(app.context, {Date: ClockDate, setTimeout: setTimer, clearTimeout: clearTimer});
  Object.assign(app.window, {setTimeout: setTimer, clearTimeout: clearTimer});
  nodes.get('toast').classList.add('hidden');
  nodes.get('testType').value = 'backup_start';
  vm.runInContext(pageScript('notifications.html'), app.context);
  const evaluate = text => vm.runInContext(text, app.context);
  const settle = async () => { for (let i = 0; i < 8; i++) await Promise.resolve(); };
  const advance = amount => { now += amount; for (const [id, timer] of [...timers]) if (timer.at <= now) { timers.delete(id); timer.fn(); } };
  return {app, nodes, requests, timers, evaluate, settle, advance};
}
async function loaded(subject, config = {}) {
  const promise = subject.evaluate('loadConfig()');
  subject.requests.at(-1).resolve(response({enabled: true, language: 'zh-CN', ...config}));
  await promise; await subject.settle();
}

function changeNotificationLanguage(subject, value) {
  const html = fs.readFileSync(path.join(root, 'templates/notifications.html'), 'utf8');
  const handler = html.match(/<select id="notificationLanguage"[^>]*onchange="([^"]+)"/)[1];
  subject.nodes.get('notificationLanguage').value = value;
  subject.evaluate('(function(){' + handler + '}).call(document.getElementById("notificationLanguage"))');
}

async function run() {
  for (const locale of ['en', 'zh-CN']) {
    const subject = setup(locale), {app, nodes, requests, evaluate} = subject;
    evaluate('testNotification()');
    assert.equal(requests.length, 0, 'initial configuration must load before a test can be sent');
    assert.equal(nodes.get('notifTestBtn').disabled, true);
    await loaded(subject);
    assert.equal(nodes.get('notifTestBtn').disabled, false);
    for (const mutation of ["cfg.language='en';markNotifDirty()", "setTemplate('backup_start','User custom template')", "addEntry('email_configs','mail')"]) {
      evaluate(mutation); const before = requests.length;
      evaluate('testNotification()');
      assert.equal(requests.length, before, 'unsaved language, template and channel changes must block tests');
      assert.equal(nodes.get('notifTestHint').textContent, app.api.t('notif.saveBeforeTest'));
      assert.equal(nodes.get('toastMsg').textContent, app.api.t('notif.saveBeforeTest'));
      app.api.setLocale(locale === 'en' ? 'zh-CN' : 'en');
      assert.equal(nodes.get('notifTestBtn').disabled, true, 'changing interface language must not enable a test');
      await loaded(subject);
    }
  }

  {
    const s = setup(); await loaded(s);
    s.app.api.setLocale('zh-CN');
    assert.ok(s.nodes.get('rules').innerHTML.includes(s.app.api.t('notif.none')), 'empty channel rules also change language');
  }

  {
    const s = setup();
    const load = s.evaluate('loadConfig()'); s.requests[0].reject(Error('offline')); await load; await s.settle();
    assert.equal(s.nodes.get('notifTestBtn').disabled, true, 'failed initial load must remain unavailable');
    assert.equal(s.nodes.get('notifTestHint').textContent, s.app.api.t('notif.loadFailed'));
    await loaded(s);
    assert.equal(s.nodes.get('notifTestBtn').disabled, false, 'successful retry restores test availability');
    const test = s.evaluate('testNotification()');
    assert.deepEqual(JSON.parse(s.requests.at(-1).options.body), {message_type: 'backup_start'}, 'test uses the server saved configuration');
    s.requests.at(-1).resolve(response({attempted: 1, succeeded: 1, failed: 0})); await test;
  }

  {
    const s = setup(); await loaded(s);
    s.evaluate("setTemplate('backup_success','Draft content')");
    const save = s.evaluate('saveConfig()'); const request = s.requests.at(-1);
    const before = s.requests.length;
    s.evaluate('testNotification();saveConfig();reloadConfig()');
    assert.equal(s.requests.length, before, 'saving blocks test, duplicate save and competing reload');
    assert.equal(s.nodes.get('notifTestHint').textContent, s.app.api.t('notif.saving'));
    request.resolve(response({error: 'failed', message_code: 'api.operation_failed'}, false)); await save;
    assert.equal(s.evaluate('cfg.notification_templates.backup_success'), 'Draft content');
    assert.equal(s.evaluate('notifDirty'), true, 'save failure preserves the draft');
    assert.equal(s.nodes.get('notifTestBtn').disabled, true);
  }

  {
    const s = setup(); await loaded(s);
    s.evaluate("setTemplate('backup_success','Saved draft')");
    const save = s.evaluate('saveConfig()'), write = s.requests.at(-1);
    assert.equal(s.nodes.get('notifSettings').disabled, false, 'saving with an accepted baseline still allows newer edits');
    s.evaluate("setTemplate('backup_success','Newer draft');setNotificationLanguage('en')");
    write.resolve(response({success: true})); await s.settle();
    const readback = s.requests.at(-1);
    assert.notEqual(readback, write, 'successful save confirms the persisted configuration');
    readback.resolve(response({enabled: true, language: 'zh-CN', notification_templates: {backup_success: 'Saved draft'}}));
    await save;
    assert.equal(s.evaluate('cfg.notification_templates.backup_success'), 'Newer draft');
    assert.equal(s.evaluate('cfg.language'), 'en');
    assert.equal(s.evaluate('notifDirty'), true, 'editing while saving remains dirty');
    assert.equal(s.nodes.get('notifTestBtn').disabled, true);
    const nextSave = s.evaluate('saveConfig()'); s.requests.at(-1).resolve(response({success: true})); await s.settle();
    s.requests.at(-1).resolve(response({enabled: true, language: 'en', notification_templates: {backup_success: 'Newer draft'}})); await nextSave;
    assert.equal(s.evaluate('notifDirty'), false);
    assert.equal(s.nodes.get('notifTestBtn').disabled, false);
  }

  {
    const s = setup(); await loaded(s);
    const old = s.evaluate('loadConfig()'), oldRequest = s.requests.at(-1);
    const latest = s.evaluate('loadConfig()'), latestRequest = s.requests.at(-1);
    latestRequest.resolve(response({enabled: true, language: 'en'})); await latest;
    oldRequest.resolve(response({enabled: true, language: 'zh-CN'})); await old;
    assert.equal(s.evaluate('cfg.language'), 'en', 'out of order GET must not overwrite the latest settings');
    const loading = s.evaluate('loadConfig()');
    s.evaluate("setTemplate('backup_start','Typed during reload')");
    s.requests.at(-1).resolve(response({enabled: true, language: 'zh-CN'})); await loading;
    assert.equal(s.evaluate('cfg.notification_templates.backup_start'), 'Typed during reload');
    assert.equal(s.evaluate('notifDirty'), true);
  }

  {
    const s = setup(); await loaded(s);
    s.evaluate("setTemplate('backup_failed','Draft A')");
    const save = s.evaluate('saveConfig()');
    s.evaluate("setTemplate('backup_failed','Newer draft B')");
    s.requests.at(-1).resolve(response({success: true})); await s.settle();
    s.requests.at(-1).reject(Error('readback offline')); await save;
    assert.equal(s.evaluate('notifDirty'), true, 'failed readback must not certify the draft as saved');
    assert.equal(s.evaluate('cfg.notification_templates.backup_failed'), 'Newer draft B');
    const count = s.requests.length; s.evaluate('testNotification()');
    assert.equal(s.requests.length, count, 'readback failure keeps testing blocked');
    assert.equal(s.nodes.get('notifSaveBtn').disabled, false, 'failed confirmation keeps newer drafts saveable');
    assert.equal(s.nodes.get('notifSettings').disabled, false, 'accepted baseline remains editable after confirmation fails');
    assert.equal(s.nodes.get('notifTestHint').textContent, s.app.api.t('notif.confirmFailed'));
    assert.equal(s.nodes.get('toastMsg').textContent, s.app.api.t('notif.confirmFailed'));
    const retry = s.evaluate('saveConfig()');
    assert.equal(JSON.parse(s.requests.at(-1).options.body).notification_templates.backup_failed, 'Newer draft B', 'retry submits the retained newer draft');
    s.requests.at(-1).resolve(response({success: true})); await s.settle();
    assert.equal(s.nodes.get('notifTestBtn').disabled, true, 'retry still waits for saved confirmation before testing');
    s.requests.at(-1).resolve(response({enabled: true, language: 'zh-CN', notification_templates: {backup_failed: 'Newer draft B'}})); await retry;
    assert.equal(s.evaluate('cfg.notification_templates.backup_failed'), 'Newer draft B');
    assert.equal(s.evaluate('notifDirty'), false);
    assert.equal(s.nodes.get('notifTestBtn').disabled, false, 'successful retry confirmation restores testing');
  }

  {
    const s = setup(); await loaded(s);
    const save = s.evaluate('saveConfig()');
    s.requests.at(-1).resolve(response({success: true})); await s.settle();
    s.requests.at(-1).reject(Error('readback offline')); await save;
    assert.equal(s.evaluate('notifDirty'), false, 'unchanged settings can also require save confirmation');
    const count = s.requests.length; await s.evaluate('testNotification()');
    assert.equal(s.requests.length, count, 'failed confirmation alone blocks testing even without draft edits');
    assert.equal(s.nodes.get('notifTestBtn').disabled, true);
    assert.equal(s.nodes.get('notifSaveBtn').disabled, false);
    s.app.api.setLocale('zh-CN');
    assert.equal(s.nodes.get('notifTestHint').textContent, s.app.api.t('notif.confirmFailed'));
    assert.equal(s.nodes.get('toastMsg').textContent, s.app.api.t('notif.confirmFailed'));
  }

  {
    const s = setup();
    const initial = s.evaluate('loadConfig()');
    const existing = {
      enabled: true, language: 'zh-CN',
      email_configs: [{name: 'Existing mail', enabled: true, configured: true, smtp_host: 'smtp.example.com', smtp_port: 587, from: 'from@example.com', to: 'to@example.com'}],
      notification_templates: {backup_start: 'Existing ${device_name} template'},
      notification_rules: {backup_start: ['Email_Existing mail']}
    };
    changeNotificationLanguage(s, 'en');
    s.requests.at(-1).resolve(response(existing)); await initial;
    assert.deepEqual(JSON.parse(s.evaluate('JSON.stringify(cfg.email_configs)')), existing.email_configs, 'initial language interaction cannot discard existing channels');
    assert.deepEqual(JSON.parse(s.evaluate('JSON.stringify(cfg.notification_templates)')), existing.notification_templates, 'initial load retains saved templates');
    assert.deepEqual(JSON.parse(s.evaluate('JSON.stringify(cfg.notification_rules)')), existing.notification_rules, 'initial load retains saved rules');
    assert.equal(s.evaluate('cfg.language'), 'zh-CN', 'editing starts only after accepting the initial configuration');
    assert.equal(s.evaluate('notifDirty'), false);
    assert.equal(s.nodes.get('notifSettings').disabled, false);
    changeNotificationLanguage(s, 'en');
    const save = s.evaluate('saveConfig()'), request = s.requests.at(-1);
    const body = JSON.parse(request.options.body);
    assert.equal(body.language, 'en');
    assert.deepEqual(body.email_configs, existing.email_configs, 'a language-only save keeps existing channels');
    assert.deepEqual(body.notification_templates, existing.notification_templates);
    assert.deepEqual(body.notification_rules, existing.notification_rules);
    request.resolve(response({success: true})); await s.settle();
    s.requests.at(-1).resolve(response({...existing, language: 'en'})); await save;
    const old = s.evaluate('loadConfig()'), oldRequest = s.requests.at(-1);
    await loaded(s, {language: 'en'});
    oldRequest.reject(Error('stale failure')); await old;
    assert.equal(s.nodes.get('notifTestBtn').disabled, false, 'stale failure cannot invalidate the latest successful load');
    assert.equal(s.evaluate('cfg.language'), 'en');
  }

  {
    const s = setup(), baseline = s.evaluate('JSON.stringify(cfg)');
    const initial = s.evaluate('loadConfig()');
    assert.equal(s.nodes.get('notifSettings').disabled, true, 'all settings are disabled before accepting a baseline');
    s.evaluate("toggleMaster();addEntry('email_configs','Premature');removeEntry('email_configs',0);toggleEntry('email_configs',0);setField('email_configs',0,'name','Premature',false);clearEntrySecret('email_configs',0);toggleRule('backup_start','Email_Premature');setTemplate('backup_start','Premature')");
    assert.equal(s.evaluate('JSON.stringify(cfg)'), baseline, 'configuration mutation entry points reject changes without a baseline');
    assert.equal(s.evaluate('notifDirty'), false);
    s.requests.at(-1).reject(Error('initial load failed')); await initial;
    assert.equal(s.nodes.get('notifSettings').disabled, true, 'failed initial load keeps settings disabled');
    changeNotificationLanguage(s, 'en');
    assert.equal(s.evaluate('JSON.stringify(cfg)'), baseline);
    await loaded(s);
    assert.equal(s.nodes.get('notifSettings').disabled, false);
  }

  {
    const s = setup(), initial = s.evaluate('loadConfig()');
    s.evaluate('markNotifDirty()');
    s.requests.at(-1).resolve(response({enabled: true, email_configs: [{name: 'Saved mail'}]})); await initial;
    assert.equal(s.evaluate('notifLoaded'), false, 'discarded initial response never certifies an empty baseline');
    const count = s.requests.length; await s.evaluate('saveConfig()');
    assert.equal(s.requests.length, count, 'saving remains blocked until a response is actually accepted');
    assert.equal(s.nodes.get('notifSaveBtn').disabled, true);
  }

  for (const locale of ['en', 'zh-CN']) {
    const s = setup(locale); await loaded(s, {email_configs: [{name: 'Mail', password: '', enabled: true}]});
    s.evaluate("setField('email_configs',0,'password','用户 Secret {name}',true);setTemplate('backup_start','原样 ${title}')");
    const password = s.nodes.get('password-input'); password.type = 'text'; password.value = '用户 Secret {name}';
    let channelRebuilds = 0;
    Object.defineProperty(s.nodes.get('channels'), 'innerHTML', {get: () => '', set() { channelRebuilds++; password.type = 'password'; password.value = ''; }});
    s.evaluate("toast('notif.saved')");
    s.advance(1000);
    const oldTimer = [...s.timers.values()][0].fn;
    s.app.api.setLocale(locale === 'en' ? 'zh-CN' : 'en');
    assert.equal(channelRebuilds, 0, 'locale changes keep input nodes, focus and password visibility');
    assert.equal(password.type, 'text'); assert.equal(password.value, '用户 Secret {name}');
    assert.equal(s.evaluate('cfg.notification_templates.backup_start'), '原样 ${title}');
    assert.equal(s.evaluate('cfg.language'), 'zh-CN', 'interface choice does not change notification language');
    assert.equal(s.nodes.get('toast').classList.contains('hidden'), false);
    assert.equal(s.nodes.get('toastMsg').textContent, s.app.api.t('notif.saved'));
    s.advance(1599); assert.equal(s.nodes.get('toast').classList.contains('hidden'), false);
    s.advance(1); assert.equal(s.nodes.get('toast').classList.contains('hidden'), true, 'locale switch keeps original expiry');
    s.app.api.setLocale(locale); assert.equal(s.nodes.get('toast').classList.contains('hidden'), true, 'expired toast stays closed');
    s.evaluate("toast({message_code:'api.notification_name_duplicate',params:{channel:'Email',name:'用户 {channel}'}},true)");
    oldTimer(); assert.equal(s.nodes.get('toast').classList.contains('hidden'), false, 'old timer cannot close a new toast');
    s.app.api.setLocale(locale === 'en' ? 'zh-CN' : 'en');
    assert.match(s.nodes.get('toastMsg').textContent, /用户 \{channel\}/, 'user message parameters stay literal');
    assert.equal(s.nodes.get('toastIcon').style.color, '#FF8A80');
  }
  console.log('notification saved configuration, draft races and localized feedback checks passed');
}
module.exports = run;
if (require.main === module) run().catch(error => { console.error(error); process.exitCode = 1; });

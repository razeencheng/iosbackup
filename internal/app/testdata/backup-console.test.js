'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const window = { document: { addEventListener() {} }, addEventListener() {} };
vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../static/backup-console.js'), 'utf8'), { window });
const api = window.IOSBKConsole;
const config = { start_time: '22:00', end_time: '06:00', backup_interval: 24, min_battery_level: 20, only_when_charging: true, auto_backup_enabled: false, backup_directory: '/backups', network_address: '' };
assert.deepEqual(Object.keys(api.configPayload('connection', config)).sort(), ['backup_directory', 'network_address']);
assert.equal(api.configPayload('schedule', config).backup_directory, undefined, 'schedule save cannot overwrite another group');
assert.equal(api.configPayload('schedule', config).auto_backup_enabled, false, 'false must survive partial updates');
assert.throws(() => api.configPayload('unknown', config));
const jobs = [
  { id: 'a', operation: 'backup', state: 'succeeded', history_state: 'failed' },
  { id: 'b', operation: 'check', state: 'succeeded', check: { state: 'unavailable' } },
  { id: 'c', state: 'failed' },
  { id: 'd', operation: 'backup', state: 'succeeded', history_state: 'saved', check: { state: 'passed' } },
];
assert.equal(api.filterJobs(jobs, 'check', 'attention')[0].id, 'b', 'successful execution is not the same as a completed check');
assert.equal(api.filterJobs(jobs, 'backup', 'attention').length, 2, 'history failure must remain visible');
assert.equal(api.filterJobs(jobs, 'all', 'succeeded').length, 3, 'execution filter does not silently redefine tool success');
assert.equal(api.isCurrentPoint({ current_source: { kind: 'point', point_id: 'new', available: true } }, { id: 'old', baseline: true }), false, 'an old-generation baseline is not the current source');
assert.equal(api.isCurrentPoint({ current_source: { kind: 'current', available: true } }, { id: 'new', baseline: true }), false);
assert.equal(api.isCurrentPoint({ current_source: { kind: 'point', point_id: 'new', available: true } }, { id: 'new' }), true);
console.log('console group saves, task filtering and current-source identity passed');

// A connection result belongs to the draft that started it, including after
// cancel restores the same value and a second request is started.
(async () => {
  const pending = [], nodes = {}, results = {};
  ['backupDir-A','networkAddress-A','dirResult-A','ipResult-A','testIPBtn-A'].forEach(id => {
    nodes[id] = {id, value:'', textContent:'', disabled:false, style:{}, classList:{add(){},remove(){}}};
  });
  const context = {
    document:{getElementById:id=>nodes[id]}, inlineResults:results,
    showInlineResult(node,ok,payload){results[node.id]={ok,payload};node.textContent=payload;},
    postJSON(){return new Promise(resolve=>pending.push(resolve));}, applyStatus(){}
  };
  const template=fs.readFileSync(path.join(__dirname,'../templates/index.html'),'utf8');
  vm.runInNewContext(template.slice(template.indexOf('/* ============== backup settings'),template.indexOf('function pairingStatusText')),context);
  nodes['backupDir-A'].value='/draft';context.checkBackupDir('A');
  assert.equal(typeof context.clearConnectionResults,'function','cancel must invalidate pending diagnostics');
  context.clearConnectionResults('A');nodes['backupDir-A'].value='/backups';
  pending.shift()({ok:true});await new Promise(setImmediate);
  assert.equal(results['dirResult-A'],undefined,'cancelled directory check cannot reappear');
  nodes['networkAddress-A'].value='10.0.0.1';context.testDeviceIP('A');
  context.clearConnectionResults('A');context.testDeviceIP('A');
  pending.shift()({ok:true});await new Promise(setImmediate);
  assert.equal(nodes['testIPBtn-A'].disabled,true,'old completion cannot unlock the new request');
  pending.shift()({ok:true});await new Promise(setImmediate);
  assert.equal(results['ipResult-A'].payload,'bset.testOk');
  assert.equal(nodes['testIPBtn-A'].disabled,false);
  console.log('cancelled connection checks cannot publish stale results');
})().catch(error=>{console.error(error);process.exitCode=1});

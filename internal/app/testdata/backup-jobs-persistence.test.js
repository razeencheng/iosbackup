'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const {test}=require('node:test');
const {boot,dom,catalogs,root}=require('./i18n-test-helpers');

for(const locale of ['en','zh-CN'])test('live task persistence does not flash a notice; final pending and errors remain visible '+locale,async()=>{
  const app=boot(),nodes=dom(app),id='bkp-000000000001';
  app.api.setLocale(locale);
  const append=function(...children){children.forEach(child=>this.appendChild(child));};
  app.document.createElement=()=>Object.assign(nodes.node(),{append});
  app.document.getElementById=id=>Object.assign(nodes.get(id),{append});
  nodes.get('jobDetails-PHONE').closest=()=>null;
  app.window.IOSBKConsole={needsAttention:()=>false};
  let job={id,state:'running',persistence:'pending',completed_at:'0001-01-01T00:00:00Z'};
  app.window.fetch=url=>Promise.resolve({ok:true,json:()=>Promise.resolve(url.startsWith('/api/backup-jobs/')?{jobs:[job],recording_status:'available'}:{job,events:[]})});
  vm.runInContext(fs.readFileSync(path.join(root,'static/backup-jobs.js'),'utf8'),app.context);
  const api=app.window.IOSBKJobs;
  await api.open('PHONE');
  for(const persistence of ['pending','saved','pending','saved']){
    job={...job,persistence};await api.select(id);
    for(const prefix of ['jobWarning-','jobDetailWarning-']){
      assert.equal(nodes.get(prefix+'PHONE').classList.contains('hidden'),true,'normal live log writes must not toggle a notice');
    }
  }
  job={...job,persistence:'error'};await api.select(id);
  assert.equal(nodes.get('jobWarning-PHONE').classList.contains('hidden'),false);
  assert.equal(nodes.get('jobWarning-PHONE').textContent,catalogs[locale]['jobs.saveError']);
  job={...job,state:'succeeded',completed_at:'2026-11-04T14:35:00Z',persistence:'pending'};await api.select(id);
  assert.equal(nodes.get('jobDetailWarning-PHONE').textContent,catalogs[locale]['jobs.saving']);
  assert.equal(nodes.get('jobDetailWarning-PHONE').classList.contains('hidden'),false);
  job={...job,persistence:'saved'};await api.select(id);
  assert.equal(nodes.get('jobDetailWarning-PHONE').classList.contains('hidden'),true);
  api.close('PHONE');
});

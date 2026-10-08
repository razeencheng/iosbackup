package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

func TestPairingStatusAvailableWithoutEventStream(t *testing.T) {
	app := newApplication()
	app.devices["PHONE"] = &device{UDID: "PHONE", IsOnline: true}
	app.setPairingState("PHONE", pairingStateFailed, "device_unavailable", "当前连接中找不到设备")
	w := httptest.NewRecorder()
	app.handleStatus(w, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	var response struct {
		Devices []struct {
			UDID             string `json:"udid"`
			PairingState     string `json:"pairing_state"`
			PairingErrorCode string `json:"pairing_error_code"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Devices) != 1 || response.Devices[0].PairingState != pairingStateFailed || response.Devices[0].PairingErrorCode != "device_unavailable" {
		t.Fatalf("暂停实时更新后仍需读取配对结果: %s", w.Body.String())
	}
}

func TestPairingPageShowsAsyncResult(t *testing.T) {
	html := renderIndex(t, homePayload{
		Devices: []*device{{UDID: "PHONE", Name: "Phone", IsOnline: true}},
		Configs: map[string]*backupConfig{"PHONE": {UDID: "PHONE"}},
	})
	if !strings.Contains(html, `id="pairingResult-PHONE"`) {
		t.Fatal("手动配对需要持续显示异步结果的区域")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js 不可用")
	}
	start := strings.Index(html, "function pairingStatusText(")
	end := strings.Index(html, "function gateMsg(")
	if start < 0 || end <= start {
		t.Fatal("缺少配对状态渲染")
	}
	program := `const assert=require('node:assert/strict');
let lang='zh';function L(zh,en){return lang==='zh'?zh:en;}
const latestPairingStatuses={};
const autoRefreshEnabled=false;
const box={textContent:'',style:{},classList:{toggle(){}}};
const button={disabled:false};
const document={getElementById(id){return id.startsWith('pairingResult-')?box:button;}};
const toasts=[];function toast(text){toasts.push(text);}
function csrfFetch(){return Promise.resolve({json:()=>Promise.resolve({pairing_state:'checking'})});}
function fetch(){return Promise.resolve({ok:true,json:()=>Promise.resolve({devices:[{udid:'PHONE',pairing_state:'failed',pairing_error_code:'device_unavailable',pairing_error:'当前连接中找不到设备，请确认设备连接后重试。'}]})});}
` + html[start:end] + `
(async()=>{
 const pending=pairDevice('PHONE');
 assert.ok(box.textContent.includes('检查'));
 await pending;
 assert.ok(!toasts.some(t=>t.includes('信任')),'接纳请求不等于正在等待信任');
 assert.ok(box.textContent.includes('找不到设备'));
 assert.equal(button.disabled,false);
 renderPairingResult({udid:'PHONE',pairing_state:'failed',pairing_error_code:'pairing_required',pairing_error:'请通过 USB 重新配对'});
 assert.ok(box.textContent.includes('USB'));
 renderPairingResult({udid:'PHONE',pairing_state:'paired'});
 assert.ok(box.textContent.includes('配对正常'));
 lang='en';
 assert.ok(pairingStatusText({pairing_state:'failed',pairing_error_code:'device_unavailable'}).includes('not found'));
})().catch(e=>{console.error(e);process.exitCode=1;});
`
	cmd := exec.Command(node)
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("配对异步反馈错误: %v %s", err, output)
	}
}

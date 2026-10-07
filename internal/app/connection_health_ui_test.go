package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestConnectionStatusAPIsExposeUnknownPresence(t *testing.T) {
	app := newApplication()
	app.devices["PHONE"] = &device{UDID: "PHONE", Name: "Phone", IsOnline: true, Connection: connectionTypeDesc(connectTypeUSB)}
	app.cmdRunner = func(context.Context, string, []string, []string) ([]byte, error) {
		return nil, errors.New("service broken")
	}
	app.refreshPresence()
	raw, _ := json.Marshal(app.buildStatusSnapshot())
	var snapshot map[string]json.RawMessage
	_ = json.Unmarshal(raw, &snapshot)
	if len(snapshot["connection_services"]) == 0 {
		t.Fatal("SSE 缺少连接服务状态")
	}
	var devices []map[string]interface{}
	_ = json.Unmarshal(snapshot["devices"], &devices)
	if len(devices) != 1 || devices[0]["presence_known"] != false || devices[0]["operations_available"] != false || devices[0]["conn"] != "unknown" {
		t.Fatalf("未知状态不应误报已确认在线: %s", raw)
	}
	w := httptest.NewRecorder()
	app.handleStatus(w, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	var status map[string]json.RawMessage
	_ = json.Unmarshal(w.Body.Bytes(), &status)
	if string(status["connection_services"]) != string(snapshot["connection_services"]) {
		t.Fatalf("HTTP 与 SSE 服务状态不同: %s / %s", status["connection_services"], snapshot["connection_services"])
	}
}

func TestConnectionPairRequestRejectedBeforeChangingState(t *testing.T) {
	app := newApplication()
	app.devices["PHONE"] = &device{UDID: "PHONE", IsOnline: true, PresenceUnknown: true, Connection: connectionTypeDesc(connectTypeUSB)}
	app.deviceOperationStates["PHONE"] = deviceOperationState{PairingState: pairingStateFailed, PairingErrorCode: "old_error"}
	w := httptest.NewRecorder()
	app.handlePairDevice(w, httptest.NewRequest(http.MethodPost, "/api/pair/PHONE", nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("服务状态未知应同步拒绝配对，实际 %d", w.Code)
	}
	if app.deviceOperationStates["PHONE"].PairingErrorCode != "old_error" {
		t.Fatal("未执行验证不能清除旧配对错误")
	}
}

func TestConnectionObservationTimesDoNotForceSSE(t *testing.T) {
	app := newApplication()
	app.hub = newEventHub()
	ch := app.hub.Subscribe()
	defer app.hub.Unsubscribe(ch)
	now := nowBeijing()
	app.publishConnectionScan(0, connectionScan{sequence: 1, completed: now, devices: map[string]*device{}})
	<-ch
	app.publishConnectionScan(0, connectionScan{sequence: 2, completed: now.Add(4 * time.Second), devices: map[string]*device{}})
	select {
	case <-ch:
		t.Fatal("只更新时间不能强制每轮 SSE 推送")
	default:
	}
}

func TestConnectionPageMessagesAndJavaScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js 不可用")
	}
	html := renderIndex(t, homePayload{InitialStatus: statusSnapshot{ConnectionServices: []connectionServiceDTO{{Connection: "usb", Health: "unhealthy", Recovery: "waiting_busy"}}}})
	start := strings.LastIndex(html, "<script>")
	end := strings.Index(html[start:], "</script>") + start
	script := html[start+len("<script>") : end]
	syntax := exec.Command(node, "--check")
	syntax.Stdin = strings.NewReader(script)
	if output, err := syntax.CombinedOutput(); err != nil {
		t.Fatalf("页面脚本语法错误: %v %s", err, output)
	}
	start = strings.Index(script, "function connectionServiceMessage(")
	end = strings.Index(script[start:], "\nfunction renderConnectionHealth") + start
	program := `let lang='zh';function L(zh,en){return lang==='zh'?zh:en;}` + script[start:end] + `
const assert=require('node:assert/strict');
for(const [phase,fragment] of [['waiting_busy','等待当前设备任务'],['restarting','正在恢复'],['backoff','稍后自动重试'],['manual_required','自动恢复失败']]) {
 const result=connectionServiceMessage({connection:'usb',health:'unhealthy',recovery:phase});
 assert.ok(result.includes(fragment),result);
 assert.ok(!result.includes('信任')&&!result.includes('解锁'),result);
}
assert.equal(connectionServiceMessage({connection:'usb',health:'healthy',recovery:'idle'}),'');
lang='en';assert.ok(connectionServiceMessage({connection:'wifi',health:'unhealthy',recovery:'waiting_busy'}).includes('current device tasks'));
`
	cmd := exec.Command(node)
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("服务状态提示错误: %v %s", err, output)
	}
}

func TestConnectionOnboardingPrioritizesServiceFailure(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js 不可用")
	}
	source, err := templateFS.ReadFile("static/onboarding.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(source)
	start := strings.Index(script, "function connectionProblem()")
	if start < 0 {
		t.Fatal("向导缺少服务异常优先提示")
	}
	end := strings.Index(script[start:], "\n  function selectedDevice") + start
	program := `let lang='zh';let snapshot={connection_services:[{connection:'usb',health:'unhealthy',recovery:'waiting_busy'}]};` + script[start:end] + `
const assert=require('node:assert/strict');assert.ok(connectionProblem().includes('等待当前设备任务'));
snapshot.connection_services[0].recovery='restarting';assert.ok(connectionProblem().includes('正在恢复'));
snapshot.connection_services[0]={connection:'usb',health:'healthy',recovery:'idle'};assert.equal(connectionProblem(),'');
`
	cmd := exec.Command(node)
	cmd.Stdin = strings.NewReader(program)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("向导健康提示错误: %v %s", err, output)
	}
}

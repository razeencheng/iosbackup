package notification

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recordingNotifier struct {
	name      string
	enabled   bool
	validate  error
	sendErr   error
	started   chan struct{}
	unblock   <-chan struct{}
	startOnce sync.Once
	count     atomic.Int64
	last      atomic.Pointer[Message]
}

func (n *recordingNotifier) Send(message *Message) error {
	n.count.Add(1)
	copy := CloneMessage(*message)
	n.last.Store(&copy)
	if n.started != nil {
		n.startOnce.Do(func() { close(n.started) })
	}
	if n.unblock != nil {
		<-n.unblock
	}
	return n.sendErr
}

func (n *recordingNotifier) GetName() string { return n.name }
func (n *recordingNotifier) IsEnabled() bool { return n.enabled }
func (n *recordingNotifier) Validate() error { return n.validate }

func TestCloneMessageAlwaysOwnsWritableExtraFields(t *testing.T) {
	emptyClone := CloneMessage(Message{})
	if emptyClone.ExtraFields == nil {
		t.Fatal("CloneMessage must preserve the base contract of a writable non-nil ExtraFields map")
	}
	emptyClone.ExtraFields["custom"] = "value"

	caller := Message{ExtraFields: map[string]string{"reason": "original"}}
	clone := CloneMessage(caller)
	clone.ExtraFields["reason"] = "clone-mutated"
	clone.ExtraFields["custom"] = "safe"
	if caller.ExtraFields["reason"] != "original" {
		t.Fatalf("clone mutation leaked to caller: %+v", caller.ExtraFields)
	}
	caller.ExtraFields["reason"] = "caller-mutated"
	if clone.ExtraFields["reason"] != "clone-mutated" {
		t.Fatalf("caller mutation leaked to clone: %+v", clone.ExtraFields)
	}
}

func TestManagerFiltersRulesAndContinuesAfterNotifierFailure(t *testing.T) {
	var logsMu sync.Mutex
	var logs []string
	now := time.Date(2026, 8, 31, 19, 20, 0, 0, time.FixedZone("CST", 8*60*60))
	manager := NewManager(Options{
		Now: func() time.Time { return now },
		Logf: func(format string, args ...any) {
			logsMu.Lock()
			defer logsMu.Unlock()
			logs = append(logs, fmt.Sprintf(format, args...))
		},
	})
	defer manager.Close()

	failing := &recordingNotifier{name: "fail", enabled: true, sendErr: errors.New("delivery failed")}
	success := &recordingNotifier{name: "ok", enabled: true}
	disabled := &recordingNotifier{name: "disabled", enabled: false}
	unselected := &recordingNotifier{name: "not-in-rule", enabled: true}
	for _, notifier := range []Notifier{failing, success, disabled, unselected} {
		if err := manager.AddNotifier(notifier); err != nil {
			t.Fatal(err)
		}
	}
	manager.SetRules(map[string][]string{string(BackupSuccess): {"fail", "ok", "disabled"}})

	result := manager.SendAndWait(context.Background(), &Message{
		Type: BackupSuccess, Title: "完成", ExtraFields: map[string]string{"reason": "original"},
	})
	if result.Attempted != 2 || result.Succeeded != 1 || result.Failed != 1 {
		t.Fatalf("best-effort fan-out result = %+v", result)
	}
	if disabled.count.Load() != 0 {
		t.Fatal("disabled notifier received a message")
	}
	if unselected.count.Load() != 0 {
		t.Fatal("notifier omitted from the rule received a message")
	}
	if got := success.last.Load(); got == nil || !got.Timestamp.Equal(now) {
		t.Fatalf("injected clock timestamp = %#v, want %s", got, now)
	}
	logsMu.Lock()
	joined := strings.Join(logs, "\n")
	logsMu.Unlock()
	if !strings.Contains(joined, "通知器 fail 发送失败") || !strings.Contains(joined, "通知器 ok 发送成功") {
		t.Fatalf("delivery logs missing: %q", joined)
	}
}

type lateReadNotifier struct {
	started  chan struct{}
	unblock  <-chan struct{}
	observed chan string
}

func (n *lateReadNotifier) Send(message *Message) error {
	close(n.started)
	<-n.unblock
	n.observed <- message.ExtraFields["reason"]
	return nil
}
func (n *lateReadNotifier) GetName() string { return "late-reader" }
func (n *lateReadNotifier) IsEnabled() bool { return true }
func (n *lateReadNotifier) Validate() error { return nil }

func TestAsyncSendOwnsExtraFieldsBeforeReturning(t *testing.T) {
	unblock := make(chan struct{})
	notifier := &lateReadNotifier{started: make(chan struct{}), unblock: unblock, observed: make(chan string, 1)}
	manager := NewManager(Options{WorkerCount: 1, QueueSize: 1})
	defer manager.Close()
	if err := manager.AddNotifier(notifier); err != nil {
		t.Fatal(err)
	}
	manager.SetRules(map[string][]string{string(BackupFailed): {"late-reader"}})
	message := &Message{Type: BackupFailed, ExtraFields: map[string]string{"reason": "original"}}
	manager.Send(message)
	select {
	case <-notifier.started:
	case <-time.After(time.Second):
		t.Fatal("notifier did not start")
	}
	message.ExtraFields["reason"] = "caller-mutated"
	close(unblock)
	if got := <-notifier.observed; got != "original" {
		t.Fatalf("caller mutation leaked into asynchronous delivery: %q", got)
	}
}

func TestManagerRejectsInvalidNotifierAndSkipsMissingRules(t *testing.T) {
	manager := NewManager(Options{})
	defer manager.Close()
	if err := manager.AddNotifier(&recordingNotifier{name: "bad", enabled: true, validate: errors.New("invalid")}); err == nil {
		t.Fatal("invalid notifier accepted")
	}
	ok := &recordingNotifier{name: "ok", enabled: true}
	if err := manager.AddNotifier(ok); err != nil {
		t.Fatal(err)
	}
	manager.Send(&Message{Type: BackupSuccess, Title: "no rule"})
	time.Sleep(20 * time.Millisecond)
	if ok.count.Load() != 0 {
		t.Fatal("message without a rule was delivered")
	}
	manager.Disable()
	manager.SetRules(map[string][]string{string(BackupSuccess): {"ok"}})
	manager.Send(&Message{Type: BackupSuccess, Title: "disabled manager"})
	time.Sleep(20 * time.Millisecond)
	if ok.count.Load() != 0 {
		t.Fatal("disabled manager delivered a message")
	}
}

type nilCapableNotifier struct{}

func (*nilCapableNotifier) Send(*Message) error { return nil }
func (*nilCapableNotifier) GetName() string     { return "typed-nil" }
func (*nilCapableNotifier) IsEnabled() bool     { return true }
func (*nilCapableNotifier) Validate() error     { return nil }

func TestAddNotifierRejectsTypedNil(t *testing.T) {
	manager := NewManager(Options{Logf: func(string, ...any) {}})
	defer manager.Close()
	var notifier *nilCapableNotifier
	if err := manager.AddNotifier(notifier); err == nil || !strings.Contains(err.Error(), "不能为空") {
		t.Fatalf("typed-nil notifier error = %v", err)
	}
	if got := len(manager.GetNotifiers()); got != 0 {
		t.Fatalf("typed-nil notifier was appended: %d", got)
	}
}

func TestAddNotifierLogsAfterUnlock(t *testing.T) {
	var manager *Manager
	logCalled := make(chan struct{}, 1)
	manager = NewManager(Options{Logf: func(string, ...any) {
		_ = manager.GetNotifiers()
		logCalled <- struct{}{}
	}})
	defer manager.Close()
	addDone := make(chan error, 1)
	go func() {
		addDone <- manager.AddNotifier(&recordingNotifier{name: "reentrant", enabled: true})
	}()
	select {
	case err := <-addDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("AddNotifier called Logf while holding manager.mu")
	}
	select {
	case <-logCalled:
	case <-time.After(time.Second):
		t.Fatal("AddNotifier did not invoke injected logger")
	}
}

func TestSendRestoresBaseNoAvailableNotifierLogging(t *testing.T) {
	var logsMu sync.Mutex
	var logs []string
	manager := NewManager(Options{Logf: func(format string, args ...any) {
		logsMu.Lock()
		logs = append(logs, fmt.Sprintf(format, args...))
		logsMu.Unlock()
	}})
	defer manager.Close()
	disabled := &recordingNotifier{name: "disabled", enabled: false}
	if err := manager.AddNotifier(disabled); err != nil {
		t.Fatal(err)
	}
	logsMu.Lock()
	logs = nil
	logsMu.Unlock()

	manager.SetRules(map[string][]string{string(BackupSuccess): {"disabled"}})
	manager.Send(&Message{Type: BackupSuccess})
	logsMu.Lock()
	withRule := strings.Join(logs, "\n")
	logs = nil
	logsMu.Unlock()
	if !strings.Contains(withRule, "消息类型 backup_success 没有可用通知器，跳过发送") {
		t.Fatalf("missing no-eligible-target log: %q", withRule)
	}

	manager.SetRules(nil)
	manager.Send(&Message{Type: BackupSuccess})
	logsMu.Lock()
	withoutRule := strings.Join(logs, "\n")
	logsMu.Unlock()
	if strings.Count(withoutRule, "跳过发送") != 2 || !strings.Contains(withoutRule, "没有配置通知规则或规则为空") || !strings.Contains(withoutRule, "没有可用通知器") {
		t.Fatalf("no-rule send did not preserve both base log entries: %q", withoutRule)
	}

	manager.SetRules(map[string][]string{string(BackupSuccess): {"disabled"}})
	manager.Disable()
	logsMu.Lock()
	logs = nil
	logsMu.Unlock()
	manager.Send(&Message{Type: BackupSuccess})
	logsMu.Lock()
	disabledManager := strings.Join(logs, "\n")
	logsMu.Unlock()
	if !strings.Contains(disabledManager, "消息类型 backup_success 没有可用通知器，跳过发送") {
		t.Fatalf("disabled manager lost the base no-available-notifier log: %q", disabledManager)
	}

	manager.Close()
	logsMu.Lock()
	logs = nil
	logsMu.Unlock()
	manager.Send(&Message{Type: BackupSuccess})
	logsMu.Lock()
	afterClose := strings.Join(logs, "\n")
	logsMu.Unlock()
	if strings.Count(afterClose, "消息类型 backup_success 没有可用通知器，跳过发送") != 1 {
		t.Fatalf("Send after Close lost the base no-available-notifier log: %q", afterClose)
	}
}

func TestManagerOwnsRulesTemplatesAndMessageCopies(t *testing.T) {
	manager := NewManager(Options{})
	defer manager.Close()
	notifier := &recordingNotifier{name: "copy", enabled: true}
	if err := manager.AddNotifier(notifier); err != nil {
		t.Fatal(err)
	}
	rules := map[string][]string{string(BackupFailed): {"copy"}}
	templates := map[string]string{string(BackupFailed): "${reason}"}
	if err := manager.SetTemplates(templates); err != nil {
		t.Fatal(err)
	}
	manager.SetRules(rules)
	rules[string(BackupFailed)][0] = "mutated"
	templates[string(BackupFailed)] = "mutated"

	message := &Message{Type: BackupFailed, ExtraFields: map[string]string{"reason": "disk full"}}
	result := manager.SendAndWait(context.Background(), message)
	message.ExtraFields["reason"] = "mutated"
	if result.Succeeded != 1 {
		t.Fatalf("copy delivery result = %+v", result)
	}
	if got := notifier.last.Load(); got == nil || got.Content != "disk full" || got.ExtraFields["reason"] != "disk full" {
		t.Fatalf("caller mutation leaked into delivery: %#v", got)
	}
	gotRules := manager.Rules()
	gotRules[string(BackupFailed)][0] = "mutated again"
	if manager.Rules()[string(BackupFailed)][0] != "copy" {
		t.Fatal("Rules returned manager-owned storage")
	}
	gotTemplates := manager.Templates()
	gotTemplates[string(BackupFailed)] = "mutated again"
	if manager.Templates()[string(BackupFailed)] != "${reason}" {
		t.Fatal("Templates returned manager-owned storage")
	}
}

func TestManagerQueueIsBoundedSendNeverBlocksAndCloseIsSafe(t *testing.T) {
	unblock := make(chan struct{})
	started := make(chan struct{})
	manager := NewManager(Options{WorkerCount: 1, QueueSize: 1})
	notifier := &recordingNotifier{name: "blocked", enabled: true, started: started, unblock: unblock}
	if err := manager.AddNotifier(notifier); err != nil {
		t.Fatal(err)
	}
	manager.SetRules(map[string][]string{string(BackupSuccess): {"blocked"}})
	manager.Send(&Message{Type: BackupSuccess})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	manager.Send(&Message{Type: BackupSuccess})
	returned := make(chan struct{})
	go func() {
		manager.Send(&Message{Type: BackupSuccess})
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Send blocked on a full queue")
	}
	if manager.Dropped() == 0 {
		t.Fatal("full queue did not increment Dropped")
	}
	close(unblock)
	manager.Close()
	manager.Close()
	var nilManager *Manager
	nilManager.Close()
	before := notifier.count.Load()
	manager.Send(&Message{Type: BackupSuccess})
	time.Sleep(20 * time.Millisecond)
	if notifier.count.Load() != before {
		t.Fatal("Send after Close was not ignored")
	}
}

type selectionGateNotifier struct {
	name    string
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
	active  atomic.Bool
}

func (n *selectionGateNotifier) Send(*Message) error { return nil }
func (n *selectionGateNotifier) GetName() string     { return n.name }
func (n *selectionGateNotifier) IsEnabled() bool {
	if !n.active.Load() {
		return true
	}
	n.once.Do(func() { close(n.entered) })
	<-n.release
	return true
}
func (n *selectionGateNotifier) Validate() error { return nil }

func TestSendSelectedBeforeCloseStillUsesBaseQueueSemantics(t *testing.T) {
	workerRelease := make(chan struct{})
	var releaseWorker sync.Once
	defer releaseWorker.Do(func() { close(workerRelease) })
	workerStarted := make(chan struct{})
	selectionRelease := make(chan struct{})
	manager := NewManager(Options{WorkerCount: 1, QueueSize: 1})
	blocker := &recordingNotifier{name: "blocker", enabled: true, started: workerStarted, unblock: workerRelease}
	queued := &recordingNotifier{name: "queued", enabled: true, unblock: workerRelease}
	selected := &selectionGateNotifier{name: "selected", entered: make(chan struct{}), release: selectionRelease}
	for _, notifier := range []Notifier{blocker, queued, selected} {
		if err := manager.AddNotifier(notifier); err != nil {
			t.Fatal(err)
		}
	}
	manager.SetRules(map[string][]string{string(BackupSuccess): {"blocker"}})
	manager.Send(&Message{Type: BackupSuccess})
	select {
	case <-workerStarted:
	case <-time.After(time.Second):
		t.Fatal("worker did not block")
	}
	manager.SetRules(map[string][]string{string(BackupSuccess): {"queued"}})
	manager.Send(&Message{Type: BackupSuccess})
	if got := len(manager.queue); got != 1 {
		t.Fatalf("failed to fill bounded queue: %d", got)
	}
	droppedBefore := manager.Dropped()
	selected.active.Store(true)
	manager.SetRules(map[string][]string{string(BackupSuccess): {"selected"}})
	done := make(chan struct{})
	go func() {
		manager.Send(&Message{Type: BackupSuccess})
		close(done)
	}()
	select {
	case <-selected.entered:
	case <-time.After(time.Second):
		t.Fatal("Send did not reach target selection")
	}
	closed := make(chan struct{})
	go func() {
		manager.Close()
		close(closed)
	}()
	deadline := time.Now().Add(time.Second)
	for !manager.closed.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !manager.closed.Load() {
		t.Fatal("Close did not publish closed state")
	}
	close(selectionRelease)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Send did not return")
	}
	if got := manager.Dropped(); got != droppedBefore+1 {
		t.Fatalf("a target selected before Close must still use queue/default; dropped=%d, want %d", got, droppedBefore+1)
	}
	releaseWorker.Do(func() { close(workerRelease) })
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not finish after notifier returned")
	}
}

func TestSendAndWaitSelectedBeforeCloseWaitsOnlyForCallerContext(t *testing.T) {
	workerRelease := make(chan struct{})
	var releaseWorker sync.Once
	defer releaseWorker.Do(func() { close(workerRelease) })
	workerStarted := make(chan struct{})
	manager := NewManager(Options{WorkerCount: 1, QueueSize: 1})
	blocker := &recordingNotifier{name: "blocker", enabled: true, started: workerStarted, unblock: workerRelease}
	queued := &recordingNotifier{name: "queued", enabled: true, unblock: workerRelease}
	selectionRelease := make(chan struct{})
	selected := &selectionGateNotifier{name: "selected", entered: make(chan struct{}), release: selectionRelease}
	for _, notifier := range []Notifier{blocker, queued, selected} {
		if err := manager.AddNotifier(notifier); err != nil {
			t.Fatal(err)
		}
	}
	manager.SetRules(map[string][]string{string(BackupSuccess): {"blocker"}})
	manager.Send(&Message{Type: BackupSuccess})
	select {
	case <-workerStarted:
	case <-time.After(time.Second):
		t.Fatal("worker did not block")
	}
	manager.SetRules(map[string][]string{string(BackupSuccess): {"queued"}})
	manager.Send(&Message{Type: BackupSuccess})
	if got := len(manager.queue); got != 1 {
		t.Fatalf("failed to fill bounded queue: %d", got)
	}

	manager.SetRules(map[string][]string{string(BackupSuccess): {"selected"}})
	selected.active.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	type timedResult struct {
		result  SendResult
		elapsed time.Duration
	}
	resultCh := make(chan timedResult, 1)
	go func() {
		started := time.Now()
		resultCh <- timedResult{result: manager.SendAndWait(ctx, &Message{Type: BackupSuccess}), elapsed: time.Since(started)}
	}()
	select {
	case <-selected.entered:
	case <-time.After(time.Second):
		t.Fatal("SendAndWait did not reach target selection")
	}
	closed := make(chan struct{})
	go func() {
		manager.Close()
		close(closed)
	}()
	deadline := time.Now().Add(time.Second)
	for !manager.closed.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !manager.closed.Load() {
		t.Fatal("Close did not publish closed state")
	}
	close(selectionRelease)
	timed := <-resultCh
	if timed.elapsed < 50*time.Millisecond {
		t.Fatalf("SendAndWait returned on manager Close instead of caller context after %s: %+v", timed.elapsed, timed.result)
	}
	if timed.result.Attempted != 0 || timed.result.Failed != 1 || len(timed.result.Deliveries) != 1 || timed.result.Deliveries[0].Error != context.DeadlineExceeded.Error() {
		t.Fatalf("caller-context result changed from base behavior: %+v", timed.result)
	}
	releaseWorker.Do(func() { close(workerRelease) })
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not finish after notifier returned")
	}
}

func TestSendAndWaitHonorsContextCancellation(t *testing.T) {
	unblock := make(chan struct{})
	started := make(chan struct{})
	manager := NewManager(Options{WorkerCount: 1, QueueSize: 1})
	notifier := &recordingNotifier{name: "blocked", enabled: true, started: started, unblock: unblock}
	if err := manager.AddNotifier(notifier); err != nil {
		t.Fatal(err)
	}
	manager.SetRules(map[string][]string{string(BackupSuccess): {"blocked"}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	result := manager.SendAndWait(ctx, &Message{Type: BackupSuccess})
	if result.Attempted != 1 || result.Failed != 1 || result.Succeeded != 0 {
		t.Fatalf("cancelled delivery result = %+v", result)
	}
	close(unblock)
	manager.Close()
}

func TestSendAndWaitCancelsWhileBoundedQueueIsFull(t *testing.T) {
	unblock := make(chan struct{})
	started := make(chan struct{})
	manager := NewManager(Options{WorkerCount: 1, QueueSize: 1})
	first := &recordingNotifier{name: "first", enabled: true, started: started, unblock: unblock}
	queued := &recordingNotifier{name: "queued", enabled: true, unblock: unblock}
	wouldBlock := &recordingNotifier{name: "would-block", enabled: true}
	for _, notifier := range []Notifier{first, queued, wouldBlock} {
		if err := manager.AddNotifier(notifier); err != nil {
			t.Fatal(err)
		}
	}
	manager.SetRules(map[string][]string{string(BackupSuccess): {"first"}})
	manager.Send(&Message{Type: BackupSuccess})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	manager.SetRules(map[string][]string{string(BackupSuccess): {"queued"}})
	manager.Send(&Message{Type: BackupSuccess})
	manager.SetRules(map[string][]string{string(BackupSuccess): {"would-block"}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	result := manager.SendAndWait(ctx, &Message{Type: BackupSuccess})
	if result.Attempted != 0 || result.Failed != 1 || len(result.Deliveries) != 1 {
		t.Fatalf("full-queue cancellation result = %+v", result)
	}
	close(unblock)
	manager.Close()
}

func TestTemplatesDefaultsValidationAndLimits(t *testing.T) {
	defaults := DefaultTemplates()
	if defaults[string(BackupSuccess)] != "设备 ${device_name} 备份成功完成" {
		t.Fatalf("unexpected defaults: %+v", defaults)
	}
	defaults[string(BackupSuccess)] = "mutated"
	if DefaultTemplates()[string(BackupSuccess)] == "mutated" {
		t.Fatal("DefaultTemplates returned shared storage")
	}
	for name, templates := range map[string]map[string]string{
		"unknown type":     {"unknown": "x"},
		"unknown variable": {string(BackupSuccess): "${secret}"},
		"too large":        {string(BackupSuccess): strings.Repeat("x", 8193)},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateTemplates(templates); err == nil {
				t.Fatal("invalid templates accepted")
			}
		})
	}
}

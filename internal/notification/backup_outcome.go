package notification

// BackupOutcome 分开传递执行、检查与历史保存状态；手动检查不调用最终备份通知。
type BackupOutcome struct {
	Execution, Check, History string
	FailureCode, Diagnostics  string
	ReasonMessageCode         string
	ReasonMessageParams       map[string]any
}

// 缺失规则才继承失败通知渠道；显式空数组永久表示关闭。
func MigrateBackupOutcomeRules(rules map[string][]string) map[string][]string {
	result := CloneRules(rules)
	if result == nil {
		result = make(map[string][]string)
	}
	for _, kind := range []Type{BackupCheckAttention, BackupHistoryAttention} {
		if _, ok := result[string(kind)]; !ok {
			result[string(kind)] = append([]string{}, result[string(BackupFailed)]...)
		}
	}
	return result
}

func (outcome BackupOutcome) events() []Type {
	var events []Type
	if outcome.Execution == "failed" {
		events = append(events, BackupFailed)
	}
	if outcome.Check == "issues" || outcome.Check == "unavailable" {
		events = append(events, BackupCheckAttention)
	}
	if outcome.History == "failed" || outcome.History == "uncertain" {
		events = append(events, BackupHistoryAttention)
	}
	if len(events) == 0 && outcome.Execution == "succeeded" {
		events = append(events, BackupSuccess)
	}
	return events
}

// 一次配置快照决定全部渠道；重叠规则每渠道仅产生一份最终摘要。
func (manager *Manager) backupOutcomeJobs(outcome BackupOutcome, name, udid string) []job {
	if manager == nil || manager.closed.Load() {
		return nil
	}
	manager.mu.RLock()
	enabled, language := manager.enabled, manager.language
	rules, templates := CloneRules(manager.notificationRules), CloneStringMap(manager.notificationTemplates)
	notifiers := append([]Notifier(nil), manager.notifiers...)
	manager.mu.RUnlock()
	if !enabled {
		return nil
	}
	check, history := outcome.Check, outcome.History
	if check == "" {
		check = "not_run"
	}
	if history == "" {
		history = "not_enabled"
	}
	states := map[string]string{"execution_result": copyFor(language, "outcome."+outcome.Execution), "check_result": copyFor(language, "outcome."+check), "history_result": copyFor(language, "outcome."+history)}
	events := outcome.events()
	sent := map[string]bool{}
	var tasks []job
	for _, event := range events {
		allowed := map[string]bool{}
		for _, name := range rules[string(event)] {
			allowed[name] = true
		}
		for _, notifier := range notifiers {
			channel := notifier.GetName()
			if !notifier.IsEnabled() || !allowed[channel] || sent[channel] {
				continue
			}
			sent[channel] = true
			code := string(event)
			level := LevelWarning
			if event == BackupSuccess {
				code = "backup_final_success"
				level = LevelInfo
			} else if event == BackupFailed {
				code = "backup_final_failed"
				level = LevelError
			}
			message := Message{Type: event, Level: level, EventCode: code, ReasonCode: outcome.FailureCode,
				ReasonMessageCode: outcome.ReasonMessageCode, ReasonMessageParams: cloneReasonMessageParams(outcome.ReasonMessageParams),
				DeviceName: name, DeviceUDID: udid, Timestamp: manager.now(), Params: states, ExtraFields: map[string]string{"reason": outcome.Diagnostics, "diagnostics": outcome.Diagnostics}}
			message = localizeMessage(message, language)
			message.Content = renderTemplate(message, templates)
			tasks = append(tasks, job{notifier: notifier, message: CloneMessage(message)})
		}
	}
	return tasks
}

// 通知沿用有界队列和 best-effort；不会阻塞备份，也不在重启后盲目补发。
func (manager *Manager) SendBackupOutcome(outcome BackupOutcome, name, udid string) {
	for _, work := range manager.backupOutcomeJobs(outcome, name, udid) {
		select {
		case manager.queue <- work:
		default:
			manager.dropped.Add(1)
		}
	}
}

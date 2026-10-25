package notification

import (
	_ "embed"
	"encoding/json"
	"strings"

	"iosbackup/internal/i18n"
)

// NormalizeLanguage keeps existing installations in Chinese until explicitly changed.
func NormalizeLanguage(language string) (string, error) {
	switch language {
	case "", "zh", "zh-CN":
		return "zh-CN", nil
	case "en":
		return "en", nil
	default:
		return "", i18n.NewError(i18n.NotificationLanguageInvalid, map[string]any{"language": language})
	}
}

//go:embed messages.json
var notificationCopyJSON []byte

var notificationCopy map[string]map[string]string

func init() {
	if err := json.Unmarshal(notificationCopyJSON, &notificationCopy); err != nil {
		panic(err)
	}
}

func copyFor(language, key string) string {
	if language != "en" {
		language = "zh-CN"
	}
	return notificationCopy[language][key]
}

func defaultTemplatesFor(language string) map[string]string {
	result := make(map[string]string)
	for _, event := range []Type{BackupStart, BackupSuccess, BackupFailed, BackupCheckAttention, BackupHistoryAttention, DeviceOnline, DeviceOffline} {
		result[string(event)] = copyFor(language, "body."+string(event))
	}
	result[string(SystemError)] = "${content}"
	return result
}

func localizeMessage(message Message, language string) Message {
	message.Locale = language
	codedEvent := message.EventCode != "" || message.ReasonCode != ""
	coded := codedEvent || message.Title == "" || message.Test
	event := message.EventCode
	if event == "" {
		event = string(message.Type)
	}
	message.EventCode = event
	if message.ExtraFields == nil {
		message.ExtraFields = make(map[string]string)
	}
	if message.DeviceUDID != "" && message.DeviceName == "" {
		message.DeviceName = copyFor(language, "device.default")
	}
	// Caller-owned titles and bodies remain compatible; coded application events
	// and messages without a title are rendered using the instance locale.
	if !coded {
		return message
	}
	if message.Test {
		if message.Params == nil {
			message.Params = map[string]string{}
		}
		message.Params["execution_result"] = copyFor(language, "outcome.succeeded")
		message.Params["check_result"] = copyFor(language, "outcome.passed")
		message.Params["history_result"] = copyFor(language, "outcome.not_enabled")
		if message.Type == BackupCheckAttention {
			message.Params["check_result"] = copyFor(language, "outcome.unavailable")
		}
		if message.Type == BackupHistoryAttention {
			message.Params["history_result"] = copyFor(language, "outcome.failed")
		}
		if message.Type == BackupFailed {
			message.Params["execution_result"] = copyFor(language, "outcome.failed")
		}
		message.DeviceName = copyFor(language, "device.test")
		message.Title = copyFor(language, "test.title") + ": " + copyFor(language, "title."+string(message.Type))
		message.Content = copyFor(language, "test.body")
		if message.Type == BackupFailed {
			message.ExtraFields["reason"] = copyFor(language, "test.reason")
		}
		return message
	}
	if message.Type == BackupFailed {
		reason := message.ExtraFields["reason"]
		if reason != "" {
			message.ExtraFields["diagnostics"] = reason
		}
		code := strings.TrimPrefix(message.ReasonCode, "api.")
		if code == "" {
			code = reason
		}
		translated := copyFor(language, "reason."+code)
		if translated != "" {
			message.ExtraFields["reason"] = translated
		} else if codedEvent {
			message.ExtraFields["reason"] = copyFor(language, "reason.unknown")
			if reason != "" {
				message.ExtraFields["diagnostics"] = reason
			}
		}
	}
	message.Title = copyFor(language, "title."+event)
	message.Content = copyFor(language, "body."+event)
	if message.Title == "" {
		message.Title = copyFor(language, "title.system_error")
	}
	if message.Content == "" {
		message.Content = copyFor(language, "body.system_error")
	}
	message.Title = expandMessageTemplate(message.Title, message)
	message.Content = expandMessageTemplate(message.Content, message)
	return message
}

func expandMessageTemplate(template string, message Message) string {
	return renderTemplate(message, map[string]string{string(message.Type): template})
}

// NewTestMessage returns a language-neutral test event. Delivery resolves the
// configured instance locale, including when no browser is connected.
func (manager *Manager) NewTestMessage(event Type) *Message {
	if _, ok := defaultTemplatesFor("en")[string(event)]; !ok {
		return nil
	}
	level := LevelInfo
	if event == BackupFailed || event == SystemError {
		level = LevelError
	}
	if event == DeviceOffline {
		level = LevelWarning
	}
	return &Message{Type: event, Level: level, Test: true, DeviceUDID: "test-device"}
}

func (manager *Manager) SendBackupFailure(deviceName, deviceUDID, code, diagnostics string) {
	manager.Send(&Message{Type: BackupFailed, Level: LevelError, EventCode: string(BackupFailed), ReasonCode: code, DeviceName: deviceName, DeviceUDID: deviceUDID, ExtraFields: map[string]string{"reason": diagnostics, "diagnostics": diagnostics}})
}

func (manager *Manager) SendSystemEvent(code string, params map[string]string) {
	manager.Send(&Message{Type: SystemError, Level: LevelError, EventCode: code, Params: params, DeviceName: params["device_name"], DeviceUDID: params["device_udid"], ExtraFields: map[string]string{"diagnostics": params["diagnostics"]}})
}

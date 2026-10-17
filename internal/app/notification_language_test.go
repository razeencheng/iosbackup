package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNotificationLanguagePersistsWithoutMaterializingTemplates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notifications.json")
	store := newNotificationConfigStore(path, nil)
	config, err := store.Public()
	if err != nil {
		t.Fatal(err)
	}
	if config.Language != "zh-CN" {
		t.Fatalf("legacy/default language: %q", config.Language)
	}
	config.Language = "en"
	config.NotificationTemplates = map[string]string{string(notificationBackupSuccess): ""}
	preview, err := store.Preview(config)
	if err != nil {
		t.Fatal(err)
	}
	if preview.NotificationTemplates[string(notificationBackupSuccess)] != "" {
		t.Fatal("preview materialized translated default")
	}
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Language != "en" || loaded.NotificationTemplates[string(notificationBackupSuccess)] != "" {
		t.Fatalf("loaded config: %+v", loaded)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "设备") {
		t.Fatal("saved default template text")
	}
}

func TestNotificationLanguageLegacyAndInvalidConfiguration(t *testing.T) {
	for _, test := range []struct{ language, want string }{{"", "zh-CN"}, {"zh", "zh-CN"}, {"en", "en"}} {
		t.Run(test.language, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy.json")
			data, _ := json.Marshal(map[string]any{"language": test.language, "notification_templates": map[string]string{"backup_success": "设备 ${device_name} 备份成功完成"}})
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			store := newNotificationConfigStore(path, nil)
			loaded, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			if loaded.Language != test.want || loaded.NotificationTemplates["backup_success"] != "设备 ${device_name} 备份成功完成" {
				t.Fatalf("legacy config modified: %+v", loaded)
			}
		})
	}
	store := newNotificationConfigStore(filepath.Join(t.TempDir(), "invalid.json"), nil)
	invalid := &notificationConfig{Language: "fr"}
	if _, err := store.Preview(invalid); err == nil {
		t.Fatal("preview accepted unsupported language")
	}
	if err := store.Save(invalid); err == nil {
		t.Fatal("save accepted unsupported language")
	}
}

package app

import (
	"errors"
	"testing"

	"iosbackup/internal/i18n"
)

func TestDeviceOperationErrorsCarryStableCodes(t *testing.T) {
	app := newApplication()
	app.devices["PHONE"] = &device{UDID: "PHONE", IsOnline: false}
	_, _, _, err := app.acquireDeviceOp("PHONE")
	var message *i18n.Error
	if !errors.As(err, &message) || message.Code != "api.device_offline" {
		t.Fatalf("offline operation returned %v, want api.device_offline", err)
	}
	if app.backupInProgress["PHONE"] {
		t.Fatal("rejected operation must not occupy a backup slot")
	}
}

func TestDeleteBackupRejectsInvalidIDWithStableCode(t *testing.T) {
	app := newApplication()
	err := app.DeleteDeviceBackup("../outside")
	var message *i18n.Error
	if !errors.As(err, &message) || message.Code != "api.invalid_udid" {
		t.Fatalf("invalid identifier returned %v, want api.invalid_udid", err)
	}
}

package app

import (
	"context"
	"testing"
)

func TestDeviceConnectionsAreLanguageIndependent(t *testing.T) {
	devices := parseDeviceList("USB-PHONE (USB)\nWIFI-PHONE (Network)\n")
	for udid, want := range map[string]string{"USB-PHONE": "usb", "WIFI-PHONE": "network"} {
		if devices[udid] == nil || devices[udid].Connection != want {
			t.Errorf("connection for %s = %+v, want code %s", udid, devices[udid], want)
		}
	}
}

func TestConnectionCodeSelectsDeviceCommandTransport(t *testing.T) {
	for _, connection := range []string{"usb", "network"} {
		t.Run(connection, func(t *testing.T) {
			app := newApplication()
			runner := &mockRunner{}
			app.cmdRunner = runner.run
			_, err := app.runIdeviceCmd(context.Background(), cmdKindShort, &device{UDID: "PHONE", Connection: connection}, cmdIdeviceInfo)
			if err != nil {
				t.Fatal(err)
			}
			call, ok := runner.lastCall()
			if !ok || argsHas(call.args, "-n") != (connection == "network") {
				t.Fatalf("connection %s routed to args %v", connection, call.args)
			}
		})
	}
}

func TestMissingDeviceNameUsesIdentifierAndPreservesUserText(t *testing.T) {
	for _, name := range []string{"", "Unknown Device", "我的 iPhone"} {
		app := newApplication()
		app.cmdRunner = func(context.Context, string, []string, []string) ([]byte, error) { return nil, nil }
		device := &device{UDID: "PHONE-ID", Name: name, Connection: connectTypeUSB}
		app.getDeviceInfoInTask(device)
		want := name
		if want == "" {
			want = device.UDID
		}
		if device.Name != want {
			t.Errorf("device name = %q, want user text or identifier %q", device.Name, want)
		}
	}
}

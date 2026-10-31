package app

import (
	"os/exec"
	"testing"
)

func TestBackupConsoleBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js 不可用")
	}
	cmd := exec.Command(node, "testdata/backup-console.test.js")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}

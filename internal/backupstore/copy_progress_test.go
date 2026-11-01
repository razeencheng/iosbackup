package backupstore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyProgressReportsTotalsAndLargeFileActivity(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "copy")
	f, err := os.Create(filepath.Join(src, "large"))
	if err != nil {
		t.Fatal(err)
	}
	const size = 64 << 20
	if err = f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	f.Close()
	var reports []CopyReport
	_, err = CopyTree(context.Background(), src, dst, CopyOptions{Progress: func(r CopyReport) { reports = append(reports, r) }})
	if err != nil {
		t.Fatal(err)
	}
	partial := false
	for _, r := range reports {
		if r.Bytes > 0 && r.Bytes < size {
			partial = true
		}
	}
	if !partial {
		t.Fatal("large file has no progress until completed")
	}
	if len(reports) > 20 {
		t.Fatal("progress not throttled", len(reports))
	}
	var last map[string]any
	b, _ := json.Marshal(reports[len(reports)-1])
	json.Unmarshal(b, &last)
	if last["Phase"] != "completed" || last["TotalBytes"] != float64(size) || last["TotalsKnown"] != true {
		t.Fatal("missing final phase or totals", last)
	}
}

func TestCopyProgressCancellationHasTerminalReport(t *testing.T) {
	src, dst := copyFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var last CopyReport
	_, err := CopyTree(ctx, src, dst, CopyOptions{Progress: func(r CopyReport) {
		last = r
		if r.Files > 0 {
			cancel()
		}
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var payload map[string]any
	b, _ := json.Marshal(last)
	json.Unmarshal(b, &payload)
	if payload["Phase"] != "cancelled" {
		t.Fatal("missing cancelled progress", payload)
	}
}

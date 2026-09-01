package persistence

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWriteFileAtomicCreatesPrivateDirectoryAndExactFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "nested", "private")
	path := filepath.Join(dir, "config.json")
	want := []byte("exact payload")

	if err := WriteFileAtomic(path, want, 0o640); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("文件内容不一致: got %q want %q", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if gotMode := info.Mode().Perm(); gotMode != 0o640 {
		t.Fatalf("文件权限应为 0640，得到 %04o", gotMode)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if gotMode := dirInfo.Mode().Perm(); gotMode&0o077 != 0 {
		t.Fatalf("新目录不能授予组或其他用户权限，得到 %04o", gotMode)
	}
	assertNoAtomicTemps(t, dir, filepath.Base(path))
}

func TestWriteFileAtomicReplacesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WriteFileAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("替换后内容 = %q, want new", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if gotMode := info.Mode().Perm(); gotMode != 0o600 {
		t.Fatalf("替换后权限应为 0600，得到 %04o", gotMode)
	}
	assertNoAtomicTemps(t, filepath.Dir(path), filepath.Base(path))
}

func TestWriteFileAtomicRenameFailurePreservesOldFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("rename failed")
	ops := defaultAtomicOps()
	ops.rename = func(oldPath, newPath string) error {
		if filepath.Dir(oldPath) != filepath.Dir(newPath) {
			t.Fatalf("临时文件必须与目标文件同目录: %s -> %s", oldPath, newPath)
		}
		return wantErr
	}

	err := writeFileAtomic(path, []byte("new"), 0o600, ops)
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "替换配置文件") {
		t.Fatalf("应包装 rename 原始错误，得到 %v", err)
	}
	assertFileState(t, path, "old", 0o640)
	assertNoAtomicTemps(t, dir, filepath.Base(path))
}

func TestWriteFileAtomicPreRenameFailuresPreserveOldFile(t *testing.T) {
	tests := []struct {
		name   string
		stage  string
		mutate func(*faultFile, error)
	}{
		{name: "write", stage: "写入临时文件", mutate: func(f *faultFile, err error) { f.writeErr = err }},
		{name: "chmod", stage: "设置临时文件权限", mutate: func(f *faultFile, err error) { f.chmodErr = err }},
		{name: "sync", stage: "同步临时文件", mutate: func(f *faultFile, err error) { f.syncErr = err }},
		{name: "close", stage: "关闭临时文件", mutate: func(f *faultFile, err error) { f.closeErr = err }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.json")
			if err := os.WriteFile(path, []byte("old"), 0o640); err != nil {
				t.Fatal(err)
			}
			wantErr := errors.New(tc.name + " failed")
			ops := defaultAtomicOps()
			createTemp := ops.createTemp
			ops.createTemp = func(dir, pattern string) (atomicTempFile, error) {
				file, err := createTemp(dir, pattern)
				if err != nil {
					return nil, err
				}
				fault := &faultFile{atomicTempFile: file}
				tc.mutate(fault, wantErr)
				return fault, nil
			}

			err := writeFileAtomic(path, []byte("new"), 0o600, ops)
			if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), tc.stage) {
				t.Fatalf("应包装 %s 原始错误，得到 %v", tc.name, err)
			}
			assertFileState(t, path, "old", 0o640)
			assertNoAtomicTemps(t, dir, filepath.Base(path))
		})
	}
}

func TestWriteFileAtomicCloseFailureDoesNotCloseTwice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	wantErr := errors.New("close failed")
	ops := defaultAtomicOps()
	createTemp := ops.createTemp
	var fault *faultFile
	ops.createTemp = func(dir, pattern string) (atomicTempFile, error) {
		file, err := createTemp(dir, pattern)
		if err != nil {
			return nil, err
		}
		fault = &faultFile{atomicTempFile: file, closeErr: wantErr}
		return fault, nil
	}

	err := writeFileAtomic(path, []byte("new"), 0o600, ops)
	if !errors.Is(err, wantErr) {
		t.Fatalf("应返回 close 错误，得到 %v", err)
	}
	if fault.closeCalls != 1 {
		t.Fatalf("Close 失败后不得重试 Close，调用次数 = %d", fault.closeCalls)
	}
	assertNoAtomicTemps(t, dir, filepath.Base(path))
}

func TestWriteFileAtomicWritesPrivatelyBeforeApplyingFinalMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	var operations []string
	ops := defaultAtomicOps()
	createTemp := ops.createTemp
	ops.createTemp = func(dir, pattern string) (atomicTempFile, error) {
		file, err := createTemp(dir, pattern)
		if err != nil {
			return nil, err
		}
		return &faultFile{atomicTempFile: file, operations: &operations, test: t}, nil
	}
	rename := ops.rename
	ops.rename = func(oldPath, newPath string) error {
		operations = append(operations, "rename")
		return rename(oldPath, newPath)
	}

	if err := writeFileAtomic(path, []byte("new"), 0o640, ops); err != nil {
		t.Fatal(err)
	}
	want := []string{"write", "chmod", "sync", "close", "rename"}
	if !reflect.DeepEqual(operations, want) {
		t.Fatalf("原子写操作顺序 = %v, want %v", operations, want)
	}
	assertFileState(t, path, "new", 0o640)
}

func TestWriteFileAtomicDirectoryOpenFailureLeavesCompleteReplacement(t *testing.T) {
	testPostRenameFailure(t, "打开配置目录", func(ops *atomicOps, wantErr error) {
		ops.openDir = func(string) (atomicDir, error) { return nil, wantErr }
	})
}

func TestWriteFileAtomicDirectorySyncFailureLeavesCompleteReplacement(t *testing.T) {
	testPostRenameFailure(t, "同步配置目录", func(ops *atomicOps, wantErr error) {
		ops.openDir = func(string) (atomicDir, error) {
			return &faultDir{syncErr: wantErr}, nil
		}
	})
}

func TestWriteFileAtomicRejectsDirectoryAsTargetWithoutDamagingIt(t *testing.T) {
	dir := t.TempDir()
	err := WriteFileAtomic(dir, []byte("new"), 0o600)
	if err == nil {
		t.Fatal("以目录作为目标必须失败")
	}
	info, statErr := os.Stat(dir)
	if statErr != nil || !info.IsDir() {
		t.Fatalf("目标目录必须保留: info=%v err=%v", info, statErr)
	}
	assertNoAtomicTemps(t, filepath.Dir(dir), filepath.Base(dir))
}

func testPostRenameFailure(t *testing.T, stage string, mutate func(*atomicOps, error)) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("directory durability failed")
	ops := defaultAtomicOps()
	mutate(&ops, wantErr)

	err := writeFileAtomic(path, []byte("complete-new"), 0o600, ops)
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), stage) {
		t.Fatalf("应包装目录刷盘错误，得到 %v", err)
	}
	// rename 已经成功，目录 open/sync 失败无法回滚；必须留下完整新文件而非部分内容。
	assertFileState(t, path, "complete-new", 0o600)
	assertNoAtomicTemps(t, dir, filepath.Base(path))
}

type faultFile struct {
	atomicTempFile
	writeErr   error
	chmodErr   error
	syncErr    error
	closeErr   error
	closeCalls int
	operations *[]string
	test       *testing.T
}

func (f *faultFile) Write(p []byte) (int, error) {
	f.record("write")
	if f.test != nil {
		info, err := os.Stat(f.Name())
		if err != nil {
			f.test.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			f.test.Fatalf("写入未完成内容时临时文件必须保持 0600，得到 %04o", got)
		}
	}
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return f.atomicTempFile.Write(p)
}

func (f *faultFile) Chmod(mode os.FileMode) error {
	f.record("chmod")
	if f.chmodErr != nil {
		return f.chmodErr
	}
	return f.atomicTempFile.Chmod(mode)
}

func (f *faultFile) Sync() error {
	f.record("sync")
	if f.syncErr != nil {
		return f.syncErr
	}
	return f.atomicTempFile.Sync()
}

func (f *faultFile) Close() error {
	f.record("close")
	f.closeCalls++
	err := f.atomicTempFile.Close()
	if f.closeErr != nil {
		return f.closeErr
	}
	return err
}

func (f *faultFile) record(operation string) {
	if f.operations != nil {
		*f.operations = append(*f.operations, operation)
	}
}

type faultDir struct {
	syncErr error
}

func (d *faultDir) Sync() error  { return d.syncErr }
func (d *faultDir) Close() error { return nil }

func assertFileState(t *testing.T, path, wantContent string, wantMode os.FileMode) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != wantContent {
		t.Fatalf("文件内容 = %q, want %q", data, wantContent)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if gotMode := info.Mode().Perm(); gotMode != wantMode {
		t.Fatalf("文件权限 = %04o, want %04o", gotMode, wantMode)
	}
}

func assertNoAtomicTemps(t *testing.T, dir, base string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	prefix := "." + base + "-"
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			t.Fatalf("遗留原子写临时文件 %s", entry.Name())
		}
	}
}

var _ io.Writer = (*faultFile)(nil)

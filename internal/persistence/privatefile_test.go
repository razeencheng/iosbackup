package persistence

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
)

func TestPrivateFileAtomicConcurrentWinners(t *testing.T) {
	root := filepath.Join(t.TempDir(), "configs")
	path := filepath.Join(root, "secret")
	const count = 16
	var wg sync.WaitGroup
	outcomes := make([]bool, count)
	errs := make([]error, count)
	for i := range outcomes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			outcomes[i], errs[i] = CreatePrivateFile(path, bytes.Repeat([]byte{byte(i + 1)}, 2048))
		}(i)
	}
	wg.Wait()
	winner := -1
	for i := range outcomes {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if outcomes[i] {
			if winner >= 0 {
				t.Fatal("多个并发创建者均成为赢家")
			}
			winner = i
		}
	}
	data, err := ReadSecretFile(path, 2048, true)
	if err != nil {
		t.Fatal(err)
	}
	if winner < 0 || !bytes.Equal(data, bytes.Repeat([]byte{byte(winner + 1)}, 2048)) {
		t.Fatal("必须只看到完整赢家内容")
	}
	for p, want := range map[string]os.FileMode{root: 0700, path: 0600} {
		info, err := os.Stat(p)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("权限错误: %s %v %v", p, info, err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("不得留下临时文件: %v %v", entries, err)
	}
}

func TestPrivateFileRejectsNonRegularAndOversize(t *testing.T) {
	for _, kind := range []string{"symlink", "dangling", "directory", "fifo", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "secret")
			switch kind {
			case "symlink":
				target := filepath.Join(root, "target")
				if err := os.WriteFile(target, []byte("secret"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "dangling":
				if err := os.Symlink(filepath.Join(root, "missing"), path); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := syscall.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			case "oversize":
				if err := os.WriteFile(path, make([]byte, 11), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ReadSecretFile(path, 10, true); err == nil {
				t.Fatal("不安全文件应拒绝且不能阻塞")
			}
			created, err := CreatePrivateFile(path, []byte("replacement"))
			if err != nil || created {
				t.Fatalf("不得替换既有路径: %v %v", created, err)
			}
		})
	}
}

func TestPrivateDirectoryRejectsSymlinkEvenWithTrailingSlash(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "configs")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, link + string(filepath.Separator)} {
		if err := EnsurePrivateDirectory(path); err == nil {
			t.Fatalf("不能跟随配置目录符号链接: %s", path)
		}
	}
}

func TestPrivateDirectoryOnlyRestrictsItsOwnMode(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "configs")
	child := filepath.Join(root, "existing")
	if err := os.MkdirAll(child, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0755); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDirectory(root); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{parent, child} {
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0755 {
			t.Fatal("不得递归修改其他目录权限")
		}
	}
	for _, path := range []string{"/", "relative", ""} {
		if err := EnsurePrivateDirectory(path); err == nil {
			t.Fatal("必须使用专用绝对路径")
		}
	}
	file := filepath.Join(parent, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDirectory(file); err == nil {
		t.Fatal("文件不能作为配置目录")
	}
	if _, err := CreatePrivateFile(filepath.Join(file, "child"), nil); err == nil {
		t.Fatal("无法创建配置目录必须报错")
	}
}

func TestReadPrivateFileRestrictsModeButExternalFileRemainsReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("value"), 0444); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSecretFile(path, 5, false); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0444 {
		t.Fatal("外部只读秘密不得被修改")
	}
	if _, err := ReadSecretFile(path, 5, true); err != nil {
		t.Fatal(err)
	}
	info, _ = os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("默认秘密文件必须收紧权限")
	}
}

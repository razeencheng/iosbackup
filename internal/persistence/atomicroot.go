//go:build go1.25

package persistence

import (
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
)

type rootedTemp struct {
	*os.File
	relative string
}

func (f rootedTemp) Name() string { return f.relative }

// WriteFileAtomicRoot 复用刷盘/替换协议，所有目录操作限制在已打开的根内。
func WriteFileAtomicRoot(root *os.Root, name string, data []byte, mode os.FileMode) error {
	if !fs.ValidPath(filepath.ToSlash(name)) {
		return os.ErrInvalid
	}
	return writeFileAtomic(name, data, mode, atomicOps{
		mkdirAll: root.MkdirAll,
		createTemp: func(dir, pattern string) (atomicTempFile, error) {
			var random [16]byte
			if _, err := rand.Read(random[:]); err != nil {
				return nil, err
			}
			path := filepath.Join(dir, ".atomic-"+hex.EncodeToString(random[:]))
			f, err := root.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
			if err != nil {
				return nil, err
			}
			return rootedTemp{f, path}, nil
		},
		rename: root.Rename, openDir: func(path string) (atomicDir, error) { return root.Open(path) }, remove: root.Remove,
	})
}
func RenameWithinRoot(root *os.Root, old, new string) error { return root.Rename(old, new) }

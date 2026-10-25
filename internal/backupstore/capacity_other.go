//go:build !linux && !darwin

package backupstore

import "os"

func StorageCapacity(path string) (Capacity, error) { return Capacity{}, nil }
func fileIdentity(info os.FileInfo) string          { return "" }

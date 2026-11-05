//go:build linux

package backupstore

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// mnt_id 区分同一设备上的 bind mount，也不把 overlayfs 的文件设备号差异当成跨挂载。
// 无法取得实际打开文件的挂载身份时拒绝封存，不退回不完整的路径判断。
func mountIdentity(file *os.File) (string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/self/fdinfo/%d", file.Fd()))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "mnt_id:" {
			id, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil || id == 0 {
				return "", ErrUnsafe
			}
			return fields[1], nil
		}
	}
	return "", ErrUnsafe
}

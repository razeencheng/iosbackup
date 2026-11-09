package backupstore

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"iosbackup/internal/boundedio"
)

type CopyOptions struct {
	PreferClone  bool
	ReserveBytes uint64
	Progress     func(CopyReport)
}
type CopyReport struct {
	Phase          string
	TotalsKnown    bool
	TotalFiles     int64
	TotalBytes     int64
	ScannedFiles   int64
	Files          int64
	Bytes          int64
	AllocatedBytes int64
	ClonedFiles    int64
	Mode           string
}
type Capacity struct {
	Bytes       uint64
	Inodes      uint64
	BytesKnown  bool
	InodesKnown bool
	QuotaKnown  bool
}
type treeStamp struct {
	Digest           [32]byte
	PersistentDigest [32]byte
	Files            int64
	Directories      int64
	Bytes            int64
}

func openSafeRoot(path string) (*os.Root, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, ErrUnsafe
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	after, err := root.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		root.Close()
		return nil, ErrChanged
	}
	return root, nil
}
func originalEntry(path string) bool {
	return path != "_unback_" && !strings.HasPrefix(path, ".iosbackup-unback-")
}

// walkTree 使用有界目录批次；拒绝链接和特殊文件。调用方持有来源读占用。
func walkTree(ctx context.Context, root *os.Root, visit func(string, os.FileInfo) error) error {
	return walkTreeDepth(ctx, root, 0, visit)
}

func walkTreeDepth(ctx context.Context, root *os.Root, initialDepth int, visit func(string, os.FileInfo) error) error {
	var walk func(string, int) error
	walk = func(path string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > 128 {
			return ErrUnsafe
		}
		info, err := root.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return ErrUnsafe
		}
		if err = visit(path, info); err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		file, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		defer file.Close()
		actual, err := file.Stat()
		if err != nil || !os.SameFile(info, actual) || !actual.IsDir() {
			return ErrChanged
		}
		for {
			entries, readErr := file.ReadDir(256)
			for _, entry := range entries {
				child := filepath.Join(path, entry.Name())
				if path == "." && !originalEntry(entry.Name()) {
					continue
				}
				if err := walk(child, depth+1); err != nil {
					return err
				}
			}
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			if readErr != nil {
				return readErr
			}
		}
	}
	return walk(".", initialDepth)
}
func stampTree(ctx context.Context, root *os.Root) (treeStamp, error) {
	return stampTreeProgress(ctx, root, nil)
}

func stampTreeProgress(ctx context.Context, root *os.Root, progress func(treeStamp)) (treeStamp, error) {
	var stamp treeStamp
	err := walkTree(ctx, root, func(path string, info os.FileInfo) error {
		// 目录枚举顺序不参与摘要；包含实体和变更时间，识别等长改写及替换。
		metadata := ""
		if filepath.Dir(path) == "." && (path == "Info.plist" || path == "Status.plist" || path == "Manifest.plist") {
			// 以原始字节校验元数据，摘要格式须兼容已有提交事务。
			file, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			if err != nil {
				return err
			}
			opened, err := file.Stat()
			if err != nil || !sameFileVersion(info, opened) {
				file.Close()
				return ErrChanged
			}
			metadata, err = boundedio.FileSHA256(ctx, file, opened.Size())
			if err == nil {
				var after os.FileInfo
				after, err = file.Stat()
				if err == nil && !sameFileVersion(opened, after) {
					err = ErrChanged
				}
			}
			closeErr := file.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%d\x00%s\x00%s", path, info.Size(), info.Mode(), info.ModTime().UnixNano(), fileIdentity(info), metadata)))
		stable := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%d\x00%s\x00%s", path, info.Size(), info.Mode(), info.ModTime().UnixNano(), persistentFileIdentity(info), metadata)))
		for i := range stamp.Digest {
			stamp.Digest[i] ^= sum[i]
			stamp.PersistentDigest[i] ^= stable[i]
		}
		if info.IsDir() {
			stamp.Directories++
			return nil
		}
		if info.Size() < 0 || stamp.Bytes > math.MaxInt64-info.Size() {
			return ErrCapacity
		}
		stamp.Bytes += info.Size()
		stamp.Files++
		if progress != nil {
			progress(stamp)
		}
		return nil
	})
	return stamp, err
}
func below(path, parent string) bool {
	rel, err := filepath.Rel(parent, path)
	return err == nil && (rel == "." || (!filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
}

// CopyTree 只创建新的目标。错误时留下未发布副本，由持有工作事务的调用方清理。
// 不使用硬链接；普通复制和克隆失败均不会改写来源。
func CopyTree(ctx context.Context, source, destination string, options CopyOptions) (CopyReport, error) {
	return copyTreeWith(ctx, source, destination, options, StorageCapacity, cloneFile)
}

func copyTreeWith(ctx context.Context, source, destination string, options CopyOptions, capacity func(string) (Capacity, error), cloneFile func(*os.File, *os.File) error) (report CopyReport, retErr error) {
	report = CopyReport{Mode: "copy", Phase: "scanning"}
	lastAt, lastBytes := time.Time{}, int64(0)
	emit := func(force bool) {
		if options.Progress == nil {
			return
		}
		now := time.Now()
		if !force && now.Sub(lastAt) < 250*time.Millisecond && report.Bytes-lastBytes < 16<<20 {
			return
		}
		lastAt, lastBytes = now, report.Bytes
		options.Progress(report)
	}
	defer func() {
		report.Phase = "completed"
		if retErr != nil {
			report.Phase = "failed"
			if errors.Is(retErr, context.Canceled) || errors.Is(retErr, context.DeadlineExceeded) {
				report.Phase = "cancelled"
			}
		}
		emit(true)
	}()
	emit(true)
	if err := ctx.Err(); err != nil {
		return report, err
	}
	srcPath, err := filepath.EvalSymlinks(source)
	if err != nil {
		return report, err
	}
	srcPath, err = filepath.Abs(srcPath)
	if err != nil {
		return report, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil {
		return report, err
	}
	parent, err = filepath.Abs(parent)
	if err != nil {
		return report, err
	}
	dstPath := filepath.Join(parent, filepath.Base(destination))
	if below(dstPath, srcPath) || below(srcPath, dstPath) {
		return report, ErrUnsafe
	}
	src, err := openSafeRoot(source)
	if err != nil {
		return report, err
	}
	defer src.Close()
	before, err := stampTreeProgress(ctx, src, func(st treeStamp) { report.ScannedFiles = st.Files; emit(false) })
	if err != nil {
		return report, err
	}
	report.TotalBytes, report.TotalFiles, report.TotalsKnown = before.Bytes, before.Files, true
	reserve := options.ReserveBytes
	if reserve == 0 {
		reserve = 16 << 20
	}
	cap, err := capacity(parent)
	if err != nil {
		return report, err
	}
	entries := uint64(before.Files + before.Directories)
	// 先仅检查探测与目录所需余量；克隆能力确认前不按整份逻辑大小拒绝。
	if err = checkCapacity(cap, 0, entries, reserve); err != nil {
		return report, err
	}
	if err = os.Mkdir(dstPath, 0700); err != nil {
		return report, err
	}
	dst, err := openSafeRoot(dstPath)
	if err != nil {
		return report, err
	}
	defer dst.Close()
	clone := false
	if options.PreferClone {
		clone, err = probeCloneWith(dst, cloneFile, dst.Remove)
		if err != nil {
			return report, err
		}
	}
	needed := uint64(before.Bytes)
	if clone {
		// 为新目录项、共享块引用和后续写入留余量；实际配额仍由写入错误确认。
		needed = saturatedProduct(entries, 4096)
	}
	cap, err = capacity(parent)
	if err != nil {
		return report, err
	}
	if err = checkCapacity(cap, needed, entries, reserve); err != nil {
		return report, err
	}
	buffer := make([]byte, 128<<10)
	report.Phase = "copying"
	emit(true)
	err = walkTree(ctx, src, func(path string, info os.FileInfo) error {
		if info.IsDir() {
			if path != "." {
				return dst.Mkdir(path, 0700)
			}
			return nil
		}
		in, err := src.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		defer in.Close()
		initial, err := in.Stat()
		if err != nil || !sameFileVersion(info, initial) {
			return ErrChanged
		}
		out, err := dst.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
		if err != nil {
			return err
		}
		defer out.Close()
		cloned := false
		if clone {
			err = cloneFile(out, in)
			cloned = err == nil
			if err != nil {
				if err = out.Truncate(0); err != nil {
					return err
				}
				if _, err = out.Seek(0, 0); err != nil {
					return err
				}
			}
		}
		if !cloned {
			if clone {
				// 单文件可能跨存储或不支持克隆；回退复制前重新按实际文件大小检查。
				available, err := capacity(parent)
				if err != nil {
					return err
				}
				if err = checkCapacity(available, uint64(info.Size()), 0, reserve); err != nil {
					return err
				}
			}
			writer := copyProgressWriter{out: out, advance: func(n int) { report.Bytes += int64(n); emit(false) }}
			if report.ClonedFiles > 0 {
				report.Mode = "mixed"
			} else {
				report.Mode = "copy"
			}
			if err = copyBytes(ctx, writer, in, buffer); err != nil {
				return err
			}
		}
		final, err := in.Stat()
		if err != nil || !sameFileVersion(initial, final) {
			return ErrChanged
		}
		if err = out.Chmod(info.Mode().Perm()); err != nil {
			return err
		}
		if err = setFileTime(out, info.ModTime()); err != nil {
			return err
		}
		output, err := out.Stat()
		if err != nil {
			return err
		}
		if output.Size() != info.Size() {
			return io.ErrUnexpectedEOF
		}
		if err = out.Close(); err != nil {
			return err
		}
		report.Files++
		report.AllocatedBytes += allocatedBytes(output)
		if cloned {
			report.Bytes += info.Size()
			report.ClonedFiles++
		}
		if report.ClonedFiles == report.Files {
			report.Mode = "clone"
		} else if report.ClonedFiles > 0 {
			report.Mode = "mixed"
		}
		emit(false)
		return nil
	})
	if err != nil {
		return report, err
	}
	report.Phase, report.ScannedFiles = "verifying", 0
	emit(true)
	after, err := stampTreeProgress(ctx, src, func(st treeStamp) { report.ScannedFiles = st.Files; emit(false) })
	if err != nil {
		return report, err
	}
	if before != after {
		return report, ErrChanged
	}
	// 子目录完成后恢复目录时间，避免随后创建子项改变时间。
	err = walkTree(ctx, src, func(path string, info os.FileInfo) error {
		if !info.IsDir() {
			return nil
		}
		f, err := dst.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		if err = f.Chmod(info.Mode().Perm()); err != nil {
			return err
		}
		return setFileTime(f, info.ModTime())
	})
	if report.ClonedFiles == report.Files && report.Files > 0 {
		report.Mode = "clone"
	} else if report.ClonedFiles > 0 {
		report.Mode = "mixed"
	}
	return report, err
}

type copyProgressWriter struct {
	out     io.Writer
	advance func(int)
}

func (w copyProgressWriter) Write(p []byte) (int, error) {
	n, err := w.out.Write(p)
	if n > 0 {
		w.advance(n)
	}
	return n, err
}
func sameFileVersion(a, b os.FileInfo) bool {
	return a != nil && b != nil && a.Mode().IsRegular() && b.Mode().IsRegular() && os.SameFile(a, b) && a.Size() == b.Size() && a.Mode() == b.Mode() && a.ModTime() == b.ModTime() && fileIdentity(a) == fileIdentity(b)
}
func copyBytes(ctx context.Context, out io.Writer, in io.Reader, buffer []byte) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := in.Read(buffer)
		if n > 0 {
			written, e := out.Write(buffer[:n])
			if e != nil {
				return e
			}
			if written != n {
				return io.ErrShortWrite
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
func checkCapacity(c Capacity, bytes, inodes, reserve uint64) error {
	if bytes > math.MaxUint64-reserve {
		return ErrCapacity
	}
	if c.BytesKnown && c.Bytes < bytes+reserve {
		return ErrCapacity
	}
	if c.InodesKnown && (inodes > math.MaxUint64-32 || c.Inodes < inodes+32) {
		return ErrCapacity
	}
	return nil
}

// probeClone 在实际目标目录创建探针，确认改写、截断和删除后的源字节不变。
func probeClone(root *os.Root) (bool, error) { return probeCloneWith(root, cloneFile, root.Remove) }
func probeCloneWith(root *os.Root, clone func(*os.File, *os.File) error, remove func(string) error) (supported bool, retErr error) {
	id, err := NewID()
	if err != nil {
		return false, err
	}
	a, b := ".clone-probe-"+id, ".clone-copy-"+id
	source, err := root.OpenFile(a, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return false, err
	}
	defer func() {
		retErr = errors.Join(retErr, source.Close())
		if e := remove(a); e != nil && !errors.Is(e, os.ErrNotExist) {
			retErr = errors.Join(retErr, e)
		}
	}()
	target, err := root.OpenFile(b, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return false, err
	}
	targetClosed := false
	defer func() {
		if !targetClosed {
			retErr = errors.Join(retErr, target.Close())
		}
		if e := remove(b); e != nil && !errors.Is(e, os.ErrNotExist) {
			retErr = errors.Join(retErr, e)
		}
	}()
	content := strings.Repeat("isolation-test", 512)
	if _, err = source.WriteString(content); err != nil {
		return false, err
	}
	if err = clone(target, source); err != nil {
		if errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.ENOTTY) || errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.EXDEV) || errors.Is(err, syscall.ENOSYS) || errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			return false, nil
		}
		return false, err
	}
	if _, err = target.WriteAt([]byte("changed"), 0); err != nil {
		return false, err
	}
	if err = target.Truncate(8); err != nil {
		return false, err
	}
	targetClosed = true
	if err = target.Close(); err != nil {
		return false, err
	}
	if err = remove(b); err != nil {
		return false, err
	}
	got := make([]byte, len(content))
	if _, err = source.ReadAt(got, 0); err != nil {
		return false, err
	}
	if string(got) != content {
		return false, ErrUnsafe
	}
	return true, nil
}

// syncTree 对提交数据刷盘，不忽略应用产物，也不接受链接或特殊文件。
func syncTree(ctx context.Context, root *os.Root) error {
	base, err := root.Open(".")
	if err != nil {
		return err
	}
	baseMount, err := mountIdentity(base)
	base.Close()
	if err != nil {
		return err
	}
	// 此处根目录是对象容器，UDID 目录才对应复制和摘要扫描的深度零。
	return walkTreeDepth(ctx, root, -1, func(path string, info fs.FileInfo) error {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || (!info.IsDir() && stat.Nlink != 1) {
			return ErrUnsafe
		}
		f, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		actual, err := f.Stat()
		if err != nil || !os.SameFile(info, actual) {
			return ErrChanged
		}
		mount, err := mountIdentity(f)
		if err != nil {
			return err
		}
		if mount != baseMount {
			return ErrUnsafe
		}
		return f.Sync()
	})
}

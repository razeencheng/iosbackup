package backupstore

import (
	"archive/tar"
	"context"
	"io"
	"os"
	"path"
	"syscall"
)

// Export streams the original source directory. The caller holds the lease until
// the stream has stopped; closing a failed archive would hide truncation.
func (l *Lease) Export(ctx context.Context, out io.Writer, udid string) error {
	if !udidPattern.MatchString(udid) || udid != l.udid {
		return ErrInvalid
	}
	for _, name := range []string{"Info.plist", "Status.plist", "Manifest.plist", "Manifest.db"} {
		info, err := l.Root.Lstat(name)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return ErrUnsafe
		}
	}
	before, err := stampTree(ctx, l.Root)
	if err != nil {
		return err
	}
	archive := tar.NewWriter(out)
	buffer := make([]byte, 128<<10)
	err = walkTree(ctx, l.Root, func(name string, info os.FileInfo) error {
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = path.Join(udid, name)
		header.Uid, header.Gid = 0, 0
		header.Uname, header.Gname = "", ""
		if info.IsDir() {
			header.Name += "/"
			return archive.WriteHeader(header)
		}
		file, err := l.Root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		defer file.Close()
		actual, err := file.Stat()
		if err != nil {
			return err
		}
		if !sameFileVersion(info, actual) {
			return ErrChanged
		}
		if err = archive.WriteHeader(header); err != nil {
			return err
		}
		if err = copyBytes(ctx, archive, io.LimitReader(file, info.Size()), buffer); err != nil {
			return err
		}
		actual, err = file.Stat()
		if err != nil {
			return err
		}
		if !sameFileVersion(info, actual) {
			return ErrChanged
		}
		return nil
	})
	if err != nil {
		return err
	}
	after, err := stampTree(ctx, l.Root)
	if err != nil {
		return err
	}
	if before != after {
		return ErrChanged
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return archive.Close()
}

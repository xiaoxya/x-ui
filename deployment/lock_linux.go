//go:build linux

package deployment

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// Lock serializes installers. The fixed lock file must reside in a trusted
// directory; O_NOFOLLOW rejects a pre-existing symbolic link.
func Lock(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || stat.Nlink != 1 || int(stat.Uid) != os.Geteuid() {
		_ = f.Close()
		return nil, fmt.Errorf("unsafe lock file ownership or permissions")
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another installation may be running: %w", err)
	}
	return f, nil
}

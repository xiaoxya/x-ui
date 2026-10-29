//go:build linux

package deployment

import (
	"errors"
	"os"
	"syscall"
)

func directoryIdentity(path string) (uint64, uint64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, 0, err
	}
	if !info.IsDir() {
		return 0, 0, errors.New("not a real directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Ino == 0 {
		return 0, 0, errors.New("directory identity unavailable")
	}
	return uint64(stat.Dev), stat.Ino, nil
}

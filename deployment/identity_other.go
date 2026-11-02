//go:build !linux

package deployment

import (
	"errors"
	"os"
)

func directoryIdentity(string) (uint64, uint64, error) {
	return 0, 0, errors.New("directory identity requires Linux")
}

func preserveFileOwner(os.FileInfo, string) error {
	return errors.New("preserving file ownership requires Linux")
}

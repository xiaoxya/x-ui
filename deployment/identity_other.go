//go:build !linux

package deployment

import "errors"

func directoryIdentity(string) (uint64, uint64, error) {
	return 0, 0, errors.New("directory identity requires Linux")
}

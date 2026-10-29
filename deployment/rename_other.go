//go:build !linux

package deployment

import "errors"

func renameNoReplace(_, _ string) error {
	return errors.New("atomic no-replace installation requires Linux")
}

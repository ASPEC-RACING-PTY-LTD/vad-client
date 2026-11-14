//go:build !windows

package avscan

import "errors"

func scanFile(path, name string) (Verdict, error) {
	return Unavailable, errors.New("AMSI is only available on Windows")
}

//go:build !windows
// +build !windows

package logpipe

import (
	"os"
	"syscall"
)

func setNonblock(file *os.File) error {
	return syscall.SetNonblock(int(file.Fd()), true)
}

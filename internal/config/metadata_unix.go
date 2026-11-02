//go:build !windows

package config

import (
	"os"
	"syscall"
)

func preserveMetadata(f *os.File, info os.FileInfo) error {
	if err := f.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return f.Chown(int(st.Uid), int(st.Gid))
	}
	return nil
}

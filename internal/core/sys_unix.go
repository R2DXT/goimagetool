//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly || solaris

package core

import (
	"os"
	"syscall"
)

func osUIDGID(fi os.FileInfo) (uint32, uint32) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint32(st.Uid), uint32(st.Gid)
	}
	return 0, 0
}

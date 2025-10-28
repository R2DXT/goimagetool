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

// mknod for char/block/fifo
func mknodSpecial(path string, mode uint32, major, minor uint32) error {
	// simple old-style makedev; works for many cases
	dev := int((major << 8) | (minor & 0xff))
	return syscall.Mknod(path, mode, dev)
}

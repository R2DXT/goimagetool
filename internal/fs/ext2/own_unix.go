//go:build unix

package ext2

import (
	"os"
	"syscall"

	"goimagetool/internal/fs/memfs"
)

func chown(path string, uid, gid int) error  { return os.Chown(path, uid, gid) }
func lchown(path string, uid, gid int) error { return os.Lchown(path, uid, gid) }

func mkfifo(path string, perm uint32) error {
	return syscall.Mkfifo(path, uint32(perm&0o7777))
}

func mknod(path string, e *memfs.Entry, maj, min uint32) error {
	mode := uint32(e.Mode) & 0o7777
	var t uint32 = syscall.S_IFCHR
	if (e.Mode & memfs.ModeBlock) != 0 {
		t = syscall.S_IFBLK
	}
	rdev := (maj << 8) | (min & 0xff) | ((min &^ 0xff) << 12)
	return syscall.Mknod(path, mode|t, int(rdev))
}

func uidOf(fi os.FileInfo) uint32 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint32(st.Uid)
	}
	return 0
}
func gidOf(fi os.FileInfo) uint32 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint32(st.Gid)
	}
	return 0
}
func rdevOf(fi os.FileInfo) (uint32, uint32) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		r := uint32(st.Rdev)
		maj := (r >> 8) & 0xfff
		min := (r & 0xff) | ((r >> 12) &^ 0xff)
		return maj, min
	}
	return 0, 0
}

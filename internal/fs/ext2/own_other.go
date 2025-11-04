//go:build !unix

package ext2

import (
	"os"
	"path/filepath"

	"goimagetool/internal/fs/memfs"
)

func chown(path string, uid, gid int) error  { return nil }
func lchown(path string, uid, gid int) error { return nil }

func mkfifo(path string, perm uint32) error {
	// emulate with empty file to keep tree shape on non-unix
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	return os.WriteFile(path, []byte{}, os.FileMode(perm&0o7777))
}

func mknod(path string, e *memfs.Entry, maj, min uint32) error {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	return os.WriteFile(path, []byte{}, os.FileMode(uint32(e.Mode)&0o7777))
}

func uidOf(fi os.FileInfo) uint32                 { return 0 }
func gidOf(fi os.FileInfo) uint32                 { return 0 }
func rdevOf(fi os.FileInfo) (uint32, uint32)      { return 0, 0 }

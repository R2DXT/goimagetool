//go:build !unix

package ext2

import (
	"os"
	"path/filepath"

	"goimagetool/internal/fs/memfs"
)

func chown(string, int, int) error  { return nil }
func lchown(string, int, int) error { return nil }

func mkfifo(path string, perm uint32) error {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	return os.WriteFile(path, nil, os.FileMode(perm&0o7777))
}

func mknod(path string, e *memfs.Entry, _, _ uint32) error {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	return os.WriteFile(path, nil, os.FileMode(uint32(e.Mode)&0o7777))
}

func uidOf(os.FileInfo) uint32            { return 0 }
func gidOf(os.FileInfo) uint32            { return 0 }
func rdevOf(os.FileInfo) (uint32, uint32) { return 0, 0 }

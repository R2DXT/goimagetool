//go:build unix

package ext2

import "os"

func chown(path string, uid, gid int) error  { return os.Chown(path, uid, gid) }
func lchown(path string, uid, gid int) error { return os.Lchown(path, uid, gid) }

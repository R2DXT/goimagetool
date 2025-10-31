//go:build !unix

package ext2

func chown(path string, uid, gid int) error  { return nil }
func lchown(path string, uid, gid int) error { return nil }

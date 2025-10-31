//go:build !unix

package squashfs

func chown(path string, uid, gid int) error  { return nil }
func lchown(path string, uid, gid int) error { return nil }

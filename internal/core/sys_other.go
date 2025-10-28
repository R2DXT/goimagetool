//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly && !solaris

package core

import "os"

func osUIDGID(fi os.FileInfo) (uint32, uint32) { return 0, 0 }

func mknodSpecial(path string, mode uint32, major, minor uint32) error {
	return nil // unsupported OS: no-op
}

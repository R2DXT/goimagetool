//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly && !solaris

package core

import "os"

func osUIDGID(fi os.FileInfo) (uint32, uint32) { return 0, 0 }

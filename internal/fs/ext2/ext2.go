package ext2

import (
	"io"
	"goimagetool/internal/common"
	"goimagetool/internal/fs/memfs"
)

func Load(r io.Reader) (*memfs.FS, error) {
	return nil, common.ErrUnsupported
}

func Store(w io.Writer, fs *memfs.FS, blockSize int) error {
	return common.ErrUnsupported
}

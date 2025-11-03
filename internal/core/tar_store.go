package core

import (
	"compress/gzip"
	"io"
	"os"
	"strings"

	"goimagetool/internal/common"
	"goimagetool/internal/image/tarball"
)

func (s *State) StoreTar(path, comp string) error {
	if s.FS == nil {
		return common.ErrNoImage
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var w io.Writer = f
	var cw io.Closer

	switch strings.ToLower(comp) {
	case "gz", "gzip":
		gw := gzip.NewWriter(f)
		cw = gw
		w = gw
	case "none", "":
		// no-op
	default:
		// пока поддерживаем только tar и tar.gz
		gw := gzip.NewWriter(f)
		cw = gw
		w = gw
	}

	err = tarball.Write(s.FS, w)
	if cerr := closeIf(cw); err == nil {
		err = cerr
	}
	return err
}

func closeIf(c io.Closer) error {
	if c != nil {
		return c.Close()
	}
	return nil
}

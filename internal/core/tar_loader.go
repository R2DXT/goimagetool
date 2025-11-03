package core

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"strings"

	"goimagetool/internal/fs/memfs"
	"goimagetool/internal/image/tarball"
)

func (s *State) LoadTar(path, comp string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// auto-detect compression by name if requested
	if comp == "" || strings.ToLower(comp) == "auto" {
		l := strings.ToLower(path)
		switch {
		case strings.HasSuffix(l, ".tar.gz"), strings.HasSuffix(l, ".tgz"), strings.HasSuffix(l, ".tar.gzip"):
			comp = "gzip"
		default:
			comp = "none"
		}
	}

	var r io.Reader = f
	var gr *gzip.Reader

	switch strings.ToLower(comp) {
	case "none":
		// no-op
	case "gz", "gzip":
		g, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		gr = g
		r = g
	default:
		return fmt.Errorf("unsupported tar compression: %s", comp)
	}

	if s.FS == nil {
		s.FS = memfs.New()
	}

	err = tarball.Load(s.FS, r)
	if gr != nil {
		_ = gr.Close()
	}
	if err != nil {
		return err
	}

	// Не трогаем Kind/Meta.
	return nil
}

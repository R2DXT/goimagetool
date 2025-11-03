package tarball

import (
	"archive/tar"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"goimagetool/internal/fs/memfs"
)

// Load: fill MemFS from an uncompressed tar stream.
func Load(m *memfs.FS, r io.Reader) error {
	tr := tar.NewReader(r)
	snap := m.Snapshot()

	ensureParents := func(p string, uid, gid uint32, mt time.Time) {
		p = filepath.ToSlash(p)
		if p == "" || p == "/" {
			return
		}
		dir := filepath.ToSlash(filepath.Dir(p))
		if dir == "." {
			dir = "/"
		}
		stack := []string{}
		for dir != "/" && snap[dir] == nil {
			stack = append(stack, dir)
			dir = filepath.ToSlash(filepath.Dir(dir))
			if dir == "." {
				dir = "/"
			}
		}
		for i := len(stack) - 1; i >= 0; i-- {
			d := stack[i]
			if snap[d] == nil {
				m.PutDir(d, uid, gid, mt)
				snap = m.Snapshot()
			}
		}
	}

	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := "/" + strings.TrimLeft(filepath.ToSlash(h.Name), "/")
		uid, gid := uint32(h.Uid), uint32(h.Gid)
		mt := h.ModTime
		if mt.IsZero() {
			mt = time.Now()
		}
		perm := memfs.Mode(uint32(h.Mode) & 0o7777)

		switch h.Typeflag {
		case tar.TypeDir:
			if name != "/" {
				m.PutDir(name, uid, gid, mt)
			}

		case tar.TypeSymlink:
			ensureParents(name, uid, gid, mt)
			m.PutSymlink(name, h.Linkname, uid, gid, mt)

		case tar.TypeChar:
			ensureParents(name, uid, gid, mt)
			m.PutNode(name, memfs.ModeChar, uint32(perm), uid, gid, uint32(h.Devmajor), uint32(h.Devminor), mt)

		case tar.TypeBlock:
			ensureParents(name, uid, gid, mt)
			m.PutNode(name, memfs.ModeBlock, uint32(perm), uid, gid, uint32(h.Devmajor), uint32(h.Devminor), mt)

		case tar.TypeFifo:
			ensureParents(name, uid, gid, mt)
			m.PutNode(name, memfs.ModeFIFO, uint32(perm), uid, gid, 0, 0, mt)

		case tar.TypeReg, tar.TypeRegA:
			ensureParents(name, uid, gid, mt)
			var buf []byte
			if h.Size > 0 {
				buf = make([]byte, 0, h.Size)
				tmp := make([]byte, 32*1024)
				for {
					n, er := tr.Read(tmp)
					if n > 0 {
						buf = append(buf, tmp[:n]...)
					}
					if er == io.EOF {
						break
					}
					if er != nil {
						return er
					}
				}
			}
			m.PutFile(name, buf, perm, uid, gid, mt)

		default:
			// skip others
		}
	}
	return nil
}

// Write: dump MemFS into an uncompressed tar stream.
func Write(m *memfs.FS, w io.Writer) error {
	tw := tar.NewWriter(w)
	defer tw.Close()

	snap := m.Snapshot()
	paths := make([]string, 0, len(snap))
	for p := range snap {
		if p == "/" {
			continue
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		e := snap[p]
		if e == nil {
			continue
		}
		name := strings.TrimPrefix(filepath.ToSlash(e.Name), "/")
		if name == "" {
			continue
		}

		h := &tar.Header{
			Name:    name,
			Mode:    int64(uint32(e.Mode) & 0o7777),
			ModTime: e.MTime,
			Uid:     int(e.UID),
			Gid:     int(e.GID),
		}
		if h.ModTime.IsZero() {
			h.ModTime = time.Now()
		}

		switch {
		case e.Mode&memfs.ModeDir != 0:
			if !strings.HasSuffix(h.Name, "/") {
				h.Name += "/"
			}
			h.Typeflag = tar.TypeDir
			h.Size = 0
			if err := tw.WriteHeader(h); err != nil {
				return err
			}

		case e.Mode&memfs.ModeLink != 0:
			h.Typeflag = tar.TypeSymlink
			h.Linkname = e.Target
			h.Size = 0
			if err := tw.WriteHeader(h); err != nil {
				return err
			}

		case e.Mode&memfs.ModeChar != 0:
			h.Typeflag = tar.TypeChar
			h.Size = 0
			if err := tw.WriteHeader(h); err != nil {
				return err
			}

		case e.Mode&memfs.ModeBlock != 0:
			h.Typeflag = tar.TypeBlock
			h.Size = 0
			if err := tw.WriteHeader(h); err != nil {
				return err
			}

		case e.Mode&memfs.ModeFIFO != 0:
			h.Typeflag = tar.TypeFifo
			h.Size = 0
			if err := tw.WriteHeader(h); err != nil {
				return err
			}

		default:
			h.Typeflag = tar.TypeReg
			h.Size = int64(len(e.Data))
			if err := tw.WriteHeader(h); err != nil {
				return err
			}
			if len(e.Data) > 0 {
				if _, err := tw.Write(e.Data); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

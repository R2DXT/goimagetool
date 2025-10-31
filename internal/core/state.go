package core

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"goimagetool/internal/compress"
	"goimagetool/internal/fs/ext2"
	"goimagetool/internal/fs/memfs"
	"goimagetool/internal/image/cpio"
	"goimagetool/internal/image/squashfs"
	"goimagetool/internal/image/uboot/fit"
	"goimagetool/internal/image/uboot/legacy"
)

type ImageKind int

const (
	KindNone ImageKind = iota
	KindInitramfs
	KindKernelLegacy
	KindKernelFIT
	KindSquashFS
	KindExt2
)

type State struct {
	Kind ImageKind
	FS   *memfs.FS
	Meta any
	Raw  []byte
}

func New() *State { return &State{Kind: KindNone, FS: memfs.New()} }

// ========== initramfs / CPIO ==========

func (s *State) LoadInitramfs(path string, compressionName string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	compressionName = strings.ToLower(compressionName)
	switch compressionName {
	case "", "auto":
		if kind := compress.Detect(b); kind != "none" {
			if b, err = compress.Decompress(b, kind); err != nil {
				return fmt.Errorf("decompress (%s): %w", kind, err)
			}
		}
	case "none":
	default:
		if b, err = compress.Decompress(b, compressionName); err != nil {
			return fmt.Errorf("decompress (%s): %w", compressionName, err)
		}
	}
	r := bytes.NewReader(b)
	fs, err := cpio.LoadNewc(r)
	if err != nil {
		return err
	}
	s.Kind = KindInitramfs
	s.FS = fs
	s.Meta = nil
	s.Raw = nil
	return nil
}

func (s *State) StoreInitramfs(path string, compressionName string) error {
	var buf bytes.Buffer
	if err := cpio.StoreNewc(&buf, s.FS); err != nil {
		return err
	}
	out := buf.Bytes()
	if strings.ToLower(compressionName) != "" && strings.ToLower(compressionName) != "none" {
		cmp, err := compress.Compress(out, compressionName)
		if err != nil {
			return fmt.Errorf("compress (%s): %w", compressionName, err)
		}
		out = cmp
	}
	return os.WriteFile(path, out, 0644)
}

// ========== uImage (legacy) ==========

func (s *State) LoadKernelLegacy(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h, data, err := legacy.Read(f)
	if err != nil {
		return err
	}
	s.Kind = KindKernelLegacy
	s.FS = memfs.New()
	s.Meta = h
	s.Raw = data
	return nil
}

func (s *State) StoreKernelLegacy(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h, _ := s.Meta.(*legacy.Header)
	if h == nil {
		return errors.New("no uImage header")
	}
	return legacy.Write(f, h, s.Raw)
}

// ========== FIT/ITB ==========

type FitMeta struct{ F *fit.FIT }

func (s *State) LoadKernelFIT(path string, compressionName string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	switch strings.ToLower(compressionName) {
	case "auto", "":
		if kind := compress.Detect(b); kind != "none" {
			if b, err = compress.Decompress(b, kind); err != nil {
				return fmt.Errorf("decompress (%s): %w", kind, err)
			}
		}
	case "none":
	default:
		if b, err = compress.Decompress(b, compressionName); err != nil {
			return fmt.Errorf("decompress (%s): %w", compressionName, err)
		}
	}
	r := bytes.NewReader(b)
	F, err := fit.Read(r)
	if err != nil {
		return err
	}
	s.Kind = KindKernelFIT
	s.FS = memfs.New()
	s.Meta = &FitMeta{F: F}
	s.Raw = nil
	return nil
}

func (s *State) StoreKernelFIT(path string, compressionName string) error {
	var buf bytes.Buffer
	if m, _ := s.Meta.(*FitMeta); m != nil && m.F != nil {
		if err := fit.Write(&buf, m.F); err != nil {
			return err
		}
	} else {
		return errors.New("no FIT meta")
	}
	out := buf.Bytes()
	if strings.ToLower(compressionName) != "" && strings.ToLower(compressionName) != "none" {
		cmp, err := compress.Compress(out, compressionName)
		if err != nil {
			return fmt.Errorf("compress (%s): %w", compressionName, err)
		}
		out = cmp
	}
	return os.WriteFile(path, out, 0644)
}

// ========== SquashFS ==========

type SquashMeta struct{ SB *squashfs.Superblock }

func (s *State) LoadSquashFS(path string, compressionName string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	switch strings.ToLower(compressionName) {
	case "auto", "":
		if kind := compress.Detect(b); kind != "none" {
			if b, err = compress.Decompress(b, kind); err != nil {
				return fmt.Errorf("decompress (%s): %w", kind, err)
			}
		}
	case "none":
	default:
		if b, err = compress.Decompress(b, compressionName); err != nil {
			return fmt.Errorf("decompress (%s): %w", compressionName, err)
		}
	}
	fs, sb, err := squashfs.Load(bytes.NewReader(b), "auto")
	if err != nil {
		return err
	}
	s.Kind, s.FS, s.Meta, s.Raw = KindSquashFS, fs, &SquashMeta{SB: sb}, nil
	return nil
}

func (s *State) StoreSquashFS(path string, compressionName string) error {
	var buf bytes.Buffer
	opts := squashfs.Options{
		Compression: strings.ToLower(strings.TrimSpace(compressionName)),
	}
	if err := squashfs.Store(&buf, s.FS, opts); err != nil {
		return err
	}
	// squashfs уже сжат внутренним кодеком
	return os.WriteFile(path, buf.Bytes(), 0644)
}

// ========== EXT2 ==========

func (s *State) LoadExt2(path string, compressionName string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	switch strings.ToLower(compressionName) {
	case "auto", "":
		if kind := compress.Detect(b); kind != "none" {
			if b, err = compress.Decompress(b, kind); err != nil {
				return fmt.Errorf("decompress (%s): %w", kind, err)
			}
		}
	case "none":
	default:
		if b, err = compress.Decompress(b, compressionName); err != nil {
			return fmt.Errorf("decompress (%s): %w", compressionName, err)
		}
	}
	fs, err := ext2.Load(bytes.NewReader(b))
	if err != nil {
		return err
	}
	s.Kind = KindExt2
	s.FS = fs
	s.Meta = nil
	return nil
}

func (s *State) StoreExt2(path string, blockSize int, compressionName string) error {
	var buf bytes.Buffer
	if err := ext2.Store(&buf, s.FS, blockSize); err != nil {
		return err
	}
	out := buf.Bytes()
	if strings.ToLower(compressionName) != "" && strings.ToLower(compressionName) != "none" {
		cmp, err := compress.Compress(out, compressionName)
		if err != nil {
			return fmt.Errorf("compress (%s): %w", compressionName, err)
		}
		out = cmp
	}
	return os.WriteFile(path, out, 0644)
}

// ========== FS helpers / extract/info ==========

func (s *State) FSAddLocal(src, dst string) error {
	dst = filepath.ToSlash(dst)
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t, err := os.Readlink(src)
		if err != nil {
			return err
		}
		uid, gid := osUIDGID(info)
		s.FS.PutSymlink(dst, t, uid, gid, info.ModTime())
		return nil
	}
	if info.IsDir() {
		return filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(src, p)
			out := filepath.ToSlash(filepath.Join(dst, rel))
			uid, gid := osUIDGID(fi)
			if fi.Mode()&os.ModeSymlink != 0 {
				t, err := os.Readlink(p)
				if err != nil {
					return err
				}
				s.FS.PutSymlink(out, t, uid, gid, fi.ModTime())
				return nil
			}
			if fi.IsDir() {
				perm := uint32(fi.Mode().Perm())
				s.FS.PutDirMode(out, memfs.Mode(0040000|perm), uid, gid, fi.ModTime())
				return nil
			}
			if (fi.Mode()&os.ModeDevice) != 0 || (fi.Mode()&os.ModeNamedPipe) != 0 {
				var maj, min uint32
				if st, ok := fi.Sys().(*syscall.Stat_t); ok {
					maj = uint32((st.Rdev >> 8) & 0xff)
					min = uint32(st.Rdev & 0xff)
				}
				perm := uint32(fi.Mode().Perm())
				var typ memfs.Mode
				if (fi.Mode() & os.ModeCharDevice) != 0 {
					typ = memfs.ModeChar
				} else if (fi.Mode() & os.ModeDevice) != 0 {
					typ = memfs.ModeBlock
				} else {
					typ = memfs.ModeFIFO
				}
				s.FS.PutNode(out, typ, perm, uid, gid, maj, min, fi.ModTime())
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			mode := memfs.Mode(0100000 | uint32(fi.Mode().Perm()))
			s.FS.PutFile(out, data, mode, uid, gid, fi.ModTime())
			return nil
		})
	} else {
		uid, gid := osUIDGID(info)
		if info.Mode()&os.ModeNamedPipe != 0 || info.Mode()&os.ModeDevice != 0 {
			perm := uint32(info.Mode().Perm())
			var typ memfs.Mode
			if (info.Mode() & os.ModeCharDevice) != 0 {
				typ = memfs.ModeChar
			} else if (info.Mode() & os.ModeDevice) != 0 {
				typ = memfs.ModeBlock
			} else {
				typ = memfs.ModeFIFO
			}
			s.FS.PutNode(dst, typ, perm, uid, gid, 0, 0, info.ModTime())
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			t, err := os.Readlink(src)
			if err != nil {
				return err
			}
			s.FS.PutSymlink(dst, t, uid, gid, info.ModTime())
			return nil
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		mode := memfs.Mode(0100000 | uint32(info.Mode().Perm()))
		s.FS.PutFile(dst, data, mode, uid, gid, info.ModTime())
		return nil
	}
}

func (s *State) FSExtract(dst string) error {
	return s.FS.Walk(func(e *memfs.Entry) error {
		if e.Name == "/" {
			return nil
		}
		out := filepath.Join(dst, strings.TrimPrefix(e.Name, "/"))
		if e.Mode&memfs.ModeDir != 0 {
			return os.MkdirAll(out, 0755)
		}
		if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
			return err
		}
		switch {
		case e.Mode&memfs.ModeLink != 0:
			_ = os.Remove(out)
			return os.Symlink(e.Target, out)
		case e.Mode&memfs.ModeChar != 0:
			return mknodSpecial(out, uint32(e.Mode), e.RdevMajor, e.RdevMinor)
		case e.Mode&memfs.ModeBlock != 0:
			return mknodSpecial(out, uint32(e.Mode), e.RdevMajor, e.RdevMinor)
		case e.Mode&memfs.ModeFIFO != 0:
			return mknodSpecial(out, uint32(e.Mode), 0, 0)
		default:
			return os.WriteFile(out, e.Data, 0644)
		}
	})
}

func (s *State) Info() string {
	switch s.Kind {
	case KindInitramfs:
		return "Kind: initramfs (cpio)"
	case KindKernelLegacy:
		if h, _ := s.Meta.(*legacy.Header); h != nil {
			return "Kind: kernel-legacy\n" + h.String()
		}
	case KindKernelFIT:
		if m, _ := s.Meta.(*FitMeta); m != nil {
			return "Kind: kernel-fit (itb) images=" + fmt.Sprint(m.F.List())
		}
	case KindSquashFS:
		if m, _ := s.Meta.(*SquashMeta); m != nil && m.SB != nil {
			return fmt.Sprintf("Kind: squashfs v%d.%d (block=%d)", m.SB.Major, m.SB.Minor, m.SB.BlockSize)
		}
	case KindExt2:
		return "Kind: ext2"
	}
	return "Kind: none"
}

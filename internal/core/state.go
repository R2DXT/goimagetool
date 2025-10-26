package core

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"goimagetool/internal/compress"
	"goimagetool/internal/fs/ext2"
	"goimagetool/internal/fs/memfs"
	"goimagetool/internal/image/cpio"
	"goimagetool/internal/image/uboot/fit"
	"goimagetool/internal/image/uboot/legacy"
)

type ImageKind int
const (
	KindNone ImageKind = iota
	KindInitramfs
	KindKernelLegacy
	KindKernelFIT
	KindExt2
)

type State struct {
	Kind ImageKind
	FS   *memfs.FS
	Meta any
	Raw  []byte
}

func New() *State { return &State{Kind: KindNone, FS: memfs.New()} }

func (s *State) LoadInitramfs(path string, compression string) error {
	b, err := os.ReadFile(path)
	if err != nil { return err }
	var r io.Reader = bytes.NewReader(b)
	if compression == "gzip" || strings.EqualFold(compression, "gz") {
		var out bytes.Buffer
		if err := compress.GzipDecompress(&out, r); err != nil { return fmt.Errorf("gzip: %w", err) }
		r = bytes.NewReader(out.Bytes())
	}
	fs, err := cpio.LoadNewc(r)
	if err != nil { return err }
	s.Kind = KindInitramfs
	s.FS = fs
	s.Meta = nil
	s.Raw = nil
	return nil
}

func (s *State) StoreInitramfs(path string, compression string) error {
	var buf bytes.Buffer
	if err := cpio.StoreNewc(&buf, s.FS); err != nil { return err }
	var out []byte
	if compression == "gzip" || strings.EqualFold(compression, "gz") {
		var gz bytes.Buffer
		if err := compress.GzipCompress(&gz, bytes.NewReader(buf.Bytes())); err != nil { return err }
		out = gz.Bytes()
	} else {
		out = buf.Bytes()
	}
	return os.WriteFile(path, out, 0644)
}

func (s *State) LoadKernelLegacy(path string) error {
	f, err := os.Open(path); if err != nil { return err }
	defer f.Close()
	h, data, err := legacy.Read(f)
	if err != nil { return err }
	s.Kind = KindKernelLegacy
	s.FS = memfs.New()
	s.Meta = h
	s.Raw = data
	return nil
}

func (s *State) StoreKernelLegacy(path string) error {
	f, err := os.Create(path); if err != nil { return err }
	defer f.Close()
	h, _ := s.Meta.(*legacy.Header)
	if h == nil { return errors.New("no uImage header") }
	return legacy.Write(f, h, s.Raw)
}

// FIT/ITB
type FitMeta struct { F *fit.FIT }

func (s *State) LoadKernelFIT(path string) error {
	f, err := os.Open(path); if err != nil { return err }
	defer f.Close()
	F, err := fit.Read(f)
	if err != nil { return err }
	s.Kind = KindKernelFIT
	s.FS = memfs.New()
	s.Meta = &FitMeta{F: F}
	s.Raw = nil
	return nil
}

func (s *State) StoreKernelFIT(path string) error {
	f, err := os.Create(path); if err != nil { return err }
	defer f.Close()
	m, _ := s.Meta.(*FitMeta)
	if m == nil || m.F == nil { return errors.New("no FIT meta") }
	return fit.Write(f, m.F)
}

// EXT2
func (s *State) LoadExt2(path string) error {
	f, err := os.Open(path); if err != nil { return err }
	defer f.Close()
	fs, err := ext2.Load(f)
	if err != nil { return err }
	s.Kind = KindExt2
	s.FS = fs
	s.Meta = nil
	return nil
}

func (s *State) StoreExt2(path string, blockSize int) error {
	f, err := os.Create(path); if err != nil { return err }
	defer f.Close()
	return ext2.Store(f, s.FS, blockSize)
}

// FS helpers
func (s *State) FSAddLocal(src, dst string) error {
	dst = filepath.ToSlash(dst)
	info, err := os.Stat(src)
	if err != nil { return err }
	if info.IsDir() {
		return filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
			if err != nil { return err }
			rel, _ := filepath.Rel(src, p)
			out := filepath.ToSlash(filepath.Join(dst, rel))
			uid, gid := osUIDGID(fi)
			if fi.IsDir() {
				perm := uint32(fi.Mode().Perm())
				s.FS.PutDirMode(out, memfs.Mode(0040000|perm), uid, gid, fi.ModTime())
				return nil
			}
			data, err := os.ReadFile(p); if err != nil { return err }
			mode := memfs.Mode(0100000 | uint32(fi.Mode().Perm()))
			s.FS.PutFile(out, data, mode, uid, gid, fi.ModTime())
			return nil
		})
	} else {
		data, err := os.ReadFile(src); if err != nil { return err }
		uid, gid := osUIDGID(info)
		mode := memfs.Mode(0100000 | uint32(info.Mode().Perm()))
		s.FS.PutFile(dst, data, mode, uid, gid, info.ModTime())
		return nil
	}
}

func (s *State) FSExtract(dst string) error {
	return s.FS.Walk(func(e *memfs.Entry) error {
		if e.Name == "/" { return nil }
		out := filepath.Join(dst, strings.TrimPrefix(e.Name, "/"))
		if e.Mode & memfs.ModeDir != 0 {
			return os.MkdirAll(out, 0755)
		}
		if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil { return err }
		return os.WriteFile(out, e.Data, 0644)
	})
}

func (s *State) Info() string {
	switch s.Kind {
	case KindInitramfs:
		return "Kind: initramfs (cpio)"
	case KindKernelLegacy:
		if h, _ := s.Meta.(*legacy.Header); h != nil { return "Kind: kernel-legacy\n" + h.String() }
	case KindKernelFIT:
		if m, _ := s.Meta.(*FitMeta); m != nil { return "Kind: kernel-fit (itb) images=" + fmt.Sprint(m.F.List()) }
	case KindExt2:
		return "Kind: ext2"
	}
	return "Kind: none"
}

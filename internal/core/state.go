package core

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"goimagetool/internal/compress"
	"goimagetool/internal/fs/ext2"
	"goimagetool/internal/fs/memfs"
	"goimagetool/internal/image/cpio"
	"goimagetool/internal/image/squashfs"
	"goimagetool/internal/image/uboot/fit"
	"goimagetool/internal/image/uboot/legacy"
)

// ImageKind denotes currently loaded image type.
type ImageKind int

const (
	KindNone ImageKind = iota
	KindInitramfs
	KindKernelLegacy
	KindKernelFIT
	KindSquashFS
	KindExt2
	KindTar
)

func (k ImageKind) String() string {
	switch k {
	case KindInitramfs:
		return "initramfs"
	case KindKernelLegacy:
		return "kernel-legacy"
	case KindKernelFIT:
		return "kernel-fit"
	case KindSquashFS:
		return "squashfs"
	case KindExt2:
		return "ext2"
	case KindTar:
		return "tar"
	default:
		return "none"
	}
}

// Metadata holders for non-FS images.
type UImageMeta struct {
	H *legacy.Header
}

type FitMeta struct {
	F *fit.FIT
}

type SquashMeta struct {
	Super *squashfs.Superblock
}

type State struct {
	Kind ImageKind
	FS   *memfs.FS
	Meta any

	// Raw keeps last raw payload for formats that are not mapped to FS directly.
	Raw []byte
}

func New() *State {
	return &State{
		Kind: KindNone,
		FS:   memfs.New(),
	}
}

func (s *State) Info() string {
	return fmt.Sprintf("Kind: %s", s.Kind.String())
}

// ---------------------------- Initramfs / CPIO ----------------------------

func (s *State) LoadInitramfs(path string, compressionName string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if strings.ToLower(compressionName) == "auto" || (compressionName != "" && strings.ToLower(compressionName) != "none") {
		out, _, derr := compress.DecompressAuto(b)
		if derr == nil {
			b = out
		} else if strings.ToLower(compressionName) != "auto" {
			return derr
		}
	}
	fs, err := cpio.LoadNewc(bytes.NewReader(b))
	if err != nil {
		return err
	}
	s.Kind = KindInitramfs
	s.FS = fs
	s.Raw = b
	s.Meta = nil
	return nil
}

func (s *State) StoreInitramfs(path string, compressionName string) error {
	if s.FS == nil {
		return errors.New("no image")
	}
	var buf bytes.Buffer
	if err := cpio.StoreNewc(&buf, s.FS); err != nil {
		return err
	}
	data := buf.Bytes()
	if compressionName != "" && strings.ToLower(compressionName) != "none" {
		enc, err := compress.Compress(data, compressionName)
		if err != nil {
			return err
		}
		data = enc
	}
	return os.WriteFile(path, data, 0o644)
}

// ---------------------------- U-Boot legacy ----------------------------

func (s *State) LoadKernelLegacy(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	h, payload, err := legacy.Read(f)
	if err != nil {
		return err
	}
	s.Kind = KindKernelLegacy
	s.Meta = &UImageMeta{H: h}
	s.Raw = payload

	// If payload looks like CPIO, map it to FS for convenience.
	if len(payload) >= 6 && string(payload[:6]) == "070701" {
		if fs, err := cpio.LoadNewc(bytes.NewReader(payload)); err == nil {
			s.FS = fs
		}
	}
	return nil
}

func (s *State) StoreKernelLegacy(path string) error {
	m, _ := s.Meta.(*UImageMeta)
	if m == nil || m.H == nil {
		return errors.New("no uImage header in meta")
	}
	data := s.Raw
	if data == nil && s.FS != nil {
		var buf bytes.Buffer
		if err := cpio.StoreNewc(&buf, s.FS); err == nil {
			data = buf.Bytes()
		}
	}
	if data == nil {
		data = []byte{}
	}
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	return legacy.Write(out, m.H, data)
}

// ---------------------------- U-Boot FIT / ITB ----------------------------

func (s *State) LoadKernelFIT(path string, compressionName string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// Accept compressed ITB as convenience.
	if strings.ToLower(compressionName) == "auto" || (compressionName != "" && strings.ToLower(compressionName) != "none") {
		if out, _, derr := compress.DecompressAuto(b); derr == nil {
			b = out
		} else if strings.ToLower(compressionName) != "auto" {
			return derr
		}
	}
	r := bytes.NewReader(b)
	f, err := fit.Read(r)
	if err != nil {
		return err
	}
	s.Kind = KindKernelFIT
	s.Meta = &FitMeta{F: f}
	s.Raw = b
	return nil
}

func (s *State) StoreKernelFIT(path string, compressionName string) error {
	m, _ := s.Meta.(*FitMeta)
	if m == nil || m.F == nil {
		return errors.New("no FIT loaded")
	}
	var buf bytes.Buffer
	if err := fit.Write(&buf, m.F); err != nil {
		return err
	}
	data := buf.Bytes()
	if compressionName != "" && strings.ToLower(compressionName) != "none" {
		enc, err := compress.Compress(data, compressionName)
		if err != nil {
			return err
		}
		data = enc
	}
	return os.WriteFile(path, data, 0o644)
}

// ---------------------------- SquashFS ----------------------------

func (s *State) LoadSquashFS(path, compression string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	fs, super, err := squashfs.Load(f, compression)
	if err != nil {
		return err
	}
	s.Kind = KindSquashFS
	s.FS = fs
	s.Meta = &SquashMeta{Super: super}
	s.Raw = nil
	return nil
}

func (s *State) StoreSquashFS(path, compression string) error {
	if s.FS == nil {
		return errors.New("no image")
	}
	var buf bytes.Buffer
	opts := squashfs.Options{Compression: compression}
	if err := squashfs.Store(&buf, s.FS, opts); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// ---------------------------- EXT2 (external tools path) ----------------------------

func (s *State) LoadExt2(path, compressionName string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if strings.ToLower(compressionName) == "auto" || (compressionName != "" && strings.ToLower(compressionName) != "none") {
		if out, _, derr := compress.DecompressAuto(b); derr == nil {
			b = out
		} else if strings.ToLower(compressionName) != "auto" {
			return derr
		}
	}
	fs := memfs.New()
	if err := ext2.Load(fs, bytes.NewReader(b)); err != nil {
		return err
	}
	s.Kind = KindExt2
	s.FS = fs
	s.Meta = nil
	s.Raw = b
	return nil
}

func (s *State) StoreExt2(path string, blockSize int, compressionName string) error {
	if s.FS == nil {
		return errors.New("no image")
	}
	var buf bytes.Buffer
	if err := ext2.Store(s.FS, &buf, ext2.Options{BlockSize: blockSize}); err != nil {
		return err
	}
	data := buf.Bytes()
	if compressionName != "" && strings.ToLower(compressionName) != "none" {
		enc, err := compress.Compress(data, compressionName)
		if err != nil {
			return err
		}
		data = enc
	}
	return os.WriteFile(path, data, 0o644)
}

// ---------------------------- FS utils ----------------------------

func (s *State) FSAddLocal(src, dst string) error {
	if s.FS == nil {
		s.FS = memfs.New()
	}
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	mt := info.ModTime()
	if info.Mode()&os.ModeSymlink != 0 {
		tgt, err := os.Readlink(src)
		if err != nil {
			return err
		}
		s.FS.PutSymlink(dst, filepath.ToSlash(tgt), 0, 0, mt)
		return nil
	}
	if info.IsDir() {
		s.FS.PutDir(dst, 0, 0, mt)
		ents, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, de := range ents {
			if err := s.FSAddLocal(filepath.Join(src, de.Name()), filepath.ToSlash(filepath.Join(dst, de.Name()))); err != nil {
				return err
			}
		}
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	mode := memfs.Mode(0o644)
	if info.Mode().Perm()&0o111 != 0 {
		mode = 0o755
	}
	s.FS.PutFile(dst, data, mode, 0, 0, mt)
	return nil
}

func (s *State) FSExtract(dst string) error {
	if s.FS == nil {
		return errors.New("no image")
	}
	return s.FS.Walk(func(e *memfs.Entry) error {
		name := strings.TrimPrefix(e.Name, "/")
		out := filepath.Join(dst, name)
		switch {
		case e.Name == "/":
			return nil
		case e.Mode&memfs.ModeDir != 0:
			return os.MkdirAll(out, 0o755)
		case e.Mode&memfs.ModeLink != 0:
			_ = os.RemoveAll(out)
			return os.Symlink(e.Target, out)
		case e.Mode&memfs.ModeChar != 0 || e.Mode&memfs.ModeBlock != 0 || e.Mode&memfs.ModeFIFO != 0:
			// skip special files
			return nil
		default:
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			return os.WriteFile(out, e.Data, 0o644)
		}
	})
}

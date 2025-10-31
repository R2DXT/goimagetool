package squashfs

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"goimagetool/internal/fs/memfs"

	befile "github.com/diskfs/go-diskfs/backend/file"
	sqfs "github.com/diskfs/go-diskfs/filesystem/squashfs"
)

var ErrBadMagic = errors.New("squashfs: bad magic")

// v4 superblock (LE)
type Superblock struct {
	Magic               uint32
	Inodes              uint32
	MkfsTime            uint32
	BlockSize           uint32
	Fragments           uint32
	CompressionID       uint16
	BlockLog            uint16
	Flags               uint16
	NoIDs               uint16
	Major               uint16
	Minor               uint16
	RootInodeRef        uint64
	BytesUsed           uint64
	IDTableStart        uint64
	XAttrIDTableStart   uint64
	InodeTableStart     uint64
	DirectoryTableStart uint64
	FragTableStart      uint64
	LookupTableStart    uint64
}

type Options struct {
	Compression   string // "", gzip, xz, zstd, lz4, lzo, lzma
	Label         string
	NonExportable bool
	NonSparse     bool
	WithXattrs    bool
}

func Detect(r io.Reader) (bool, error) {
	var hdr [4]byte
	n, err := io.ReadFull(r, hdr[:])
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return false, err
	}
	return n >= 4 && hdr[0] == 'h' && hdr[1] == 's' && hdr[2] == 'q' && hdr[3] == 's', nil
}

// Load: валидируем superblock и копируем в memfs.
func Load(r io.Reader, _ string) (*memfs.FS, *Superblock, error) {
	tmp, err := os.MkdirTemp("", "goimagetool-sqfs-in-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(tmp)

	img := filepath.Join(tmp, "in.squashfs")
	f, err := os.Create(img)
	if err != nil {
		return nil, nil, err
	}
	if _, err = io.Copy(f, r); err != nil {
		f.Close()
		return nil, nil, err
	}
	if err = f.Close(); err != nil {
		return nil, nil, err
	}

	sb, err := readSuper(img)
	if err != nil {
		return nil, nil, err
	}
	if sb.Magic != 0x73717368 {
		return nil, nil, ErrBadMagic
	}

	b, err := befile.OpenFromPath(img, true)
	if err != nil {
		return nil, nil, err
	}
	fs, err := sqfs.Read(b, 0, 0, 0)
	if err != nil {
		return nil, nil, err
	}
	defer fs.Close()

	m := memfs.New()
	if err := copyOut(fs, m, "/"); err != nil {
		return nil, nil, err
	}
	return m, sb, nil
}

func readSuper(path string) (*Superblock, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var sb Superblock
	if err := binary.Read(io.LimitReader(f, 96), binary.LittleEndian, &sb); err != nil {
		return nil, err
	}
	return &sb, nil
}

func copyOut(sfs *sqfs.FileSystem, m *memfs.FS, dir string) error {
	ents, err := sfs.ReadDir(dir)
	if err != nil {
		if dir == "/" {
			ents, err = sfs.ReadDir("")
		}
		if err != nil {
			return err
		}
	}
	for _, fi := range ents {
		name := fi.Name()
		if name == "." || name == ".." {
			continue
		}
		src := filepath.Clean("/" + strings.TrimPrefix(filepath.Join(dir, name), "/"))
		mode := fi.Mode()
		perm := uint32(mode.Perm())

		switch {
		case mode.IsDir():
			m.PutDirMode(src, memfs.Mode(0040000|perm), 0, 0, fi.ModTime())
			if err := copyOut(sfs, m, src); err != nil {
				return err
			}

		case mode&os.ModeSymlink != 0:
			fr, err := sfs.OpenFile(src, os.O_RDONLY)
			if err != nil {
				m.PutSymlink(src, "", 0, 0, fi.ModTime())
				continue
			}
			data, _ := io.ReadAll(fr)
			_ = fr.Close()
			target := strings.TrimSpace(string(data))
			m.PutSymlink(src, target, 0, 0, fi.ModTime())

		default:
			fr, err := sfs.OpenFile(src, os.O_RDONLY)
			if err != nil {
				return err
			}
			data, err := io.ReadAll(fr)
			_ = fr.Close()
			if err != nil {
				return err
			}
			m.PutFile(src, data, memfs.Mode(0100000|perm), 0, 0, fi.ModTime())
		}
	}
	return nil
}

// Store: выгружаем memfs в workspace и финализируем SquashFS.
// Сохраняем mode/mtime, best-effort chown/lchown на Unix.
func Store(w io.Writer, m *memfs.FS, opt Options) error {
	tmp, err := os.MkdirTemp("", "goimagetool-sqfs-out-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	out := filepath.Join(tmp, "out.squashfs")
	b, err := befile.CreateFromPath(out, 0)
	if err != nil {
		return err
	}
	sfs, err := sqfs.Create(b, 0, 0, 0)
	if err != nil {
		return err
	}
	defer sfs.Close()

	if opt.Label != "" {
		_ = sfs.SetLabel(opt.Label)
	}
	ws := sfs.Workspace()
	if ws == "" {
		return fmt.Errorf("squashfs: empty workspace")
	}

	err = m.Walk(func(e *memfs.Entry) error {
		if e.Name == "/" {
			return nil
		}
		dst := filepath.Join(ws, filepath.FromSlash(strings.TrimPrefix(e.Name, "/")))
		switch {
		case e.Mode&memfs.ModeDir != 0:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			applyDirMeta(dst, e)

		case e.Mode&memfs.ModeLink != 0:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			_ = os.Remove(dst)
			if err := os.Symlink(e.Target, dst); err != nil {
				return err
			}
			applyLinkMeta(dst, e)

		case (e.Mode&(memfs.ModeChar|memfs.ModeBlock|memfs.ModeFIFO)) != 0:
			// спец-узлы пропускаем (squashfs writer из go-diskfs их не собирает)
			return nil

		default:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(uint32(e.Mode) & 0o7777)
			if mode == 0 {
				mode = 0o644
			}
			if err := os.WriteFile(dst, e.Data, mode); err != nil {
				return err
			}
			applyFileMeta(dst, e)
		}
		return nil
	})
	if err != nil {
		return err
	}

	comp, err := toCompressor(opt.Compression)
	if err != nil {
		return err
	}
	if err := sfs.Finalize(sqfs.FinalizeOptions{
		Compression:   comp,
		NonExportable: opt.NonExportable,
		NonSparse:     opt.NonSparse,
		Xattrs:        opt.WithXattrs,
	}); err != nil {
		return err
	}

	f, err := os.Open(out)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

func toCompressor(name string) (sqfs.Compressor, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "gzip":
		return &sqfs.CompressorGzip{}, nil
	case "xz":
		return &sqfs.CompressorXz{}, nil
	case "zstd":
		return &sqfs.CompressorZstd{}, nil
	case "lz4":
		return &sqfs.CompressorLz4{}, nil
	case "lzo":
		return &sqfs.CompressorLzo{}, nil
	case "lzma":
		return &sqfs.CompressorLzma{}, nil
	default:
		return nil, fmt.Errorf("unknown compressor: %s", name)
	}
}

// --- metadata helpers ---

func applyFileMeta(path string, e *memfs.Entry) {
	_ = os.Chtimes(path, safeTime(e.MTime), safeTime(e.MTime))
	_ = os.Chmod(path, os.FileMode(uint32(e.Mode)&0o7777))
	_ = chown(path, int(e.UID), int(e.GID)) // no-op на !unix
}

func applyDirMeta(path string, e *memfs.Entry) {
	_ = os.Chmod(path, os.FileMode(uint32(e.Mode)&0o7777))
	_ = os.Chtimes(path, safeTime(e.MTime), safeTime(e.MTime))
	_ = chown(path, int(e.UID), int(e.GID))
}

func applyLinkMeta(path string, e *memfs.Entry) {
	// chmod/chtimes для symlink либо не поддерживаются, либо меняют цель → пропускаем
	_ = lchown(path, int(e.UID), int(e.GID)) // no-op на !unix
}

func safeTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Unix(0, 0)
	}
	return t
}

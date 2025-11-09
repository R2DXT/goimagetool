package ext2

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"goimagetool/internal/fs/memfs"
)

type Options struct {
	BlockSize int
}

func Load(dst *memfs.FS, r io.Reader) error {
	if dst == nil {
		return fmt.Errorf("memfs is nil")
	}
	tmp, err := os.MkdirTemp("", "goimagetool-ext2-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	img := filepath.Join(tmp, "img.ext2")
	f, err := os.Create(img)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		return err
	}
	_ = f.Close()
	if runtime.GOOS != "windows" {
		if _, err := exec.LookPath("debugfs"); err == nil {
			rdump := filepath.Join(tmp, "rdump")
			if err := os.MkdirAll(rdump, 0o755); err != nil {
				return err
			}
			cmd := exec.Command("debugfs", "-R", fmt.Sprintf("rdump / %s", rdump), img)
			out, err := cmd.CombinedOutput()
			if err == nil {
				*dst = *memfs.New()
				dst.PutDir("/", 0, 0, time.Unix(0, 0))
				err = filepath.Walk(rdump, func(p string, fi os.FileInfo, e error) error {
					if e != nil {
						return e
					}
					rel, _ := filepath.Rel(rdump, p)
					if rel == "." {
						return nil
					}
					ap := "/" + filepath.ToSlash(rel)
					switch mode := fi.Mode(); {
					case mode.IsDir():
						dst.PutDir(ap, uidOf(fi), gidOf(fi), fi.ModTime())
					case (mode & os.ModeSymlink) != 0:
						t, err := os.Readlink(p)
						if err != nil {
							return err
						}
						dst.PutSymlink(ap, t, uidOf(fi), gidOf(fi), fi.ModTime())
					case (mode & os.ModeNamedPipe) != 0:
						dst.PutNode(ap, memfs.ModeFIFO, uint32(mode.Perm()), uidOf(fi), gidOf(fi), 0, 0, fi.ModTime())
					case (mode & os.ModeDevice) != 0:
						m := memfs.ModeChar
						if (mode & os.ModeCharDevice) == 0 {
							m = memfs.ModeBlock
						}
						maj, min := rdevOf(fi)
						dst.PutNode(ap, m, uint32(mode.Perm()), uidOf(fi), gidOf(fi), maj, min, fi.ModTime())
					default:
						b, err := os.ReadFile(p)
						if err != nil {
							return err
						}
						dst.PutFile(ap, b, memfs.ModeFile|memfs.Mode(uint32(mode.Perm())), uidOf(fi), gidOf(fi), fi.ModTime())
					}
					return nil
				})
				return err
			} else {
				_ = out
			}
		}
	}
	data, err := os.ReadFile(img)
	if err != nil {
		return err
	}
	return LoadNative(dst, bytes.NewReader(data))
}

func Store(src *memfs.FS, w io.Writer, opts Options) error {
	if src == nil {
		return fmt.Errorf("memfs is nil")
	}
	if opts.BlockSize == 0 {
		opts.BlockSize = 1024
	}
	if runtime.GOOS == "windows" {
		return fmt.Errorf("mke2fs is required")
	}
	mke2, err := exec.LookPath("mke2fs")
	if err != nil {
		return fmt.Errorf("mke2fs not found: %w", err)
	}
	tmp, err := os.MkdirTemp("", "goimagetool-ext2-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	staging := filepath.Join(tmp, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return err
	}
	if err := materialize(staging, src); err != nil {
		return err
	}
	size, err := estimate(staging, opts.BlockSize)
	if err != nil {
		return err
	}
	if size < 16*1024*1024 {
		size = 16 * 1024 * 1024
	}
	blocks := size / opts.BlockSize
	if blocks <= 0 {
		blocks = 1
	}
	img := filepath.Join(tmp, "fs.img")
	args := []string{
		"-t", "ext2",
		"-q",
		"-d", staging,
		"-b", fmt.Sprintf("%d", opts.BlockSize),
		"-I", "128",
		img,
		fmt.Sprintf("%d", blocks),
	}
	cmd := exec.Command(mke2, args...)
	cmd.Stdin = bytes.NewReader(nil)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mke2fs: %v: %s", err, string(out))
	}
	f, err := os.Open(img)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

func materialize(base string, m *memfs.FS) error {
	snap := m.Snapshot()
	paths := make([]string, 0, len(snap))
	for p := range snap {
		if p != "" {
			paths = append(paths, p)
		}
	}
	sort.Slice(paths, func(i, j int) bool {
		ci := strings.Count(paths[i], "/")
		cj := strings.Count(paths[j], "/")
		if ci != cj {
			return ci < cj
		}
		return paths[i] < paths[j]
	})
	for _, p := range paths {
		if p == "/" {
			continue
		}
		e := snap[p]
		dst := filepath.Join(base, strings.TrimPrefix(p, "/"))
		switch {
		case e.Mode&memfs.ModeDir != 0:
			if err := os.MkdirAll(dst, os.FileMode(uint32(e.Mode)&0o7777)); err != nil {
				return err
			}
			_ = os.Chtimes(dst, e.MTime, e.MTime)
			_ = chown(dst, int(e.UID), int(e.GID))
		case e.Mode&memfs.ModeLink != 0:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			_ = os.Remove(dst)
			if err := os.Symlink(e.Target, dst); err != nil {
				return err
			}
			_ = lchown(dst, int(e.UID), int(e.GID))
		case e.Mode&memfs.ModeFIFO != 0:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := mkfifo(dst, uint32(e.Mode)&0o7777); err != nil {
				return err
			}
			_ = os.Chtimes(dst, e.MTime, e.MTime)
			_ = lchown(dst, int(e.UID), int(e.GID))
		case e.Mode&memfs.ModeChar != 0 || e.Mode&memfs.ModeBlock != 0:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := mknod(dst, e, e.RdevMajor, e.RdevMinor); err != nil {
				return err
			}
			_ = os.Chtimes(dst, e.MTime, e.MTime)
			_ = lchown(dst, int(e.UID), int(e.GID))
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
			_ = os.Chtimes(dst, e.MTime, e.MTime)
			_ = chown(dst, int(e.UID), int(e.GID))
		}
	}
	return nil
}

func estimate(dir string, bs int) (int, error) {
	var tot int64
	err := filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.Mode().IsRegular() {
			tot += fi.Size()
		}
		tot += 512
		return nil
	})
	if err != nil {
		return 0, err
	}
	tot = tot + tot/6 + 4*1024*1024
	if rem := tot % int64(bs); rem != 0 {
		tot += int64(bs) - rem
	}
	return int(tot), nil
}

type blob struct{ b []byte }

func (b *blob) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, io.ErrUnexpectedEOF
	}
	if int(off) >= len(b.b) {
		return 0, io.EOF
	}
	n := copy(p, b.b[int(off):])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

type super struct {
	InodesCount     uint32
	BlocksCount     uint32
	RBlocksCount    uint32
	FreeBlocksCount uint32
	FreeInodesCount uint32
	FirstDataBlock  uint32
	LogBlockSize    uint32
	LogFragSize     uint32
	BlocksPerGroup  uint32
	FragsPerGroup   uint32
	InodesPerGroup  uint32
	Mtime           uint32
	Wtime           uint32
	MntCount        uint16
	MaxMntCount     uint16
	Magic           uint16
	State           uint16
	Errors          uint16
	MinorRev        uint16
	Lastcheck       uint32
	Checkinterval   uint32
	CreatorOS       uint32
	RevLevel        uint32
	DefResuid       uint16
	DefResgid       uint16
	FirstIno        uint32
	InodeSize       uint16
	BlockGroupNR    uint16
	FeatureCompat   uint32
	FeatureIncompat uint32
	FeatureROCompat uint32
	UUID            [16]byte
	VolumeName      [16]byte
	LastMounted     [64]byte
	AlgoBitmap      uint32
}

type gdesc struct {
	BlockBitmap      uint32
	InodeBitmap      uint32
	InodeTable       uint32
	FreeBlocksCount  uint16
	FreeInodesCount  uint16
	UsedDirsCount    uint16
	Padding          uint16
	Reserved         [12]byte
}

type inode struct {
	Mode        uint16
	Uid         uint16
	SizeLo      uint32
	Atime       uint32
	Ctime       uint32
	Mtime       uint32
	Dtime       uint32
	Gid         uint16
	LinksCount  uint16
	Blocks512   uint32
	Flags       uint32
	OSD1        uint32
	Block       [15]uint32
	Generation  uint32
	FileACL     uint32
	DirACL      uint32
	Faddr       uint32
	OSD2        [12]byte
}

type dirent struct {
	Ino     uint32
	RecLen  uint16
	NameLen uint8
	FileTyp uint8
	Name    string
}

func LoadNative(dst *memfs.FS, r io.Reader) error {
	if dst == nil {
		return fmt.Errorf("memfs is nil")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	img := &blob{b: data}
	sb, err := readSuper(img)
	if err != nil {
		return err
	}
	if sb.Magic != 0xEF53 {
		return fmt.Errorf("not ext2")
	}
	bs := int(1024 << sb.LogBlockSize)
	if bs <= 0 {
		return fmt.Errorf("bad block size")
	}
	isz := int(sb.InodeSize)
	if isz == 0 {
		isz = 128
	}
	gr := int((uint32(sb.InodesCount) + sb.InodesPerGroup - 1) / sb.InodesPerGroup)
	if gr <= 0 {
		return fmt.Errorf("no groups")
	}
	gdt, err := readGDT(img, bs, gr)
	if err != nil {
		return err
	}
	*dst = *memfs.New()
	dst.PutDir("/", 0, 0, time.Unix(int64(sb.Mtime), 0))
	seen := map[uint32]bool{}
	return walkDir(img, sb, gdt, bs, isz, 2, "/", dst, seen)
}

func readSuper(r io.ReaderAt) (*super, error) {
	var sb super
	buf := make([]byte, 1024)
	if _, err := r.ReadAt(buf, 1024); err != nil {
		return nil, err
	}
	br := bytes.NewReader(buf[:1024])
	if err := binary.Read(br, binary.LittleEndian, &sb); err != nil {
		return nil, err
	}
	return &sb, nil
}

func readGDT(r io.ReaderAt, bs int, groups int) ([]gdesc, error) {
	size := groups * 32
	buf := make([]byte, size)
	off := int64(2048)
	_, err := r.ReadAt(buf, off)
	if err != nil {
		align := (off + int64(bs-1)) &^ int64(bs-1)
		if _, err2 := r.ReadAt(buf, align); err2 != nil {
			return nil, err
		}
	}
	out := make([]gdesc, groups)
	br := bytes.NewReader(buf)
	for i := 0; i < groups; i++ {
		if err := binary.Read(br, binary.LittleEndian, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func readInode(r io.ReaderAt, sb *super, gdt []gdesc, bs, isz int, ino uint32) (*inode, error) {
	if ino == 0 {
		return nil, fmt.Errorf("inode 0")
	}
	g := int((ino - 1) / sb.InodesPerGroup)
	idx := int((ino - 1) % sb.InodesPerGroup)
	if g < 0 || g >= len(gdt) {
		return nil, fmt.Errorf("inode group oob")
	}
	off := int64(gdt[g].InodeTable)*int64(bs) + int64(idx*isz)
	buf := make([]byte, isz)
	if _, err := r.ReadAt(buf, off); err != nil {
		return nil, err
	}
	var in inode
	br := bytes.NewReader(buf)
	if err := binary.Read(br, binary.LittleEndian, &in); err != nil {
		return nil, err
	}
	return &in, nil
}

func readDirBlock(r io.ReaderAt, off int64, bs int) ([]dirent, error) {
	buf := make([]byte, bs)
	if _, err := r.ReadAt(buf, off); err != nil && err != io.EOF {
		return nil, err
	}
	out := []dirent{}
	i := 0
	for i+8 <= bs {
		ino := binary.LittleEndian.Uint32(buf[i : i+4])
		rec := int(binary.LittleEndian.Uint16(buf[i+4 : i+6]))
		if rec < 8 || i+rec > bs {
			break
		}
		nlen := int(buf[i+6])
		ft := buf[i+7]
		name := ""
		if nlen > 0 && 8+nlen <= rec {
			name = string(buf[i+8 : i+8+nlen])
		}
		if ino != 0 && name != "" {
			out = append(out, dirent{Ino: ino, RecLen: uint16(rec), NameLen: uint8(nlen), FileTyp: ft, Name: name})
		}
		i += rec
	}
	return out, nil
}

func collectBlocks(r io.ReaderAt, in *inode, bs int, need int) ([]uint32, error) {
	var out []uint32
	for i := 0; i < 12; i++ {
		if in.Block[i] != 0 {
			out = append(out, in.Block[i])
			if len(out)*bs >= need && need > 0 {
				return out, nil
			}
		}
	}
	if in.Block[12] != 0 {
		buf := make([]byte, bs)
		if _, err := r.ReadAt(buf, int64(in.Block[12])*int64(bs)); err != nil && err != io.EOF {
			return nil, err
		}
		for j := 0; j+4 <= bs; j += 4 {
			blk := binary.LittleEndian.Uint32(buf[j : j+4])
			if blk != 0 {
				out = append(out, blk)
				if len(out)*bs >= need && need > 0 {
					return out, nil
				}
			}
		}
	}
	if in.Block[13] != 0 {
		l2 := make([]byte, bs)
		if _, err := r.ReadAt(l2, int64(in.Block[13])*int64(bs)); err != nil && err != io.EOF {
			return nil, err
		}
		for i := 0; i+4 <= bs; i += 4 {
			p := binary.LittleEndian.Uint32(l2[i : i+4])
			if p == 0 {
				continue
			}
			l1 := make([]byte, bs)
			if _, err := r.ReadAt(l1, int64(p)*int64(bs)); err != nil && err != io.EOF {
				return nil, err
			}
			for j := 0; j+4 <= bs; j += 4 {
				blk := binary.LittleEndian.Uint32(l1[j : j+4])
				if blk != 0 {
					out = append(out, blk)
					if len(out)*bs >= need && need > 0 {
						return out, nil
					}
				}
			}
		}
	}
	if in.Block[14] != 0 {
		l3 := make([]byte, bs)
		if _, err := r.ReadAt(l3, int64(in.Block[14])*int64(bs)); err != nil && err != io.EOF {
			return nil, err
		}
		for a := 0; a+4 <= bs; a += 4 {
			p2 := binary.LittleEndian.Uint32(l3[a : a+4])
			if p2 == 0 {
				continue
			}
			l2 := make([]byte, bs)
			if _, err := r.ReadAt(l2, int64(p2)*int64(bs)); err != nil && err != io.EOF {
				return nil, err
			}
			for i := 0; i+4 <= bs; i += 4 {
				p1 := binary.LittleEndian.Uint32(l2[i : i+4])
				if p1 == 0 {
					continue
				}
				l1 := make([]byte, bs)
				if _, err := r.ReadAt(l1, int64(p1)*int64(bs)); err != nil && err != io.EOF {
					return nil, err
				}
				for j := 0; j+4 <= bs; j += 4 {
					blk := binary.LittleEndian.Uint32(l1[j : j+4])
					if blk != 0 {
						out = append(out, blk)
						if len(out)*bs >= need && need > 0 {
							return out, nil
						}
					}
				}
			}
		}
	}
	return out, nil
}

func readFileData(r io.ReaderAt, in *inode, bs int) ([]byte, error) {
	sz := int(in.SizeLo)
	if sz < 0 {
		return nil, fmt.Errorf("bad size")
	}
	blocks, err := collectBlocks(r, in, bs, sz)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, sz)
	for _, b := range blocks {
		chunk := bs
		if len(out)+chunk > sz {
			chunk = sz - len(out)
		}
		buf := make([]byte, chunk)
		if _, err := r.ReadAt(buf, int64(b)*int64(bs)); err != nil && err != io.EOF {
			return nil, err
		}
		out = append(out, buf...)
		if len(out) >= sz {
			break
		}
	}
	return out, nil
}

func readSymlinkTarget(in *inode, bs int, r io.ReaderAt) (string, error) {
	sz := int(in.SizeLo)
	if sz <= 60 {
		var raw [60]byte
		for i := 0; i < 15; i++ {
			binary.LittleEndian.PutUint32(raw[i*4:(i+1)*4], in.Block[i])
		}
		n := sz
		if n > len(raw) {
			n = len(raw)
		}
		return string(raw[:n]), nil
	}
	b, err := readFileData(r, in, bs)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func walkDir(r io.ReaderAt, sb *super, gdt []gdesc, bs, isz int, ino uint32, path string, dst *memfs.FS, seen map[uint32]bool) error {
	if seen[ino] {
		return nil
	}
	seen[ino] = true
	in, err := readInode(r, sb, gdt, bs, isz, ino)
	if err != nil {
		return err
	}
	blocks, err := collectBlocks(r, in, bs, 0)
	if err != nil {
		return err
	}
	for _, b := range blocks {
		ents, err := readDirBlock(r, int64(b)*int64(bs), bs)
		if err != nil {
			return err
		}
		for _, de := range ents {
			if de.Name == "." || de.Name == ".." {
				continue
			}
			child, err := readInode(r, sb, gdt, bs, isz, de.Ino)
			if err != nil {
				return err
			}
			full := join(path, de.Name)
			perm := memfs.Mode(uint32(child.Mode) & 0o7777)
			uid := uint32(child.Uid)
			gid := uint32(child.Gid)
			mt := time.Unix(int64(child.Mtime), 0)
			switch {
			case (child.Mode&0xF000) == 0x4000:
				dst.PutDir(full, uid, gid, mt)
				if err := walkDir(r, sb, gdt, bs, isz, de.Ino, full, dst, seen); err != nil {
					return err
				}
			case (child.Mode&0xF000) == 0xA000:
				tgt, err := readSymlinkTarget(child, bs, r)
				if err != nil {
					return err
				}
				dst.PutSymlink(full, tgt, uid, gid, mt)
			case (child.Mode&0xF000) == 0x1000:
				dst.PutNode(full, memfs.ModeFIFO, uint32(perm), uid, gid, 0, 0, mt)
			case (child.Mode&0xF000) == 0x2000:
				dev := child.Block[0]
				maj := (dev >> 8) & 0xfff
				min := (dev & 0xff) | ((dev >> 12) &^ 0xff)
				dst.PutNode(full, memfs.ModeChar, uint32(perm), uid, gid, maj, min, mt)
			case (child.Mode&0xF000) == 0x6000:
				dev := child.Block[0]
				maj := (dev >> 8) & 0xfff
				min := (dev & 0xff) | ((dev >> 12) &^ 0xff)
				dst.PutNode(full, memfs.ModeBlock, uint32(perm), uid, gid, maj, min, mt)
			default:
				if (child.Mode&0xF000) == 0x8000 {
					b, err := readFileData(r, child, bs)
					if err != nil {
						return err
					}
					dst.PutFile(full, b, memfs.ModeFile|perm, uid, gid, mt)
				}
			}
		}
	}
	return nil
}

func join(a, b string) string {
	if a == "/" {
		return "/" + b
	}
	return a + "/" + b
}

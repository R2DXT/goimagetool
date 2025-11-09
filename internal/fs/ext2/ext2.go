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

// Options для Store (внешний mke2fs).
type Options struct {
	BlockSize int // 1024/2048/4096
}

//
// ============== ВНЕШНИЙ ПУТЬ (как было): debugfs/mke2fs ==============
//

// Load: r -> tmp.img -> debugfs rdump -> MemFS
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

	if runtime.GOOS == "windows" {
		return fmt.Errorf("debugfs is required on non-Windows")
	}
	if _, err := exec.LookPath("debugfs"); err != nil {
		return fmt.Errorf("debugfs not found: %w", err)
	}

	rdump := filepath.Join(tmp, "rdump")
	if err := os.MkdirAll(rdump, 0o755); err != nil {
		return err
	}

	cmd := exec.Command("debugfs", "-R", fmt.Sprintf("rdump / %s", rdump), img)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("debugfs rdump: %v: %s", err, string(out))
	}

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
}

// Store: MemFS -> staging dir -> mke2fs -d -> w
func Store(src *memfs.FS, w io.Writer, opts Options) error {
	if src == nil {
		return fmt.Errorf("memfs is nil")
	}
	if opts.BlockSize == 0 {
		opts.BlockSize = 1024
	}
	if runtime.GOOS == "windows" {
		return fmt.Errorf("mke2fs is required on non-Windows")
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
			_ = os.Chtimes(dst, safeTime(e.MTime), safeTime(e.MTime))
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
			_ = os.Chtimes(dst, safeTime(e.MTime), safeTime(e.MTime))
			_ = lchown(dst, int(e.UID), int(e.GID))

		case e.Mode&memfs.ModeChar != 0 || e.Mode&memfs.ModeBlock != 0:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := mknod(dst, e, e.RdevMajor, e.RdevMinor); err != nil {
				return err
			}
			_ = os.Chtimes(dst, safeTime(e.MTime), safeTime(e.MTime))
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
			_ = os.Chtimes(dst, safeTime(e.MTime), safeTime(e.MTime))
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

func safeTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Unix(0, 0)
	}
	return t
}

//
// ======================= НАТИВНЫЙ ПУТЬ (чистый Go, RO) =======================
//

// LoadNative: чистый Go разбор EXT2 (RO): superblock, GDT, inode/dir, direct+indirect1.
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
		return fmt.Errorf("not an ext2: magic=0x%04x", sb.Magic)
	}
	bs := int(1024 << sb.LogBlockSize)
	if bs <= 0 {
		return fmt.Errorf("bad block size")
	}
	isz := int(sb.InodeSize)
	if isz == 0 {
		isz = 128
	}

	groups := int((uint32(sb.InodesCount) + sb.InodesPerGroup - 1) / sb.InodesPerGroup)
	if groups <= 0 {
		return fmt.Errorf("no groups")
	}
	gdt, err := readGDT(img, bs, groups)
	if err != nil {
		return err
	}

	// Корень
	*dst = *memfs.New()
	dst.PutDir("/", 0, 0, time.Unix(int64(sb.Mtime), 0))

	seenDir := map[uint32]bool{}
	return walkDir(img, sb, gdt, bs, isz, 2, "/", dst, seenDir) // inode #2 — root
}

// -------- низкоуровневые структуры/чтение --------

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

func readGDT(r io.ReaderAt, bs int, groups int) ([]gdesc, error) {
	gdtOff := int64(1024 + 1024) // сразу после superblock
	size := groups * 32
	buf := make([]byte, size)
	if _, err := r.ReadAt(buf, gdtOff); err != nil && err != io.EOF {
		// некоторые реализации держат GDT на границе блока; попробуем выровнять
		align := (gdtOff + int64(bs-1)) &^ int64(bs-1)
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

func readInode(r io.ReaderAt, sb *super, gdt []gdesc, bs, isz int, ino uint32) (*inode, error) {
	if ino == 0 {
		return nil, fmt.Errorf("inode 0")
	}
	g := int((ino - 1) / sb.InodesPerGroup)
	idx := int((ino - 1) % sb.InodesPerGroup)
	if g < 0 || g >= len(gdt) {
		return nil, fmt.Errorf("inode group OOB")
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

const (
	ifmtSIFSOCK = 0xC000
	ifmtSIFLNK  = 0xA000
	ifmtSIFREG  = 0x8000
	ifmtSIFBLK  = 0x6000
	ifmtSIFDIR  = 0x4000
	ifmtSIFCHR  = 0x2000
	ifmtSIFIFO  = 0x1000
)

func isDir(m uint16) bool  { return (m & 0xF000) == ifmtSIFDIR }
func isReg(m uint16) bool  { return (m & 0xF000) == ifmtSIFREG }
func isLnk(m uint16) bool  { return (m & 0xF000) == ifmtSIFLNK }
func isChr(m uint16) bool  { return (m & 0xF000) == ifmtSIFCHR }
func isBlk(m uint16) bool  { return (m & 0xF000) == ifmtSIFBLK }
func isFifo(m uint16) bool { return (m & 0xF000) == ifmtSIFIFO }

func readFileData(r io.ReaderAt, in *inode, bs int) ([]byte, error) {
	sz := int(in.SizeLo)
	if sz < 0 {
		return nil, fmt.Errorf("bad size")
	}
	out := make([]byte, 0, sz)
	// direct
	for i := 0; i < 12 && len(out) < sz; i++ {
		blk := in.Block[i]
		if blk == 0 {
			continue
		}
		off := int64(blk) * int64(bs)
		chunk := min(bs, sz-len(out))
		buf := make([]byte, chunk)
		if _, err := r.ReadAt(buf, off); err != nil && err != io.EOF {
			return nil, err
		}
		out = append(out, buf...)
	}
	if len(out) >= sz {
		return out, nil
	}
	// single indirect
	if in.Block[12] != 0 && len(out) < sz {
		ind := in.Block[12]
		indBuf := make([]byte, bs)
		if _, err := r.ReadAt(indBuf, int64(ind)*int64(bs)); err != nil && err != io.EOF {
			return nil, err
		}
		for j := 0; j+4 <= bs && len(out) < sz; j += 4 {
			blk := binary.LittleEndian.Uint32(indBuf[j : j+4])
			if blk == 0 {
				continue
			}
			off := int64(blk) * int64(bs)
			chunk := min(bs, sz-len(out))
			buf := make([]byte, chunk)
			if _, err := r.ReadAt(buf, off); err != nil && err != io.EOF {
				return nil, err
			}
			out = append(out, buf...)
		}
	}
	if len(out) > sz {
		out = out[:sz]
	}
	return out, nil
}

func readSymlinkTarget(in *inode, bs int, r io.ReaderAt) (string, error) {
	sz := int(in.SizeLo)
	if sz <= 60 {
		// fast symlink в i_block
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
	// длинный — как обычный файл
	b, err := readFileData(r, in, bs)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

type dirent struct {
	Ino     uint32
	RecLen  uint16
	NameLen uint8
	FileTyp uint8
	Name    string
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
			out = append(out, dirent{
				Ino:     ino,
				RecLen:  uint16(rec),
				NameLen: uint8(nlen),
				FileTyp: ft,
				Name:    name,
			})
		}
		i += rec
	}
	return out, nil
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
	mt := time.Unix(int64(in.Mtime), 0)

	// прочитать директорию, разложить детей
	// direct blocks + single indirect
	blocks := make([]uint32, 0, 16)
	for i := 0; i < 12; i++ {
		if in.Block[i] != 0 {
			blocks = append(blocks, in.Block[i])
		}
	}
	if in.Block[12] != 0 {
		ind := in.Block[12]
		ibuf := make([]byte, bs)
		if _, err := r.ReadAt(ibuf, int64(ind)*int64(bs)); err == nil || err == io.EOF {
			for j := 0; j+4 <= bs; j += 4 {
				blk := binary.LittleEndian.Uint32(ibuf[j : j+4])
				if blk != 0 {
					blocks = append(blocks, blk)
				}
			}
		}
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
			switch {
			case isDir(child.Mode):
				dst.PutDir(full, uid, gid, time.Unix(int64(child.Mtime), 0))
				if err := walkDir(r, sb, gdt, bs, isz, de.Ino, full, dst, seen); err != nil {
					return err
				}
			case isLnk(child.Mode):
				target, err := readSymlinkTarget(child, bs, r)
				if err != nil {
					return err
				}
				dst.PutSymlink(full, target, uid, gid, time.Unix(int64(child.Mtime), 0))
			case isFifo(child.Mode):
				dst.PutNode(full, memfs.ModeFIFO, uint32(perm), uid, gid, 0, 0, time.Unix(int64(child.Mtime), 0))
			case isChr(child.Mode):
				dst.PutNode(full, memfs.ModeChar, uint32(perm), uid, gid, 0, 0, time.Unix(int64(child.Mtime), 0))
			case isBlk(child.Mode):
				dst.PutNode(full, memfs.ModeBlock, uint32(perm), uid, gid, 0, 0, time.Unix(int64(child.Mtime), 0))
			case isReg(child.Mode):
				data, err := readFileData(r, child, bs)
				if err != nil {
					return err
				}
				dst.PutFile(full, data, memfs.ModeFile|perm, uid, gid, time.Unix(int64(child.Mtime), 0))
			default:
				// сокеты/прочее — пропускаем
				_ = mt
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

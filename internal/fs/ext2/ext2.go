package ext2

// EXT2 reader/writer:
// - Read: 1K/2K/4K, files/dirs, symlinks (inline & external), char/block/fifo.
// - Write: single group; 1K/2K/4K; files/dirs with 1/2/3-indirect; symlinks (inline if <=60b), char/block/fifo.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"time"

	"goimagetool/internal/common"
	"goimagetool/internal/fs/memfs"
)

const (
	ext2Magic        = 0xEF53
	defaultBlockSize = 1024
	inodeSize        = 128

	modeIFIFO = 0x1000
	modeIFCHR = 0x2000
	modeIFDIR = 0x4000
	modeIFBLK = 0x6000
	modeIFREG = 0x8000
	modeIFLNK = 0xA000
)

var errUnsupported = common.ErrUnsupported

type superblock struct {
	InodesCount       uint32
	BlocksCount       uint32
	RBlocksCount      uint32
	FreeBlocksCount   uint32
	FreeInodesCount   uint32
	FirstDataBlock    uint32
	LogBlockSize      uint32
	LogFragSize       uint32
	BlocksPerGroup    uint32
	FragsPerGroup     uint32
	InodesPerGroup    uint32
	MTime             uint32
	WTime             uint32
	MntCount          uint16
	MaxMntCount       uint16
	Magic             uint16
	State             uint16
	Errors            uint16
	MinorRevLevel     uint16
	LastCheck         uint32
	CheckInterval     uint32
	CreatorOS         uint32
	RevLevel          uint32
	DefResUID         uint16
	DefResGID         uint16
	FirstIno          uint32
	InodeSize         uint16
	BlockGroupNr      uint16
	FeatureCompat     uint32
	FeatureIncompat   uint32
	FeatureRoCompat   uint32
	UUID              [16]byte
	VolumeName        [16]byte
	LastMounted       [64]byte
	AlgoBitmap        uint32
	PreallocBlocks    uint8
	PreallocDirBlocks uint8
	Alignment         uint16
	JournalUUID       [16]byte
	JournalInum       uint32
	JournalDev        uint32
	LastOrphan        uint32
	HashSeed          [4]uint32
	DefHashVersion    uint8
	ReservedCharPad   uint8
	ReservedWordPad   uint16
	DefaultMountOpts  uint32
	FirstMetaBg       uint32
	Reserved          [190]byte
}

type groupDesc struct {
	BlockBitmap     uint32
	InodeBitmap     uint32
	InodeTable      uint32
	FreeBlocksCount uint16
	FreeInodesCount uint16
	UsedDirsCount   uint16
	Pad             uint16
	Reserved        [12]byte
}

type inode struct {
	Mode       uint16
	UID        uint16
	Size       uint32
	ATime      uint32
	CTime      uint32
	MTime      uint32
	DTime      uint32
	GID        uint16
	LinksCount uint16
	Blocks     uint32 // in 512-byte sectors
	Flags      uint32
	OSD1       uint32
	Block      [15]uint32 // also holds inline symlink bytes (60 bytes)
	Gen        uint32
	FileACL    uint32
	DirACL     uint32
	Faddr      uint32
	OSD2       [12]byte
}

type dirent struct {
	Inode    uint32
	RecLen   uint16
	NameLen  uint8
	FileType uint8
}

func readAt(all []byte, off int, out any) error {
	if off < 0 || off+binary.Size(out) > len(all) {
		return io.ErrUnexpectedEOF
	}
	return binary.Read(bytes.NewReader(all[off:off+binary.Size(out)]), binary.LittleEndian, out)
}

func writeAt(buf []byte, off int, v any) {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, v)
	copy(buf[off:off+b.Len()], b.Bytes())
}

func getBlockSize(sb *superblock) (int, error) {
	if sb.LogBlockSize > 2 {
		return 0, errUnsupported
	}
	return 1024 << sb.LogBlockSize, nil
}

func inlineSymlinkFromInode(ino *inode) string {
	var raw [60]byte
	var off int
	for i := 0; i < 15; i++ {
		b := ino.Block[i]
		raw[off+0] = byte(b)
		raw[off+1] = byte(b >> 8)
		raw[off+2] = byte(b >> 16)
		raw[off+3] = byte(b >> 24)
		off += 4
	}
	// size is ino.Size
	n := int(ino.Size)
	if n > len(raw) { n = len(raw) }
	return string(raw[:n])
}

func inlineSymlinkPack(target string) [15]uint32 {
	var out [15]uint32
	buf := make([]byte, 60)
	copy(buf, []byte(target))
	for i := 0; i < 15; i++ {
		o := i * 4
		out[i] = uint32(buf[o]) | uint32(buf[o+1])<<8 | uint32(buf[o+2])<<16 | uint32(buf[o+3])<<24
	}
	return out
}

// ========== Reader ==========

func Load(r io.Reader) (*memfs.FS, error) {
	b, err := io.ReadAll(r)
	if err != nil { return nil, err }
	if len(b) < 2048 { return nil, errors.New("too small") }

	var sb superblock
	if err := readAt(b, 1024, &sb); err != nil { return nil, err }
	if sb.Magic != ext2Magic { return nil, errors.New("not ext2") }
	if sb.RevLevel < 1 { return nil, errUnsupported }
	bsz, err := getBlockSize(&sb); if err != nil { return nil, err }

	var gd groupDesc
	gdtBlock := 2
	if bsz > 1024 { gdtBlock = 1 }
	if err := readAt(b, gdtBlock*bsz, &gd); err != nil { return nil, err }
	inodeTableOff := int(gd.InodeTable) * bsz
	iSize := int(sb.InodeSize)
	if iSize == 0 { iSize = inodeSize }

	getInode := func(n uint32) (inode, error) {
		var ino inode
		off := inodeTableOff + int(n-1)*iSize
		if err := readAt(b, off, &ino); err != nil { return inode{}, err }
		return ino, nil
	}

	var expandBlocks func(p uint32, level int) ([]uint32, error)
	expandBlocks = func(p uint32, level int) ([]uint32, error) {
		if p == 0 { return nil, nil }
		if level == 0 { return []uint32{p}, nil }
		ptrsPerBlock := bsz / 4
		off := int(p) * bsz
		if off+bsz > len(b) { return nil, io.ErrUnexpectedEOF }
		ptrs := make([]uint32, ptrsPerBlock)
		if err := binary.Read(bytes.NewReader(b[off:off+bsz]), binary.LittleEndian, &ptrs); err != nil { return nil, err }
		out := make([]uint32, 0, ptrsPerBlock)
		for _, q := range ptrs {
			if q == 0 { break }
			sub, err := expandBlocks(q, level-1)
			if err != nil { return nil, err }
			out = append(out, sub...)
		}
		return out, nil
	}

	readFileData := func(ino *inode) ([]byte, error) {
		if ino.Blocks == 0 && ino.Size == 0 { return nil, nil }
		var blocks []uint32
		for i := 0; i < 12; i++ {
			if ino.Block[i] != 0 { blocks = append(blocks, ino.Block[i]) }
		}
		for i, lvl := range []int{1,2,3} {
			p := ino.Block[12+i]
			if p != 0 {
				list, err := expandBlocks(p, lvl); if err != nil { return nil, err }
				blocks = append(blocks, list...)
			}
		}
		var out bytes.Buffer
		left := int(ino.Size)
		for _, blk := range blocks {
			if left <= 0 { break }
			off := int(blk) * bsz
			chunk := bsz
			if chunk > left { chunk = left }
			if off+chunk > len(b) { return nil, io.ErrUnexpectedEOF }
			out.Write(b[off:off+chunk])
			left -= chunk
		}
		return out.Bytes(), nil
	}

	fs := memfs.New()

	var walkDir func(inum uint32, parent string) error
	walkDir = func(inum uint32, parent string) error {
		ino, err := getInode(inum); if err != nil { return err }
		if ino.Mode&modeIFDIR == 0 { return fmt.Errorf("inode %d not a dir", inum) }
		data, err := readFileData(&ino); if err != nil { return err }
		p := 0
		for p+8 <= len(data) {
			var de dirent
			if err := binary.Read(bytes.NewReader(data[p:p+8]), binary.LittleEndian, &de); err != nil { return err }
			if de.Inode == 0 || de.RecLen == 0 { break }
			recEnd := p + int(de.RecLen)
			if recEnd > len(data) { break }
			nlen := int(de.NameLen)
			if 8+nlen > int(de.RecLen) || p+8+nlen > len(data) { break }
			name := string(data[p+8 : p+8+nlen])
			p = recEnd
			if name == "." || name == ".." { continue }
			child, err := getInode(de.Inode); if err != nil { return err }
			full := path.Join(parent, name)
			mt := time.Unix(int64(child.MTime), 0)

			switch child.Mode & 0xF000 {
			case modeIFDIR:
				fs.PutDirMode(full, memfs.Mode(child.Mode), uint32(child.UID), uint32(child.GID), mt)
				if err := walkDir(de.Inode, full); err != nil { return err }
			case modeIFREG:
				b, err := readFileData(&child); if err != nil { return err }
				fs.PutFile(full, b, memfs.Mode(child.Mode), uint32(child.UID), uint32(child.GID), mt)
			case modeIFLNK:
				if child.Size <= 60 {
					t := inlineSymlinkFromInode(&child)
					fs.PutSymlink(full, t, uint32(child.UID), uint32(child.GID), mt)
				} else {
					b, err := readFileData(&child); if err != nil { return err }
					fs.PutSymlink(full, string(b), uint32(child.UID), uint32(child.GID), mt)
				}
			case modeIFCHR:
				r := child.Block[0]
				maj := (r >> 8) & 0xff
				min := r & 0xff
				fs.PutNode(full, memfs.ModeChar, uint32(child.Mode&0o7777), uint32(child.UID), uint32(child.GID), uint32(maj), uint32(min), mt)
			case modeIFBLK:
				r := child.Block[0]
				maj := (r >> 8) & 0xff
				min := r & 0xff
				fs.PutNode(full, memfs.ModeBlock, uint32(child.Mode&0o7777), uint32(child.UID), uint32(child.GID), uint32(maj), uint32(min), mt)
			case modeIFIFO:
				fs.PutNode(full, memfs.ModeFIFO, uint32(child.Mode&0o7777), uint32(child.UID), uint32(child.GID), 0, 0, mt)
			}
		}
		return nil
	}

	fs.PutDir("/", 0, 0, time.Unix(int64(sb.MTime), 0))
	if err := walkDir(2, "/"); err != nil { return nil, err }
	return fs, nil
}

// ========== Writer ==========

type fileRec struct {
	Inode     uint32
	Path      string
	Data      []byte
	IsDir     bool
	IsSymlink bool
	Target    string
	IsChar    bool
	IsBlock   bool
	IsFIFO    bool
	RdevMaj   uint32
	RdevMin   uint32
	MTime     time.Time
	Mode      uint16
	UID       uint16
	GID       uint16
}

func ceilDiv(x, y int) int { if x <= 0 { return 0 }; return (x + y - 1) / y }

func Store(w io.Writer, fs *memfs.FS, blockSize int) error {
	// block size
	bsz := defaultBlockSize
	switch blockSize {
	case 0, 1024: bsz = 1024
	case 2048, 4096: bsz = blockSize
	default: return fmt.Errorf("unsupported blockSize: %d (use 1024/2048/4096)", blockSize)
	}

	// Snapshot to fileRecs
	var all []*fileRec
	ss := fs.Snapshot()
	keys := make([]string, 0, len(ss))
	for k := range ss { keys = append(keys, k) }
	sort.Strings(keys)
	for _, k := range keys {
		e := ss[k]
		if e.Name == "/" { continue }
		r := &fileRec{
			Path:    e.Name,
			IsDir:   e.Mode&memfs.ModeDir != 0,
			MTime:   e.MTime,
			Mode:    uint16(uint32(e.Mode) & 0xFFFF),
			UID:     uint16(e.UID),
			GID:     uint16(e.GID),
			IsSymlink: e.Mode&memfs.ModeLink != 0,
			IsChar:  e.Mode&memfs.ModeChar != 0,
			IsBlock: e.Mode&memfs.ModeBlock != 0,
			IsFIFO:  e.Mode&memfs.ModeFIFO != 0,
			Target:  e.Target,
			RdevMaj: e.RdevMajor,
			RdevMin: e.RdevMinor,
		}
		if !r.IsDir && !r.IsSymlink && !r.IsChar && !r.IsBlock && !r.IsFIFO {
			r.Data = e.Data
		}
		all = append(all, r)
	}

	// Inode numbering: dirs, then others
	inodeNum := uint32(3)
	for _, r := range all { if r.IsDir { r.Inode = inodeNum; inodeNum++ } }
	for _, r := range all { if r.Inode == 0 { r.Inode = inodeNum; inodeNum++ } }
	totalInodes := int(inodeNum - 1); if totalInodes < 11 { totalInodes = 11 }

	// Children map for dirs + link counts
	children := map[string][]*fileRec{}
	for _, r := range all {
		d := path.Dir(r.Path)
		children[d] = append(children[d], r)
	}

	// directory payload builder
	buildDirPayload := func(selfIno uint32, dirPath string) []byte {
		type ent struct{ ino uint32; ft uint8; nm string }
		list := []ent{{selfIno, 2, "."}}
		parIno := uint32(2)
		if dirPath != "/" {
			parent := path.Dir(dirPath)
			for _, pr := range all { if pr.Path == parent { parIno = pr.Inode; break } }
		}
		list = append(list, ent{parIno, 2, ".."})
		chs := children[dirPath]
		sort.Slice(chs, func(i, j int) bool { return chs[i].Path < chs[j].Path })
		for _, ch := range chs {
			var ft uint8 = 1
			switch {
			case ch.IsDir: ft = 2
			case ch.IsSymlink: ft = 7 // symlink
			case ch.IsChar: ft = 3
			case ch.IsBlock: ft = 4
			case ch.IsFIFO: ft = 5
			default: ft = 1
			}
			list = append(list, ent{ch.Inode, ft, path.Base(ch.Path)})
		}
		var out bytes.Buffer
		blockPos := 0
		for idx, e := range list {
			nl := len(e.nm)
			rec := 8 + nl
			if rec%4 != 0 { rec += 4 - (rec % 4) }
			if blockPos+rec > bsz && blockPos > 0 {
				for blockPos < bsz { out.WriteByte(0); blockPos++ }
				blockPos = 0
			}
			useRec := rec
			if blockPos+useRec == bsz || idx == len(list)-1 {
				useRec = bsz - blockPos
			}
			de := dirent{Inode: e.ino, RecLen: uint16(useRec), NameLen: uint8(nl), FileType: e.ft}
			var hdr bytes.Buffer; _ = binary.Write(&hdr, binary.LittleEndian, de)
			out.Write(hdr.Bytes()); out.WriteString(e.nm)
			used := 8 + nl; for used < useRec { out.WriteByte(0); used++ }
			blockPos += useRec; if blockPos == bsz { blockPos = 0 }
		}
		if blockPos != 0 { for blockPos < bsz { out.WriteByte(0); blockPos++ } }
		return out.Bytes()
	}

	dirPayload := map[uint32][]byte{}
	for _, r := range all { if r.IsDir { dirPayload[r.Inode] = buildDirPayload(r.Inode, r.Path) } }
	rootPayload := buildDirPayload(2, "/")

	blocksFor := func(n int) int { return ceilDiv(n, bsz) }

	// choose meta layout
	gdtBlk := 2
	if bsz > 1024 { gdtBlk = 1 }
	blkBitmapBlk := gdtBlk + 1
	inoBitmapBlk := blkBitmapBlk + 1
	inodeTableBlocks := ceilDiv(totalInodes*inodeSize, bsz)
	inodeTableBlk := inoBitmapBlk + 1
	firstDataBlk := inodeTableBlk + inodeTableBlocks

	type plan struct {
		data []int
		ind1 int
		ind2Root int
		ind2L1   []int
		ind3Root int
		ind3L2   []int
		ind3L1   []int
	}
	ptrsPer := bsz / 4

	planForDataBlocks := func(n int) plan {
		var p plan
		if n <= 12 { return p }
		rem := n - 12
		if rem > 0 {
			use := rem; if use > ptrsPer { use = ptrsPer }
			if use > 0 { p.ind1 = -1; rem -= use }
		}
		if rem > 0 {
			cap2 := ptrsPer * ptrsPer
			use := rem; if use > cap2 { use = cap2 }
			if use > 0 { p.ind2Root = -1; p.ind2L1 = make([]int, ceilDiv(use, ptrsPer)); rem -= use }
		}
		if rem > 0 {
			cap3 := ptrsPer * ptrsPer * ptrsPer
			use := rem; if use > cap3 { use = cap3 }
			if use > 0 { p.ind3Root = -1; p.ind3L2 = make([]int, ceilDiv(use, ptrsPer*ptrsPer)); p.ind3L1 = make([]int, ceilDiv(use, ptrsPer)) }
		}
		return p
	}

	// count data/ptr blocks
	totalData := blocksFor(len(rootPayload))
	rootPlan := planForDataBlocks(totalData)
	countPtr := func(pl plan) int {
		c := 0
		if pl.ind1 == -1 { c++ }
		if pl.ind2Root == -1 { c += 1 + len(pl.ind2L1) }
		if pl.ind3Root == -1 { c += 1 + len(pl.ind3L2) + len(pl.ind3L1) }
		return c
	}
	totalPtr := countPtr(rootPlan)

	filePlan := map[uint32]*plan{}
	for _, r := range all {
		var n int
		switch {
		case r.IsDir:
			n = blocksFor(len(dirPayload[r.Inode]))
		case r.IsSymlink:
			if len(r.Target) <= 60 { n = 0 } else { n = blocksFor(len([]byte(r.Target))) }
		case r.IsChar || r.IsBlock || r.IsFIFO:
			n = 0
		default:
			n = blocksFor(len(r.Data))
		}
		pl := planForDataBlocks(n)
		filePlan[r.Inode] = &pl
		totalData += n
		totalPtr += countPtr(pl)
	}

	// allocate
	curBlk := firstDataBlk
	allocData := func(n int) []int {
		out := make([]int, n)
		for i := 0; i < n; i++ { out[i] = curBlk; curBlk++ }
		return out
	}
	rootData := allocData(blocksFor(len(rootPayload)))

	allocPtr := func(pl *plan) {
		if pl.ind1 == -1 { pl.ind1 = curBlk; curBlk++ }
		if pl.ind2Root == -1 {
			pl.ind2Root = curBlk; curBlk++
			for i := range pl.ind2L1 { pl.ind2L1[i] = curBlk; curBlk++ }
		}
		if pl.ind3Root == -1 {
			pl.ind3Root = curBlk; curBlk++
			for i := range pl.ind3L2 { pl.ind3L2[i] = curBlk; curBlk++ }
			for i := range pl.ind3L1 { pl.ind3L1[i] = curBlk; curBlk++ }
		}
	}
	allocPtr(&rootPlan)

	for _, r := range all {
		pl := filePlan[r.Inode]
		var n int
		switch {
		case r.IsDir:
			n = blocksFor(len(dirPayload[r.Inode]))
		case r.IsSymlink:
			if len(r.Target) <= 60 { n = 0 } else { n = blocksFor(len([]byte(r.Target))) }
		case r.IsChar || r.IsBlock || r.IsFIFO:
			n = 0
		default:
			n = blocksFor(len(r.Data))
		}
		if n > 0 { pl.data = allocData(n) }
		allocPtr(pl)
	}

	totalBlocks := curBlk

	// buffer
	img := make([]byte, totalBlocks*bsz)

	// super + group
	var sb superblock
	now := uint32(time.Now().Unix())
	sb.InodesCount = uint32(totalInodes)
	sb.BlocksCount = uint32(totalBlocks)
	sb.FreeBlocksCount = 0
	sb.FreeInodesCount = 0
	if bsz == 1024 { sb.FirstDataBlock = 1 } else { sb.FirstDataBlock = 0 }
	switch bsz { case 1024: sb.LogBlockSize=0; case 2048: sb.LogBlockSize=1; case 4096: sb.LogBlockSize=2 }
	sb.BlocksPerGroup = uint32(totalBlocks)
	sb.InodesPerGroup = uint32(totalInodes)
	sb.MTime = now; sb.WTime = now
	sb.Magic = ext2Magic; sb.State = 1; sb.RevLevel = 1
	sb.InodeSize = inodeSize; sb.FirstIno = 11
	writeAt(img, 1024, &sb)

	var gd groupDesc
	gd.BlockBitmap = uint32(blkBitmapBlk)
	gd.InodeBitmap = uint32(inoBitmapBlk)
	gd.InodeTable = uint32(inodeTableBlk)
	writeAt(img, gdtBlk*bsz, &gd)

	// bitmaps
	blkMap := make([]byte, bsz)
	inoMap := make([]byte, bsz)
	markBlk := func(bn int) { blkMap[bn/8] |= 1 << uint(bn%8) }
	markIno := func(in int) { inoMap[(in-1)/8] |= 1 << uint((in-1)%8) }

	for i := 0; i < firstDataBlk; i++ { markBlk(i) }
	for _, bn := range rootData { markBlk(bn) }
	markPtr := func(pl *plan) {
		if pl.ind1 > 0 { markBlk(pl.ind1) }
		if pl.ind2Root > 0 { markBlk(pl.ind2Root); for _, x := range pl.ind2L1 { markBlk(x) } }
		if pl.ind3Root > 0 { markBlk(pl.ind3Root); for _, x := range pl.ind3L2 { markBlk(x) }; for _, x := range pl.ind3L1 { markBlk(x) } }
	}
	markPtr(&rootPlan)
	for _, r := range all {
		pl := filePlan[r.Inode]
		for _, bn := range pl.data { markBlk(bn) }
		markPtr(pl)
	}
	markIno(2)
	for _, r := range all { markIno(int(r.Inode)) }

	copy(img[blkBitmapBlk*bsz:], blkMap)
	copy(img[inoBitmapBlk*bsz:], inoMap)

	writeInode := func(n uint32, ino *inode) {
		off := (inodeTableBlk * bsz) + int(n-1)*inodeSize
		writeAt(img, off, ino)
	}

	writeBlocks := func(list []int, data []byte) {
		left := len(data); pos := 0
		for _, bn := range list {
			if left <= 0 { break }
			n := bsz; if n > left { n = left }
			copy(img[bn*bsz:], data[pos:pos+n])
			pos += n; left -= n
		}
	}

	writePointers := func(pl *plan, dataBlocks []int, ino *inode) {
		d := len(dataBlocks); if d > 12 { d = 12 }
		for i := 0; i < d; i++ { ino.Block[i] = uint32(dataBlocks[i]) }
		idx := d; if idx >= len(dataBlocks) { return }
		ptrsPer := bsz / 4
		if pl.ind1 > 0 {
			ino.Block[12] = uint32(pl.ind1)
			ptrs := make([]uint32, ptrsPer)
			n := len(dataBlocks) - idx; if n > ptrsPer { n = ptrsPer }
			for i := 0; i < n; i++ { ptrs[i] = uint32(dataBlocks[idx+i]) }
			var buf bytes.Buffer; _ = binary.Write(&buf, binary.LittleEndian, ptrs)
			copy(img[pl.ind1*bsz:], buf.Bytes())
			idx += n; if idx >= len(dataBlocks) { return }
		}
		if pl.ind2Root > 0 {
			ino.Block[13] = uint32(pl.ind2Root)
			root := make([]uint32, ptrsPer)
			for i := 0; i < len(pl.ind2L1) && i < ptrsPer; i++ { root[i] = uint32(pl.ind2L1[i]) }
			var rbuf bytes.Buffer; _ = binary.Write(&rbuf, binary.LittleEndian, root)
			copy(img[pl.ind2Root*bsz:], rbuf.Bytes())
			for i := 0; i < len(pl.ind2L1); i++ {
				l1 := make([]uint32, ptrsPer)
				for j := 0; j < ptrsPer && idx < len(dataBlocks); j++ { l1[j] = uint32(dataBlocks[idx]); idx++ }
				var b bytes.Buffer; _ = binary.Write(&b, binary.LittleEndian, l1)
				copy(img[pl.ind2L1[i]*bsz:], b.Bytes())
				if idx >= len(dataBlocks) { break }
			}
			if idx >= len(dataBlocks) { return }
		}
		if pl.ind3Root > 0 {
			ino.Block[14] = uint32(pl.ind3Root)
			root := make([]uint32, ptrsPer)
			for i := 0; i < len(pl.ind3L2) && i < ptrsPer; i++ { root[i] = uint32(pl.ind3L2[i]) }
			var rbuf bytes.Buffer; _ = binary.Write(&rbuf, binary.LittleEndian, root)
			copy(img[pl.ind3Root*bsz:], rbuf.Bytes())
			l1Idx := 0
			for l2i := 0; l2i < len(pl.ind3L2); l2i++ {
				l1List := make([]uint32, ptrsPer)
				countL1 := ptrsPer
				if l1Idx+countL1 > len(pl.ind3L1) { countL1 = len(pl.ind3L1) - l1Idx; if countL1 < 0 { countL1 = 0 } }
				for k := 0; k < countL1; k++ { l1List[k] = uint32(pl.ind3L1[l1Idx+k]) }
				var l2buf bytes.Buffer; _ = binary.Write(&l2buf, binary.LittleEndian, l1List)
				copy(img[pl.ind3L2[l2i]*bsz:], l2buf.Bytes())
				for k := 0; k < countL1; k++ {
					l1ptrs := make([]uint32, ptrsPer)
					for j := 0; j < ptrsPer && idx < len(dataBlocks); j++ { l1ptrs[j] = uint32(dataBlocks[idx]); idx++ }
					var lb bytes.Buffer; _ = binary.Write(&lb, binary.LittleEndian, l1ptrs)
					copy(img[pl.ind3L1[l1Idx+k]*bsz:], lb.Bytes())
					if idx >= len(dataBlocks) { break }
				}
				l1Idx += countL1
				if idx >= len(dataBlocks) { break }
			}
		}
	}

	// write root
	writeBlocks(rootData, rootPayload)
	rootIno := inode{
		Mode:       modeIFDIR | 0o755,
		UID:        0, GID: 0,
		Size:       uint32(len(rootPayload)),
		ATime: now, CTime: now, MTime: now,
		LinksCount: 2,
		Blocks:     uint32(ceilDiv(len(rootPayload), 512)),
	}
	writePointers(&rootPlan, rootData, &rootIno)
	writeInode(2, &rootIno)

	// links count for dirs
	dirLinks := func(r *fileRec) uint16 {
		// 2 + subdirs
		var n int
		for _, ch := range children[r.Path] { if ch.IsDir { n++ } }
		return uint16(2 + n)
	}

	// write others
	for _, r := range all {
		pl := filePlan[r.Inode]
		switch {
		case r.IsDir:
			data := dirPayload[r.Inode]
			writeBlocks(pl.data, data)
			ts := uint32(r.MTime.Unix())
			ino := inode{
				Mode:       uint16((uint32(r.Mode)&0xFFFF)|uint32(modeIFDIR)),
				UID:        r.UID, GID: r.GID,
				Size:       uint32(len(data)),
				ATime: ts, CTime: ts, MTime: ts,
				LinksCount: dirLinks(r),
				Blocks:     uint32(ceilDiv(len(data), 512)),
			}
			writePointers(pl, pl.data, &ino)
			writeInode(r.Inode, &ino)

		case r.IsSymlink:
			ts := uint32(r.MTime.Unix())
			if len(r.Target) <= 60 {
				blk := inlineSymlinkPack(r.Target)
				ino := inode{
					Mode:       uint16((uint32(r.Mode)&0xFFFF)|uint32(modeIFLNK)),
					UID:        r.UID, GID: r.GID,
					Size:       uint32(len(r.Target)),
					ATime: ts, CTime: ts, MTime: ts,
					LinksCount: 1,
					Blocks:     0,
					Block:      blk,
				}
				writeInode(r.Inode, &ino)
			} else {
				data := []byte(r.Target)
				writeBlocks(pl.data, data)
				ino := inode{
					Mode:       uint16((uint32(r.Mode)&0xFFFF)|uint32(modeIFLNK)),
					UID:        r.UID, GID: r.GID,
					Size:       uint32(len(data)),
					ATime: ts, CTime: ts, MTime: ts,
					LinksCount: 1,
					Blocks:     uint32(ceilDiv(len(data), 512)),
				}
				writePointers(pl, pl.data, &ino)
				writeInode(r.Inode, &ino)
			}

		case r.IsChar || r.IsBlock || r.IsFIFO:
			ts := uint32(r.MTime.Unix())
			var typ uint16
			if r.IsChar { typ = modeIFCHR } else if r.IsBlock { typ = modeIFBLK } else { typ = modeIFIFO }
			var blk0 uint32
			if r.IsChar || r.IsBlock {
				blk0 = (r.RdevMaj << 8) | (r.RdevMin & 0xff)
			}
			ino := inode{
				Mode:       uint16((uint32(r.Mode)&0xFFFF)|uint32(typ)),
				UID:        r.UID, GID: r.GID,
				Size:       0,
				ATime: ts, CTime: ts, MTime: ts,
				LinksCount: 1,
				Blocks:     0,
			}
			ino.Block[0] = blk0
			writeInode(r.Inode, &ino)

		default:
			writeBlocks(pl.data, r.Data)
			ts := uint32(r.MTime.Unix())
			ino := inode{
				Mode:       uint16((uint32(r.Mode)&0xFFFF)|uint32(modeIFREG)),
				UID:        r.UID, GID: r.GID,
				Size:       uint32(len(r.Data)),
				ATime: ts, CTime: ts, MTime: ts,
				LinksCount: 1,
				Blocks:     uint32(ceilDiv(len(r.Data), 512)),
			}
			writePointers(pl, pl.data, &ino)
			writeInode(r.Inode, &ino)
		}
	}

	_, err := w.Write(img)
	return err
}

package partition

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"unicode/utf16"
)

type gptHeader struct {
	Sig               [8]byte
	Rev               uint32
	HdrSize           uint32
	HdrCRC            uint32
	_                 uint32
	CurrentLBA        uint64
	BackupLBA         uint64
	FirstUsableLBA    uint64
	LastUsableLBA     uint64
	DiskGUID          [16]byte
	PartEntryLBA      uint64
	NumPartEntries    uint32
	PartEntrySize     uint32
	PartEntryArrayCRC uint32
}

type gptEntry struct {
	TypeGUID  [16]byte
	PartGUID  [16]byte
	FirstLBA  uint64
	LastLBA   uint64
	Attrs     uint64
	NameUTF16 [72]byte
}

func readGPT(r io.ReadSeeker) (*Table, error) {
	if _, err := r.Seek(int64(SectorSize), io.SeekStart); err != nil {
		return nil, err
	}
	var h gptHeader
	if err := binary.Read(r, binary.LittleEndian, &h); err != nil {
		return nil, err
	}
	if string(h.Sig[:]) != "EFI PART" {
		return nil, errors.New("no gpt sig")
	}
	hdr := h

	peBytes := int64(h.NumPartEntries) * int64(h.PartEntrySize)
	if _, err := r.Seek(int64(h.PartEntryLBA)*int64(SectorSize), io.SeekStart); err != nil {
		return nil, err
	}
	data := make([]byte, peBytes)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	if crc32LE(data) != h.PartEntryArrayCRC {
	}

	var entries []gptEntry
	br := bytes.NewReader(data)
	for i := uint32(0); i < h.NumPartEntries; i++ {
		var e gptEntry
		if err := binary.Read(br, binary.LittleEndian, &e); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	var out []Entry
	for i := range entries {
		e := &entries[i]
		if isZero16(e.TypeGUID[:]) || e.FirstLBA == 0 || e.LastLBA == 0 || e.LastLBA < e.FirstLBA {
			continue
		}
		out = append(out, Entry{
			Index:    len(out) + 1,
			StartLBA: e.FirstLBA,
			EndLBA:   e.LastLBA,
			Type:     guidStr(e.TypeGUID),
			Name:     ucs2ToString(e.NameUTF16[:]),
			Bootable: false,
		})
	}
	return &Table{
		Scheme:     GPT,
		SectorSize: SectorSize,
		Entries:    out,
		gptPrimary: &hdr,
		gptPE:      entries,
	}, nil
}

func (t *Table) maxUsedLBA() uint64 {
	var m uint64
	for _, e := range t.Entries {
		if e.EndLBA > m {
			m = e.EndLBA
		}
	}
	return m
}

func (t *Table) ResizeAware(path string, newSize int64) error {
	if t.Scheme != GPT {
		return os.Truncate(path, newSize)
	}
	fd, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer fd.Close()

	fi, err := fd.Stat()
	if err != nil {
		return err
	}
	_ = fi

	newSectors := uint64(newSize / int64(SectorSize))
	if newSectors < 64 {
		return fmt.Errorf("too small")
	}

	peBytes := uint64(t.gptPrimary.NumPartEntries) * uint64(t.gptPrimary.PartEntrySize)
	peSectors := (peBytes + uint64(SectorSize) - 1) / uint64(SectorSize)

	newLastLBA := newSectors - 1
	newBackupHeaderLBA := newLastLBA
	newBackupPEStart := newLastLBA - peSectors
	newLastUsable := newBackupPEStart - 1

	if t.maxUsedLBA() > newLastUsable {
		return fmt.Errorf("shrink below last used LBA (%d > %d)", t.maxUsedLBA(), newLastUsable)
	}

	if err := os.Truncate(path, newSize); err != nil {
		return err
	}

	peBuf := new(bytes.Buffer)
	for i := range t.gptPE {
		if err := binary.Write(peBuf, binary.LittleEndian, &t.gptPE[i]); err != nil {
			return err
		}
	}
	if _, err := fd.Seek(int64(newBackupPEStart)*int64(SectorSize), io.SeekStart); err != nil {
		return err
	}
	if _, err := fd.Write(peBuf.Bytes()); err != nil {
		return err
	}

	bhdr := *t.gptPrimary
	bhdr.CurrentLBA = newBackupHeaderLBA
	bhdr.BackupLBA = 1
	bhdr.PartEntryLBA = newBackupPEStart
	bhdr.FirstUsableLBA = t.gptPrimary.FirstUsableLBA
	bhdr.LastUsableLBA = newLastUsable
	bhdr.HdrCRC = 0

	hb := new(bytes.Buffer)
	if err := binary.Write(hb, binary.LittleEndian, &bhdr); err != nil {
		return err
	}
	h := hb.Bytes()
	putLE32(h[16:20], crc32LE(h[:bhdr.HdrSize]))
	if _, err := fd.Seek(int64(newBackupHeaderLBA)*int64(SectorSize), io.SeekStart); err != nil {
		return err
	}
	if _, err := fd.Write(h); err != nil {
		return err
	}

	ph := *t.gptPrimary
	ph.BackupLBA = newBackupHeaderLBA
	ph.LastUsableLBA = newLastUsable
	ph.HdrCRC = 0

	pb := new(bytes.Buffer)
	if err := binary.Write(pb, binary.LittleEndian, &ph); err != nil {
		return err
	}
	p := pb.Bytes()
	putLE32(p[16:20], crc32LE(p[:ph.HdrSize]))
	if _, err := fd.Seek(int64(SectorSize), io.SeekStart); err != nil {
		return err
	}
	if _, err := fd.Write(p); err != nil {
		return err
	}
	return nil
}

func guidStr(b [16]byte) string {
	a := binary.LittleEndian.Uint32(b[0:4])
	b2 := binary.LittleEndian.Uint16(b[4:6])
	c := binary.LittleEndian.Uint16(b[6:8])
	d := b[8:10]
	e := b[10:16]
	return fmt.Sprintf("%08x-%04x-%04x-%02x%02x-%02x%02x%02x%02x%02x%02x",
		a, b2, c, d[0], d[1], e[0], e[1], e[2], e[3], e[4], e[5])
}

func ucs2ToString(b []byte) string {
	if len(b)%2 != 0 {
		return ""
	}
	u16 := make([]uint16, 0, len(b)/2)
	for i := 0; i < len(b); i += 2 {
		v := binary.LittleEndian.Uint16(b[i:])
		if v == 0 {
			break
		}
		u16 = append(u16, v)
	}
	return string(utf16.Decode(u16))
}

func isZero16(b []byte) bool {
	for _, v := range b {
		if v != 0 {
		 return false
		}
	}
	return true
}

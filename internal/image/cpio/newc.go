package cpio

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"

	"goimagetool/internal/common"
	"goimagetool/internal/fs/memfs"
)

type header struct {
	Magic      string // "070701" or "070702"
	Ino        uint32
	Mode       uint32
	UID        uint32
	GID        uint32
	NLink      uint32
	MTime      uint32
	FileSize   uint32
	DevMajor   uint32
	DevMinor   uint32
	RDevMajor  uint32
	RDevMinor  uint32
	NameSize   uint32
	Check      uint32
}

func parseHex(b []byte) (uint32, error) {
	var out uint32
	for _, c := range b {
		var v byte
		switch {
		case c >= '0' && c <= '9':
			v = c - '0'
		case c >= 'a' && c <= 'f':
			v = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			v = c - 'A' + 10
		default:
			return 0, fmt.Errorf("bad hex: %q", c)
		}
		out = (out << 4) | uint32(v)
	}
	return out, nil
}

func readHeader(r io.Reader) (*header, error) {
	buf := make([]byte, 110)
	if _, err := io.ReadFull(r, buf); err != nil { return nil, err }
	h := &header{Magic: string(buf[:6])}
	if h.Magic != "070701" && h.Magic != "070702" {
		return nil, fmt.Errorf("unsupported cpio magic: %q", h.Magic)
	}
	off := 6
	get := func(n int) (uint32, error) { v, e := parseHex(buf[off:off+n]); off += n; return v, e }
	var err error
	if h.Ino, err = get(8); err != nil { return nil, err }
	if h.Mode, err = get(8); err != nil { return nil, err }
	if h.UID, err = get(8); err != nil { return nil, err }
	if h.GID, err = get(8); err != nil { return nil, err }
	if h.NLink, err = get(8); err != nil { return nil, err }
	if h.MTime, err = get(8); err != nil { return nil, err }
	if h.FileSize, err = get(8); err != nil { return nil, err }
	if h.DevMajor, err = get(8); err != nil { return nil, err }
	if h.DevMinor, err = get(8); err != nil { return nil, err }
	if h.RDevMajor, err = get(8); err != nil { return nil, err }
	if h.RDevMinor, err = get(8); err != nil { return nil, err }
	if h.NameSize, err = get(8); err != nil { return nil, err }
	if h.Check, err = get(8); err != nil { return nil, err }
	return h, nil
}

func pad4(n uint64) uint64 { return common.AlignUp(n, 4) }

func LoadNewc(r io.Reader) (*memfs.FS, error) {
	br := bufio.NewReader(r)
	fs := memfs.New()
	for {
		h, err := readHeader(br); if err != nil { return nil, err }
		nameBytes := make([]byte, h.NameSize)
		if _, err := io.ReadFull(br, nameBytes); err != nil { return nil, err }
		name := strings.TrimRight(string(nameBytes), "\x00")
		namePad := int(pad4(uint64(110 + h.NameSize)) - uint64(110+h.NameSize))
		if namePad > 0 { if _, err := io.CopyN(io.Discard, br, int64(namePad)); err != nil { return nil, err } }
		if name == "TRAILER!!!" { break }
		data := make([]byte, h.FileSize)
		if _, err := io.ReadFull(br, data); err != nil { return nil, err }
		datPad := int(pad4(uint64(h.FileSize)) - uint64(h.FileSize))
		if datPad > 0 { if _, err := io.CopyN(io.Discard, br, int64(datPad)); err != nil { return nil, err } }
		modeType := memfs.Mode(h.Mode & 0170000)
		if modeType == memfs.ModeDir {
			fs.PutDir(name, h.UID, h.GID, time.Unix(int64(h.MTime), 0))
		} else {
			fs.PutFile(name, data, memfs.Mode(h.Mode), h.UID, h.GID, time.Unix(int64(h.MTime), 0))
		}
	}
	return fs, nil
}

func StoreNewc(w io.Writer, fs *memfs.FS) error {
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	writeHex := func(v uint32, n int) { fmt.Fprintf(bw, "%0*X", n, v) }
	writeHeader := func(h *header, name string) error {
		if _, err := bw.WriteString("070701"); err != nil { return err }
		writeHex(h.Ino, 8); writeHex(h.Mode, 8); writeHex(h.UID, 8); writeHex(h.GID, 8)
		writeHex(h.NLink, 8); writeHex(h.MTime, 8); writeHex(h.FileSize, 8)
		writeHex(h.DevMajor, 8); writeHex(h.DevMinor, 8); writeHex(h.RDevMajor, 8); writeHex(h.RDevMinor, 8)
		writeHex(h.NameSize, 8); writeHex(0, 8)
		if _, err := bw.WriteString(name); err != nil { return err }
		if _, err := bw.Write([]byte{0}); err != nil { return err }
		pad := int(pad4(uint64(110 + h.NameSize)) - uint64(110+h.NameSize))
		if pad > 0 { _, _ = bw.Write(bytes.Repeat([]byte{0}, pad)) }
		return nil
	}
	var files []*memfs.Entry
	_ = fs.Walk(func(e *memfs.Entry) error { if e.Name != "/" { files = append(files, e) }; return nil })
	for _, e := range files {
		name := strings.TrimPrefix(e.Name, "/")
		if name == "" { continue }
		h := &header{
			Ino: 0, UID: e.UID, GID: e.GID, NLink: 1, MTime: uint32(e.MTime.Unix()),
			DevMajor: 0, DevMinor: 0, RDevMajor: 0, RDevMinor: 0,
			NameSize: uint32(len(name) + 1),
		}
		if e.Mode & memfs.ModeDir != 0 {
			h.Mode = uint32(memfs.ModeDir | 0755)
			h.FileSize = 0
			if err := writeHeader(h, name); err != nil { return err }
		} else {
			h.Mode = uint32(e.Mode)
			h.FileSize = uint32(len(e.Data))
			if err := writeHeader(h, name); err != nil { return err }
			if _, err := bw.Write(e.Data); err != nil { return err }
			pad := int(pad4(uint64(h.FileSize)) - uint64(h.FileSize))
			if pad > 0 { _, _ = bw.Write(bytes.Repeat([]byte{0}, pad)) }
		}
	}
	tr := &header{ NameSize: uint32(len("TRAILER!!!")+1) }
	if err := writeHeader(tr, "TRAILER!!!"); err != nil { return err }
	return nil
}

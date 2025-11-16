package partition

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"strings"
)

const (
	SectorSize = 512
)

type Scheme int

const (
	None Scheme = iota
	MBR
	GPT
)

type Entry struct {
	Index    int
	StartLBA uint64
	EndLBA   uint64
	Type     string
	Name     string
	Bootable bool
}

type Table struct {
	Scheme     Scheme
	SectorSize int
	Entries    []Entry
	// GPT specifics
	gptPrimary *gptHeader
	gptBackup  *gptHeader
	gptPE      []gptEntry
}

var errNoPT = errors.New("no partition table")

func Detect(path string) (*Table, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return DetectR(f)
}

func DetectR(r io.ReadSeeker) (*Table, error) {
	buf := make([]byte, SectorSize)
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	if isProtectiveMBR(buf) {
		t, err := readGPT(r)
		if err == nil && len(t.Entries) > 0 {
			return t, nil
		}
	}
	if t, err := readMBR(buf); err == nil && len(t.Entries) > 0 {
		return t, nil
	}
	// Try GPT even if not protective (some tools write bad MBR)
	if t, err := readGPT(r); err == nil && len(t.Entries) > 0 {
		return t, nil
	}
	return nil, errNoPT
}

func List(path string) ([]Entry, Scheme, error) {
	t, err := Detect(path)
	if err != nil {
		return nil, None, err
	}
	return t.Entries, t.Scheme, nil
}

func Extract(path string, idxOrName string, out string) error {
	t, err := Detect(path)
	if err != nil {
		return err
	}
	i, ok := t.findIdx(idxOrName)
	if !ok {
		return fmt.Errorf("partition %q not found", idxOrName)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	g, err := os.Create(out)
	if err != nil {
		return err
	}
	defer g.Close()
	start := int64(t.Entries[i].StartLBA) * int64(SectorSize)
	end := int64(t.Entries[i].EndLBA+1) * int64(SectorSize)
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return err
	}
	left := end - start
	buf := make([]byte, 1<<20)
	for left > 0 {
		chunk := int64(len(buf))
		if chunk > left {
			chunk = left
		}
		n, err := io.ReadFull(f, buf[:chunk])
		if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
			return err
		}
		if n == 0 {
			break
		}
		if _, err := g.Write(buf[:n]); err != nil {
			return err
		}
		left -= int64(n)
	}
	return nil
}

func Replace(path string, idxOrName string, in string) error {
	t, err := Detect(path)
	if err != nil {
		return err
	}
	i, ok := t.findIdx(idxOrName)
	if !ok {
		return fmt.Errorf("partition %q not found", idxOrName)
	}
	fd, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer fd.Close()
	src, err := os.Open(in)
	if err != nil {
		return err
	}
	defer src.Close()

	start := int64(t.Entries[i].StartLBA) * int64(SectorSize)
	end := int64(t.Entries[i].EndLBA+1) * int64(SectorSize)
	capacity := end - start

	if _, err := fd.Seek(start, io.SeekStart); err != nil {
		return err
	}
	written := int64(0)
	buf := make([]byte, 1<<20)
	for {
		n, er := src.Read(buf)
		if n > 0 {
			if written+int64(n) > capacity {
				return fmt.Errorf("input is larger than partition capacity (%d > %d)", written+int64(n), capacity)
			}
			if _, ew := fd.Write(buf[:n]); ew != nil {
				return ew
			}
			written += int64(n)
		}
		if er == io.EOF {
			break
		}
		if er != nil {
			return er
		}
	}
	// zero-pad the rest
	zero := make([]byte, len(buf))
	for written < capacity {
		to := capacity - written
		if to > int64(len(zero)) {
			to = int64(len(zero))
		}
		if _, err := fd.Write(zero[:to]); err != nil {
			return err
		}
		written += to
	}
	return nil
}

func (t *Table) findIdx(s string) (int, bool) {
	// by index (1-based)
	if len(s) > 0 && s[0] >= '0' && s[0] <= '9' {
		var x int
		fmt.Sscanf(s, "%d", &x)
		if x >= 1 && x <= len(t.Entries) {
			return x - 1, true
		}
	}
	// by name (GPT)
	ns := strings.ToLower(s)
	for i, e := range t.Entries {
		if strings.ToLower(e.Name) == ns {
			return i, true
		}
	}
	return 0, false
}

func isProtectiveMBR(sec []byte) bool {
	if len(sec) < SectorSize {
		return false
	}
	if sec[510] != 0x55 || sec[511] != 0xAA {
		return false
	}
	for i := 0; i < 4; i++ {
		typ := sec[446+i*16+4]
		if typ == 0xEE {
			return true
		}
	}
	return false
}

// CRC32 LE
func crc32LE(p []byte) uint32 {
	return crc32.ChecksumIEEE(p)
}

func putLE32(b []byte, v uint32) { binary.LittleEndian.PutUint32(b, v) }
func putLE64(b []byte, v uint64) { binary.LittleEndian.PutUint64(b, v) }

package partition

import (
	"encoding/binary"
	"fmt"
)

type mbrEntry struct {
	Boot byte
	_    [3]byte
	Type byte
	_    [3]byte
	LBA  uint32
	Sect uint32
}

func readMBR(sec []byte) (*Table, error) {
	if len(sec) < SectorSize {
		return nil, fmt.Errorf("short mbr")
	}
	if sec[510] != 0x55 || sec[511] != 0xAA {
		return nil, fmt.Errorf("bad mbr signature")
	}
	var ents []Entry
	for i := 0; i < 4; i++ {
		off := 446 + i*16
		e := mbrEntry{
			Boot: sec[off],
			Type: sec[off+4],
			LBA:  binary.LittleEndian.Uint32(sec[off+8:]),
			Sect: binary.LittleEndian.Uint32(sec[off+12:]),
		}
		if e.Type == 0 || e.Sect == 0 {
			continue
		}
		start := uint64(e.LBA)
		end := start + uint64(e.Sect) - 1
		ents = append(ents, Entry{
			Index:    len(ents) + 1,
			StartLBA: start,
			EndLBA:   end,
			Type:     fmt.Sprintf("MBR 0x%02X", e.Type),
			Name:     "",
			Bootable: e.Boot == 0x80,
		})
	}
	return &Table{
		Scheme:     MBR,
		SectorSize: SectorSize,
		Entries:    ents,
	}, nil
}

package legacy

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

const Magic uint32 = 0x27051956

type Header struct {
	Magic      uint32
	HCRC       uint32
	Time       uint32
	Size       uint32
	Load       uint32
	Entry      uint32
	DCRC       uint32
	OS         uint8
	Arch       uint8
	Type       uint8
	Comp       uint8
	Name       [32]byte
}

func (h *Header) String() string {
	return fmt.Sprintf("uImage name=%q size=%d load=0x%08x entry=0x%08x type=%d comp=%d",
		bytes.Trim(h.Name[:], "\x00"), h.Size, h.Load, h.Entry, h.Type, h.Comp)
}

func Read(r io.Reader) (*Header, []byte, error) {
	var h Header
	if err := binary.Read(r, binary.BigEndian, &h); err != nil { return nil, nil, err }
	if h.Magic != Magic { return nil, nil, errors.New("invalid uImage magic") }
	orig := h.HCRC
	h.HCRC = 0
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.BigEndian, &h); err != nil { return nil, nil, err }
	if crc32.ChecksumIEEE(buf.Bytes()) != orig { return nil, nil, errors.New("uImage header CRC mismatch") }
	data := make([]byte, h.Size)
	if _, err := io.ReadFull(r, data); err != nil { return nil, nil, err }
	if crc32.ChecksumIEEE(data) != h.DCRC { return nil, nil, errors.New("uImage data CRC mismatch") }
	return &h, data, nil
}

func Write(w io.Writer, h *Header, data []byte) error {
	h.Magic = Magic
	h.Size = uint32(len(data))
	h.DCRC = crc32.ChecksumIEEE(data)
	h.HCRC = 0
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.BigEndian, h); err != nil { return err }
	h.HCRC = crc32.ChecksumIEEE(buf.Bytes())
	buf.Reset()
	if err := binary.Write(&buf, binary.BigEndian, h); err != nil { return err }
	if _, err := w.Write(buf.Bytes()); err != nil { return err }
	_, err := w.Write(data)
	return err
}

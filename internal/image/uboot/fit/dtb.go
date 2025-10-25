package fit

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	fdtMagic uint32 = 0xd00dfeed
	FDT_BEGIN_NODE = 1
	FDT_END_NODE   = 2
	FDT_PROP       = 3
	FDT_NOP        = 4
	FDT_END        = 9
)

type fdtHeader struct {
	Magic         uint32
	TotalSize     uint32
	OffStruct     uint32
	OffStrings    uint32
	OffMemRsvmap  uint32
	Version       uint32
	LastCompVer   uint32
	BootCPUID     uint32
	SizeStrings   uint32
	SizeStruct    uint32
}

type Node struct {
	Name string
	Props map[string][]byte
	Children []*Node
}

func readCString(r *bytes.Reader) (string, error) {
	var buf []byte
	for {
		b, err := r.ReadByte()
		if err != nil { return "", err }
		if b == 0 { return string(buf), nil }
		buf = append(buf, b)
	}
}

func parseFDT(b []byte) (*Node, error) {
	if len(b) < 40 { return nil, errors.New("short fdt") }
	h := fdtHeader{}
	buf := bytes.NewReader(b)
	if err := binary.Read(buf, binary.BigEndian, &h); err != nil { return nil, err }
	if h.Magic != fdtMagic { return nil, errors.New("bad fdt magic") }
	structBlk := b[h.OffStruct:]
	stringsBlk := b[h.OffStrings: h.OffStrings+h.SizeStrings]
	rs := bytes.NewReader(structBlk)
	root := &Node{Name: "", Props: map[string][]byte{}}
	stack := []*Node{root}
	for {
		var tok uint32
		if err := binary.Read(rs, binary.BigEndian, &tok); err != nil { return nil, err }
		switch tok {
		case FDT_BEGIN_NODE:
			name, err := readCString(rs); if err != nil { return nil, err }
			for rs.Size()%4 != 0 { if _, err := rs.ReadByte(); err != nil { return nil, err } }
			n := &Node{Name: name, Props: map[string][]byte{}}
			parent := stack[len(stack)-1]
			parent.Children = append(parent.Children, n)
			stack = append(stack, n)
		case FDT_END_NODE:
			if len(stack) <= 1 { return nil, errors.New("unbalanced FDT_END_NODE") }
			stack = stack[:len(stack)-1]
		case FDT_PROP:
			var sz uint32
			var nameOff uint32
			if err := binary.Read(rs, binary.BigEndian, &sz); err != nil { return nil, err }
			if err := binary.Read(rs, binary.BigEndian, &nameOff); err != nil { return nil, err }
			if int(nameOff) >= len(stringsBlk) { return nil, errors.New("nameOff out of range") }
			name := string(stringsBlk[nameOff:][:bytes.IndexByte(stringsBlk[nameOff:], 0)])
			data := make([]byte, sz)
			if _, err := io.ReadFull(rs, data); err != nil { return nil, err }
			for rs.Size()%4 != 0 { if _, err := rs.ReadByte(); err != nil { return nil, err } }
			cur := stack[len(stack)-1]
			cur.Props[name] = data
		case FDT_NOP:
			continue
		case FDT_END:
			if len(stack) != 1 { return nil, errors.New("unterminated fdt") }
			return root, nil
		default:
			return nil, fmt.Errorf("unknown token %d", tok)
		}
	}
}

func writeCString(w *bytes.Buffer, s string) {
	w.WriteString(s)
	w.WriteByte(0)
	for w.Len()%4 != 0 { w.WriteByte(0) }
}

func buildFDT(root *Node) ([]byte, error) {
	var structBuf bytes.Buffer
	var stringsBuf bytes.Buffer
	strOff := map[string]uint32{}
	getStr := func(s string) uint32 {
		if off, ok := strOff[s]; ok { return off }
		off := uint32(stringsBuf.Len())
		stringsBuf.WriteString(s)
		stringsBuf.WriteByte(0)
		strOff[s] = off
		return off
	}
	var walk func(n *Node)
	walk = func(n *Node) {
		binary.Write(&structBuf, binary.BigEndian, uint32(FDT_BEGIN_NODE))
		writeCString(&structBuf, n.Name)
		for k, v := range n.Props {
			binary.Write(&structBuf, binary.BigEndian, uint32(FDT_PROP))
			binary.Write(&structBuf, binary.BigEndian, uint32(len(v)))
			binary.Write(&structBuf, binary.BigEndian, getStr(k))
			structBuf.Write(v)
			for structBuf.Len()%4 != 0 { structBuf.WriteByte(0) }
		}
		for _, ch := range n.Children { walk(ch) }
		binary.Write(&structBuf, binary.BigEndian, uint32(FDT_END_NODE))
	}
	walk(root)
	binary.Write(&structBuf, binary.BigEndian, uint32(FDT_END))

	h := fdtHeader{
		Magic: fdtMagic,
		TotalSize: 0,
		OffStruct: 40,
		OffStrings: 40 + uint32(structBuf.Len()),
		OffMemRsvmap: 0,
		Version: 17,
		LastCompVer: 16,
		BootCPUID: 0,
		SizeStrings: uint32(stringsBuf.Len()),
		SizeStruct:  uint32(structBuf.Len()),
	}
	var out bytes.Buffer
	binary.Write(&out, binary.BigEndian, h)
	out.Write(structBuf.Bytes())
	out.Write(stringsBuf.Bytes())
	b := out.Bytes()
	binary.BigEndian.PutUint32(b[4:8], uint32(len(b)))
	return b, nil
}

package fit

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

const (
	fdtMagic = 0xd00dfeed

	fdtBeginNode = 1
	fdtEndNode   = 2
	fdtProp      = 3
	fdtNop       = 4
	fdtEnd       = 9
)

type fdtHeader struct {
	Magic          uint32
	TotalSize      uint32
	OffDTStruct    uint32
	OffDTStrings   uint32
	OffMemRsvmap   uint32
	Version        uint32
	LastCompVer    uint32
	BootCPUIDPhys  uint32
	SizeDTStrings  uint32
	SizeDTStruct   uint32
}

func align8(n int) int { return (n + 7) &^ 7 }

func getCString(b []byte, off uint32) string {
	if int(off) >= len(b) {
		return ""
	}
	i := int(off)
	for i < len(b) && b[i] != 0 {
		i++
	}
	return string(b[off:uint32(i)])
}

func putCString(buf *bytes.Buffer, s string) {
	buf.WriteString(s)
	buf.WriteByte(0)
}

type nodeCtx struct {
	name string
	path string
}

func parseFDT(b []byte) (structBlk, strBlk []byte, err error) {
	if len(b) < 40 {
		return nil, nil, errors.New("fdt: short header")
	}
	var h fdtHeader
	_ = binary.Read(bytes.NewReader(b[:40]), binary.BigEndian, &h)
	if h.Magic != fdtMagic {
		return nil, nil, errors.New("fdt: bad magic")
	}
	if int(h.OffDTStruct)+int(h.SizeDTStruct) > len(b) ||
		int(h.OffDTStrings)+int(h.SizeDTStrings) > len(b) ||
		int(h.OffMemRsvmap) > len(b) {
		return nil, nil, errors.New("fdt: bad offsets")
	}
	structBlk = b[h.OffDTStruct : h.OffDTStruct+h.SizeDTStruct]
	strBlk = b[h.OffDTStrings : h.OffDTStrings+h.SizeDTStrings]
	return
}

func asString(v []byte) string {
	i := bytes.IndexByte(v, 0)
	if i < 0 {
		return string(v)
	}
	return string(v[:i])
}

// Read(r): старый core вызывает Read(io.Reader). Реальный парсинг FIT ITB (FDT).
func Read(r io.Reader) (*Fit, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(b) < 4 || binary.BigEndian.Uint32(b[:4]) != fdtMagic {
		f := New()
		_ = f.AddTyped("blob0", b, "sha1", "custom")
		f.Default = "blob0"
		return f, nil
	}

	structBlk, strBlk, err := parseFDT(b)
	if err != nil {
		return nil, err
	}

	f := New()
	stack := make([]nodeCtx, 0, 8)
	rd := bytes.NewReader(structBlk)

	var inImages, inConfigs bool
	var curImg *Image
	var curImgName string
	var defaultConfig string
	var cfgKernel string

	for {
		var token uint32
		if err := binary.Read(rd, binary.BigEndian, &token); err != nil {
			return nil, err
		}
		switch token {
		case fdtBeginNode:
			nameBuf := make([]byte, 0, 32)
			for {
				b1 := make([]byte, 1)
				if _, err := rd.Read(b1); err != nil {
					return nil, err
				}
				if b1[0] == 0 {
					break
				}
				nameBuf = append(nameBuf, b1[0])
			}
			if pad := (4 - (1+len(nameBuf))%4) % 4; pad > 0 {
				if _, err := rd.Seek(int64(pad), 1); err != nil {
					return nil, err
				}
			}
			name := string(nameBuf)
			path := "/"
			if len(stack) > 0 {
				if stack[len(stack)-1].path == "/" {
					path = "/" + name
				} else {
					path = stack[len(stack)-1].path + "/" + name
				}
			}
			stack = append(stack, nodeCtx{name: name, path: path})

			switch path {
			case "/images":
				inImages = true
			case "/configurations":
				inConfigs = true
			}
			if inImages && len(stack) >= 2 && stack[len(stack)-2].path == "/images" && name != "" {
				curImgName = name
				curImg = &Image{Name: name, HashAlgo: "sha1", Type: "custom"}
			}

		case fdtEndNode:
			if len(stack) == 0 {
				return nil, errors.New("fdt: stack underflow")
			}
			if inImages && len(stack) >= 2 && stack[len(stack)-2].path == "/images" && stack[len(stack)-1].name == curImgName && curImg != nil {
				if curImg.Digest == nil {
					curImg.Digest = hashData(curImg.HashAlgo, curImg.Data)
				}
				f.imgs[curImg.Name] = curImg
				if f.Default == "" {
					f.Default = curImg.Name
				}
				curImg = nil
				curImgName = ""
			}
			if stack[len(stack)-1].path == "/images" {
				inImages = false
			}
			if stack[len(stack)-1].path == "/configurations" {
				inConfigs = false
			}
			stack = stack[:len(stack)-1]

		case fdtProp:
			var sz, nameOff uint32
			if err := binary.Read(rd, binary.BigEndian, &sz); err != nil {
				return nil, err
			}
			if err := binary.Read(rd, binary.BigEndian, &nameOff); err != nil {
				return nil, err
			}
			val := make([]byte, sz)
			if sz > 0 {
				if _, err := rd.Read(val); err != nil {
					return nil, err
				}
			}
			if pad := (4 - (int(sz)%4)) % 4; pad > 0 {
				if _, err := rd.Seek(int64(pad), 1); err != nil {
					return nil, err
				}
			}
			propName := getCString(strBlk, nameOff)
			if len(stack) == 0 {
				continue
			}
			curPath := stack[len(stack)-1].path

			if inImages && curImg != nil && len(stack) >= 2 && stack[len(stack)-2].path == "/images" {
				switch propName {
				case "data":
					curImg.Data = append([]byte(nil), val...)
				case "type":
					t := asString(val)
					if t == "flat_dt" {
						curImg.Type = "fdt"
					} else {
						curImg.Type = t
					}
				}
			}
			if inImages && curImg != nil && len(stack) >= 3 && stack[len(stack)-3].path == "/images" && stringsHasPrefix(stack[len(stack)-1].name, "hash") {
				switch propName {
				case "algo":
					a := asString(val)
					if a == "sha-1" {
						a = "sha1"
					}
					curImg.HashAlgo = normAlgo(a)
				case "value":
					curImg.Digest = append([]byte(nil), val...)
				}
			}

			if inConfigs && curPath == "/configurations" && propName == "default" {
				defaultConfig = asString(val)
			}
			if inConfigs && len(stack) >= 2 && stack[len(stack)-2].path == "/configurations" {
				switch propName {
				case "kernel":
					cfgKernel = asString(val)
				}
			}

		case fdtNop:
		case fdtEnd:
			if defaultConfig != "" && cfgKernel != "" {
				f.Default = cfgKernel
			}
			if f.Default == "" {
				names := f.List()
				if len(names) > 0 {
					f.Default = names[0]
				}
			}
			return f, nil
		default:
			return nil, errors.New("fdt: bad token")
		}
	}
}

// Write(w, f): старый core вызывает Write(io.Writer, *FIT). Собираем валидный ITB.
func Write(w io.Writer, f *Fit) error {
	if f == nil || len(f.imgs) == 0 {
		return errors.New("fit: empty")
	}
	_ = f.Verify()

	sb := new(bytes.Buffer)
	addStr := func(s string) uint32 {
		off := sb.Len()
		putCString(sb, s)
		return uint32(off)
	}
	offData := addStr("data")
	offType := addStr("type")
	_ = addStr("images")
	offAlgo := addStr("algo")
	offValue := addStr("value")
	_ = addStr("configurations")
	offDefault := addStr("default")
	offKernel := addStr("kernel")
	offFdt := addStr("fdt")
	offRamdisk := addStr("ramdisk")

	sbStruct := new(bytes.Buffer)
	putU32 := func(v uint32) { _ = binary.Write(sbStruct, binary.BigEndian, v) }
	putToken := func(t uint32) { putU32(t) }
	putProp := func(nameOff uint32, val []byte) {
		putToken(fdtProp)
		putU32(uint32(len(val)))
		putU32(nameOff)
		sbStruct.Write(val)
		if pad := (4 - (len(val)%4)) % 4; pad > 0 {
			sbStruct.Write(make([]byte, pad))
		}
	}
	putBegin := func(name string) {
		putToken(fdtBeginNode)
		sbStruct.WriteString(name)
		sbStruct.WriteByte(0)
		if pad := (4 - (1+len(name))%4) % 4; pad > 0 {
			sbStruct.Write(make([]byte, pad))
		}
	}
	putEnd := func() { putToken(fdtEndNode) }

	putBegin("") // root

	putBegin("images")
	names := f.List()
	for _, name := range names {
		img := f.imgs[name]
		if img == nil {
			continue
		}
		putBegin(img.Name)
		putProp(offData, img.Data)
		t := img.Type
		if t == "fdt" {
			t = "flat_dt"
		}
		if t == "" {
			t = "custom"
		}
		putProp(offType, append([]byte(t), 0x00))

		putBegin("hash")
		algo := img.HashAlgo
		if algo == "sha1" {
			algo = "sha-1"
		}
		putProp(offAlgo, append([]byte(algo), 0x00))
		putProp(offValue, img.Digest)
		putEnd() // hash

		putEnd() // image
	}
	putEnd() // images

	putBegin("configurations")
	defCfg := "conf-1"
	putProp(offDefault, append([]byte(defCfg), 0x00))
	putBegin(defCfg)

	defKernel := f.Default
	if defKernel == "" && len(names) > 0 {
		defKernel = names[0]
	}
	if defKernel != "" {
		putProp(offKernel, append([]byte(defKernel), 0x00))
	}
	var fdtName, rdName string
	for _, n := range names {
		if fdtName == "" && f.imgs[n].Type == "fdt" {
			fdtName = n
		}
		if rdName == "" && f.imgs[n].Type == "ramdisk" {
			rdName = n
		}
	}
	if fdtName != "" {
		putProp(offFdt, append([]byte(fdtName), 0x00))
	}
	if rdName != "" {
		putProp(offRamdisk, append([]byte(rdName), 0x00))
	}
	putEnd() // conf-1
	putEnd() // configurations

	putEnd()              // root
	putToken(fdtEnd)      // end token
	mem := make([]byte, 16) // empty mem_rsvmap + terminator

	h := fdtHeader{
		Magic:        fdtMagic,
		TotalSize:    0,
		OffDTStruct:  0,
		OffDTStrings: 0,
		OffMemRsvmap: 40,
		Version:      17,
		LastCompVer:  16,
	}
	offStruct := align8(int(h.OffMemRsvmap) + len(mem))
	offStrings := offStruct + sbStruct.Len()
	h.OffDTStruct = uint32(offStruct)
	h.OffDTStrings = uint32(offStrings)
	h.SizeDTStruct = uint32(sbStruct.Len())
	h.SizeDTStrings = uint32(sb.Len())
	h.TotalSize = uint32(offStrings + sb.Len())

	out := new(bytes.Buffer)
	_ = binary.Write(out, binary.BigEndian, &h)
	if out.Len() < int(h.OffMemRsvmap) {
		out.Write(make([]byte, int(h.OffMemRsvmap)-out.Len()))
	}
	out.Write(mem)
	if out.Len() < offStruct {
		out.Write(make([]byte, offStruct-out.Len()))
	}
	out.Write(sbStruct.Bytes())
	out.Write(sb.Bytes())

	_, err := w.Write(out.Bytes())
	return err
}

func stringsHasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}

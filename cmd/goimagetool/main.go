package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"goimagetool/internal/core"
	"goimagetool/internal/fs/memfs"
	"goimagetool/internal/image/uboot/fit"
)

func usage() {
	fmt.Println(`goimagetool - unified image tool (Go)
Usage:
  goimagetool [--session <path|auto>] <commands...>

Load:
  goimagetool load auto <path>
  goimagetool load initramfs <path> [compression]        # auto|none|gzip|zstd|lz4|lzma|bzip2|xz
  goimagetool load kernel-legacy <uImagePath>
  goimagetool load kernel-fit <itbPath> [compression]
  goimagetool load squashfs <imgPath> [compression]
  goimagetool load ext2 <imgPath> [compression]
  goimagetool load tar <path> [compression]              # auto|none|gzip

Store:
  goimagetool store initramfs <path> [compression]
  goimagetool store kernel-legacy <uImagePath>
  goimagetool store kernel-fit <itbPath> [compression]
  goimagetool store squashfs <imgPath> [compression]          # gzip|xz|zstd|lz4|lzo|lzma
  goimagetool store ext2 <imgPath> [blockSize] [compression]  # 1024|2048|4096
  goimagetool store tar <path> [compression]                  # none|gzip

FS:
  goimagetool fs ls [-L] [path]
  goimagetool fs add <srcPath> <dstPathInImage>
  goimagetool fs extract <dstDir>
  goimagetool fs ln -s <target> <dstPathInImage>
  goimagetool fs mknod <c|b|p> <major> <minor> <dstPathInImage>

FIT:
  goimagetool fit new|ls|add|rm|set-default|extract|verify ...

TUI:
  goimagetool fm [hostStartDir]

Image (host file ops):
  goimagetool image resize <path> (+SIZE|-SIZE|--to SIZE[K|M|G])
  goimagetool image pad    <path> --align SIZE[K|M|G]

Session:
  goimagetool session save [path] | load [path] | clear

Other:
  goimagetool info | help
`)
}

func defaultSessionPath() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return "session.json"
	}
	return filepath.Join(home, ".cache", "goimagetool", "session.json")
}

func defaultSessionPathAuto() string {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = "/tmp"
	}
	wd, _ := os.Getwd()
	h := sha1.Sum([]byte(wd))
	name := fmt.Sprintf("%x.session", h[:6])
	return filepath.Join(base, "goimagetool", name)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

type autoDetect struct {
	typ  string
	comp string
}

func detectImageType(path string) (autoDetect, error) {
	var r autoDetect
	f, err := os.Open(path)
	if err != nil {
		return r, err
	}
	defer f.Close()

	head := make([]byte, 4096)
	n, _ := io.ReadFull(f, head)
	head = head[:n]

	ext := strings.ToLower(filepath.Ext(path))

	if n >= 4 {
		be := binary.BigEndian.Uint32(head[:4])
		le := binary.LittleEndian.Uint32(head[:4])
		switch be {
		case 0x27051956:
			return autoDetect{typ: "kernel-legacy", comp: "auto"}, nil
		case 0xd00dfeed:
			return autoDetect{typ: "kernel-fit", comp: "auto"}, nil
		}
		switch le {
		case 0x73717368:
			return autoDetect{typ: "squashfs", comp: "auto"}, nil
		}
	}
	if n >= 262 && bytes.Equal(head[257:257+5], []byte("ustar")) {
		return autoDetect{typ: "tar", comp: "none"}, nil
	}
	if n >= 6 && bytes.Equal(head[:6], []byte("070701")) {
		return autoDetect{typ: "initramfs", comp: "none"}, nil
	}
	if n >= 2 && head[0] == 0x1f && head[1] == 0x8b {
		if strings.HasSuffix(strings.ToLower(path), ".tar.gz") || strings.HasSuffix(strings.ToLower(path), ".tgz") {
			return autoDetect{typ: "tar", comp: "gzip"}, nil
		}
		return autoDetect{typ: "initramfs", comp: "auto"}, nil
	}
	if n >= 6 && bytes.Equal(head[:6], []byte{0xfd, 0x37, 0x7a, 0x58, 0x5a, 0x00}) {
		return autoDetect{typ: "initramfs", comp: "auto"}, nil
	}
	if n >= 4 && bytes.Equal(head[:4], []byte{0x28, 0xb5, 0x2f, 0xfd}) {
		return autoDetect{typ: "initramfs", comp: "auto"}, nil
	}
	if n >= 4 && bytes.Equal(head[:4], []byte{0x04, 0x22, 0x4d, 0x18}) {
		return autoDetect{typ: "initramfs", comp: "auto"}, nil
	}
	if n >= 3 && bytes.Equal(head[:3], []byte("BZh")) {
		return autoDetect{typ: "initramfs", comp: "auto"}, nil
	}
	if n >= 5 && bytes.Equal(head[:5], []byte{0xfd, 0x37, 0x7a, 0x58, 0x00}) {
		return autoDetect{typ: "initramfs", comp: "auto"}, nil
	}

	switch ext {
	case ".itb":
		return autoDetect{typ: "kernel-fit", comp: "auto"}, nil
	case ".uimage":
		return autoDetect{typ: "kernel-legacy", comp: "auto"}, nil
	case ".tar":
		return autoDetect{typ: "tar", comp: "none"}, nil
	case ".tgz":
		return autoDetect{typ: "tar", comp: "gzip"}, nil
	case ".gz":
		if strings.HasSuffix(strings.ToLower(path), ".tar.gz") {
			return autoDetect{typ: "tar", comp: "gzip"}, nil
		}
		return autoDetect{typ: "initramfs", comp: "auto"}, nil
	case ".cpio", ".cpio.gz", ".cpio.zst", ".cpio.xz", ".cpio.lz4", ".cpio.bz2", ".cpio.lzma",
		".zst", ".xz", ".lz4", ".bz2", ".lzma":
		return autoDetect{typ: "initramfs", comp: "auto"}, nil
	case ".sqsh", ".squashfs":
		return autoDetect{typ: "squashfs", comp: "auto"}, nil
	case ".ext2", ".img":
		buf := make([]byte, 2)
		if _, err := f.Seek(1024+56, io.SeekStart); err == nil {
			if _, err := io.ReadFull(f, buf); err == nil {
				if binary.LittleEndian.Uint16(buf) == 0xEF53 {
					return autoDetect{typ: "ext2", comp: "none"}, nil
				}
			}
		}
	}
	return autoDetect{typ: "initramfs", comp: "auto"}, nil
}

func parseSize(arg string) (int64, error) {
	if arg == "" {
		return 0, fmt.Errorf("empty size")
	}
	mult := int64(1)
	end := len(arg)
	last := arg[len(arg)-1]
	switch last {
	case 'K', 'k':
		mult = 1024
		end--
	case 'M', 'm':
		mult = 1024 * 1024
		end--
	case 'G', 'g':
		mult = 1024 * 1024 * 1024
		end--
	}
	var v int64
	if _, err := fmt.Sscanf(arg[:end], "%d", &v); err != nil {
		return 0, err
	}
	if v < 0 {
		return 0, fmt.Errorf("negative size")
	}
	return v * mult, nil
}

func doImageResize(path, spec string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	cur := fi.Size()

	switch {
	case strings.HasPrefix(spec, "+"):
		delta, err := parseSize(strings.TrimPrefix(spec, "+"))
		if err != nil {
			return err
		}
		if delta < 0 {
			return fmt.Errorf("delta < 0")
		}
		return os.Truncate(path, cur+delta)
	case strings.HasPrefix(spec, "-"):
		delta, err := parseSize(strings.TrimPrefix(spec, "-"))
		if err != nil {
			return err
		}
		if delta < 0 || delta > cur {
			return fmt.Errorf("invalid shrink")
		}
		return os.Truncate(path, cur-delta)
	case spec == "--to":
		return fmt.Errorf("use: image resize <path> --to SIZE[K|M|G]")
	default:
		if strings.HasPrefix(spec, "--to") {
			parts := strings.SplitN(spec, " ", 2)
			if len(parts) != 2 {
				return fmt.Errorf("use: image resize <path> --to SIZE[K|M|G]")
			}
			newSize, err := parseSize(strings.TrimSpace(parts[1]))
			if err != nil {
				return err
			}
			return os.Truncate(path, newSize)
		}
		// support: image resize <path> --to SIZE  (split args already)
		return fmt.Errorf("invalid spec: %q", spec)
	}
}

func doImagePad(path string, alignStr string) error {
	if alignStr == "" {
		return fmt.Errorf("missing --align")
	}
	align, err := parseSize(alignStr)
	if err != nil || align <= 0 {
		return fmt.Errorf("bad align")
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	cur := fi.Size()
	mod := cur % align
	if mod == 0 {
		return nil
	}
	return os.Truncate(path, cur+(align-mod))
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
		return
	}

	var sessionPath string
	if env := os.Getenv("GOIMAGETOOL_SESSION"); env != "" {
		sessionPath = env
	}
	if len(args) >= 1 && args[0] == "--session" {
		switch {
		case len(args) >= 2 && args[1] == "auto":
			sessionPath = defaultSessionPathAuto()
			args = args[2:]
		case len(args) >= 2 && !strings.HasPrefix(args[1], "-"):
			sessionPath = args[1]
			args = args[2:]
		default:
			sessionPath = defaultSessionPath()
			args = args[1:]
		}
	}

	st := core.New()
	loaded := false

	if sessionPath != "" {
		if err := st.LoadSession(sessionPath); err == nil {
			loaded = true
		}
		if dir := filepath.Dir(sessionPath); dir != "" {
			_ = os.MkdirAll(dir, 0o755)
		}
	}

	i := 0
	for i < len(args) {
		switch args[i] {
		case "help", "-h", "--help":
			usage()
			return

		case "session":
			if i+1 >= len(args) {
				usage()
				os.Exit(1)
			}
			act := args[i+1]
			switch act {
			case "save":
				p := sessionPath
				if i+2 < len(args) {
					p = args[i+2]
					i++
				}
				if p == "" {
					p = defaultSessionPath()
				}
				if err := st.SaveSession(p); err != nil {
					fmt.Fprintln(os.Stderr, "session save:", err)
					os.Exit(2)
				}
				i += 2
			case "load":
				p := sessionPath
				if i+2 < len(args) {
					p = args[i+2]
					i++
				}
				if p == "" {
					p = defaultSessionPath()
				}
				if err := st.LoadSession(p); err != nil {
					fmt.Fprintln(os.Stderr, "session load:", err)
					os.Exit(2)
				}
				loaded = true
				i += 2
			case "clear":
				st = core.New()
				loaded = false
				i += 2
			default:
				fmt.Fprintln(os.Stderr, "unknown session action:", act)
				os.Exit(2)
			}

		case "load":
			if i+2 >= len(args) {
				usage()
				os.Exit(1)
			}
			typ := args[i+1]
			switch typ {
			case "auto":
				p := args[i+2]
				ad, err := detectImageType(p)
				if err != nil {
					fmt.Fprintln(os.Stderr, "auto:", err)
					os.Exit(2)
				}
				switch ad.typ {
				case "initramfs":
					if err := st.LoadInitramfs(p, ad.comp); err != nil {
						fmt.Fprintln(os.Stderr, "load:", err)
						os.Exit(2)
					}
				case "kernel-legacy":
					if err := st.LoadKernelLegacy(p); err != nil {
						fmt.Fprintln(os.Stderr, "load:", err)
						os.Exit(2)
					}
				case "kernel-fit":
					if err := st.LoadKernelFIT(p, ad.comp); err != nil {
						fmt.Fprintln(os.Stderr, "load:", err)
						os.Exit(2)
					}
				case "squashfs":
					if err := st.LoadSquashFS(p, ad.comp); err != nil {
						fmt.Fprintln(os.Stderr, "load:", err)
						os.Exit(2)
					}
				case "ext2":
					if err := st.LoadExt2(p, ad.comp); err != nil {
						fmt.Fprintln(os.Stderr, "load:", err)
						os.Exit(2)
					}
				case "tar":
					if err := st.LoadTar(p, ad.comp); err != nil {
						fmt.Fprintln(os.Stderr, "load:", err)
						os.Exit(2)
					}
				default:
					fmt.Fprintln(os.Stderr, "auto: unknown type")
					os.Exit(2)
				}
				loaded = true
				i += 3

			case "initramfs", "kernel-legacy", "kernel-fit", "squashfs", "ext2", "tar":
				p := args[i+2]
				comp := "auto"
				if (typ == "initramfs" || typ == "kernel-fit" || typ == "ext2" || typ == "squashfs" || typ == "tar") && i+3 < len(args) {
					comp = args[i+3]
					i++
				}
				var err error
				switch typ {
				case "initramfs":
					err = st.LoadInitramfs(p, comp)
				case "kernel-legacy":
					err = st.LoadKernelLegacy(p)
				case "kernel-fit":
					err = st.LoadKernelFIT(p, comp)
				case "squashfs":
					err = st.LoadSquashFS(p, comp)
				case "ext2":
					err = st.LoadExt2(p, comp)
				case "tar":
					err = st.LoadTar(p, comp)
				}
				if err != nil {
					fmt.Fprintln(os.Stderr, "load:", err)
					os.Exit(2)
				}
				loaded = true
				i += 3

			default:
				fmt.Fprintln(os.Stderr, "unknown load type:", typ)
				os.Exit(2)
			}

		case "fs":
			if !loaded {
				fmt.Fprintln(os.Stderr, "no image loaded; use 'load' or 'session load' first")
				os.Exit(2)
			}
			if i+1 >= len(args) {
				usage()
				os.Exit(1)
			}
			a := args[i+1]
			switch a {
			case "ls":
				p := "/"
				follow := false
				consumed := 2
				j := i + 2
				if j < len(args) && args[j] == "-L" {
					follow = true
					j++
					consumed++
				}
				if j < len(args) && !strings.HasPrefix(args[j], "-") {
					p = args[j]
					consumed++
				}
				resolved, ent := resolvePathFollow(st.FS, p, follow)
				fmt.Printf("TYPE MODE    UID:GID  SIZE  NAME\n")
				if ent == nil {
					i += consumed
					break
				}
				if ent.Mode&memfs.ModeDir != 0 {
					for _, e := range st.FS.List(resolved) {
						printEntryLine(e)
					}
				} else {
					printEntryLine(ent)
				}
				i += consumed

			case "add":
				if i+3 >= len(args) {
					usage()
					os.Exit(1)
				}
				src, dst := args[i+2], args[i+3]
				if err := st.FSAddLocal(src, dst); err != nil {
					fmt.Fprintln(os.Stderr, "fs add:", err)
					os.Exit(2)
				}
				i += 4
			case "extract":
				if i+2 >= len(args) {
					usage()
					os.Exit(1)
				}
				dst := args[i+2]
				if err := os.MkdirAll(dst, 0755); err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(2)
				}
				if err := st.FSExtract(dst); err != nil {
					fmt.Fprintln(os.Stderr, "fs extract:", err)
					os.Exit(2)
				}
				i += 3
			case "ln":
				if i+4 >= len(args) || args[i+2] != "-s" {
					usage()
					os.Exit(1)
				}
				target, dst := args[i+3], args[i+4]
				st.FS.PutSymlink(dst, target, 0, 0, time.Now())
				i += 5
			case "mknod":
				if i+6 >= len(args) {
					usage()
					os.Exit(1)
				}
				typ := args[i+2]
				var maj, min int
				if _, err := fmt.Sscanf(args[i+3], "%d", &maj); err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(2)
				}
				if _, err := fmt.Sscanf(args[i+4], "%d", &min); err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(2)
				}
				dst := args[i+5]
				var mode memfs.Mode
				switch typ {
				case "c":
					mode = memfs.ModeChar
				case "b":
					mode = memfs.ModeBlock
				case "p":
					mode = memfs.ModeFIFO
				default:
					fmt.Fprintln(os.Stderr, "unknown node type, use c|b|p")
					os.Exit(2)
				}
				st.FS.PutNode(dst, mode, 0o666, 0, 0, uint32(maj), uint32(min), time.Now())
				i += 6
			default:
				fmt.Fprintln(os.Stderr, "unknown fs action:", a)
				os.Exit(2)
			}

		case "fit":
			if i+1 >= len(args) {
				usage()
				os.Exit(1)
			}
			a := args[i+1]
			switch a {
			case "new":
				st.Kind = core.KindKernelFIT
				st.Meta = &core.FitMeta{F: fit.New()}
				loaded = true
				i += 2

			case "ls":
				m, _ := st.Meta.(*core.FitMeta)
				if m == nil || m.F == nil {
					fmt.Fprintln(os.Stderr, "no FIT loaded")
					os.Exit(2)
				}
				for _, name := range m.F.List() {
					mark := ""
					if m.F.Default == name {
						mark = " *"
					}
					img, _ := m.F.Get(name)
					typ := img.Type
					if typ == "" {
						typ = "blob"
					}
					fmt.Printf("%s%s (%s, %s)\n", name, mark, typ, img.HashAlgo)
				}
				i += 2

			case "add":
				m, _ := st.Meta.(*core.FitMeta)
				if m == nil || m.F == nil {
					fmt.Fprintln(os.Stderr, "no FIT loaded")
					os.Exit(2)
				}
				j := i + 2
				setType := ""
				setHash := "sha1"
				for j < len(args) && strings.HasPrefix(args[j], "-") {
					switch args[j] {
					case "--type", "-t":
						if j+1 >= len(args) {
							fmt.Fprintln(os.Stderr, "fit add: missing value for --type")
							os.Exit(2)
						}
						setType = args[j+1]
						j += 2
						continue
					case "--hash", "-H":
						if j+1 >= len(args) {
							fmt.Fprintln(os.Stderr, "fit add: missing value for --hash")
							os.Exit(2)
						}
						setHash = args[j+1]
						j += 2
						continue
					default:
						fmt.Fprintln(os.Stderr, "fit add: unknown flag", args[j])
						os.Exit(2)
					}
				}
				if j+1 >= len(args) {
					usage()
					os.Exit(1)
				}
				name, file := args[j], args[j+1]
				b, err := os.ReadFile(file)
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(2)
				}
				if err := m.F.AddTyped(name, b, setHash, setType); err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(2)
				}
				i = j + 2

			case "rm":
				if i+2 >= len(args) {
					usage()
					os.Exit(1)
				}
				name := args[i+2]
				m, _ := st.Meta.(*core.FitMeta)
				if m == nil || m.F == nil {
					fmt.Fprintln(os.Stderr, "no FIT loaded")
					os.Exit(2)
				}
				m.F.Remove(name)
				i += 3

			case "set-default":
				if i+2 >= len(args) {
					usage()
					os.Exit(1)
				}
				name := args[i+2]
				m, _ := st.Meta.(*core.FitMeta)
				if m == nil || m.F == nil {
					fmt.Fprintln(os.Stderr, "no FIT loaded")
					os.Exit(2)
				}
				m.F.SetDefault(name)
				i += 3

			case "extract":
				if i+3 >= len(args) {
					usage()
					os.Exit(1)
				}
				name, out := args[i+2], args[i+3]
				m, _ := st.Meta.(*core.FitMeta)
				if m == nil || m.F == nil {
					fmt.Fprintln(os.Stderr, "no FIT loaded")
					os.Exit(2)
				}
				img, err := m.F.Get(name)
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(2)
				}
				if err := os.WriteFile(out, img.Data, 0644); err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(2)
				}
				i += 4

			case "verify":
				m, _ := st.Meta.(*core.FitMeta)
				if m == nil || m.F == nil {
					fmt.Fprintln(os.Stderr, "no FIT loaded")
					os.Exit(2)
				}
				if i+2 < len(args) && !strings.HasPrefix(args[i+2], "-") {
					ok, err := m.F.VerifyOne(args[i+2])
					if err != nil {
						fmt.Fprintln(os.Stderr, err)
						os.Exit(2)
					}
					if !ok {
						fmt.Fprintln(os.Stderr, "verify: mismatch")
						os.Exit(2)
					}
					fmt.Println("OK")
					i += 3
				} else {
					if err := m.F.Verify(); err != nil {
						fmt.Fprintln(os.Stderr, err)
						os.Exit(2)
					}
					fmt.Println("OK")
					i += 2
				}

			default:
				fmt.Fprintln(os.Stderr, "unknown fit action:", a)
				os.Exit(2)
			}

		case "store":
			if !loaded {
				fmt.Fprintln(os.Stderr, "nothing loaded to store")
				os.Exit(2)
			}
			if i+2 >= len(args) {
				usage()
				os.Exit(1)
			}
			typ := args[i+1]
			switch typ {
			case "initramfs":
				out := args[i+2]
				comp := "none"
				if i+3 < len(args) {
					comp = args[i+3]
					i++
				}
				if err := st.StoreInitramfs(out, comp); err != nil {
					fmt.Fprintln(os.Stderr, "store:", err)
					os.Exit(2)
				}
				i += 3
			case "kernel-legacy":
				out := args[i+2]
				if err := st.StoreKernelLegacy(out); err != nil {
					fmt.Fprintln(os.Stderr, "store:", err)
					os.Exit(2)
				}
				i += 3
			case "kernel-fit":
				out := args[i+2]
				comp := "none"
				if i+3 < len(args) {
					comp = args[i+3]
					i++
				}
				if err := st.StoreKernelFIT(out, comp); err != nil {
					fmt.Fprintln(os.Stderr, "store:", err)
					os.Exit(2)
				}
				i += 3
			case "squashfs":
				out := args[i+2]
				comp := "gzip"
				if i+3 < len(args) {
					comp = args[i+3]
					i++
				}
				if err := st.StoreSquashFS(out, comp); err != nil {
					fmt.Fprintln(os.Stderr, "store:", err)
					os.Exit(2)
				}
				i += 3
			case "ext2":
				out := args[i+2]
				bs := 1024
				comp := "none"
				if i+3 < len(args) {
					nxt := args[i+3]
					if isDigits(nxt) {
						fmt.Sscanf(nxt, "%d", &bs)
						if i+4 < len(args) {
							comp = args[i+4]
							i++
						}
					} else {
						comp = nxt
					}
					i++
				}
				if err := st.StoreExt2(out, bs, comp); err != nil {
					fmt.Fprintln(os.Stderr, "store:", err)
					os.Exit(2)
				}
				i += 3
			case "tar":
				out := args[i+2]
				comp := "none"
				if i+3 < len(args) {
					comp = args[i+3]
					i++
				}
				if err := st.StoreTar(out, comp); err != nil {
					fmt.Fprintln(os.Stderr, "store:", err)
					os.Exit(2)
				}
				i += 3
			default:
				fmt.Fprintln(os.Stderr, "unknown store type:", typ)
				os.Exit(2)
			}

		case "info":
			fmt.Println(st.Info())
			i++

		case "fm":
			host := ""
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				host = args[i+1]
				i += 2
			} else {
				i++
			}
			// TUI запускается из отдельного пакета; здесь просто хелпер
			fmt.Println("TUI FM is available in this build. Run: goimagetool fm <dir> (if integrated).")
			_ = host

		case "image":
			if i+1 >= len(args) {
				usage()
				os.Exit(1)
			}
			sub := args[i+1]
			switch sub {
			case "resize":
				if i+2 >= len(args) {
					usage()
					os.Exit(1)
				}
				path := args[i+2]
				if i+3 >= len(args) {
					usage()
					os.Exit(1)
				}
				spec := args[i+3]
				// также поддержим форму: "--to", "<SIZE>"
				if spec == "--to" {
					if i+4 >= len(args) {
						fmt.Fprintln(os.Stderr, "use: image resize <path> --to SIZE[K|M|G]")
						os.Exit(2)
					}
					spec = "--to " + args[i+4]
					i++
				}
				if err := doImageResize(path, spec); err != nil {
					fmt.Fprintln(os.Stderr, "image resize:", err)
					os.Exit(2)
				}
				i += 4
			case "pad":
				if i+2 >= len(args) || i+3 >= len(args) || args[i+3] == "" || args[i+3] == "--align" && i+4 >= len(args) {
					usage()
					os.Exit(1)
				}
				path := args[i+2]
				if args[i+3] != "--align" {
					fmt.Fprintln(os.Stderr, "use: image pad <path> --align SIZE[K|M|G]")
					os.Exit(2)
				}
				if i+4 >= len(args) {
					fmt.Fprintln(os.Stderr, "use: image pad <path> --align SIZE[K|M|G]")
					os.Exit(2)
				}
				align := args[i+4]
				if err := doImagePad(path, align); err != nil {
					fmt.Fprintln(os.Stderr, "image pad:", err)
					os.Exit(2)
				}
				i += 5
			default:
				fmt.Fprintln(os.Stderr, "unknown image action:", sub)
				os.Exit(2)
			}

		default:
			usage()
			os.Exit(1)
		}
	}

	if sessionPath != "" {
		_ = st.SaveSession(sessionPath)
	}
}

// util

func printEntryLine(e *memfs.Entry) {
	t := "-"
	name := strings.TrimPrefix(e.Name, "/")
	size := len(e.Data)
	switch {
	case e.Mode&memfs.ModeDir != 0:
		t = "d"
	case e.Mode&memfs.ModeLink != 0:
		t = "l"
		name = fmt.Sprintf("%s -> %s", name, e.Target)
		size = len(e.Target)
	case e.Mode&memfs.ModeChar != 0:
		t = "c"
	case e.Mode&memfs.ModeBlock != 0:
		t = "b"
	case e.Mode&memfs.ModeFIFO != 0:
		t = "p"
	default:
		t = "f"
	}
	fmt.Printf("%s    %06o %d:%d %5d %s\n",
		t, uint32(e.Mode)&0o7777, e.UID, e.GID, size, name)
}

func resolvePathFollow(fs *memfs.FS, p string, follow bool) (string, *memfs.Entry) {
	p = filepath.ToSlash(p)
	if p == "" {
		p = "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	snap := fs.Snapshot()
	if !follow {
		return p, snap[p]
	}

	limit := 40
	cur := "/"
	rest := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for step := 0; step < limit; step++ {
		if len(rest) == 0 {
			return cur, snap[cur]
		}
		c := rest[0]
		if c == "" {
			rest = rest[1:]
			continue
		}
		var next string
		if cur == "/" {
			next = "/" + c
		} else {
			next = cur + "/" + c
		}
		e := snap[next]
		if e == nil {
			return next, nil
		}
		if e.Mode&memfs.ModeLink != 0 {
			tgt := e.Target
			if tgt == "" {
				return next, e
			}
			var newPath string
			if strings.HasPrefix(tgt, "/") {
				newPath = tgt
			} else {
				if cur == "/" {
					newPath = "/" + tgt
				} else {
					newPath = cur + "/" + tgt
				}
			}
			if len(rest) > 1 {
				tail := strings.Join(rest[1:], "/")
				if strings.HasSuffix(newPath, "/") {
					newPath += tail
				} else {
					newPath = newPath + "/" + tail
				}
			}
			newPath = filepath.ToSlash(filepath.Clean("/" + strings.TrimPrefix(newPath, "/")))
			cur = "/"
			rest = strings.Split(strings.TrimPrefix(newPath, "/"), "/")
			continue
		}
		cur = next
		rest = rest[1:]
	}
	return cur, snap[cur]
}

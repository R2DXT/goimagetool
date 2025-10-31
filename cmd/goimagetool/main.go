package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"goimagetool/internal/core"
	"goimagetool/internal/fs/memfs"
	"goimagetool/internal/image/uboot/fit"
)

func usage() {
	fmt.Println(`goimagetool - unified image tool (Go)
Usage:
  goimagetool [--session <path>] <commands...>

  goimagetool load initramfs <path> [compression]        # compression: auto|none|gzip|zstd|lz4|lzma|bzip2|xz
  goimagetool load kernel-legacy <uImagePath>
  goimagetool load kernel-fit <itbPath> [compression]
  goimagetool load squashfs <imgPath> [compression]
  goimagetool load ext2 <imgPath> [compression]

  goimagetool store initramfs <path> [compression]
  goimagetool store kernel-legacy <uImagePath>
  goimagetool store kernel-fit <itbPath> [compression]
  goimagetool store squashfs <imgPath> [compression]          # gzip|xz|zstd|lz4|lzo|lzma
  goimagetool store ext2 <imgPath> [blockSize] [compression]  # 1024|2048|4096; compression optional

  goimagetool fs ls [-L] [path]            # -L follow symlinks if 'path' is a symlink
  goimagetool fs add <srcPath> <dstPathInImage>
  goimagetool fs extract <dstDir>
  goimagetool fs ln -s <target> <dstPathInImage>
  goimagetool fs mknod <c|b|p> <major> <minor> <dstPathInImage>

  goimagetool fit new
  goimagetool fit ls
  goimagetool fit add <name> <file>
  goimagetool fit rm <name>
  goimagetool fit set-default <name>
  goimagetool fit extract <imageName> <outPath>

  goimagetool session save [path]
  goimagetool session load [path]
  goimagetool session clear

  goimagetool info
  goimagetool help
`)
}

func defaultSessionPath() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return "session.json"
	}
	return filepath.Join(home, ".cache", "goimagetool", "session.json")
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

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
		return
	}

	var sessionPath string
	if len(args) >= 1 && args[0] == "--session" {
		if len(args) >= 2 && !strings.HasPrefix(args[1], "-") {
			sessionPath = args[1]
			args = args[2:]
		} else {
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
			p := args[i+2]
			comp := "auto"
			if (typ == "initramfs" || typ == "kernel-fit" || typ == "ext2" || typ == "squashfs") && i+3 < len(args) {
				comp = args[i+3]
				i++
			}
			switch typ {
			case "initramfs":
				if err := st.LoadInitramfs(p, comp); err != nil {
					fmt.Fprintln(os.Stderr, "load:", err)
					os.Exit(2)
				}
			case "kernel-legacy":
				if err := st.LoadKernelLegacy(p); err != nil {
					fmt.Fprintln(os.Stderr, "load:", err)
					os.Exit(2)
				}
			case "kernel-fit":
				if err := st.LoadKernelFIT(p, comp); err != nil {
					fmt.Fprintln(os.Stderr, "load:", err)
					os.Exit(2)
				}
			case "squashfs":
				if err := st.LoadSquashFS(p, comp); err != nil {
					fmt.Fprintln(os.Stderr, "load:", err)
					os.Exit(2)
				}
			case "ext2":
				if err := st.LoadExt2(p, comp); err != nil {
					fmt.Fprintln(os.Stderr, "load:", err)
					os.Exit(2)
				}
			default:
				fmt.Fprintln(os.Stderr, "unknown load type:", typ)
				os.Exit(2)
			}
			loaded = true
			i += 3

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
				// fs ls [-L] [path]
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
					// нет такого пути — пробуем показать как есть (пусто)
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
				st.FS.PutSymlink(dst, target, 0, 0, st.FS.Snapshot()["/"].MTime)
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
				st.FS.PutNode(dst, mode, 0o666, 0, 0, uint32(maj), uint32(min), st.FS.Snapshot()["/"].MTime)
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
					fmt.Println(name)
				}
				i += 2
			case "add":
				if i+3 >= len(args) {
					usage()
					os.Exit(1)
				}
				name, file := args[i+2], args[i+3]
				m, _ := st.Meta.(*core.FitMeta)
				if m == nil || m.F == nil {
					fmt.Fprintln(os.Stderr, "no FIT loaded")
					os.Exit(2)
				}
				b, err := os.ReadFile(file)
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(2)
				}
				m.F.Add(name, b, "sha1")
				i += 4
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
			default:
				fmt.Fprintln(os.Stderr, "unknown store type:", typ)
				os.Exit(2)
			}

		case "info":
			fmt.Println(st.Info())
			i++

		default:
			usage()
			os.Exit(1)
		}
	}

	if sessionPath != "" {
		_ = st.SaveSession(sessionPath)
	}
}

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

// resolvePathFollow: если follow = true и p — симлинк, разыменовываем до реального пути.
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

	// пошаговое разрешение по компонентам пути
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
		// если симлинк — подставляем цель и продолжаем
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
		// обычный узел — шагаем дальше
		cur = next
		rest = rest[1:]
	}
	// защита от циклов
	return cur, snap[cur]
}

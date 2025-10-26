package main

import (
	"fmt"
	"os"
	"strings"

	"goimagetool/internal/core"
	"goimagetool/internal/image/uboot/fit"
)

func usage() {
	fmt.Println(`goimagetool - unified image tool (Go)
Usage:
  goimagetool load initramfs <path> [compression]     # compression: none|gzip
  goimagetool load kernel-legacy <uImagePath>
  goimagetool load kernel-fit <itbPath>
  goimagetool load ext2 <imgPath>

  goimagetool store initramfs <path> [compression]
  goimagetool store kernel-legacy <uImagePath>
  goimagetool store kernel-fit <itbPath>
  goimagetool store ext2 <imgPath> [blockSize]         # 1024|2048|4096

  goimagetool fs ls [path]
  goimagetool fs add <srcPath> <dstPathInImage>
  goimagetool fs extract <dstDir>

  goimagetool fit new
  goimagetool fit ls
  goimagetool fit add <name> <file>
  goimagetool fit rm <name>
  goimagetool fit set-default <name>
  goimagetool fit extract <imageName> <outPath>

  goimagetool info
  goimagetool help
`)
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 { usage(); return }

	st := core.New()

	i := 0
	loaded := false

	for i < len(args) {
		switch args[i] {
		case "help", "-h", "--help":
			usage(); return

		case "load":
			if i+2 >= len(args) { usage(); os.Exit(1) }
			typ := args[i+1]
			p := args[i+2]
			comp := "none"
			if typ == "initramfs" && i+3 < len(args) { comp = args[i+3]; i++ }
			switch typ {
			case "initramfs":
				if err := st.LoadInitramfs(p, comp); err != nil { fmt.Fprintln(os.Stderr, "load:", err); os.Exit(2) }
			case "kernel-legacy":
				if err := st.LoadKernelLegacy(p); err != nil { fmt.Fprintln(os.Stderr, "load:", err); os.Exit(2) }
			case "kernel-fit":
				if err := st.LoadKernelFIT(p); err != nil { fmt.Fprintln(os.Stderr, "load:", err); os.Exit(2) }
			case "ext2":
				if err := st.LoadExt2(p); err != nil { fmt.Fprintln(os.Stderr, "load:", err); os.Exit(2) }
			default:
				fmt.Fprintln(os.Stderr, "unknown load type:", typ); os.Exit(2)
			}
			loaded = true
			i += 3

		case "fs":
			if !loaded { fmt.Fprintln(os.Stderr, "no image loaded; use 'load' first"); os.Exit(2) }
			if i+1 >= len(args) { usage(); os.Exit(1) }
			a := args[i+1]
			if a == "ls" {
				p := "/"
				if i+2 < len(args) { p = args[i+2]; i++ }
				fmt.Printf("TYPE MODE   UID:GID  SIZE  NAME\n")
				for _, e := range st.FS.List(p) {
					t := "f"
					if e.Mode & 0040000 != 0 { t = "d" }
					fmt.Printf("%s    %06o %d:%d %5d %s\n",
						t, uint32(e.Mode)&0o7777, e.UID, e.GID, len(e.Data), strings.TrimPrefix(e.Name, "/"))
				}
				i += 2
			} else if a == "add" {
				if i+3 >= len(args) { usage(); os.Exit(1) }
				src, dst := args[i+2], args[i+3]
				if err := st.FSAddLocal(src, dst); err != nil { fmt.Fprintln(os.Stderr, "fs add:", err); os.Exit(2) }
				i += 4
			} else if a == "extract" {
				if i+2 >= len(args) { usage(); os.Exit(1) }
				dst := args[i+2]
				if err := os.MkdirAll(dst, 0755); err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(2) }
				if err := st.FSExtract(dst); err != nil { fmt.Fprintln(os.Stderr, "fs extract:", err); os.Exit(2) }
				i += 3
			} else {
				fmt.Fprintln(os.Stderr, "unknown fs action:", a); os.Exit(2)
			}

		case "fit":
			if i+1 >= len(args) { usage(); os.Exit(1) }
			a := args[i+1]
			switch a {
			case "new":
				st.Kind = core.KindKernelFIT
				st.Meta = &core.FitMeta{F: fit.New()}
				loaded = true
				i += 2
			case "ls":
				m, _ := st.Meta.(*core.FitMeta)
				if m == nil || m.F == nil { fmt.Fprintln(os.Stderr, "no FIT loaded"); os.Exit(2) }
				for _, name := range m.F.List() { fmt.Println(name) }
				i += 2
			case "add":
				if i+3 >= len(args) { usage(); os.Exit(1) }
				name, file := args[i+2], args[i+3]
				m, _ := st.Meta.(*core.FitMeta)
				if m == nil || m.F == nil { fmt.Fprintln(os.Stderr, "no FIT loaded"); os.Exit(2) }
				b, err := os.ReadFile(file); if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(2) }
				m.F.Add(name, b, "sha1")
				i += 4
			case "rm":
				if i+2 >= len(args) { usage(); os.Exit(1) }
				name := args[i+2]
				m, _ := st.Meta.(*core.FitMeta)
				if m == nil || m.F == nil { fmt.Fprintln(os.Stderr, "no FIT loaded"); os.Exit(2) }
				m.F.Remove(name)
				i += 3
			case "set-default":
				if i+2 >= len(args) { usage(); os.Exit(1) }
				name := args[i+2]
				m, _ := st.Meta.(*core.FitMeta)
				if m == nil || m.F == nil { fmt.Fprintln(os.Stderr, "no FIT loaded"); os.Exit(2) }
				m.F.SetDefault(name)
				i += 3
			case "extract":
				if i+3 >= len(args) { usage(); os.Exit(1) }
				name, out := args[i+2], args[i+3]
				m, _ := st.Meta.(*core.FitMeta)
				if m == nil || m.F == nil { fmt.Fprintln(os.Stderr, "no FIT loaded"); os.Exit(2) }
				img, err := m.F.Get(name); if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(2) }
				if err := os.WriteFile(out, img.Data, 0644); err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(2) }
				i += 4
			default:
				fmt.Fprintln(os.Stderr, "unknown fit action:", a); os.Exit(2)
			}

		case "store":
			if !loaded { fmt.Fprintln(os.Stderr, "nothing loaded to store"); os.Exit(2) }
			if i+2 >= len(args) { usage(); os.Exit(1) }
			typ := args[i+1]
			switch typ {
			case "initramfs":
				out := args[i+2]
				comp := "none"
				if i+3 < len(args) { comp = args[i+3]; i++ }
				if err := st.StoreInitramfs(out, comp); err != nil { fmt.Fprintln(os.Stderr, "store:", err); os.Exit(2) }
				i += 3
			case "kernel-legacy":
				out := args[i+2]
				if err := st.StoreKernelLegacy(out); err != nil { fmt.Fprintln(os.Stderr, "store:", err); os.Exit(2) }
				i += 3
			case "kernel-fit":
				out := args[i+2]
				if err := st.StoreKernelFIT(out); err != nil { fmt.Fprintln(os.Stderr, "store:", err); os.Exit(2) }
				i += 3
			case "ext2":
				out := args[i+2]
				bs := 1024
				if i+3 < len(args) { fmt.Sscanf(args[i+3], "%d", &bs); i++ }
				if err := st.StoreExt2(out, bs); err != nil { fmt.Fprintln(os.Stderr, "store:", err); os.Exit(2) }
				i += 3
			default:
				fmt.Fprintln(os.Stderr, "unknown store type:", typ); os.Exit(2)
			}

		case "info":
			fmt.Println(st.Info())
			i++

		default:
			usage(); os.Exit(1)
		}
	}
}

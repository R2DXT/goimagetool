package main

import (
	"fmt"
	"os"
	"strings"

	"goimagetool/internal/core"
)

func usage() {
	fmt.Println(`goimagetool - unified image tool (Go)
Usage:
  goimagetool load initramfs <path> [compression]     # compression: none|gzip
  goimagetool load kernel-legacy <uImagePath>

  goimagetool store initramfs <path> [compression]
  goimagetool store kernel-legacy <uImagePath>

  goimagetool fs ls [path]
  goimagetool fs add <srcPath> <dstPathInImage>
  goimagetool fs extract <dstDir>

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
			if typ == "initramfs" {
				if err := st.LoadInitramfs(p, comp); err != nil { fmt.Fprintln(os.Stderr, "load:", err); os.Exit(2) }
			} else if typ == "kernel-legacy" {
				if err := st.LoadKernelLegacy(p); err != nil { fmt.Fprintln(os.Stderr, "load:", err); os.Exit(2) }
			} else {
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
				for _, e := range st.FS.List(p) {
					t := "file"
					if e.Mode & 0040000 != 0 { t = "dir" }
					fmt.Printf("%s\t%s\t%d\n", t, strings.TrimPrefix(e.Name, "/"), len(e.Data))
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
		case "store":
			if !loaded { fmt.Fprintln(os.Stderr, "nothing loaded to store"); os.Exit(2) }
			if i+2 >= len(args) { usage(); os.Exit(1) }
			typ := args[i+1]
			if typ == "initramfs" {
				out := args[i+2]
				comp := "none"
				if i+3 < len(args) { comp = args[i+3]; i++ }
				if err := st.StoreInitramfs(out, comp); err != nil { fmt.Fprintln(os.Stderr, "store:", err); os.Exit(2) }
				i += 3
			} else if typ == "kernel-legacy" {
				out := args[i+2]
				if err := st.StoreKernelLegacy(out); err != nil { fmt.Fprintln(os.Stderr, "store:", err); os.Exit(2) }
				i += 3
			} else {
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

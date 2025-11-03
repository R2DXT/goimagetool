package main

import (
	"fmt"
	"os"
	"strings"

	"goimagetool/internal/core"
	"goimagetool/internal/tui/fm"
)

// Вызовите runFM(st, args) из вашего switch/case в main.go (case "fm": ...)

func runFM(st *core.State, args []string) error {
	hostDir := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		hostDir = args[0]
	}
	if st == nil || st.FS == nil {
		return fmt.Errorf("no image loaded; use 'load' first")
	}
	return fm.Run(st, hostDir)
}

// Для справки в usage()
func fmUsage() string {
	return "  goimagetool fm [<hostDir>]        # two-pane TUI: left=image FS, right=host\n"
}

// Если у вас есть общий usage(), можно печатать fmUsage() рядом.
func printIfErr(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fm:", err)
		os.Exit(2)
	}
}

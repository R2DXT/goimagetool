package fm

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"goimagetool/internal/core"
	"goimagetool/internal/fs/memfs"
)

type panel int

const (
	pLeft panel = iota
	pRight
)

type item struct {
	name    string
	isDir   bool
	isLink  bool
	path    string
	size    int64
	modTime time.Time
}

// фиксированные ширины колонок «как VC»
const (
	colNameWidth = 12
	colExtWidth  = 4
	colSizeWidth = 8
	colDateWidth = 10
	colTimeWidth = 5
)

type fm struct {
	app    *tview.Application
	pages  *tview.Pages
	grid   *tview.Grid
	header *tview.TextView
	left   *tview.TextView
	right  *tview.TextView
	footer *tview.TextView

	st         *core.State
	leftPath   string
	rightPath  string
	active     panel
	leftIndex  int
	rightIndex int
	leftItems  []item
	rightItems []item
}

func Run(st *core.State, hostStart string) error {
	if st == nil {
		st = core.New()
	}
	if st.FS == nil {
		st.FS = memfs.New()
		st.FS.PutDir("/", 0, 0, time.Now())
	}
	if hostStart == "" {
		if wd, _ := os.Getwd(); wd != "" {
			hostStart = wd
		} else {
			hostStart = string(os.PathSeparator)
		}
	}
	hostStart, _ = filepath.Abs(hostStart)

	f := &fm{
		app:       tview.NewApplication(),
		pages:     tview.NewPages(),
		grid:      tview.NewGrid(),
		header:    tview.NewTextView(),
		left:      tview.NewTextView(),
		right:     tview.NewTextView(),
		footer:    tview.NewTextView(),
		st:        st,
		leftPath:  "/",
		rightPath: hostStart,
		active:    pLeft,
	}

	f.style()
	f.layout()
	f.bindKeys()

	if err := f.refresh(pLeft); err != nil { return err }
	if err := f.refresh(pRight); err != nil { return err }
	f.drawHeader()
	f.drawFooter()
	f.updateTitles()

	f.pages.AddAndSwitchToPage("main", f.grid, true)
	f.app.SetRoot(f.pages, true)
	f.app.SetFocus(f.left)
	return f.app.Run()
}

func (f *fm) style() {
	// фон/рамки
	tview.Styles.PrimitiveBackgroundColor = tcell.ColorNavy
	tview.Styles.ContrastBackgroundColor = tcell.ColorBlue
	tview.Styles.BorderColor = tcell.ColorSkyblue
	tview.Styles.PrimaryTextColor = tcell.ColorWhite

	f.header.SetBorder(true)
	f.header.SetDynamicColors(true)
	f.header.SetTitle(" Volkov-style FM ")
	f.header.SetTitleColor(tcell.ColorSkyblue)
	fmt.Fprint(f.header, " ")

	f.footer.SetBorder(true)
	f.footer.SetDynamicColors(true)
	fmt.Fprint(f.footer, f.footerText())

	for _, tv := range []*tview.TextView{f.left, f.right} {
		tv.SetBorder(true)
		tv.SetTitleAlign(tview.AlignLeft)
		tv.SetBackgroundColor(tcell.ColorBlue)
		tv.SetDynamicColors(true) // обязательно для [fg:bg] тегов
		tv.SetScrollable(false)
	}
	f.left.SetTitle(" image FS ")
	f.right.SetTitle(" host ")
}

func (f *fm) footerText() string {
	lbl := func(fn, t string) string { return fmt.Sprintf("[black:white] %s [-:-:-] [yellow]%s[-]", fn, t) }
	return strings.Join([]string{
		lbl("F1", "Help"),
		lbl("F2", "Menu"),
		lbl("F3", "View"),
		lbl("F4", "Edit"),
		lbl("F5", "Copy"),
		lbl("F6", "Move"),
		lbl("F7", "Mkdir"),
		lbl("F8", "Delete"),
		lbl("F9", "PullDn"),
		lbl("F10", "Quit"),
	}, "  ")
}

func (f *fm) layout() {
	f.grid.SetRows(3, 0, 2).SetColumns(0, 0).SetBorders(false)
	center := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(f.left, 0, 1, true).
		AddItem(f.right, 0, 1, false)
	f.grid.AddItem(f.header, 0, 0, 1, 2, 0, 0, false)
	f.grid.AddItem(center, 1, 0, 1, 2, 0, 0, true)
	f.grid.AddItem(f.footer, 2, 0, 1, 2, 0, 0, false)
}

func (f *fm) drawHeader() {
	f.header.Clear()
	fmt.Fprintf(f.header, "[yellow]FS[-]: [white]%s[-]   [yellow]HOST[-]: [white]%s[-]",
		f.safe(f.leftPath), f.safe(f.rightPath))
}

func (f *fm) drawFooter() { f.footer.Clear(); fmt.Fprint(f.footer, f.footerText()) }

func (f *fm) updateTitles() {
	if f.active == pLeft {
		f.left.SetTitleColor(tcell.ColorWhite)
		f.right.SetTitleColor(tcell.ColorSteelBlue)
		f.app.SetFocus(f.left)
	} else {
		f.left.SetTitleColor(tcell.ColorSteelBlue)
		f.right.SetTitleColor(tcell.ColorWhite)
		f.app.SetFocus(f.right)
	}
}

func (f *fm) formatFileItem(name string, isDir bool, size int64, modTime time.Time) string {
	base := name
	ext := ""
	if !isDir {
		if dot := strings.LastIndex(name, "."); dot > 0 {
			base = name[:dot]
			ext = name[dot+1:]
		}
	}
	if len(base) > colNameWidth {
		base = base[:colNameWidth]
	} else {
		base += strings.Repeat(" ", colNameWidth-len(base))
	}
	if len(ext) > colExtWidth {
		ext = ext[:colExtWidth]
	} else {
		ext += strings.Repeat(" ", colExtWidth-len(ext))
	}
	var sizeStr string
	if isDir {
		sizeStr = strings.Repeat(" ", colSizeWidth)
	} else {
		sizeStr = fmt.Sprintf("%8d", size)
	}
	dateStr := modTime.Format("02.01.06")
	if modTime.IsZero() {
		dateStr = strings.Repeat(" ", colDateWidth)
	}
	timeStr := modTime.Format("15:04")
	if modTime.IsZero() {
		timeStr = strings.Repeat(" ", colTimeWidth)
	}
	return fmt.Sprintf("%s %s %s %s %s", base, ext, sizeStr, dateStr, timeStr)
}

func (f *fm) drawPanel(pn panel) {
	var tv *tview.TextView
	var items []item
	var currentIndex int
	var path string

	if pn == pLeft {
		tv, items, currentIndex, path = f.left, f.leftItems, f.leftIndex, f.leftPath
	} else {
		tv, items, currentIndex, path = f.right, f.rightItems, f.rightIndex, f.rightPath
	}

	tv.Clear()
	displayItems := items
	hasParent := (pn == pLeft && path != "/") || (pn == pRight && !f.isRoot(path))
	if hasParent {
		parentItem := item{name: "..", isDir: true, path: filepath.Dir(path)}
		displayItems = append([]item{parentItem}, items...)
	}
	for i, it := range displayItems {
		line := f.formatFileItem(it.name, it.isDir, it.size, it.modTime)
		if i == currentIndex {
			// фиксированные цвета для стабильности
			fmt.Fprintf(tv, "[black:teal]%s[-:-:-]\n", line)
		} else {
			fmt.Fprintf(tv, "%s\n", line)
		}
	}
}

func (f *fm) refresh(pn panel) error {
	if pn == pLeft {
		items, err := f.listFS(f.leftPath)
		if err != nil { return err }
		f.leftItems = items
		f.drawPanel(pLeft)
		return nil
	}
	items, err := f.listHost(f.rightPath)
	if err != nil { return err }
	f.rightItems = items
	f.drawPanel(pRight)
	return nil
}

func (f *fm) listFS(path string) ([]item, error) {
	res := []item{}
	snap := f.st.FS.Snapshot()
	p := filepath.ToSlash(path)
	if p == "" { p = "/" }
	prefix := p
	if prefix == "/" { prefix = "" }
	seen := map[string]bool{}

	for full := range snap {
		if full == "/" || !strings.HasPrefix(full, p) { continue }
		rest := strings.TrimPrefix(full, prefix)
		if !strings.HasPrefix(rest, "/") { continue }
		rest = strings.TrimPrefix(rest, "/")
		if rest == "" { continue }
		first := rest
		if i := strings.IndexByte(rest, '/'); i >= 0 { first = rest[:i] }
		if seen[first] { continue }
		seen[first] = true
		child := snap[f.join(p, first)]
		isDir := child != nil && (child.Mode&memfs.ModeDir) != 0
		isLink := child != nil && (child.Mode&memfs.ModeLink) != 0
		var size int64
		var modTime time.Time
		if child != nil {
			size = int64(len(child.Data))
			modTime = child.MTime
		}
		res = append(res, item{
			name:    first,
			isDir:   isDir,
			isLink:  isLink,
			path:    f.join(p, first),
			size:    size,
			modTime: modTime,
		})
	}
	sort.Slice(res, func(i, j int) bool {
		if res[i].isDir != res[j].isDir { return res[i].isDir && !res[j].isDir }
		return strings.ToLower(res[i].name) < strings.ToLower(res[j].name)
	})
	return res, nil
}

func (f *fm) listHost(path string) ([]item, error) {
	ents, err := os.ReadDir(path)
	if err != nil { return nil, err }
	out := make([]item, 0, len(ents))
	for _, de := range ents {
		info, err := de.Info(); if err != nil { continue }
		isLink := (info.Mode() & os.ModeSymlink) != 0
		out = append(out, item{
			name:    de.Name(),
			isDir:   de.IsDir(),
			isLink:  isLink,
			path:    filepath.Join(path, de.Name()),
			size:    info.Size(),
			modTime: info.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].isDir != out[j].isDir { return out[i].isDir && !out[j].isDir }
		return strings.ToLower(out[i].name) < strings.ToLower(out[j].name)
	})
	return out, nil
}

func (f *fm) bindKeys() {
	f.app.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyTab:
			if f.active == pLeft { f.active = pRight } else { f.active = pLeft }
			f.updateTitles(); f.drawHeader(); return nil
		case tcell.KeyUp:
			f.moveCursor(-1); return nil
		case tcell.KeyDown:
			f.moveCursor(+1); return nil
		case tcell.KeyPgUp:
			f.moveCursor(-15); return nil
		case tcell.KeyPgDn:
			f.moveCursor(+15); return nil
		case tcell.KeyHome:
			f.setIndex(0); return nil
		case tcell.KeyEnd:
			f.setIndex(1<<30 - 1); return nil
		case tcell.KeyRight, tcell.KeyEnter:
			f.enter(); return nil
		case tcell.KeyLeft:
			f.up(); return nil
		case tcell.KeyF1:
			f.alert("F1 Help  F2 Menu  F3 View  F4 Edit  F5 Copy  F6 Move  F7 Mkdir  F8 Delete  F9 PullDn  F10 Quit\nTAB — panel, Enter/→ — open, ← — up")
			return nil
		case tcell.KeyF3:
			_ = f.view(); return nil
		case tcell.KeyF4:
			_ = f.edit(); return nil
		case tcell.KeyF5:
			_ = f.copy(); return nil
		case tcell.KeyF7:
			_ = f.mkdir(); return nil
		case tcell.KeyF10, tcell.KeyEsc:
			f.app.Stop(); return nil
		}
		return ev
	})
}

func (f *fm) setIndex(i int) {
	if f.active == pLeft {
		max := len(f.leftItems)
		if f.leftPath != "/" { max++ }
		if max == 0 { return }
		if i < 0 { i = 0 }
		if i >= max { i = max - 1 }
		f.leftIndex = i
		f.drawPanel(pLeft)
		return
	}
	max := len(f.rightItems)
	if !f.isRoot(f.rightPath) { max++ }
	if max == 0 { return }
	if i < 0 { i = 0 }
	if i >= max { i = max - 1 }
	f.rightIndex = i
	f.drawPanel(pRight)
}

func (f *fm) moveCursor(d int) { f.setIndex(f.currentIndex()+d) }

func (f *fm) currentIndex() int {
	if f.active == pLeft { return f.leftIndex }
	return f.rightIndex
}

func (f *fm) enter() {
	if f.active == pLeft {
		if f.leftIndex < 0 || len(f.leftItems) == 0 { return }
		if f.leftPath != "/" && f.leftIndex == 0 { f.up(); return }
		idx := f.leftIndex
		if f.leftPath != "/" { idx-- }
		if idx >= 0 && idx < len(f.leftItems) && f.leftItems[idx].isDir {
			f.leftPath = f.leftItems[idx].path
			f.leftIndex = 0
			_ = f.refresh(pLeft); f.drawHeader()
		}
		return
	}
	if f.rightIndex < 0 || len(f.rightItems) == 0 { return }
	if !f.isRoot(f.rightPath) && f.rightIndex == 0 { f.up(); return }
	idx := f.rightIndex
	if !f.isRoot(f.rightPath) { idx-- }
	if idx >= 0 && idx < len(f.rightItems) && f.rightItems[idx].isDir {
		f.rightPath = f.rightItems[idx].path
		f.rightIndex = 0
		_ = f.refresh(pRight); f.drawHeader()
	}
}

func (f *fm) up() {
	if f.active == pLeft {
		if f.leftPath == "/" { return }
		f.leftPath = filepath.ToSlash(filepath.Dir(f.leftPath))
		if f.leftPath == "." { f.leftPath = "/" }
		f.leftIndex = 0
		_ = f.refresh(pLeft); f.drawHeader()
		return
	}
	if f.isRoot(f.rightPath) { return }
	f.rightPath = filepath.Dir(f.rightPath)
	f.rightIndex = 0
	_ = f.refresh(pRight); f.drawHeader()
}

func (f *fm) view() error {
	if f.active == pLeft {
		if f.leftIndex < 0 || len(f.leftItems) == 0 { return nil }
		idx := f.leftIndex; if f.leftPath != "/" { idx-- }
		if idx < 0 || idx >= len(f.leftItems) || f.leftItems[idx].isDir { return nil }
		e := f.st.FS.Snapshot()[f.leftItems[idx].path]
		if e == nil || (e.Mode&memfs.ModeDir) != 0 { return nil }
		f.viewBytes(e.Data, f.leftItems[idx].name); return nil
	}
	if f.rightIndex < 0 || len(f.rightItems) == 0 { return nil }
	idx := f.rightIndex; if !f.isRoot(f.rightPath) { idx-- }
	if idx < 0 || idx >= len(f.rightItems) || f.rightItems[idx].isDir { return nil }
	b, err := os.ReadFile(f.rightItems[idx].path); if err != nil { return err }
	f.viewBytes(b, f.rightItems[idx].name); return nil
}

func (f *fm) edit() error {
	var tmpPath string
	if f.active == pLeft {
		if f.leftIndex < 0 || len(f.leftItems) == 0 { return nil }
		idx := f.leftIndex; if f.leftPath != "/" { idx-- }
		if idx < 0 || idx >= len(f.leftItems) || f.leftItems[idx].isDir { return nil }
		e := f.st.FS.Snapshot()[f.leftItems[idx].path]
		if e == nil || (e.Mode&memfs.ModeDir) != 0 { return nil }
		tmp := filepath.Join(os.TempDir(), "goimagetool-edit-"+filepath.Base(f.leftItems[idx].name))
		if err := os.WriteFile(tmp, e.Data, 0o600); err != nil { return err }
		tmpPath = tmp
		defer os.Remove(tmp)
	} else {
		if f.rightIndex < 0 || len(f.rightItems) == 0 { return nil }
		idx := f.rightIndex; if !f.isRoot(f.rightPath) { idx-- }
		if idx < 0 || idx >= len(f.rightItems) || f.rightItems[idx].isDir { return nil }
		tmpPath = f.rightItems[idx].path
	}
	editor := os.Getenv("EDITOR")
	if editor == "" { editor = os.Getenv("VISUAL") }
	if editor == "" {
		if runtime.GOOS == "windows" { editor = "notepad" } else
		if _, err := exec.LookPath("nano"); err == nil { editor = "nano" } else { editor = "vi" }
	}
	cmd := exec.Command(editor, tmpPath)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	var runErr error
	f.app.Suspend(func() { runErr = cmd.Run() })
	if runErr != nil { return runErr }
	if f.active == pLeft {
		b, err := os.ReadFile(tmpPath); if err != nil { return err }
		idx := f.leftIndex; if f.leftPath != "/" { idx-- }
		e := f.st.FS.Snapshot()[f.leftItems[idx].path]
		f.st.FS.PutFile(f.leftItems[idx].path, b, e.Mode&0o7777, e.UID, e.GID, time.Now())
		_ = f.refresh(pLeft)
	}
	return nil
}

func (f *fm) copy() error {
	if f.active == pLeft {
		if f.leftIndex < 0 || len(f.leftItems) == 0 { return nil }
		idx := f.leftIndex; if f.leftPath != "/" { idx-- }
		if idx < 0 || idx >= len(f.leftItems) { return nil }
		dst := filepath.Join(f.rightPath, f.leftItems[idx].name)
		if exist(dst) && !f.confirm("Overwrite host file?") { return nil }
		if err := f.copyFSToHost(f.leftItems[idx].path, dst); err != nil { return err }
		return f.refresh(pRight)
	}
	if f.rightIndex < 0 || len(f.rightItems) == 0 { return nil }
	idx := f.rightIndex; if !f.isRoot(f.rightPath) { idx-- }
	if idx < 0 || idx >= len(f.rightItems) { return nil }
	dst := f.join(f.leftPath, filepath.Base(f.rightItems[idx].path))
	if snap := f.st.FS.Snapshot(); snap[dst] != nil && !f.confirm("Overwrite image file?") { return nil }
	if err := f.copyHostToFS(f.rightItems[idx].path, dst); err != nil { return err }
	return f.refresh(pLeft)
}

func (f *fm) copyFSToHost(srcFS, dstHost string) error {
	e := f.st.FS.Snapshot()[srcFS]; if e == nil { return fmt.Errorf("not found: %s", srcFS) }
	if e.Mode&memfs.ModeDir != 0 {
		if err := os.MkdirAll(dstHost, 0o755); err != nil { return err }
		children, _ := f.listFS(srcFS)
		for _, c := range children {
			if err := f.copyFSToHost(c.path, filepath.Join(dstHost, c.name)); err != nil { return err }
		}
		return nil
	}
	if e.Mode&memfs.ModeLink != 0 {
		_ = os.RemoveAll(dstHost)
		return os.Symlink(e.Target, dstHost)
	}
	return os.WriteFile(dstHost, e.Data, 0o644)
}

func (f *fm) copyHostToFS(srcHost, dstFS string) error {
	fi, err := os.Lstat(srcHost); if err != nil { return err }
	if fi.IsDir() {
		f.st.FS.PutDir(dstFS, 0, 0, fi.ModTime())
		ents, err := os.ReadDir(srcHost); if err != nil { return err }
		for _, de := range ents {
			if err := f.copyHostToFS(filepath.Join(srcHost, de.Name()), f.join(dstFS, de.Name())); err != nil { return err }
		}
		return nil
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		tgt, err := os.Readlink(srcHost); if err != nil { return err }
		f.st.FS.PutSymlink(dstFS, tgt, 0, 0, fi.ModTime())
		return nil
	}
	b, err := os.ReadFile(srcHost); if err != nil { return err }
	f.st.FS.PutFile(dstFS, b, memfs.Mode(0o644), 0, 0, fi.ModTime())
	return nil
}

func (f *fm) mkdir() error {
	if f.active == pLeft {
		name := prompt(f, "mkdir (image FS): name"); if name == "" { return nil }
		dst := f.join(f.leftPath, filepath.Base(name))
		f.st.FS.PutDir(dst, 0, 0, time.Now())
		return f.refresh(pLeft)
	}
	name := prompt(f, "mkdir (host): name"); if name == "" { return nil }
	dst := filepath.Join(f.rightPath, filepath.Base(name))
	if err := os.MkdirAll(dst, 0o755); err != nil { return err }
	return f.refresh(pRight)
}

// helpers

func (f *fm) alert(text string) {
	m := tview.NewModal().SetText(text).AddButtons([]string{"OK"})
	f.pages.AddAndSwitchToPage("modal", m, true)
	m.SetDoneFunc(func(_ int, _ string) { f.pages.RemovePage("modal") })
}

func (f *fm) confirm(text string) bool {
	ok := false
	m := tview.NewModal().SetText(text).AddButtons([]string{"Yes", "No"})
	f.pages.AddAndSwitchToPage("confirm", m, true)
	done := make(chan struct{}, 1)
	m.SetDoneFunc(func(i int, _ string) { ok = (i == 0); done <- struct{}{} })
	<-done
	f.pages.RemovePage("confirm")
	return ok
}

func (f *fm) viewBytes(b []byte, title string) {
	const max = 256 * 1024
	if len(b) > max { b = b[:max] }
	txt := decodeOrHex(b)
	tv := tview.NewTextView()
	tv.SetText(txt)
	tv.SetScrollable(true)
	tv.SetDynamicColors(true)
	tv.SetBorder(true)
	tv.SetTitle(fmt.Sprintf(" View: %s ", title))
	wrap := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(nil, 1, 0, false).
		AddItem(tv, 0, 1, true).
		AddItem(nil, 1, 0, false)
	f.pages.AddAndSwitchToPage("view", wrap, true)
	tv.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyEsc || ev.Key() == tcell.KeyF10 {
			f.pages.RemovePage("view")
			if f.active == pLeft { f.app.SetFocus(f.left) } else { f.app.SetFocus(f.right) }
			return nil
		}
		return ev
	})
}

func decodeOrHex(b []byte) string {
	if utf8.Valid(b) { return string(b) }
	var out bytes.Buffer
	const cols = 16
	for i := 0; i < len(b); i += cols {
		end := i + cols; if end > len(b) { end = len(b) }
		chunk := b[i:end]
		fmt.Fprintf(&out, "%08x  ", i)
		for j := 0; j < cols; j++ {
			if i+j < len(b) { fmt.Fprintf(&out, "%02x ", b[i+j]) } else { out.WriteString("   ") }
		}
		out.WriteString(" ")
		for _, c := range chunk {
			if c >= 32 && c < 127 { out.WriteByte(c) } else { out.WriteByte('.') }
		}
		out.WriteByte('\n')
	}
	return out.String()
}

func prompt(fm *fm, title string) string {
	out := ""
	form := tview.NewForm().
		AddInputField("name", "", 40, nil, func(s string) { out = s }).
		AddButton("OK", nil).
		AddButton("Cancel", nil)
	form.SetBorder(true)
	form.SetTitle(" " + title + " ")
	form.SetTitleAlign(tview.AlignLeft)
	dlg := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(nil, 1, 0, false).
		AddItem(form, 7, 0, true).
		AddItem(nil, 1, 0, false)
	done := make(chan struct{}, 1)
	fm.pages.AddAndSwitchToPage("prompt", dlg, true)
	form.GetButton(0).SetSelectedFunc(func() { done <- struct{}{} })
	form.GetButton(1).SetSelectedFunc(func() { out = ""; done <- struct{}{} })
	<-done
	fm.pages.RemovePage("prompt")
	return out
}

func (f *fm) safe(p string) string {
	if p == "" { return "/" }
	return p
}

func (f *fm) isRoot(p string) bool {
	vol := filepath.VolumeName(p)
	rest := strings.TrimPrefix(p, vol)
	return rest == string(os.PathSeparator) || rest == ""
}

func (f *fm) join(a, b string) string {
	if a == "/" { return "/" + strings.TrimPrefix(filepath.ToSlash(b), "/") }
	return filepath.ToSlash(strings.TrimRight(a, "/") + "/" + strings.TrimPrefix(b, "/"))
}

func exist(p string) bool { _, err := os.Lstat(p); return err == nil }

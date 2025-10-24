package memfs

import (
	"bytes"
	"errors"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Mode uint32

const (
	ModeFile Mode = 0100000
	ModeDir  Mode = 0040000
)

type Entry struct {
	Name string
	Mode Mode
	UID, GID uint32
	MTime time.Time
	Data []byte // for files only
}

type FS struct {
	m map[string]*Entry
}

func New() *FS { return &FS{m: map[string]*Entry{"/": {Name: "/", Mode: ModeDir}}} }

func clean(p string) string {
	if p == "" { return "/" }
	p = filepath.ToSlash(p)
	if !strings.HasPrefix(p, "/") { p = "/" + p }
	p = path.Clean(p)
	if p == "." { return "/" }
	return p
}

func (fs *FS) MkdirAll(dir string, uid, gid uint32, mt time.Time) {
	d := clean(dir)
	parts := strings.Split(d, "/")[1:]
	cur := ""
	for _, p := range parts {
		cur += "/" + p
		if _, ok := fs.m[cur]; !ok {
			fs.m[cur] = &Entry{Name: cur, Mode: ModeDir, UID: uid, GID: gid, MTime: mt}
		}
	}
}

func (fs *FS) PutFile(p string, data []byte, mode Mode, uid, gid uint32, mt time.Time) {
	p = clean(p)
	fs.MkdirAll(path.Dir(p), uid, gid, mt)
	fs.m[p] = &Entry{Name: p, Mode: mode|ModeFile, UID: uid, GID: gid, MTime: mt, Data: append([]byte(nil), data...)}
}

func (fs *FS) PutDir(p string, uid, gid uint32, mt time.Time) {
	p = clean(p)
	fs.MkdirAll(p, uid, gid, mt)
	fs.m[p] = &Entry{Name: p, Mode: ModeDir, UID: uid, GID: gid, MTime: mt}
}

func (fs *FS) Get(p string) (*Entry, bool) {
	p = clean(p)
	e, ok := fs.m[p]
	return e, ok
}

func (fs *FS) List(dir string) []*Entry {
	dir = clean(dir)
	if dir != "/" && !strings.HasSuffix(dir, "/") { dir += "/" }
	var out []*Entry
	for k, v := range fs.m {
		if k == dir || strings.HasPrefix(k, dir) {
			rel := strings.TrimPrefix(k, dir)
			if rel == "" || strings.Contains(rel, "/") { continue }
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (fs *FS) Walk(fn func(*Entry) error) error {
	keys := make([]string, 0, len(fs.m))
	for k := range fs.m { keys = append(keys, k) }
	sort.Strings(keys)
	for _, k := range keys {
		if err := fn(fs.m[k]); err != nil { return err }
	}
	return nil
}

func (fs *FS) Remove(p string) error {
	p = clean(p)
	if p == "/" { return errors.New("cannot remove root") }
	for k := range fs.m {
		if k == p || strings.HasPrefix(k, p + "/") { delete(fs.m, k) }
	}
	return nil
}

func (fs *FS) ReadFile(p string) ([]byte, error) {
	p = clean(p)
	if e, ok := fs.m[p]; ok && e.Mode&ModeFile != 0 {
		return append([]byte(nil), e.Data...), nil
	}
	return nil, errors.New("not a file")
}

func (fs *FS) WriteFile(p string, data []byte) error {
	p = clean(p)
	if e, ok := fs.m[p]; ok && e.Mode&ModeFile != 0 {
		e.Data = append(e.Data[:0], data...)
		return nil
	}
	return errors.New("not a file")
}

func (fs *FS) Snapshot() map[string]*Entry {
	out := make(map[string]*Entry, len(fs.m))
	for k, v := range fs.m {
		cpy := *v
		cpy.Data = append([]byte(nil), v.Data...)
		out[k] = &cpy
	}
	return out
}

func (fs *FS) HasFiles() bool {
	for _, v := range fs.m {
		if v.Mode & ModeFile != 0 && len(v.Data) > 0 { return true }
	}
	return false
}

func (fs *FS) CompareBytes(p string, b []byte) bool {
	e, ok := fs.m[clean(p)]
	if !ok || e.Mode&ModeFile == 0 { return false }
	return bytes.Equal(e.Data, b)
}

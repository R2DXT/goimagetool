package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"goimagetool/internal/fs/memfs"
	"goimagetool/internal/image/uboot/fit"
)

type sessionEntry struct {
	Name      string
	Mode      uint32
	UID, GID  uint32
	MTimeUnix int64
	Data      []byte
	Target    string
	RdevMajor uint32
	RdevMinor uint32
}

type Session struct {
	Kind    ImageKind
	FS      []sessionEntry
	MetaFIT *fit.FIT
	Raw     []byte
}

func (s *State) ToSession() *Session {
	ss := s.FS.Snapshot()
	entries := make([]sessionEntry, 0, len(ss))
	for _, e := range ss {
		entries = append(entries, sessionEntry{
			Name:      e.Name,
			Mode:      uint32(e.Mode),
			UID:       e.UID,
			GID:       e.GID,
			MTimeUnix: e.MTime.Unix(),
			Data:      append([]byte(nil), e.Data...),
			Target:    e.Target,
			RdevMajor: e.RdevMajor,
			RdevMinor: e.RdevMinor,
		})
	}
	var mf *fit.FIT
	if m, _ := s.Meta.(*FitMeta); m != nil {
		mf = m.F
	}
	return &Session{Kind: s.Kind, FS: entries, MetaFIT: mf, Raw: append([]byte(nil), s.Raw...)}
}

func (s *State) FromSession(sess *Session) {
	s.Kind = sess.Kind
	fs := memfs.New()
	for _, e := range sess.FS {
		mt := time.Unix(e.MTimeUnix, 0)
		mode := memfs.Mode(e.Mode)
		switch {
		case mode&memfs.ModeDir != 0:
			fs.PutDirMode(e.Name, mode, e.UID, e.GID, mt)
		case mode&memfs.ModeLink != 0:
			fs.PutSymlink(e.Name, e.Target, e.UID, e.GID, mt)
		case mode&memfs.ModeChar != 0 || mode&memfs.ModeBlock != 0 || mode&memfs.ModeFIFO != 0:
			typ := mode &^ 0o7777 | (mode & 0o7777) // keep bits
			fs.PutNode(e.Name, typ, e.Mode, e.UID, e.GID, e.RdevMajor, e.RdevMinor, mt)
		default:
			fs.PutFile(e.Name, e.Data, mode, e.UID, e.GID, mt)
		}
	}
	s.FS = fs
	if sess.MetaFIT != nil {
		s.Meta = &FitMeta{F: sess.MetaFIT}
	}
	s.Raw = append([]byte(nil), sess.Raw...)
}

func (s *State) SaveSession(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(s.ToSession())
}

func (s *State) LoadSession(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var sess Session
	if err := json.Unmarshal(b, &sess); err != nil {
		return err
	}
	s.FromSession(&sess)
	return nil
}

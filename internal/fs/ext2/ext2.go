package ext2

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"goimagetool/internal/fs/memfs"
)

// Load reads an EXT2/3/4 image from r by writing it to a temp file and
// using debugfs to rdump the tree into a staging directory, then
// reconstructs a MemFS from that staging content.
// Works on Unix-like systems with e2fsprogs installed.
// On other systems returns an error if debugfs is not available.
func Load(r io.Reader) (*memfs.FS, error) {
	// write to tmp image
	tmpParent, err := os.MkdirTemp("", "goimagetool-ext2-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpParent)

	img := filepath.Join(tmpParent, "img.ext2")
	f, err := os.Create(img)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		return nil, err
	}
	_ = f.Close()

	if runtime.GOOS == "windows" {
		return nil, fmt.Errorf("ext2 load requires debugfs (unsupported on windows)")
	}
	if _, err := exec.LookPath("debugfs"); err != nil {
		return nil, fmt.Errorf("debugfs not found: %w", err)
	}

	staging := filepath.Join(tmpParent, "rdump")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return nil, err
	}

	// rdump all files
	cmd := exec.Command("debugfs", "-R", fmt.Sprintf("rdump / %s", staging), img)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("debugfs rdump: %v: %s", err, string(out))
	}

	// Build MemFS
	fs := memfs.New()
	// Ensure root exists
	fs.PutDir("/", 0, 0, time.Unix(0, 0))

	// Walk the staging directory
	err = filepath.Walk(staging, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, _ := filepath.Rel(staging, p)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		dst := "/" + rel
		switch {
		case info.Mode().IsDir():
			fs.PutDir(dst, uidOf(info), gidOf(info), info.ModTime())
		case (info.Mode() & os.ModeSymlink) != 0:
			tgt, err := os.Readlink(p)
			if err != nil {
				return err
			}
			fs.PutSymlink(dst, tgt, uidOf(info), gidOf(info), info.ModTime())
		case (info.Mode() & os.ModeNamedPipe) != 0:
			fs.PutNode(dst, memfs.ModeFIFO, uint32(info.Mode().Perm()), uidOf(info), gidOf(info), 0, 0, info.ModTime())
		case (info.Mode() & os.ModeDevice) != 0:
			typ := memfs.ModeChar
			if (info.Mode() & os.ModeCharDevice) == 0 {
				typ = memfs.ModeBlock
			}
			maj, min := rdevOf(info)
			fs.PutNode(dst, typ, uint32(info.Mode().Perm()), uidOf(info), gidOf(info), maj, min, info.ModTime())
		default:
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			mode := memfs.ModeFile | memfs.Mode(uint32(info.Mode().Perm()))
			fs.PutFile(dst, b, mode, uidOf(info), gidOf(info), info.ModTime())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return fs, nil
}

// Store creates an EXT2 image from MemFS by materializing it into a
// staging dir and invoking mke2fs -d. The result is written to w.
// Requires mke2fs on Unix-like systems.
func Store(w io.Writer, m *memfs.FS, blockSize int) error {
	if m == nil {
		return fmt.Errorf("nil memfs")
	}
	if blockSize == 0 {
		blockSize = 1024
	}
	if runtime.GOOS == "windows" {
		return fmt.Errorf("ext2 store requires mke2fs (unsupported on windows)")
	}
	mke2fsPath, err := exec.LookPath("mke2fs")
	if err != nil {
		return fmt.Errorf("mke2fs not found: %w", err)
	}

	tmpParent, err := os.MkdirTemp("", "goimagetool-ext2-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpParent)

	staging := filepath.Join(tmpParent, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return err
	}
	if err := materializeStaging(staging, m); err != nil {
		return err
	}

	sizeBytes, err := estimateImageSize(staging, blockSize)
	if err != nil {
		return err
	}
	if sizeBytes < 16*1024*1024 {
		sizeBytes = 16 * 1024 * 1024
	}
	blocks := sizeBytes / blockSize
	if blocks <= 0 {
		blocks = 1
	}

	img := filepath.Join(tmpParent, "fs.img")

	// Build args
	args := []string{
		"-t", "ext2",
		"-q",
		"-d", staging,
		"-b", fmt.Sprintf("%d", blockSize),
		"-I", "128",
		img,
		fmt.Sprintf("%d", blocks),
	}
	cmd := exec.Command(mke2fsPath, args...)
	// Silence stdin
	cmd.Stdin = bytes.NewReader(nil)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mke2fs: %v: %s", err, string(out))
	}

	// read image and stream to w
	f, err := os.Open(img)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

func materializeStaging(base string, m *memfs.FS) error {
	snap := m.Snapshot()
	paths := make([]string, 0, len(snap))
	for p := range snap {
		if p == "" {
			continue
		}
		paths = append(paths, p)
	}
	sort.Slice(paths, func(i, j int) bool {
		// parents first by path components count
		pi := strings.Count(paths[i], "/")
		pj := strings.Count(paths[j], "/")
		if pi != pj {
			return pi < pj
		}
		return paths[i] < paths[j]
	})

	for _, p := range paths {
		if p == "/" {
			continue
		}
		e := snap[p]
		dst := filepath.Join(base, strings.TrimPrefix(p, "/"))
		switch {
		case e.Mode&memfs.ModeDir != 0:
			if err := os.MkdirAll(dst, os.FileMode(uint32(e.Mode)&0o7777)); err != nil {
				return err
			}
			_ = os.Chtimes(dst, safeTime(e.MTime), safeTime(e.MTime))
			_ = chown(dst, int(e.UID), int(e.GID))

		case e.Mode&memfs.ModeLink != 0:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			_ = os.Remove(dst)
			if err := os.Symlink(e.Target, dst); err != nil {
				return err
			}
			_ = lchown(dst, int(e.UID), int(e.GID))

		case (e.Mode & memfs.ModeFIFO) != 0:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := mkfifo(dst, uint32(e.Mode)&0o7777); err != nil {
				return err
			}
			_ = os.Chtimes(dst, safeTime(e.MTime), safeTime(e.MTime))
			_ = lchown(dst, int(e.UID), int(e.GID))

		case (e.Mode & memfs.ModeChar) != 0:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := mknod(dst, e, e.RdevMajor, e.RdevMinor); err != nil {
				return err
			}
			_ = os.Chtimes(dst, safeTime(e.MTime), safeTime(e.MTime))
			_ = lchown(dst, int(e.UID), int(e.GID))

		case (e.Mode & memfs.ModeBlock) != 0:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := mknod(dst, e, e.RdevMajor, e.RdevMinor); err != nil {
				return err
			}
			_ = os.Chtimes(dst, safeTime(e.MTime), safeTime(e.MTime))
			_ = lchown(dst, int(e.UID), int(e.GID))

		default:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(uint32(e.Mode) & 0o7777)
			if mode == 0 {
				mode = 0o644
			}
			if err := os.WriteFile(dst, e.Data, mode); err != nil {
				return err
			}
			_ = os.Chtimes(dst, safeTime(e.MTime), safeTime(e.MTime))
			_ = chown(dst, int(e.UID), int(e.GID))
		}
	}
	return nil
}

func estimateImageSize(dir string, blockSize int) (int, error) {
	var total int64
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		// approximate: file size rounded up to block
		if info.Mode().IsRegular() {
			sz := info.Size()
			total += sz
		}
		// add small overhead per inode/dir
		total += 512
		return nil
	})
	if err != nil {
		return 0, err
	}
	// add 16% overhead + 4 MiB slack and align to block size
	total = total + total/6 + 4*1024*1024
	if rem := total % int64(blockSize); rem != 0 {
		total += int64(blockSize) - rem
	}
	return int(total), nil
}

func safeTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Unix(0, 0)
	}
	return t
}

// The following helpers are declared in platform shims (own_unix.go / own_other.go):
//   func chown(path string, uid, gid int) error
//   func lchown(path string, uid, gid int) error
//   func mkfifo(path string, perm uint32) error
//   func mknod(path string, e *memfs.Entry, maj, min uint32) error
//   func uidOf(fi os.FileInfo) uint32
//   func gidOf(fi os.FileInfo) uint32
//   func rdevOf(fi os.FileInfo) (uint32, uint32)

package ext2

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"goimagetool/internal/fs/memfs"
)

var (
	ErrMke2fsNotFound = errors.New("ext2: mke2fs not found; install e2fsprogs")
)

func Load(r io.Reader) (*memfs.FS, error) {
	return nil, fmt.Errorf("ext2 load: not implemented here (unchanged)")
}

// Store via mke2fs -d; fsck-clean.
func Store(w io.Writer, m *memfs.FS, blockSize int) error {
	if blockSize != 1024 && blockSize != 2048 && blockSize != 4096 && blockSize != 0 {
		return fmt.Errorf("ext2: unsupported block size %d (use 1024/2048/4096)", blockSize)
	}
	if blockSize == 0 {
		blockSize = 4096
	}
	mke2fsPath, _ := exec.LookPath("mke2fs")
	if mke2fsPath == "" {
		return ErrMke2fsNotFound
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

	image := filepath.Join(tmpParent, "out.ext2")
	f, err := os.Create(image)
	if err != nil {
		return err
	}
	if err := f.Truncate(int64(sizeBytes)); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	args := []string{
		"-t", "ext2",
		"-F",
		"-b", fmt.Sprint(blockSize),
		"-I", "128",
		"-E", "lazy_itable_init=0",
		"-d", staging,
		image,
	}
	cmd := exec.Command(mke2fsPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if runtime.GOOS == "darwin" {
			return fmt.Errorf("%w (install: brew install e2fsprogs)", err)
		}
		return err
	}

	in, err := os.Open(image)
	if err != nil {
		return err
	}
	defer in.Close()
	_, err = io.Copy(w, in)
	return err
}

func materializeStaging(root string, m *memfs.FS) error {
	snap := m.Snapshot()
	if rootEnt, ok := snap["/"]; ok {
		_ = os.Chmod(root, os.FileMode(uint32(rootEnt.Mode)&0o7777))
		_ = os.Chtimes(root, safeTime(rootEnt.MTime), safeTime(rootEnt.MTime))
		_ = chown(root, int(rootEnt.UID), int(rootEnt.GID))
	}

	return m.Walk(func(e *memfs.Entry) error {
		if e.Name == "/" {
			return nil
		}
		dst := filepath.Join(root, strings.TrimPrefix(e.Name, "/"))
		switch {
		case e.Mode&memfs.ModeDir != 0:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			_ = os.Chmod(dst, os.FileMode(uint32(e.Mode)&0o7777))
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

		case (e.Mode&(memfs.ModeChar|memfs.ModeBlock|memfs.ModeFIFO)) != 0:
			return nil

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
		return nil
	})
}

func estimateImageSize(dir string, blockSize int) (int, error) {
	var total int64 = 0
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		} else if info.IsDir() {
			total += int64(blockSize)
		} else if (info.Mode() & os.ModeSymlink) != 0 {
			total += 128
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
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

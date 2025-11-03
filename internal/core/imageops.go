package core

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

var (
	ErrNegativeSize  = errors.New("size must be >= 0")
	ErrShrinkData    = errors.New("cannot shrink: trailing area is not zero-filled")
	ErrBadSizeSyntax = errors.New("bad size syntax")
	ErrAlignNonPos   = errors.New("align must be > 0")
)

func ParseSize(s string) (int64, error) {
	if s == "" {
		return 0, ErrBadSizeSyntax
	}
	mul := int64(1)
	ss := strings.ToUpper(strings.TrimSpace(s))
	switch {
	case strings.HasSuffix(ss, "K"):
		mul = 1024
		ss = strings.TrimSuffix(ss, "K")
	case strings.HasSuffix(ss, "M"):
		mul = 1024 * 1024
		ss = strings.TrimSuffix(ss, "M")
	case strings.HasSuffix(ss, "G"):
		mul = 1024 * 1024 * 1024
		ss = strings.TrimSuffix(ss, "G")
	}
	var v int64
	_, err := fmt.Sscanf(ss, "%d", &v)
	if err != nil || v < 0 {
		return 0, ErrBadSizeSyntax
	}
	return v * mul, nil
}

func FileSize(path string) (int64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

func ResizeFileTo(path string, newSize int64) error {
	if newSize < 0 {
		return ErrNegativeSize
	}
	cur, err := FileSize(path)
	if err != nil {
		return err
	}
	switch {
	case newSize == cur:
		return nil
	case newSize > cur:
		return growFile(path, newSize-cur)
	default:
		return shrinkFile(path, newSize)
	}
}

func ResizeFileDelta(path string, delta int64) error {
	if delta == 0 {
		return nil
	}
	if delta > 0 {
		return growFile(path, delta)
	}
	cur, err := FileSize(path)
	if err != nil {
		return err
	}
	newSize := cur + delta
	if newSize < 0 {
		return ErrNegativeSize
	}
	return shrinkFile(path, newSize)
}

func PadAlign(path string, align int64) error {
	if align <= 0 {
		return ErrAlignNonPos
	}
	cur, err := FileSize(path)
	if err != nil {
		return err
	}
	rem := cur % align
	if rem == 0 {
		return nil
	}
	return growFile(path, align-rem)
}

func growFile(path string, add int64) error {
	if add <= 0 {
		return nil
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	const chunk = 1 << 20
	zero := make([]byte, chunk)
	for add > 0 {
		n := int64(chunk)
		if n > add {
			n = add
		}
		if _, err := f.Write(zero[:n]); err != nil {
			return err
		}
		add -= n
	}
	return nil
}

func shrinkFile(path string, newSize int64) error {
	cur, err := FileSize(path)
	if err != nil {
		return err
	}
	if newSize >= cur {
		return nil
	}
	lenTail := cur - newSize
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(newSize, io.SeekStart); err != nil {
		return err
	}
	const chunk = 1 << 20
	buf := make([]byte, chunk)
	for rem := lenTail; rem > 0; {
		n := int64(chunk)
		if n > rem {
			n = rem
		}
		read, rerr := f.Read(buf[:n])
		if read > 0 {
			for i := 0; i < read; i++ {
				if buf[i] != 0 {
					return ErrShrinkData
				}
			}
			rem -= int64(read)
		}
		if rerr != nil {
			if rerr == io.EOF && rem == 0 {
				break
			}
			return rerr
		}
	}
	return os.Truncate(path, newSize)
}

package compress

// Pluggable compression codecs + auto-detect.
// RW: gzip, zstd, lz4, lzma, bzip2
// R-only: xz (lzo TODO)
// Names: none|auto|gzip|gz|zstd|zst|lz4|lzma|bzip2|bz2|xz|lzo

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/ioutil"

	"github.com/dsnet/compress/bzip2"
	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"github.com/ulikunitz/xz"
	"github.com/ulikunitz/xz/lzma"
)

var ErrUnsupported = errors.New("compression: unsupported operation")

// ---------- name helpers ----------

func normalize(name string) string {
	switch name {
	case "", "auto":
		return "auto"
	case "none", "raw":
		return "none"
	case "gz":
		return "gzip"
	case "zst":
		return "zstd"
	case "bz2":
		return "bzip2"
	default:
		return name
	}
}

// ---------- magic detection (best-effort) ----------

func Detect(data []byte) string {
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		return "gzip"
	}
	if len(data) >= 4 && data[0] == 0x28 && data[1] == 0xB5 && data[2] == 0x2F && data[3] == 0xFD {
		return "zstd"
	}
	if len(data) >= 4 && data[0] == 0x04 && data[1] == 0x22 && data[2] == 0x4D && data[3] == 0x18 {
		return "lz4"
	}
	if len(data) >= 6 && data[0] == 0xFD && data[1] == '7' && data[2] == 'z' && data[3] == 'X' && data[4] == 'Z' && data[5] == 0x00 {
		return "xz"
	}
	if len(data) >= 3 && data[0] == 'B' && data[1] == 'Z' && data[2] == 'h' {
		return "bzip2"
	}
	// lzma "alone" и lzo raw без надёжной сигнатуры
	return "none"
}

// ---------- high-level API (buffer-based) ----------

func DecompressAuto(in []byte) ([]byte, string, error) {
	kind := Detect(in)
	if kind == "none" {
		return in, "none", nil
	}
	out, err := Decompress(in, kind)
	return out, kind, err
}

func Decompress(in []byte, name string) ([]byte, error) {
	switch normalize(name) {
	case "none":
		return in, nil
	case "gzip":
		gr, err := gzip.NewReader(bytes.NewReader(in))
		if err != nil {
			return nil, err
		}
		defer gr.Close()
		return io.ReadAll(gr)
	case "zstd":
		d, err := zstd.NewReader(bytes.NewReader(in))
		if err != nil {
			return nil, err
		}
		defer d.Close()
		return io.ReadAll(d)
	case "lz4":
		lr := lz4.NewReader(bytes.NewReader(in))
		return io.ReadAll(lr)
	case "xz":
		xr, err := xz.NewReader(bytes.NewReader(in))
		if err != nil {
			return nil, err
		}
		return io.ReadAll(xr)
	case "lzma":
		lr, err := lzma.NewReader(bytes.NewReader(in))
		if err != nil {
			return nil, err
		}
		return io.ReadAll(lr)
	case "bzip2":
		br, err := bzip2.NewReader(bytes.NewReader(in), &bzip2.ReaderConfig{})
		if err != nil {
			return nil, err
		}
		defer br.Close()
		return io.ReadAll(br)
	case "lzo":
		// TODO: lzo raw reader (R-only)
		return nil, ErrUnsupported
	case "auto":
		out, _, err := DecompressAuto(in)
		return out, err
	default:
		return nil, ErrUnsupported
	}
}

func Compress(in []byte, name string) ([]byte, error) {
	switch normalize(name) {
	case "none", "auto":
		return in, nil
	case "gzip":
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		if _, err := gw.Write(in); err != nil {
			return nil, err
		}
		if err := gw.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case "zstd":
		var buf bytes.Buffer
		zw, err := zstd.NewWriter(&buf)
		if err != nil {
			return nil, err
		}
		if _, err := zw.Write(in); err != nil {
			return nil, err
		}
		if err := zw.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case "lz4":
		var buf bytes.Buffer
		lw := lz4.NewWriter(&buf)
		if _, err := lw.Write(in); err != nil {
			return nil, err
		}
		if err := lw.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case "lzma":
		var buf bytes.Buffer
		lw, err := lzma.NewWriter(&buf)
		if err != nil {
			return nil, err
		}
		if _, err := lw.Write(in); err != nil {
			return nil, err
		}
		if err := lw.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case "bzip2":
		var buf bytes.Buffer
		bw, err := bzip2.NewWriter(&buf, &bzip2.WriterConfig{})
		if err != nil {
			return nil, err
		}
		if _, err := bw.Write(in); err != nil {
			return nil, err
		}
		if err := bw.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case "xz":
		// xz write — TODO
		return nil, ErrUnsupported
	case "lzo":
		return nil, ErrUnsupported
	default:
		return nil, ErrUnsupported
	}
}

// Optional stream helpers (for future use)

func Reader(name string, r io.Reader) (io.ReadCloser, error) {
	in, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	out, err := Decompress(in, name)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(out)), nil
}

func Writer(name string, w io.Writer) (io.WriteCloser, error) {
	// Buffering writer to match Compress() signature on Close
	var buf bytes.Buffer
	// Встроенное поле *bytes.Buffer инициализируем позиционно.
	return nopWriteCloser{&buf, w, normalize(name)}, nil
}

type nopWriteCloser struct {
	*bytes.Buffer
	sink io.Writer
	name string
}

func (n nopWriteCloser) Close() error {
	out := n.Buffer.Bytes()
	var err error
	out, err = Compress(out, n.name)
	if err != nil {
		return err
	}
	_, err = io.Copy(n.sink, bytes.NewReader(out))
	return err
}

// Convenience: ReadAll wraps io.ReadAll with close
func ReadAllAndClose(r io.ReadCloser) ([]byte, error) {
	defer r.Close()
	return ioutil.ReadAll(r)
}

package compress

import (
	"compress/gzip"
	"io"
)

func GzipDecompress(dst io.Writer, src io.Reader) error {
	gr, err := gzip.NewReader(src)
	if err != nil {
		return err
	}
	defer gr.Close()
	_, err = io.Copy(dst, gr)
	return err
}

func GzipCompress(dst io.Writer, src io.Reader) error {
	gw, err := gzip.NewWriterLevel(dst, gzip.DefaultCompression)
	if err != nil {
		return err
	}
	if _, err := io.Copy(gw, src); err != nil {
		_ = gw.Close()
		return err
	}
	return gw.Close()
}

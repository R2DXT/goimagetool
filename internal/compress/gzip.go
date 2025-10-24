package compress

import (
	"compress/gzip"
	"io"
)

func GzipDecompress(dst io.Writer, src io.Reader) error {
	r, err := gzip.NewReader(src)
	if err != nil { return err }
	defer r.Close()
	_, err = io.Copy(dst, r)
	return err
}

func GzipCompress(dst io.Writer, src io.Reader) error {
	w := gzip.NewWriter(dst)
	defer w.Close()
	_, err := io.Copy(w, src)
	return err
}

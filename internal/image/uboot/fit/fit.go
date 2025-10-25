package fit

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io"
)

type Image struct {
	Name string
	Data []byte
	Algo string // e.g., "sha1"
	Hash string // hex
}

type FIT struct {
	Images map[string]*Image
	DefaultConfig string
}

func Read(r io.Reader) (*FIT, error) {
	all, err := io.ReadAll(r)
	if err != nil { return nil, err }
	root, err := parseFDT(all)
	if err != nil { return nil, err }
	f := &FIT{Images: map[string]*Image{}}
	var imagesNode, confNode *Node
	for _, ch := range root.Children {
		if ch.Name == "images" { imagesNode = ch }
		if ch.Name == "configurations" { confNode = ch }
	}
	if imagesNode != nil {
		for _, img := range imagesNode.Children {
			data := img.Props["data"]
			algo := string(img.Props["algo"])
			hash := string(img.Props["hash"])
			name := img.Name
			f.Images[name] = &Image{Name: name, Data: append([]byte(nil), data...), Algo: algo, Hash: hash}
		}
	}
	if confNode != nil {
		if d, ok := confNode.Props["default"]; ok {
			f.DefaultConfig = string(d)
		}
	}
	return f, nil
}

func Write(w io.Writer, f *FIT) error {
	root := &Node{Name: "", Props: map[string][]byte{}}
	images := &Node{Name: "images", Props: map[string][]byte{}}
	root.Children = append(root.Children, images)
	configs := &Node{Name: "configurations", Props: map[string][]byte{}}
	if f.DefaultConfig != "" { configs.Props["default"] = []byte(f.DefaultConfig) }
	root.Children = append(root.Children, configs)
	for name, im := range f.Images {
		n := &Node{Name: name, Props: map[string][]byte{}}
		n.Props["data"] = append([]byte(nil), im.Data...)
		if im.Algo == "" { im.Algo = "sha1" }
		n.Props["algo"] = []byte(im.Algo)
		if im.Hash == "" {
			s := sha1.Sum(im.Data)
			im.Hash = hex.EncodeToString(s[:])
		}
		n.Props["hash"] = []byte(im.Hash)
		images.Children = append(images.Children, n)
	}
	if f.DefaultConfig == "" && len(f.Images) > 0 {
		for k := range f.Images { f.DefaultConfig = k; break }
		configs.Props["default"] = []byte(f.DefaultConfig)
	}
	b, err := buildFDT(root)
	if err != nil { return err }
	_, err = io.Copy(w, bytes.NewReader(b))
	return err
}

func (f *FIT) List() []string {
	out := make([]string, 0, len(f.Images))
	for k := range f.Images { out = append(out, k) }
	return out
}

func (f *FIT) Get(name string) (*Image, error) {
	if v, ok := f.Images[name]; ok { return v, nil }
	return nil, errors.New("image not found")
}

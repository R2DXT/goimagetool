package fit

import (
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"sort"
	"strings"
)

type Image struct {
	Name     string
	Type     string // kernel|fdt|ramdisk|custom
	Data     []byte
	HashAlgo string // sha1|sha256|sha512
	Digest   []byte
}

type Fit struct {
	imgs    map[string]*Image
	Default string
}

// Старое имя, которого ждёт core.
type FIT = Fit

func New() *Fit { return &Fit{imgs: make(map[string]*Image)} }

func normAlgo(a string) string {
	switch strings.ToLower(a) {
	case "sha256", "sha-256":
		return "sha256"
	case "sha512", "sha-512":
		return "sha512"
	default:
		return "sha1"
	}
}

func hashData(algo string, b []byte) []byte {
	switch algo {
	case "sha256":
		h := sha256.Sum256(b)
		return h[:]
	case "sha512":
		h := sha512.Sum512(b)
		return h[:]
	default:
		h := sha1.Sum(b)
		return h[:]
	}
}

func (f *Fit) Add(name string, data []byte, algo string) { _ = f.AddTyped(name, data, algo, "") }

func (f *Fit) AddTyped(name string, data []byte, algo, typ string) error {
	if name == "" {
		return errors.New("fit: empty name")
	}
	if f.imgs == nil {
		f.imgs = make(map[string]*Image)
	}
	a := normAlgo(algo)
	img := &Image{
		Name:     name,
		Type:     strings.ToLower(typ),
		Data:     append([]byte(nil), data...),
		HashAlgo: a,
		Digest:   hashData(a, data),
	}
	f.imgs[name] = img
	if f.Default == "" {
		f.Default = name
	}
	return nil
}

func (f *Fit) Remove(name string) {
	if f == nil || f.imgs == nil {
		return
	}
	delete(f.imgs, name)
	if f.Default == name {
		f.Default = ""
	}
}

func (f *Fit) SetDefault(name string) {
	if f == nil || f.imgs == nil {
		return
	}
	if _, ok := f.imgs[name]; ok {
		f.Default = name
	}
}

func (f *Fit) List() []string {
	if f == nil || f.imgs == nil {
		return nil
	}
	out := make([]string, 0, len(f.imgs))
	for k := range f.imgs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (f *Fit) Get(name string) (*Image, error) {
	if f == nil || f.imgs == nil {
		return nil, errors.New("fit: empty")
	}
	img, ok := f.imgs[name]
	if !ok {
		return nil, errors.New("fit: not found")
	}
	return img, nil
}

func (f *Fit) Verify() error {
	if f == nil || f.imgs == nil {
		return errors.New("fit: empty")
	}
	for _, img := range f.imgs {
		if img == nil {
			continue
		}
		got := hashData(img.HashAlgo, img.Data)
		if len(img.Digest) == 0 {
			img.Digest = got
			continue
		}
		if !equalBytes(got, img.Digest) {
			return errors.New("fit: verify failed: " + img.Name)
		}
	}
	return nil
}

// VerifyOne — то же самое, но для одного образа; если digest пуст — заполняем им.
func (f *Fit) VerifyOne(name string) (bool, error) {
	img, err := f.Get(name)
	if err != nil {
		return false, err
	}
	got := hashData(img.HashAlgo, img.Data)
	if len(img.Digest) == 0 {
		img.Digest = got
		return true, nil
	}
	return equalBytes(got, img.Digest), nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

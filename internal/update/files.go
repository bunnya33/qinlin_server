package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var tagPattern = regexp.MustCompile(`^v(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)
var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func ValidTag(tag string) bool { return tagPattern.MatchString(tag) }
func Newer(a, b string) bool {
	a = "v" + strings.TrimPrefix(a, "v")
	b = "v" + strings.TrimPrefix(b, "v")
	if !ValidTag(a) || !ValidTag(b) {
		return false
	}
	x, y := strings.Split(a[1:], "."), strings.Split(b[1:], ".")
	for i := range x {
		l, _ := strconv.Atoi(x[i])
		r, _ := strconv.Atoi(y[i])
		if l != r {
			return l > r
		}
	}
	return false
}

type Candidate struct {
	Tag    string `json:"tag"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

func (c Candidate) Valid() bool {
	return ValidTag(c.Tag) && hashPattern.MatchString(c.SHA256) && c.Size > 0 && c.Size <= MaxBinarySize
}
func (c Candidate) Name() string           { return "candidate-" + c.Tag + ".bin" }
func (c Candidate) Path(dir string) string { return filepath.Join(dir, c.Name()) }

func ReadJSON(path string, value any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 64<<10))
	if err = dec.Decode(value); err != nil {
		return err
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return errors.New("invalid metadata")
	}
	return nil
}
func WriteJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".metadata-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(value)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return replaceFile(f.Name(), path)
}
func Verify(path string, c Candidate) error {
	if !c.Valid() {
		return errors.New("invalid candidate")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, MaxBinarySize+1))
	if err != nil {
		return err
	}
	if n != c.Size || hex.EncodeToString(h.Sum(nil)) != c.SHA256 {
		return errors.New("checksum mismatch")
	}
	return nil
}

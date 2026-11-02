package update

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const Repository = "bunnya33/qinlin_server"
const MaxBinarySize int64 = 128 << 20

var ErrNoRelease = errors.New("update_not_published")

type Asset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}
type Release struct {
	Tag        string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Notes      string  `json:"body"`
	Assets     []Asset `json:"assets"`
}

func (r Release) URL() string { return "https://github.com/" + Repository + "/releases/tag/" + r.Tag }
func (r Release) Asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

type Source struct{ Client *http.Client }

func NewSource() Source {
	return Source{Client: &http.Client{Timeout: 3 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !trustedURL(req.URL) {
			return errors.New("untrusted release redirect")
		}
		return nil
	}}}
}
func trustedURL(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	switch u.Hostname() {
	case "api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com", "github-releases.githubusercontent.com":
		return true
	}
	return false
}
func (s Source) get(ctx context.Context, address string) (*http.Response, error) {
	u, err := url.Parse(address)
	if err != nil || !trustedURL(u) {
		return nil, errors.New("untrusted release URL")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "qinlin-server-updater")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 404 {
		resp.Body.Close()
		return nil, ErrNoRelease
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("release HTTP status %d", resp.StatusCode)
	}
	return resp, nil
}
func (s Source) Latest(ctx context.Context) (Release, error) {
	var r Release
	resp, err := s.get(ctx, "https://api.github.com/repos/"+Repository+"/releases/latest")
	if err != nil {
		return r, err
	}
	defer resp.Body.Close()
	if err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r); err != nil {
		return r, err
	}
	if !ValidTag(r.Tag) || r.Draft || r.Prerelease {
		return r, errors.New("invalid stable release")
	}
	if len(r.Notes) > 4000 {
		r.Notes = r.Notes[:4000]
	}
	return r, nil
}
func (s Source) Download(ctx context.Context, r Release, name string) (io.ReadCloser, int64, error) {
	a, ok := r.Asset(name)
	if !ok {
		return nil, 0, errors.New("release asset missing")
	}
	limit := MaxBinarySize
	if name == "SHA256SUMS" {
		limit = 128 << 10
	}
	if a.Size <= 0 || a.Size > limit {
		return nil, 0, errors.New("invalid release asset size")
	}
	resp, err := s.get(ctx, "https://github.com/"+Repository+"/releases/download/"+r.Tag+"/"+name)
	if err != nil {
		return nil, 0, err
	}
	if resp.ContentLength > limit {
		resp.Body.Close()
		return nil, 0, errors.New("release asset too large")
	}
	return resp.Body, a.Size, nil
}
func (s Source) Checksum(ctx context.Context, r Release, name string) (string, error) {
	body, _, err := s.Download(ctx, r, "SHA256SUMS")
	if err != nil {
		return "", err
	}
	defer body.Close()
	scanner := bufio.NewScanner(io.LimitReader(body, 128<<10+1))
	found := ""
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			if found != "" || !hashPattern.MatchString(fields[0]) {
				return "", errors.New("invalid release checksum")
			}
			found = fields[0]
		}
	}
	if err = scanner.Err(); err != nil {
		return "", err
	}
	if found == "" {
		return "", errors.New("missing release checksum")
	}
	return found, nil
}

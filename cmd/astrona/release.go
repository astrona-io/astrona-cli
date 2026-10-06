package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"
)

const (
	releaseRepo = "astrona-io/astrona-cli"
	// maxReleaseBinary bounds a downloaded astrona binary.
	maxReleaseBinary = 256 << 20
)

var releaseClient = &http.Client{Timeout: 5 * time.Minute}

// releaseAssetName is this machine's binary in a release.
func releaseAssetName() string {
	return fmt.Sprintf("astrona-%s-%s", goruntime.GOOS, goruntime.GOARCH)
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	Draft   bool   `json:"draft"`
	Assets  []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Digest string `json:"digest"`
	} `json:"assets"`
}

func getJSON(url string, v any) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errNoRelease
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API: %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v)
}

var errNoRelease = errors.New("no such release")

// releaseAsset finds this machine's binary in release tag, with the
// SHA-256 digest GitHub records for it.
func releaseAsset(tag string) (url, sha string, err error) {
	var rel githubRelease
	if err := getJSON("https://api.github.com/repos/"+releaseRepo+"/releases/tags/"+tag, &rel); err != nil {
		if errors.Is(err, errNoRelease) {
			return "", "", fmt.Errorf("astrona %s doesn't exist — `astrona versions available` lists releases", tag)
		}
		return "", "", fmt.Errorf("look up release %s: %w", tag, err)
	}
	for _, a := range rel.Assets {
		if a.Name != releaseAssetName() {
			continue
		}
		sha, ok := strings.CutPrefix(a.Digest, "sha256:")
		if !ok || len(sha) != 64 {
			return "", "", fmt.Errorf("release %s has no SHA-256 digest for %s — refusing to install an unverified binary", tag, a.Name)
		}
		return a.URL, sha, nil
	}
	return "", "", fmt.Errorf("release %s has no binary for %s/%s", tag, goruntime.GOOS, goruntime.GOARCH)
}

// releaseTags lists published release tags, newest first.
func releaseTags() ([]string, error) {
	var rels []githubRelease
	if err := getJSON("https://api.github.com/repos/"+releaseRepo+"/releases?per_page=100", &rels); err != nil {
		return nil, err
	}
	var tags []string
	for _, r := range rels {
		if !r.Draft {
			tags = append(tags, r.TagName)
		}
	}
	return tags, nil
}

// downloadVerifiedRelease installs release tag's binary for this machine
// at dest: downloaded next to it, checked against GitHub's SHA-256 digest,
// made executable, then renamed into place — dest is never half-written,
// and nothing unverified is ever left executable.
func downloadVerifiedRelease(tag, dest string) error {
	url, want, err := releaseAsset(tag)
	if err != nil {
		return err
	}
	resp, err := releaseClient.Get(url)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dest), ".astrona-download-")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, maxReleaseBinary+1))
	tmp.Close()
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	if n > maxReleaseBinary {
		return fmt.Errorf("download %s: larger than %d MB", url, maxReleaseBinary>>20)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("checksum mismatch for %s: got sha256:%s, GitHub says sha256:%s — not installed", url, got, want)
	}
	if err := os.Chmod(tmp.Name(), 0755); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return fmt.Errorf("install %s: %w", dest, err)
	}
	return nil
}

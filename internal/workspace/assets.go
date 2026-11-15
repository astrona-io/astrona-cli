package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
)

// A solution guide keeps the relative paths of its images
// (![x](images/01.png)). Astrona publishes the images of an official
// training's solution guides on its CDN, under a name made of their
// SHA-256, with one manifest per lab that maps each image's path (relative
// to the lab's directory) to that name. When the page asks for a solution,
// the server reads the lab's manifest and points the images it lists at the
// CDN. Nothing is copied to this computer and no lab file is read here.
//
// The manifest only ever chooses a file name: every image URL is built
// from SolutionAssetsBase and a name that matches assetName, so a manifest
// cannot send the page anywhere else. A lab without a manifest, a CDN that
// cannot be reached or an image the manifest does not list leaves the
// guide as it was: readable, with that image not loading.

// SolutionAssetsBase is where Astrona publishes solution images and their
// manifests. The page's Content Security Policy allows images from its
// origin only.
const SolutionAssetsBase = "https://cdn.astrona.io/courses-content"

const (
	// assetManifestTimeout bounds one manifest request.
	assetManifestTimeout = 4 * time.Second
	// maxAssetManifestBytes bounds the manifest read from the CDN.
	maxAssetManifestBytes = 1 << 20
	// maxAssetManifestImages bounds the images one manifest may list.
	maxAssetManifestImages = 5000
	// assetManifestTTL is how long a manifest is reused; the CDN caches it
	// about as long.
	assetManifestTTL = 5 * time.Minute
	// assetManifestRetry is how long "no manifest" is reused before the
	// CDN is asked again.
	assetManifestRetry = time.Minute
	// maxAssetManifests bounds the manifests kept in memory.
	maxAssetManifests = 64
)

var (
	// assetName is a published image: its SHA-256 and type.
	assetName = regexp.MustCompile(`^[0-9a-f]{64}\.(png|jpg|webp|gif)$`)
	// assetRepoPart is a GitHub owner or repository name.
	assetRepoPart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// solutionAssetsBase is SolutionAssetsBase; a variable so a test can name
// its own server.
var solutionAssetsBase = SolutionAssetsBase

// SolutionAssets names the lab whose published solution images the run
// shows: its GitHub repository and the lab's directory (the one holding
// config.yaml) in it.
type SolutionAssets struct {
	Owner   string `json:"owner"`
	Repo    string `json:"repo"`
	LabPath string `json:"labPath"`
}

// valid: the three parts are plain names and a clean relative path, so the
// manifest URL built from them stays under SolutionAssetsBase.
func (a *SolutionAssets) valid() bool {
	return a != nil && assetRepoPart.MatchString(a.Owner) && assetRepoPart.MatchString(a.Repo) && cleanLabRelPath(a.LabPath)
}

// manifestURL is the lab's image manifest on the CDN.
func (a *SolutionAssets) manifestURL() string {
	parts := []string{url.PathEscape(a.Owner), url.PathEscape(a.Repo)}
	for seg := range strings.SplitSeq(a.LabPath, "/") {
		parts = append(parts, url.PathEscape(seg))
	}
	return solutionAssetsBase + "/manifests/" + strings.Join(parts, "/") + "/assets.json"
}

// cleanLabRelPath: p is a normalised, /-separated path that stays inside
// the directory it is relative to.
func cleanLabRelPath(p string) bool {
	if p == "" || p == "." || len(p) > 1024 || path.Clean(p) != p || strings.HasPrefix(p, "/") ||
		p == ".." || strings.HasPrefix(p, "../") || strings.Contains(p, `\`) {
		return false
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// solutionImageKey is the manifest key of the image a solution names: dest
// (an image source in the Markdown) resolved against the directory of the
// solution's file, both relative to the lab's directory. Only a relative
// path has a key: never a URL, an absolute path, or one that leaves the lab.
func solutionImageKey(file, dest string) (string, bool) {
	if i := strings.IndexAny(dest, "?#"); i >= 0 {
		dest = dest[:i]
	}
	first, _, _ := strings.Cut(dest, "/")
	if dest == "" || strings.HasPrefix(dest, "/") || strings.Contains(first, ":") {
		return "", false
	}
	decoded, err := url.PathUnescape(dest)
	if err != nil || strings.HasPrefix(decoded, "/") {
		return "", false
	}
	key := path.Join(path.Dir(file), decoded)
	if !cleanLabRelPath(file) || !cleanLabRelPath(key) {
		return "", false
	}
	return key, true
}

// assetManifestClient reads manifests: a short timeout, and never a
// redirect (the manifest is at its own URL or there is none).
var assetManifestClient = &http.Client{
	Timeout: assetManifestTimeout,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// fetchAssetManifest reads the manifest at manifestURL: the images it
// lists, by their path in the lab. Any answer but a usable 200 is an error
// (the CDN answers 403 for a lab without one). Entries that are not a
// clean path and a published name are left out.
func fetchAssetManifest(ctx context.Context, manifestURL string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, assetManifestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := assetManifestClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("no image manifest")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAssetManifestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxAssetManifestBytes {
		return nil, errors.New("the image manifest is too large")
	}
	var doc struct {
		Images map[string]string `json:"images"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Images) > maxAssetManifestImages {
		return nil, errors.New("the image manifest lists too many images")
	}
	images := make(map[string]string, len(doc.Images))
	for key, name := range doc.Images {
		if cleanLabRelPath(key) && assetName.MatchString(name) {
			images[key] = name
		}
	}
	return images, nil
}

// assetManifests caches manifests per URL for a few minutes, "none"
// included, so a guide with several images asks the CDN once.
type assetManifests struct {
	mu      sync.Mutex
	entries map[string]assetManifestEntry
}

type assetManifestEntry struct {
	images map[string]string // nil: no manifest
	until  time.Time
}

func (c *assetManifests) images(ctx context.Context, manifestURL string) map[string]string {
	now := time.Now()
	c.mu.Lock()
	e, ok := c.entries[manifestURL]
	c.mu.Unlock()
	if ok && now.Before(e.until) {
		return e.images
	}
	images, err := fetchAssetManifest(ctx, manifestURL)
	e = assetManifestEntry{images: images, until: now.Add(assetManifestTTL)}
	if err != nil {
		e = assetManifestEntry{until: now.Add(assetManifestRetry)}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil || len(c.entries) >= maxAssetManifests {
		c.entries = map[string]assetManifestEntry{}
	}
	c.entries[manifestURL] = e
	return e.images
}

// solutionImages returns what turns an image source of the solution in
// file (relative to the lab's directory) into its CDN URL, for
// renderMarkdownImages; nil when the run has no published images to look
// for. The manifest is read when the first relative image is met, so a
// guide without images never asks the CDN.
func (s *Server) solutionImages(ctx context.Context, rec RunRecord, file string) func(dest string) (string, bool) {
	if !rec.solutionAllowed() || !rec.SolutionAssets.valid() || !cleanLabRelPath(file) {
		return nil
	}
	var images map[string]string
	fetched := false
	return func(dest string) (string, bool) {
		key, ok := solutionImageKey(file, dest)
		if !ok {
			return "", false
		}
		if !fetched {
			images, fetched = s.solutionAssets.images(ctx, rec.SolutionAssets.manifestURL()), true
		}
		name, ok := images[key]
		if !ok {
			return "", false
		}
		return SolutionAssetsBase + "/assets/" + name, true
	}
}

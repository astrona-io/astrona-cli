package config

import (
	"fmt"
	"strings"
)

// maxPreloadImages bounds runtime.kind.preloadImages — each image is
// saved and loaded into every node, so a long list is a lot of disk and
// time.
const maxPreloadImages = 30

// ImageHasPinnedTag reports whether ref carries a digest or a tag other
// than "latest". Kubernetes defaults imagePullPolicy to Always for
// untagged and :latest images, which would re-pull and make preloading
// pointless.
func ImageHasPinnedTag(ref string) bool {
	if strings.Contains(ref, "@sha256:") {
		return true
	}
	// The tag is after the last ':' that comes after the last '/' (a ':'
	// before that is a registry port, e.g. localhost:5000/img).
	lastSlash := strings.LastIndex(ref, "/")
	colon := strings.LastIndex(ref, ":")
	if colon <= lastSlash {
		return false
	}
	return ref[colon+1:] != "latest"
}

func validatePreloadImages(images []string) error {
	if len(images) > maxPreloadImages {
		return fmt.Errorf("runtime.kind.preloadImages has %d images — at most %d", len(images), maxPreloadImages)
	}
	seen := make(map[string]bool, len(images))
	for _, img := range images {
		if !imageRefPattern.MatchString(img) {
			return fmt.Errorf("runtime.kind.preloadImages: '%s' is not a valid image reference", img)
		}
		if !ImageHasPinnedTag(img) {
			return fmt.Errorf("runtime.kind.preloadImages: '%s' needs an explicit tag (not 'latest') or digest — Kubernetes always re-pulls untagged/:latest images, so preloading them has no effect", img)
		}
		if seen[img] {
			return fmt.Errorf("runtime.kind.preloadImages: '%s' is listed twice", img)
		}
		seen[img] = true
	}
	return nil
}

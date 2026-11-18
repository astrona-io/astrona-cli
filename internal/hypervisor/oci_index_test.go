package hypervisor

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"astrona/internal/config"
)

// fakeOras writes a stand-in `oras` that answers `manifest fetch
// --descriptor` like a registry holding either a multi-arch image index
// (index=true) or a single artifact manifest, and logs every call's
// arguments so a test can see which platform astrona asked for.
func fakeOras(t *testing.T, index bool) (orasPath, logPath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake oras is a shell script")
	}
	dir := t.TempDir()
	logPath = filepath.Join(dir, "calls.log")
	top := `{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:single"}`
	if index {
		top = `{"mediaType":"application/vnd.oci.image.index.v1+json","digest":"sha256:index"}`
	}
	script := `#!/bin/sh
echo "$*" >> '` + logPath + `'
case "$*" in
  *"--platform linux/arm64"*) echo '{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:arm64"}' ;;
  *--platform*) echo 'Error: no matching manifest was found' >&2; exit 1 ;;
  *) echo '` + top + `' ;;
esac
`
	orasPath = filepath.Join(dir, "oras")
	if err := os.WriteFile(orasPath, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return orasPath, logPath
}

func TestResolveOCIManifestPicksPlatformFromIndex(t *testing.T) {
	orasPath, logPath := fakeOras(t, true)

	desc, platform, err := resolveOCIManifest(context.Background(), orasPath, "ghcr.io/x/img:tag", "arm64")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc.Digest != "sha256:arm64" || platform != "linux/arm64" {
		t.Errorf("got digest %q platform %q, want the linux/arm64 manifest", desc.Digest, platform)
	}
	calls, _ := os.ReadFile(logPath)
	if !strings.Contains(string(calls), "--platform linux/arm64") {
		t.Errorf("oras was never asked for linux/arm64:\n%s", calls)
	}
}

func TestResolveOCIManifestLeavesSingleManifestAlone(t *testing.T) {
	orasPath, logPath := fakeOras(t, false)

	desc, platform, err := resolveOCIManifest(context.Background(), orasPath, "ghcr.io/x/img:tag-arm64", "arm64")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc.Digest != "sha256:single" || platform != "" {
		t.Errorf("got digest %q platform %q, want the manifest itself with no platform", desc.Digest, platform)
	}
	calls, _ := os.ReadFile(logPath)
	if strings.Contains(string(calls), "--platform") {
		t.Errorf("a single manifest must not be resolved with --platform (oras rejects it for artifacts):\n%s", calls)
	}
}

func TestResolveOCIManifestIndexWithoutHostArchFails(t *testing.T) {
	orasPath, _ := fakeOras(t, true)

	_, _, err := resolveOCIManifest(context.Background(), orasPath, "ghcr.io/x/img:tag", "amd64")
	if err == nil || !strings.Contains(err.Error(), "linux/amd64") || !strings.Contains(err.Error(), "no matching manifest") {
		t.Fatalf("want an error naming linux/amd64 and oras's reason, got %v", err)
	}
}

func TestUnverifiedCacheSourceKeysIndexByArch(t *testing.T) {
	plain := config.QEMUImageSource{Type: "oci", Source: "ghcr.io/x/img:tag"}
	if got := unverifiedCacheSource(plain, plain.Source, "arm64"); got != "ghcr.io/x/img:tag#linux/arm64" {
		t.Errorf("plain oci ref: got %q", got)
	}
	perArch := config.QEMUImageSource{Type: "oci", Source: "ghcr.io/x/img:tag-{ARCH}"}
	if got := unverifiedCacheSource(perArch, "ghcr.io/x/img:tag-arm64", "arm64"); got != "ghcr.io/x/img:tag-arm64" {
		t.Errorf("{ARCH} oci ref must keep its existing cache key, got %q", got)
	}
	url := config.QEMUImageSource{Type: "url", Source: "https://example.com/img.qcow2"}
	if got := unverifiedCacheSource(url, url.Source, "arm64"); got != url.Source {
		t.Errorf("url source must keep its existing cache key, got %q", got)
	}
}

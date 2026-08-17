package config

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const downloadHTTPTimeout = 30 * time.Minute

// DownloadToTemp fetches a URL to a temp file. The returned cleanup func
// removes the temp file once the caller is done. Only https:// URLs are
// accepted, and the body is capped at maxBytes so an untrusted remote
// source can't make this CLI write an unbounded amount of data to disk.
// The file is never chmod +x: LocalExecutor runs it as `bash scriptPath`
// and SSHExecutor pipes it over stdin to `bash -s` — neither ever executes
// it directly, so there's no reason to set the executable bit.
func DownloadToTemp(url, filePattern string, maxBytes int64) (string, func(), error) {
	cleanup := func() {}

	if !strings.HasPrefix(url, "https://") {
		return "", cleanup, fmt.Errorf("refusing to download from non-https URL '%s': only https:// sources are allowed", url)
	}

	client := &http.Client{Timeout: downloadHTTPTimeout}

	resp, err := client.Get(url)
	if err != nil {
		return "", cleanup, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", cleanup, fmt.Errorf("server returned status: %s", resp.Status)
	}

	tmpFile, err := os.CreateTemp("", filePattern)
	if err != nil {
		return "", cleanup, err
	}

	cleanup = func() {
		os.Remove(tmpFile.Name())
	}

	n, err := io.Copy(tmpFile, io.LimitReader(resp.Body, maxBytes+1))
	tmpFile.Close()
	if err != nil {
		cleanup()
		return "", cleanup, err
	}

	if n > maxBytes {
		cleanup()
		return "", func() {}, fmt.Errorf("download from '%s' exceeds %d byte limit", url, maxBytes)
	}

	return tmpFile.Name(), cleanup, nil
}

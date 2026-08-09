package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// gitCacheDir returns (creating if needed) the cache directory a given repo
// URL + ref pins to. Keyed by a hash of url+ref, not the raw URL, so
// arbitrary remote-supplied strings never become path components (same
// path-safety concern as joinWithinBaseDir in scripts.go, different
// mechanism — here we don't need the directory name to be human-readable).
func gitCacheDir(url, ref string) (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve user cache dir: %w", err)
	}

	sum := sha256.Sum256([]byte(url + "@" + ref))
	key := hex.EncodeToString(sum[:])[:16]

	dir := filepath.Join(cacheDir, "astrona", "repos", key)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create git cache dir '%s': %w", dir, err)
	}

	return dir, nil
}

// runGit runs a git subcommand, optionally scoped to repoDir (git -C
// repoDir ...), with output wired to the terminal like every other
// subprocess call in this repo.
func runGit(gitPath, repoDir string, args ...string) error {
	if repoDir != "" {
		args = append([]string{"-C", repoDir}, args...)
	}

	cmd := exec.Command(gitPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// gitRefIsRemoteBranch checks whether ref names a branch on origin, so
// cloneOrUpdateGitRepo can tell a branch (which should track origin/<ref>)
// apart from a tag or commit sha (checked out directly). Output is
// deliberately discarded — this is an internal check, not user-facing.
func gitRefIsRemoteBranch(gitPath, destDir, ref string) bool {
	cmd := exec.Command(gitPath, "-C", destDir, "rev-parse", "--verify", "--quiet", "origin/"+ref)
	return cmd.Run() == nil
}

// cloneOrUpdateGitRepo makes destDir a clean checkout of url at ref (or the
// repo's default branch if ref is empty): clones if destDir isn't a git
// repo yet, otherwise fetches. Either way it finishes with
// `checkout --force -B astrona-lab <target>` + `clean -fdx`, so destDir is
// always left as a clean checkout — this directory is entirely
// astrona-managed and must never be pointed at a real user directory, since
// any local changes in it are discarded on every call.
//
// Offline-tolerant when a cache already exists: a failed fetch only warns
// and falls back to whatever was cached from the last successful
// clone/fetch — the checkout step reads git's local remote-tracking refs,
// not the network, so a lab that worked before still works with no
// connection. This only covers a ref that's already been fetched at least
// once; a brand-new branch/tag/commit still needs network the first time,
// same as a first-time clone (no cache to fall back to).
//
// No URL scheme restriction here (unlike downloadToTemp's https-only rule
// in scripts.go): git itself owns the transport — https://, git@host:, or
// ssh://, including TLS cert and SSH host-key verification — the same
// trust posture already given to kind/docker/kubectl as external binaries
// this CLI shells out to rather than reimplements.
//
// Submodules are not fetched (plain `git clone`/`git fetch`, no
// --recurse-submodules) — a lab repo that needs them isn't supported today.
func cloneOrUpdateGitRepo(url, ref, destDir string) error {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return fmt.Errorf("git not found in PATH: %w", err)
	}

	if _, err := os.Stat(filepath.Join(destDir, ".git")); os.IsNotExist(err) {
		fmt.Printf("Cloning %s...\n", url)
		if err := runGit(gitPath, "", "clone", url, destDir); err != nil {
			return fmt.Errorf("failed to clone %s: %w", url, err)
		}
	} else {
		fmt.Printf("Updating %s...\n", url)
		if err := runGit(gitPath, destDir, "fetch", "--all", "--prune"); err != nil {
			// Not fatal: destDir already has a cached checkout from a
			// previous successful fetch/clone, and the checkout below reads
			// from git's local remote-tracking refs — it doesn't need the
			// fetch to have just succeeded, only to have succeeded at some
			// point. Losing network here shouldn't break a lab that was
			// already working offline.
			fmt.Printf("[WARN] failed to update %s, using cached checkout instead: %s\n", url, err)
		}
	}

	target := "origin/HEAD"
	switch {
	case ref == "":
		// keep origin/HEAD — the repo's default branch
	case gitRefIsRemoteBranch(gitPath, destDir, ref):
		target = "origin/" + ref
	default:
		target = ref // tag or commit sha
	}

	if err := runGit(gitPath, destDir, "checkout", "--force", "-B", "astrona-lab", target); err != nil {
		return fmt.Errorf("failed to checkout '%s' from %s: %w", target, url, err)
	}

	if err := runGit(gitPath, destDir, "clean", "-fdx"); err != nil {
		return fmt.Errorf("failed to clean git checkout for %s: %w", url, err)
	}

	return nil
}

// resolveGitConfigSource clones/updates url@ref into its cache dir and
// returns that local path — callers treat the result exactly like any
// other local --config directory, no special-casing needed downstream.
func resolveGitConfigSource(url, ref string) (string, error) {
	destDir, err := gitCacheDir(url, ref)
	if err != nil {
		return "", err
	}

	if err := cloneOrUpdateGitRepo(url, ref, destDir); err != nil {
		return "", err
	}

	return destDir, nil
}

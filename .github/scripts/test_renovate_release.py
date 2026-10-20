import copy
import importlib.util
import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock


spec = importlib.util.spec_from_file_location(
    "renovate_release", Path(__file__).with_name("renovate-release.py")
)
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class RenovateReleaseTests(unittest.TestCase):
    def test_only_patch_changes_and_prereleases_are_ignored(self):
        for tags, expected in [
            (["v0.4.6"], "v0.4.7"),
            (["v0.4.9", "v0.4.10", "v0.5.0-rc.1", "dev"], "v0.4.11"),
            (["v0.9.99", "v1.2.99"], "v1.2.100"),
            (["v0.4.6", "v00.9.9", "v0.4.7-extra"], "v0.4.7"),
        ]:
            with self.subTest(tags=tags):
                self.assertEqual(release.next_patch(tags), expected)
        with self.assertRaises(ValueError):
            release.next_patch(["v1.0.0-rc1"])

    def test_only_merged_same_repository_renovate_prs_qualify(self):
        repo = {"full_name": "astrona-io/astrona-cli"}
        pr = {
            "merged": True, "user": {"login": "renovate[bot]"},
            "base": {"ref": "main", "repo": repo},
            "head": {"ref": "renovate/go", "repo": repo},
        }
        self.assertTrue(release.eligible(pr, repo["full_name"]))
        for path, value in [
            (("merged",), False),
            (("user", "login"), "someone"),
            (("base", "ref"), "dev"),
            (("head", "ref"), "feature/renovate"),
            (("head", "repo", "full_name"), "fork/astrona-cli"),
        ]:
            changed = copy.deepcopy(pr)
            target = changed
            for key in path[:-1]:
                target = target[key]
            target[path[-1]] = value
            self.assertFalse(release.eligible(changed, repo["full_name"]))

    def test_allocation_retry_and_delayed_merges(self):
        tags = {"v0.4.6": "old", "v0.4.7": "new"}
        ancestry = {("old", "new"), ("middle", "new"), ("new", "next")}
        is_ancestor = lambda a, b: (a, b) in ancestry
        self.assertEqual(
            release.plan("next", tags, tags.__getitem__, is_ancestor), ("v0.4.8", True)
        )
        self.assertEqual(
            release.plan("new", tags, tags.__getitem__, is_ancestor), ("v0.4.7", False)
        )
        for sha in ("old", "middle"):
            self.assertEqual(release.plan(sha, tags, tags.__getitem__, is_ancestor), ("", False))
        with self.assertRaises(ValueError):
            release.plan("unrelated", tags, tags.__getitem__, is_ancestor)

    def test_real_git_merge_tag_and_retry(self):
        # Exercise real tag enumeration, annotated tags and ancestry. Only the
        # GitHub write is replaced: no release or remote tag is published.
        with tempfile.TemporaryDirectory() as directory:
            previous = os.getcwd()
            self.addCleanup(os.chdir, previous)
            os.chdir(directory)
            git_env = {
                "GIT_AUTHOR_NAME": "Release test", "GIT_COMMITTER_NAME": "Release test",
                "GIT_AUTHOR_EMAIL": "test@example.com", "GIT_COMMITTER_EMAIL": "test@example.com",
            }
            with mock.patch.dict(os.environ, git_env):
                release.git("init", "-q")
                release.git("-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", "baseline")
                release.git("-c", "tag.gpgsign=false", "tag", "-a", "v0.4.6", "-m", "baseline")
                release.git("-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", "dependency merge")
                sha = release.git("rev-parse", "HEAD")
                release.git("update-ref", "refs/remotes/origin/main", sha)
                event = {
                    "pull_request": {
                        "number": 123, "merged": True, "merge_commit_sha": sha,
                        "user": {"login": "renovate[bot]"},
                        "base": {"ref": "main", "repo": {"full_name": "astrona-io/astrona-cli"}},
                        "head": {"ref": "renovate/go", "repo": {"full_name": "astrona-io/astrona-cli"}},
                    }
                }
                writes = []
                real_run = subprocess.run

                def run(command, **kwargs):
                    if command[0] != "gh":
                        return real_run(command, **kwargs)
                    if "/commits/" in command[2]:
                        return subprocess.CompletedProcess(command, 0, stdout=json.dumps([[event["pull_request"]]]))
                    if command[2].endswith("/pulls/123"):
                        return subprocess.CompletedProcess(command, 0, stdout=json.dumps(event["pull_request"]))
                    self.assertEqual(command[2], "repos/astrona-io/astrona-cli/git/refs")
                    payload = json.loads(kwargs["input"])
                    writes.append(payload)
                    release.git("-c", "tag.gpgsign=false", "tag", payload["ref"].removeprefix("refs/tags/"), payload["sha"])
                    return subprocess.CompletedProcess(command, 0)

                with mock.patch.dict(os.environ, {
                    "GITHUB_REPOSITORY": "astrona-io/astrona-cli",
                    "GITHUB_SHA": sha,
                    "GITHUB_OUTPUT": str(Path("output").resolve()),
                }), mock.patch.object(release.subprocess, "run", side_effect=run):
                    release.main()
                    release.main()
                self.assertEqual(writes, [{"ref": "refs/tags/v0.4.7", "sha": sha}])
                self.assertEqual(Path("output").read_text(), "tag=v0.4.7\ntag=v0.4.7\n")
                self.assertEqual(release.git("rev-parse", "v0.4.7"), sha)
                os.chdir(previous)


if __name__ == "__main__":
    unittest.main()

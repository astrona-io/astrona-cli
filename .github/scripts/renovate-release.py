"""Allocate a stable patch tag for a merged Renovate PR; never publish locally."""

import json
import os
import re
import subprocess


STABLE = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)")


def eligible(pr, repository):
    return (
        pr.get("merged") is True
        and pr["user"]["login"] == "renovate[bot]"
        and pr["base"]["ref"] == "main"
        and pr["base"]["repo"]["full_name"] == repository
        and pr["head"]["repo"]["full_name"] == repository
        and pr["head"]["ref"].startswith("renovate/")
    )


def stable_tags(tags):
    return sorted(
        (tag for tag in tags if STABLE.fullmatch(tag)),
        key=lambda tag: tuple(map(int, STABLE.fullmatch(tag).groups())),
    )


def next_patch(tags):
    stable = stable_tags(tags)
    if not stable:
        raise ValueError("No stable vX.Y.Z tag exists; create the initial release manually")
    major, minor, patch = map(int, STABLE.fullmatch(stable[-1]).groups())
    return f"v{major}.{minor}.{patch + 1}"


def git(*args):
    return subprocess.check_output(["git", *args], text=True).strip()


def ancestor(older, newer):
    result = subprocess.run(["git", "merge-base", "--is-ancestor", older, newer], check=False)
    if result.returncode not in (0, 1):
        raise RuntimeError("Could not verify release ancestry")
    return result.returncode == 0


def plan(sha, tags, resolve, is_ancestor):
    stable = stable_tags(tags)
    # A retry reuses its tag. Never republish an older, superseded release.
    for tag in reversed(stable):
        if resolve(tag) == sha:
            return (tag, False) if tag == stable[-1] else ("", False)
    tag = next_patch(stable)
    latest = resolve(stable[-1])
    if is_ancestor(sha, latest):
        # A delayed event is already included in a newer release. Never tag
        # that older source with a newer version and roll users backwards.
        return "", False
    if not is_ancestor(latest, sha):
        raise ValueError("Merged commit does not descend from the latest stable release")
    return tag, True


def main():
    repository = os.environ["GITHUB_REPOSITORY"]
    sha = os.environ["GITHUB_SHA"]
    if not re.fullmatch(r"[0-9a-f]{40}", sha):
        raise ValueError("Invalid merge commit SHA")

    def read_api(path, paginate=False):
        args = ["gh", "api", f"repos/{repository}/{path}"]
        if paginate:
            args.extend(["--paginate", "--slurp"])
        return json.loads(subprocess.check_output(args, text=True))

    # Push context identifies the built commit accurately in provenance. Match
    # the actual merge commit, not merely a PR that contains this commit.
    pages = read_api(f"commits/{sha}/pulls?per_page=100", paginate=True)
    pr = None
    for candidate in (item for page in pages for item in page):
        if candidate.get("merge_commit_sha") != sha or candidate["user"]["login"] != "renovate[bot]":
            continue
        detail = read_api(f"pulls/{candidate['number']}")
        if eligible(detail, repository) and detail["merge_commit_sha"] == sha:
            pr = detail
            break
    if pr is None:
        print("Not a merged, same-repository Renovate PR into main; skipping")
        return
    if not ancestor(sha, "origin/main"):
        raise ValueError("Merge commit is not on main")
    tag, create = plan(
        sha, git("tag", "--list").splitlines(),
        lambda tag: git("rev-parse", f"{tag}^{{commit}}"), ancestor,
    )
    if not tag:
        print("Merged dependency update is already included in a newer stable release; skipping")
        return
    if create:
        # GITHUB_TOKEN-created tags do not trigger push workflows. The caller
        # invokes the reusable release/docs workflows explicitly after this.
        subprocess.run(
            ["gh", "api", f"repos/{repository}/git/refs", "--method", "POST", "--input", "-"],
            input=json.dumps({"ref": f"refs/tags/{tag}", "sha": sha}),
            text=True, check=True, stdout=subprocess.DEVNULL,
        )
    with open(os.environ["GITHUB_OUTPUT"], "a") as output:
        output.write(f"tag={tag}\n")
    print(f"Release {tag} from Renovate PR #{pr['number']} ({sha})")


if __name__ == "__main__":
    main()

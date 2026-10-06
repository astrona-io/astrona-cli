# Offline Bundles

For classrooms and air-gapped machines: pack a kind lab with everything it would otherwise download, carry one file, run it without internet.

```sh
# on a machine with internet
astrona bundle create -c ./labs/k8s-web-01 -o k8s-web-01.tar.gz

# on the offline machine
astrona bundle inspect k8s-web-01.tar.gz    # what's inside (verifies, installs nothing)
astrona bundle load k8s-web-01.tar.gz       # verify, load images, unpack the lab
astrona run -c ~/.astrona/bundles/k8s-web-01-<hash>
```

## What's in a bundle

| Content | Why |
|---|---|
| The lab directory (without `.git`) | config, docs, scripts, manifests, reference solution |
| Node image (`runtime.kind.version` / `image`, or `--node-image`) | what kind boots the cluster from |
| `runtime.kind.preloadImages` | the lab's own workloads |
| Addon manifests + every image they reference | Calico, cert-manager, metrics-server install offline |

Every image is a `docker/podman save` archive recorded with its SHA-256; `astrona-bundle.json` describes it all.

## Loading

`astrona bundle load`:

1. Extracts safely — refuses absolute paths, `..`, symlinks and hard links, and anything over 30 GB / 100,000 entries.
2. Verifies every image and addon manifest against its recorded SHA-256, and the CPU architecture (an arm64 bundle won't load on amd64).
3. Asks before installing anything — a bundle is someone else's code, so you see what the lab will do, pinned to the bundle's SHA-256 (`--trust` to approve without asking; see [Approving remote labs](remote-config.md#approving-remote-labs)).
4. Loads the images into Docker/Podman.
5. Seeds the addon cache — **only** with manifests matching the exact versions astrona itself pins, so a bundle can't change what an addon installs.
6. Unpacks the lab into `~/.astrona/bundles/<lab>-<hash>/`.

After that it's an ordinary local lab — `run`, `test`, `reset`, `submit` work as usual, and addon images are loaded into the cluster nodes from the host (no registry).

## Limits

- **kind labs only** — qemu labs already cache their base images.
- **The node image must be pinned** — `runtime.kind.version`/`image`, or `--node-image` when creating; kind's built-in default can't be determined reliably.
- **No `type: url` scripts or manifests** — they're fetched at run time; make them local files.
- **Not the `gatewayAPI` addon yet** — Envoy Gateway starts its proxy image at runtime and it isn't listed in its manifest.
- **[Linked clusters](linked-labs.md) must pin their node image** (`version` or `image` on each `runtime.kind.clusters` entry) — offline, an unpinned cluster would boot kind's default image, which may not be the one in the bundle. Their node images, preload images and addon manifests are bundled with the lab's (each once), and addon images are preloaded into every cluster that installs addons.
- Bundles are per CPU architecture.

"""Keywords the Robot suites cannot express cleanly on their own.

Everything else lives in resources/astrona.resource so each step shows up in
the Robot log. Keep this file small: isolation, JSON, kind and qemu lookups,
and the cached VM base image.
"""

import hashlib
import json
import os
import platform
import re
import shutil
import subprocess
import sys
import tempfile
import urllib.request

from robot.api.deco import keyword, library

# PIDs of every process that owns a window, from the window server. Needs no
# screen-recording or accessibility permission: only window titles do.
_MACOS_WINDOW_OWNERS = """
ObjC.import('CoreGraphics');
var list = ObjC.castRefToObject($.CGWindowListCopyWindowInfo($.kCGWindowListOptionAll, 0));
var pids = [];
for (var i = 0; i < list.count; i++) {
  pids.push(ObjC.unwrap(list.objectAtIndex(i).objectForKey('kCGWindowOwnerPID')));
}
JSON.stringify(pids);
"""


def _sha512(path):
    digest = hashlib.sha512()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


@library(scope="GLOBAL")
class AstronaLib:
    def __init__(self):
        self._home = None

    @keyword
    def prepare_isolated_home(self):
        """Return the environment for astrona processes, with a fresh HOME.

        astrona keeps its state under ~/.astrona-dev (development builds), so
        a throwaway HOME keeps the suite away from the contributor's own labs.
        The container engine's config stays on the real HOME, or Podman and
        Docker Desktop lose their machine connection. Set OAT_ISOLATE_HOME=false
        to run against the real HOME instead.
        """
        env = dict(os.environ, ASTRONA_NO_UPDATE_CHECK="1")
        if env.get("OAT_ISOLATE_HOME", "true").lower() == "false":
            return env
        real_home = os.path.expanduser("~")
        self._home = tempfile.mkdtemp(prefix="astrona-oat-home-")
        env.update({
            "HOME": self._home,
            "KUBECONFIG": os.path.join(self._home, ".kube", "config"),
            "XDG_CONFIG_HOME": os.environ.get("XDG_CONFIG_HOME", os.path.join(real_home, ".config")),
            "XDG_DATA_HOME": os.environ.get("XDG_DATA_HOME", os.path.join(real_home, ".local", "share")),
            "DOCKER_CONFIG": os.environ.get("DOCKER_CONFIG", os.path.join(real_home, ".docker")),
        })
        return env

    @keyword
    def remove_isolated_home(self):
        if self._home:
            shutil.rmtree(self._home, ignore_errors=True)
            self._home = None

    @keyword
    def read_profile_file(self, env, name):
        """Return the stripped text of ``name`` in the profile home of the HOME
        in ``env`` (~/.astrona-dev for development builds, else ~/.astrona);
        fails when neither has it."""
        home = env.get("HOME") or os.path.expanduser("~")
        tried = []
        for profile in (".astrona-dev", ".astrona"):
            path = os.path.join(home, profile, name)
            tried.append(path)
            if os.path.isfile(path):
                with open(path, encoding="utf-8") as f:
                    return f.read().strip()
        raise AssertionError(f"{name} is not in the profile home: tried {', '.join(tried)}")

    @keyword
    def profile_queue_length(self, env, name):
        """How many reports wait in the queue folder ``name`` (guest-runs, outbox)
        of the profile homes of the HOME in ``env``: its *.json files, not
        hidden ones or the outbox's status.json. A missing folder is empty."""
        home = env.get("HOME") or os.path.expanduser("~")
        count = 0
        for profile in (".astrona-dev", ".astrona"):
            folder = os.path.join(home, profile, name)
            if os.path.isdir(folder):
                count += sum(1 for entry in os.listdir(folder)
                             if entry.endswith(".json") and not entry.startswith(".") and entry != "status.json")
        return count

    @keyword
    def parse_json_output(self, text):
        """Parse astrona's JSON stdout; fails with the text when it is not JSON."""
        try:
            return json.loads(text)
        except json.JSONDecodeError as err:
            raise AssertionError(f"stdout is not JSON ({err}):\n{text}") from None

    @keyword
    def kind_cluster_should_not_exist(self, lab):
        """Refuse to run against a kind cluster the suite did not create.

        kind cluster names are global to the container engine, so an isolated
        HOME does not protect a contributor's running lab of the same name.
        """
        found = self._lab_clusters(lab)
        if found:
            raise AssertionError(
                f"kind cluster(s) {found} for lab {lab!r} already exist; destroy them "
                f"(astrona destroy {lab}) before running the acceptance suite, it would tear them down"
            )

    @keyword
    def kind_cluster_should_exist(self, lab):
        if not self._lab_clusters(lab):
            raise AssertionError(f"no kind cluster for lab {lab!r}; have: {self._kind_clusters()}")

    @keyword
    def lab_should_be_listed(self, labs, lab):
        """`astrona list -o json` names kind labs by their cluster name."""
        names = [entry.get("name") for entry in labs]
        if not any(self._cluster_pattern(lab).match(name or "") for name in names):
            raise AssertionError(f"lab {lab!r} not in astrona list: {names}")

    @keyword
    def prepare_vm_lab(self, source_dir, dest_dir, url_template, sha512_by_arch):
        """Copy the VM lab at source_dir to dest_dir with the base image as base.qcow2.

        The image is downloaded once into the cache (OAT_CACHE_DIR, default
        ~/.cache/astrona-oat) and verified against its pinned SHA-512 on every
        use. astrona refuses a symlinked base image, so the lab gets a hard
        link to the cached file, or a copy across filesystems.
        """
        arch = self._oci_arch()
        if arch not in sha512_by_arch:
            raise AssertionError(f"no pinned VM image for host arch {arch!r}; have {sorted(sha512_by_arch)}")
        image = self._cached_image(url_template.replace("{ARCH}", arch), sha512_by_arch[arch])
        shutil.copytree(source_dir, dest_dir)
        target = os.path.join(dest_dir, "base.qcow2")
        try:
            os.link(image, target)
        except OSError:
            shutil.copyfile(image, target)
        return dest_dir

    @keyword
    def vm_should_not_be_running(self, lab):
        """Refuse to run against a VM the suite did not create.

        qemu processes and their ports are global to the machine, so an
        isolated HOME does not protect a contributor's running lab.
        """
        found = self._vm_processes(lab)
        if found:
            raise AssertionError(
                f"qemu process(es) {found} for lab {lab!r} already run; destroy the lab "
                f"(astrona destroy {lab}) before running the acceptance suite"
            )

    @keyword
    def vm_should_be_running(self, lab):
        if not self._vm_processes(lab):
            raise AssertionError(f"no qemu process for lab {lab!r}")

    @keyword
    def vm_should_be_gone(self, lab):
        """No qemu process and no saved VM state (overlay disk, keys) is left."""
        self.vm_should_not_be_running(lab)
        home = self._home or os.path.expanduser("~")
        left = [
            os.path.join(root, name)
            for root in (os.path.join(home, ".astrona", "qemu"), os.path.join(home, ".astrona-dev", "qemu"))
            if os.path.isdir(root)
            for name in os.listdir(root)
            if self._cluster_pattern(lab).match(name)
        ]
        if left:
            raise AssertionError(f"VM state for lab {lab!r} is left behind: {left}")

    @keyword
    def desktop_window_should_be_open(self, lab):
        """The lab's qemu runs in window mode and owns a window on the display.

        That window is the desktop astrona offers today (runtime.qemu
        display: true). macOS asks the window server which processes own
        windows; Linux needs an X display (CI runs under xvfb-run) and looks
        for qemu's window title, "QEMU (<process name>)".
        """
        procs = self._vm_processes(lab)
        if len(procs) != 1:
            raise AssertionError(f"expected one qemu process for lab {lab!r}, found {procs}")
        pid, command = procs[0]
        if "-display none" in command:
            raise AssertionError(f"qemu for lab {lab!r} runs headless: {command}")
        if sys.platform == "darwin":
            out = subprocess.run(
                ["osascript", "-l", "JavaScript", "-e", _MACOS_WINDOW_OWNERS],
                capture_output=True, text=True, check=True,
            ).stdout
            if pid not in json.loads(out):
                raise AssertionError(f"qemu (pid {pid}) for lab {lab!r} owns no window")
            return
        if not os.environ.get("DISPLAY"):
            raise AssertionError("the desktop target needs an X display: set DISPLAY or run under xvfb-run")
        tree = subprocess.run(
            ["xwininfo", "-root", "-tree"], capture_output=True, text=True, check=True
        ).stdout
        name = re.search(r"-name (\S+)", command).group(1)
        if name not in tree:
            raise AssertionError(f"no window titled after {name!r} on display {os.environ['DISPLAY']}:\n{tree}")

    def _vm_processes(self, lab):
        # astrona names each VM "astrona-<cluster name>" (qemu -name).
        pattern = re.compile(rf"-name astrona-astro(dev)?-(test-)?{re.escape(lab)}(\s|$)")
        out = subprocess.run(
            ["ps", "-ax", "-ww", "-o", "pid=,command="], capture_output=True, text=True, check=True
        ).stdout
        found = []
        for line in out.splitlines():
            pid, _, command = line.strip().partition(" ")
            if "qemu-system" in command and pattern.search(command):
                found.append((int(pid), command))
        return found

    @staticmethod
    def _oci_arch():
        machine = platform.machine().lower()
        return {"x86_64": "amd64", "amd64": "amd64", "arm64": "arm64", "aarch64": "arm64"}.get(machine, machine)

    @staticmethod
    def _cached_image(url, sha512):
        cache = os.environ.get("OAT_CACHE_DIR") or os.path.join(
            os.environ.get("XDG_CACHE_HOME") or os.path.expanduser("~/.cache"), "astrona-oat"
        )
        images = os.path.join(cache, "images")
        os.makedirs(images, exist_ok=True)
        path = os.path.join(images, url.rsplit("/", 1)[-1])
        if os.path.exists(path):
            if _sha512(path) == sha512:
                return path
            os.remove(path)  # partial or replaced upstream: fetch again
        fd, tmp = tempfile.mkstemp(dir=images, prefix=".download-")
        try:
            with os.fdopen(fd, "wb") as out, urllib.request.urlopen(url, timeout=60) as resp:
                shutil.copyfileobj(resp, out, 1 << 20)
            actual = _sha512(tmp)
            if actual != sha512:
                raise AssertionError(f"{url}: SHA-512 {actual}, pinned {sha512}")
            os.replace(tmp, path)
        finally:
            if os.path.exists(tmp):
                os.remove(tmp)
        return path

    def _lab_clusters(self, lab):
        pattern = self._cluster_pattern(lab)
        return [name for name in self._kind_clusters() if pattern.match(name)]

    @staticmethod
    def _cluster_pattern(lab):
        # Release builds prefix clusters with astro-, development builds with
        # astrodev-, and `astrona test` adds test- (internal/apphome).
        return re.compile(rf"^astro(dev)?-(test-)?{re.escape(lab)}$")

    @staticmethod
    def _kind_clusters():
        out = subprocess.run(
            ["kind", "get", "clusters"], capture_output=True, text=True, check=True
        ).stdout
        return [line.strip() for line in out.splitlines() if line.strip()]

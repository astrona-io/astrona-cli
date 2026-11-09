"""Keywords the Robot suites cannot express cleanly on their own.

Everything else lives in resources/astrona.resource so each step shows up in
the Robot log. Keep this file small: isolation, JSON and kind lookups only.
"""

import json
import os
import re
import shutil
import subprocess
import tempfile

from robot.api.deco import keyword, library


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

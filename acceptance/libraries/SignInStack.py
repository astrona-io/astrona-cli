"""A throwaway sign-in stack for the login suite: no astrona.io, no real account.

* Keycloak, the same image the Astrona workspace runs, in a container (podman
  or docker, whichever answers first; OAT_CONTAINER_RUNTIME picks one), with
  acceptance/keycloak/astrona-oat-realm.json: a
  public `astrona-cli` client with the OAuth 2.0 device grant and consent.
* A test user created at setup with a random password that only this process
  knows: no keyword takes or returns it, so Robot never logs it.
* A fake Astrona site (stdlib http.server in a thread) that speaks the CLI's
  /api/cli contract: it proxies the device flow to Keycloak, validates every
  bearer token against Keycloak's userinfo and records what the CLI sent, so
  the suite can assert on it.
* Playwright (Chromium) signing in on Keycloak's own device pages.

The astrona.io device page and astrona-svc-labs are not exercised: the fake
site stands in for both. Browsers are installed once with:
playwright install chromium
"""

import hashlib
import json
import os
import re
import secrets
import shutil
import socket
import subprocess
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from robot.api import logger
from robot.api.deco import keyword, library
from robot.libraries.BuiltIn import BuiltIn

KEYCLOAK_IMAGE = "quay.io/keycloak/keycloak:26.8"
REALM = "astrona-oat"
CLIENT_ID = "astrona-cli"
USERNAME = "oat-learner"
REALM_DIR = os.path.normpath(os.path.join(os.path.dirname(__file__), "..", "keycloak"))

LINK = re.compile(r"open (\S+) on any device")
CODE = re.compile(r"Confirm the code (\S+) matches")
SESSION_PATH = re.compile(r"^/api/cli/lab-sessions/([A-Za-z0-9-]+)/(results|progress|usage|stop|renew|pause|resume)$")


def _free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _now():
    return datetime.now(timezone.utc)


def _rfc3339(t):
    return t.strftime("%Y-%m-%dT%H:%M:%SZ")


def _http(method, url, form=None, data=None, bearer=None, timeout=15):
    """One request; returns (status, parsed JSON or None). Never raises on HTTP errors."""
    headers = {"Accept": "application/json"}
    body = None
    if form is not None:
        body = urllib.parse.urlencode(form).encode()
        headers["Content-Type"] = "application/x-www-form-urlencoded"
    elif data is not None:
        body = json.dumps(data).encode()
        headers["Content-Type"] = "application/json"
    if bearer:
        headers["Authorization"] = "Bearer " + bearer
    req = urllib.request.Request(url, data=body, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            status, raw = resp.status, resp.read()
    except urllib.error.HTTPError as err:
        status, raw = err.code, err.read()
    try:
        return status, json.loads(raw) if raw else None
    except ValueError:
        return status, None


class Keycloak:
    """Keycloak in a container on a free loopback port."""

    def __init__(self):
        self.runtime = None
        self.name = f"astrona-oat-keycloak-{secrets.token_hex(4)}"
        self.port = _free_port()
        self.url = f"http://127.0.0.1:{self.port}"
        self.realm_url = f"{self.url}/realms/{REALM}"
        self._admin_password = secrets.token_urlsafe(24)
        self.user_password = secrets.token_urlsafe(24)

    def _run(self, *args, timeout=60, env=None):
        return subprocess.run([self.runtime, *args], capture_output=True, text=True, timeout=timeout, env=env)

    @staticmethod
    def find_runtime():
        tried = []
        choice = os.environ.get("OAT_CONTAINER_RUNTIME", "").strip()
        for name in [choice] if choice else ["podman", "docker"]:
            path = shutil.which(name)
            if not path:
                continue
            try:
                ok = subprocess.run([path, "info"], capture_output=True, timeout=60).returncode == 0
            except subprocess.TimeoutExpired:
                ok = False
            if ok:
                return path
            tried.append(name)
        wanted = choice or "podman or docker"
        detail = f" ({', '.join(tried)} found but not running)" if tried else ""
        raise AssertionError(
            f"the login suite runs Keycloak in a container and needs {wanted}{detail}; "
            "exclude it with --exclude login"
        )

    def start(self, timeout):
        self.runtime = self.find_runtime()
        logger.info(f"Keycloak: {KEYCLOAK_IMAGE} as {self.name} on {self.url} ({self.runtime})")
        pull = self._run("pull", "--quiet", KEYCLOAK_IMAGE, timeout=600)
        if pull.returncode != 0:
            raise AssertionError(f"pulling {KEYCLOAK_IMAGE} failed: {pull.stderr.strip()}")
        # The admin password reaches the container through the environment
        # (-e NAME without a value), never the command line.
        env = dict(os.environ, KC_BOOTSTRAP_ADMIN_PASSWORD=self._admin_password)
        create = self._run(
            "create", "--name", self.name,
            "-p", f"127.0.0.1:{self.port}:8080",
            "-e", "KC_BOOTSTRAP_ADMIN_USERNAME=admin",
            "-e", "KC_BOOTSTRAP_ADMIN_PASSWORD",
            KEYCLOAK_IMAGE, "start-dev", "--http-port=8080", "--import-realm",
            env=env,
        )
        if create.returncode != 0:
            raise AssertionError(f"creating the Keycloak container failed: {create.stderr.strip()}")
        # Copied rather than bind-mounted: works the same on a podman machine.
        for args in (("cp", REALM_DIR, f"{self.name}:/opt/keycloak/data/import"), ("start", self.name)):
            step = self._run(*args)
            if step.returncode != 0:
                raise AssertionError(f"{self.runtime} {args[0]} for Keycloak failed: {step.stderr.strip()}")
        self._wait_ready(float(timeout))
        self._create_user()

    def _wait_ready(self, timeout):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            try:
                status, _ = _http("GET", f"{self.realm_url}/.well-known/openid-configuration", timeout=5)
                if status == 200:
                    return
            except OSError:
                pass
            state = self._run("inspect", "--format", "{{.State.Running}}", self.name)
            if state.stdout.strip() != "true":
                raise AssertionError(f"Keycloak exited while starting:\n{self.logs()}")
            time.sleep(2)
        raise AssertionError(f"Keycloak was not ready within {timeout:.0f}s:\n{self.logs()}")

    def _create_user(self):
        status, token = _http("POST", f"{self.url}/realms/master/protocol/openid-connect/token", form={
            "grant_type": "password", "client_id": "admin-cli",
            "username": "admin", "password": self._admin_password,
        })
        if status != 200:
            raise AssertionError(f"Keycloak admin sign-in failed: HTTP {status}")
        status, _ = _http("POST", f"{self.url}/admin/realms/{REALM}/users", bearer=token["access_token"], data={
            "username": USERNAME, "email": f"{USERNAME}@example.invalid",
            "firstName": "OAT", "lastName": "Learner",
            "enabled": True, "emailVerified": True,
            "credentials": [{"type": "password", "value": self.user_password, "temporary": False}],
        })
        if status != 201:
            raise AssertionError(f"creating the Keycloak test user failed: HTTP {status}")

    def logs(self):
        if not self.runtime:
            return ""
        out = self._run("logs", "--tail", "40", self.name)
        return (out.stdout + out.stderr).strip()

    def stop(self):
        if self.runtime:
            self._run("rm", "-f", self.name)

    # Endpoints the fake site talks to.
    def endpoint(self, name):
        return f"{self.realm_url}/protocol/openid-connect/{name}"

    def userinfo(self, token):
        status, info = _http("GET", self.endpoint("userinfo"), bearer=token)
        return info.get("preferred_username") if status == 200 and info else None


class FakeSite:
    """The CLI's side of an Astrona site, backed by Keycloak."""

    def __init__(self, keycloak):
        self.kc = keycloak
        self.lock = threading.Lock()
        self.requests = []  # what the CLI sent; never a token
        self.sessions = {}  # id -> {"lab", "username", "attempts"}
        self.verify = {}  # user code -> Keycloak's verification_uri_complete
        self._valid = {}  # sha256(token) -> username
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), self._handler())
        self.url = f"http://127.0.0.1:{self.server.server_address[1]}"
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)

    def start(self):
        self.thread.start()

    def stop(self):
        self.server.shutdown()
        self.server.server_close()

    def record(self, **entry):
        with self.lock:
            self.requests.append(entry)

    def account(self, header):
        """The Keycloak username behind an Authorization header, or None."""
        if not header or not header.startswith("Bearer "):
            return None
        token = header[len("Bearer "):]
        key = hashlib.sha256(token.encode()).hexdigest()
        with self.lock:
            if key in self._valid:
                return self._valid[key]
        username = self.kc.userinfo(token)
        if username:
            with self.lock:
                self._valid[key] = username
        return username

    def _handler(self):
        site = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def send_json(self, status, body):
                raw = json.dumps(body).encode()
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

            def body(self):
                length = int(self.headers.get("Content-Length") or 0)
                if not length:
                    return {}
                try:
                    return json.loads(self.rfile.read(length))
                except ValueError:
                    return None

            def do_GET(self):
                url = urllib.parse.urlsplit(self.path)
                if url.path == "/api/cli/config":
                    return self.send_json(200, {
                        "device_endpoint": "/api/cli/device",
                        "token_endpoint": "/api/cli/token",
                        "revocation_endpoint": "/api/cli/revoke",
                    })
                if url.path == "/cli/authorize":
                    code = urllib.parse.parse_qs(url.query).get("code", [""])[0]
                    target = site.verify.get(code) or f"{site.kc.realm_url}/device"
                    self.send_response(302)
                    self.send_header("Location", target)
                    self.end_headers()
                    return None
                if url.path.startswith("/labs/sessions/"):
                    raw = b"<!doctype html><title>Lab session</title><p>Astrona acceptance suite: lab page.</p>"
                    self.send_response(200)
                    self.send_header("Content-Type", "text/html")
                    self.send_header("Content-Length", str(len(raw)))
                    self.end_headers()
                    self.wfile.write(raw)
                    return None
                return self.send_json(404, {"statusMessage": "not found"})

            def do_POST(self):
                path = urllib.parse.urlsplit(self.path).path
                body = self.body()
                if body is None:
                    return self.send_json(400, {"statusMessage": "the body is not JSON"})
                if path == "/api/cli/device":
                    return self.device(body)
                if path == "/api/cli/token":
                    return self.token(body)
                if path == "/api/cli/revoke":
                    return self.revoke(body)
                if path == "/api/cli/lab-sessions" or SESSION_PATH.match(path):
                    username = site.account(self.headers.get("Authorization"))
                    if not username:
                        site.record(path=path, account=None)
                        return self.send_json(401, {"statusMessage": "sign in again"})
                    if path == "/api/cli/lab-sessions":
                        return self.create_session(body, username)
                    match = SESSION_PATH.match(path)
                    return self.session_action(match.group(1), match.group(2), body, username)
                return self.send_json(404, {"statusMessage": "not found"})

            def device(self, body):
                status, answer = _http("POST", site.kc.endpoint("auth/device"), form={
                    "client_id": CLIENT_ID, "scope": "openid",
                })
                site.record(path="/api/cli/device", device_name=body.get("device_name"), status=status)
                if status != 200:
                    return self.send_json(502, {"statusMessage": f"Keycloak answered HTTP {status}"})
                code = answer["user_code"]
                site.verify[code] = answer["verification_uri_complete"]
                return self.send_json(200, {
                    "device_code": answer["device_code"],
                    "user_code": code,
                    "verification_uri": f"{site.url}/cli/authorize",
                    "verification_uri_complete": f"{site.url}/cli/authorize?code={urllib.parse.quote(code)}",
                    "expires_in": answer.get("expires_in", 600),
                    "interval": answer.get("interval", 5),
                })

            def token(self, body):
                grant = body.get("grant_type")
                if grant == "device_code":
                    form = {"grant_type": "urn:ietf:params:oauth:grant-type:device_code",
                            "device_code": body.get("device_code", "")}
                elif grant == "refresh_token":
                    form = {"grant_type": "refresh_token", "refresh_token": body.get("refresh_token", "")}
                else:
                    return self.send_json(400, {"error": "unsupported_grant_type"})
                form["client_id"] = CLIENT_ID
                status, answer = _http("POST", site.kc.endpoint("token"), form=form)
                answer = answer or {}
                if status != 200:
                    site.record(path="/api/cli/token", grant=grant, error=answer.get("error"))
                    return self.send_json(status, {
                        "error": answer.get("error", "server_error"),
                        "error_description": answer.get("error_description", ""),
                    })
                username = site.kc.userinfo(answer["access_token"])
                site.record(path="/api/cli/token", grant=grant, account=username)
                return self.send_json(200, {
                    "access_token": answer["access_token"],
                    "refresh_token": answer["refresh_token"],
                    "expires_in": answer.get("expires_in", 300),
                    "token_type": "Bearer",
                    "username": username or "",
                    "device_id": "",
                })

            def revoke(self, body):
                status, _ = _http("POST", site.kc.endpoint("revoke"), form={
                    "client_id": CLIENT_ID, "token": body.get("refresh_token", ""),
                    "token_type_hint": "refresh_token",
                })
                site.record(path="/api/cli/revoke", status=status)
                return self.send_json(200 if status == 200 else 502, {})

            def create_session(self, body, username):
                lab = body.get("lab", "")
                site.record(path="/api/cli/lab-sessions", account=username, lab=lab, kind=body.get("kind"))
                sid = str(uuid.uuid4())
                with site.lock:
                    site.sessions[sid] = {"lab": lab, "username": username, "attempts": 0}
                return self.send_json(201, {
                    "id": sid,
                    "token": secrets.token_urlsafe(32),
                    "lab": lab,
                    "username": username,
                    "expires_at": _rfc3339(_now() + timedelta(hours=2)),
                    "url": f"{site.url}/labs/sessions/{sid}",
                    "max_minutes": 120,
                    "kind": body.get("kind") or "lab",
                })

            def session_action(self, sid, action, body, username):
                with site.lock:
                    session = site.sessions.get(sid)
                if not session or session["username"] != username:
                    site.record(path=f"/api/cli/lab-sessions/{{id}}/{action}", account=username, session=None)
                    return self.send_json(404, {"statusMessage": "no such lab session"})
                entry = {"path": f"/api/cli/lab-sessions/{{id}}/{action}", "account": username, "lab": session["lab"]}
                if action == "results":
                    result = body.get("result")
                    passed = bool(result.get("passed")) if isinstance(result, dict) else False
                    with site.lock:
                        session["attempts"] += 1
                        attempt = session["attempts"]
                    site.record(**entry, passed=passed)
                    return self.send_json(200, {
                        "id": sid, "attempt": attempt, "passed": passed,
                        "finished_at": _rfc3339(_now()), "url": f"{site.url}/labs/sessions/{sid}",
                    })
                site.record(**entry)
                if action == "usage":
                    return self.send_json(200, {"accepted": len(body.get("samples") or [])})
                if action == "renew":
                    return self.send_json(409, {"statusMessage": "only a playground can be renewed"})
                return self.send_json(200, {})

        return Handler


@library(scope="GLOBAL")
class SignInStack:
    def __init__(self):
        self.keycloak = None
        self.site = None

    @keyword
    def start_sign_in_stack(self, timeout=240):
        """Start Keycloak with a fresh test user and the fake site; return the site's URL."""
        self.keycloak = Keycloak()
        self.keycloak.start(timeout)
        self.site = FakeSite(self.keycloak)
        self.site.start()
        logger.info(f"Fake Astrona site on {self.site.url}")
        return self.site.url

    @keyword
    def stop_sign_in_stack(self):
        """Stop the fake site and remove the Keycloak container."""
        if self.site:
            self.site.stop()
            self.site = None
        if self.keycloak:
            self.keycloak.stop()
            self.keycloak = None

    @keyword
    def test_username(self):
        return USERNAME

    @keyword
    def wait_for_sign_in_link(self, path, timeout=60):
        """Read `astrona login --headless` output at path until it prints its link and code."""
        deadline = time.monotonic() + float(timeout)
        text = ""
        while time.monotonic() < deadline:
            with open(path, encoding="utf-8", errors="replace") as f:
                text = f.read()
            link, code = LINK.search(text), CODE.search(text)
            if link and code:
                return link.group(1), code.group(1)
            time.sleep(0.5)
        raise AssertionError(f"astrona login printed no sign-in link within {timeout}s:\n{text}")

    @keyword
    def authorize_in_browser(self, link, code, timeout=60):
        """Open link, sign in on Keycloak as the test user and grant the CLI access.

        code is checked against the user code the fake site handed to Keycloak:
        the link must lead to this sign-in, not any other.
        """
        # Imported here so suites that never sign in don't need Playwright.
        from playwright.sync_api import Error, expect, sync_playwright

        parts = urllib.parse.urlsplit(link)
        if f"{parts.scheme}://{parts.netloc}" != self.site.url:
            raise AssertionError(f"astrona login printed {link}, not a link on the fake site {self.site.url}")
        if urllib.parse.parse_qs(parts.query).get("code") != [code]:
            raise AssertionError(f"astrona login's link {link} doesn't carry the code it printed, {code}")
        timeout_ms = float(timeout) * 1000
        with sync_playwright() as pw:
            browser = pw.chromium.launch()
            page = browser.new_page()
            page.set_default_timeout(timeout_ms)
            expect.set_options(timeout=timeout_ms)
            try:
                # The fake site sends the browser on to Keycloak with the user code
                # filled in, so Keycloak goes straight to its sign-in form.
                page.goto(link)
                expect(page).to_have_url(re.compile("^" + re.escape(self.keycloak.realm_url) + "/"))
                page.locator("#username").fill(USERNAME)
                page.locator("#password").fill(self.keycloak.user_password)
                page.locator("#kc-login").click()
                # Consent: the client asks for it, so the browser grants the CLI access.
                expect(page.locator("body")).to_contain_text("Do you grant these access privileges?")
                page.locator("#kc-login").click()
                expect(page.locator("body")).to_contain_text("Device Login Successful")
            except (Error, AssertionError) as err:
                shot = os.path.join(BuiltIn().get_variable_value("${OUTPUT_DIR}"), "login-failure.png")
                page.screenshot(path=shot, full_page=True)
                # Playwright's message names selectors, not typed values; masked anyway.
                message = str(err).replace(self.keycloak.user_password, "********")
                raise AssertionError(f"authorizing in the browser failed at {page.url} ({shot}): {message}") from None
            finally:
                browser.close()

    def _matching(self, path, **fields):
        with self.site.lock:
            seen = list(self.site.requests)
        hits = [r for r in seen if r.get("path") == path and all(r.get(k) == v for k, v in fields.items())]
        return hits, seen

    @keyword
    def site_should_have_received(self, path, **fields):
        """Fail unless the fake site recorded a request to path with every field given.

        A request to a lab-session path is recorded only with the Keycloak
        account its bearer token belongs to (account=None: rejected token).
        """
        hits, seen = self._matching(path, **fields)
        if not hits:
            raise AssertionError(f"the fake site got no {path} with {fields}; it got: {seen}")

    @keyword
    def site_should_not_have_received(self, path, **fields):
        hits, _ = self._matching(path, **fields)
        if hits:
            raise AssertionError(f"the fake site got {path} with {fields}: {hits}")

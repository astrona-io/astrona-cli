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
  the suite can assert on it. It checks lab-session and guest-run bodies the
  way astrona-web and astrona-svc-labs do and answers with their status codes
  (201 new guest run, 200 replay, 422 invalid, 409 a result after the attempt
  ended); a suite can make it fail the next request to a path with a 5xx.
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
SESSION_PATH = re.compile(r"^/api/cli/lab-sessions/([A-Za-z0-9-]+)/(results|progress|usage|stop|renew|pause|resume|start)$")
GUEST_RUNS = "/api/cli/guest-runs"

# What the website and the labs service accept, copied from astrona-web
# (server/api/cli/guest-runs.post.ts, server/api/cli/lab-sessions/**) and
# astrona-svc-labs (app/api/schemas.py). Keep them in step when either changes.
BEARER = re.compile(r"Bearer [A-Za-z0-9._~+/=-]+")
SESSION_ID = re.compile(r"[0-9a-fA-F-]{36}")
SESSION_LAB = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]{0,63}(/[A-Za-z0-9][A-Za-z0-9._-]{0,63}){0,6}")
RESULT_KEY = re.compile(r"[A-Za-z0-9._:-]{1,64}")
MAX_RESULT_CHECKS = 200
GUEST_MAX_BODY = 2048
GUEST_MAX_SECONDS = 8 * 60 * 60
_ISO = re.compile(r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})")
GUEST_FIELDS = {
    "install_id": re.compile(r"[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}"),
    "kind": re.compile(r"lab|playground"),
    "lab": re.compile(r"(?!.*\.\.)[A-Za-z0-9][A-Za-z0-9._/-]{0,199}"),
    "started_at": _ISO,
    "finished_at": _ISO,
    "end_reason": re.compile(r"[a-z][a-z0-9_]{0,31}"),
    "idempotency_key": re.compile(r"[A-Za-z0-9._:-]{1,128}"),
    "cli_version": re.compile(r"[A-Za-z0-9.+_-]{1,64}"),
}


def _free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _now():
    return datetime.now(timezone.utc)


def _rfc3339(t):
    return t.strftime("%Y-%m-%dT%H:%M:%SZ")


def _parse_time(text):
    """An RFC 3339 time as the CLI sends it; None when it is not one."""
    try:
        # Go sends up to nine fraction digits; older Pythons read at most six.
        text = re.sub(r"(\.[0-9]{6})[0-9]+", r"\1", text.replace("Z", "+00:00"))
        t = datetime.fromisoformat(text)
    except (AttributeError, TypeError, ValueError):
        return None
    return t if t.tzinfo else None


def _guest_run_error(body):
    """Why the website would refuse this guest run report (422), or None."""
    if not isinstance(body, dict):
        return "The report must be an object"
    for name, pattern in GUEST_FIELDS.items():
        value = body.get(name)
        if not isinstance(value, str) or not pattern.fullmatch(value):
            return f"Invalid {name}"
    used = body.get("used_seconds")
    if isinstance(used, bool) or not isinstance(used, int) or not 0 <= used <= GUEST_MAX_SECONDS:
        return "Invalid used_seconds"
    start, finish = _parse_time(body["started_at"]), _parse_time(body["finished_at"])
    if start is None or finish is None or finish < start:
        return "finished_at is before started_at"
    if finish > _now() + timedelta(minutes=5):
        return "finished_at is in the future"
    return None


def _session_error(body):
    """Why labs would refuse this lab session request (422), or None."""
    lab = body.get("lab")
    if not isinstance(lab, str) or len(lab) > 200 or not SESSION_LAB.fullmatch(lab):
        return "lab must be a catalog name such as ATS000/section-010/lab-01"
    if body.get("kind", "lab") not in ("lab", "playground"):
        return "kind must be lab or playground"
    limit = body.get("time_limit_minutes")
    if limit is not None and (isinstance(limit, bool) or not isinstance(limit, int) or not 5 <= limit <= 1440):
        return "time_limit_minutes must be 5 to 1440"
    return None


def _result_error(body):
    """Why labs would refuse this graded result (422), or None."""
    result = body.get("result")
    if not isinstance(result, dict):
        return "result is required"
    if not isinstance(result.get("lab"), str) or not isinstance(result.get("pass"), bool):
        return "result needs lab and pass"
    checks = result.get("checks")
    if not isinstance(checks, list) or len(checks) > MAX_RESULT_CHECKS:
        return "result.checks must be a list"
    for check in checks:
        if not isinstance(check, dict) or not isinstance(check.get("name"), str) or not isinstance(check.get("pass"), bool):
            return "every check needs name and pass"
    key = body.get("idempotency_key")
    if key is not None and (not isinstance(key, str) or not RESULT_KEY.fullmatch(key)):
        return "idempotency_key is invalid"
    at = body.get("at")
    if at is not None and _parse_time(at) is None:
        return "at is not a time"
    if body.get("usage") is not None and not isinstance(body["usage"], dict):
        return "usage must be an object"
    return None


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
        self.sessions = {}  # id -> {"lab", "username", "kind", "attempts", "results", "finished"}
        self.guest_runs = {}  # idempotency key -> the answer labs gave
        self.failures = {}  # path -> statuses to answer its next requests with
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

    def take_failure(self, path):
        """The status the next request to path must fail with, or None."""
        with self.lock:
            queued = self.failures.get(path)
            return queued.pop(0) if queued else None

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

            def raw_body(self):
                length = int(self.headers.get("Content-Length") or 0)
                return self.rfile.read(length) if length else b""

            def body(self, raw):
                if not raw:
                    return {}
                try:
                    return json.loads(raw)
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
                raw = self.raw_body()
                if path == GUEST_RUNS:
                    return self.guest_run(raw)
                body = self.body(raw)
                if body is None:
                    return self.send_json(400, {"statusMessage": "the body is not JSON"})
                if path == "/api/cli/device":
                    return self.device(body)
                if path == "/api/cli/token":
                    return self.token(body)
                if path == "/api/cli/revoke":
                    return self.revoke(body)
                if path == "/api/cli/lab-sessions" or SESSION_PATH.match(path):
                    match = SESSION_PATH.match(path)
                    recorded = f"/api/cli/lab-sessions/{{id}}/{match.group(2)}" if match else path
                    header = self.headers.get("Authorization") or ""
                    username = site.account(header) if BEARER.fullmatch(header) else None
                    if not username:
                        site.record(path=recorded, account=None, status=401)
                        return self.send_json(401, {"statusMessage": "Sign in with `astrona login` first"})
                    if match and not SESSION_ID.fullmatch(match.group(1)):
                        site.record(path=recorded, account=username, status=404)
                        return self.send_json(404, {"statusMessage": "Session not found"})
                    failure = site.take_failure(recorded)
                    if failure:
                        site.record(path=recorded, account=username, status=failure)
                        return self.send_json(failure, {"statusMessage": "Lab sessions are unavailable right now"})
                    if not match:
                        return self.create_session(body, username)
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

            def guest_run(self, raw):
                """POST /api/cli/guest-runs: no sign-in; 201 a new report, 200 a replayed key."""
                entry = {"path": GUEST_RUNS, "authorized": bool(self.headers.get("Authorization"))}
                if len(raw) > GUEST_MAX_BODY:
                    site.record(**entry, status=413, valid=False)
                    return self.send_json(413, {"statusMessage": "The report is too large"})
                try:
                    body = json.loads(raw or b"")
                except ValueError:
                    site.record(**entry, status=400, valid=False)
                    return self.send_json(400, {"statusMessage": "The report must be JSON"})
                if isinstance(body, dict):
                    entry.update({name: body.get(name) for name in (*GUEST_FIELDS, "used_seconds")})
                error = _guest_run_error(body)
                if error:
                    site.record(**entry, status=422, valid=False, error=error)
                    return self.send_json(422, {"statusCode": 422, "statusMessage": error})
                failure = site.take_failure(GUEST_RUNS)
                if failure:
                    site.record(**entry, status=failure, valid=True)
                    return self.send_json(failure, {"statusMessage": "Lab time is unavailable right now"})
                with site.lock:
                    stored = site.guest_runs.get(body["idempotency_key"])
                    status = 200 if stored else 201
                    if not stored:
                        stored = {"id": str(uuid.uuid4()), "kind": body["kind"], "lab": body["lab"],
                                  "used_seconds": body["used_seconds"]}
                        site.guest_runs[body["idempotency_key"]] = stored
                site.record(**entry, status=status, valid=True)
                return self.send_json(status, stored)

            def create_session(self, body, username):
                lab, kind = body.get("lab"), body.get("kind", "lab")
                entry = {"path": "/api/cli/lab-sessions", "account": username, "lab": lab, "kind": body.get("kind"),
                         "time_limit_minutes": body.get("time_limit_minutes")}
                error = _session_error(body)
                if error:
                    site.record(**entry, status=422, error=error)
                    return self.send_json(422, {"statusMessage": error})
                sid = str(uuid.uuid4())
                with site.lock:
                    site.sessions[sid] = {"lab": lab, "username": username, "kind": kind, "attempts": 0,
                                          "results": {}, "finished": False}
                # The website relays labs' 201 body with its own 200.
                site.record(**entry, status=200)
                answer = {
                    "id": sid,
                    "token": secrets.token_urlsafe(32),
                    "lab": lab,
                    "username": username,
                    "expires_at": _rfc3339(_now() + timedelta(hours=2)),
                    "url": f"{site.url}/labs/sessions/{sid}",
                    "max_minutes": 120,
                    "kind": kind,
                }
                if kind == "playground":
                    minutes = body.get("time_limit_minutes") or 60
                    answer["deadline_at"] = _rfc3339(_now() + timedelta(minutes=minutes))
                return self.send_json(200, answer)

            def session_action(self, sid, action, body, username):
                with site.lock:
                    session = site.sessions.get(sid)
                if not session or session["username"] != username:
                    site.record(path=f"/api/cli/lab-sessions/{{id}}/{action}", account=username, session=None)
                    return self.send_json(404, {"statusMessage": "no such lab session"})
                entry = {"path": f"/api/cli/lab-sessions/{{id}}/{action}", "account": username, "lab": session["lab"]}
                if action == "results":
                    return self.result(sid, session, body, entry)
                if action == "stop":
                    # The website passes on only these, "destroyed" unless it is "time_limit".
                    entry.update(reason="time_limit" if body.get("reason") == "time_limit" else "destroyed",
                                 sent_reason=body.get("reason"), at=isinstance(body.get("at"), str))
                site.record(**entry, status=200 if action != "renew" else 409)
                if action == "usage":
                    return self.send_json(200, {"accepted": len(body.get("samples") or [])})
                if action == "renew":
                    return self.send_json(409, {"statusMessage": "only a playground can be renewed"})
                return self.send_json(200, {})

            def result(self, sid, session, body, entry):
                """A graded result: 200 (the website's status for labs' 201 and its replay),
                422 invalid, 409 a new result once a lab's attempt has ended."""
                key = body.get("idempotency_key")
                result = body.get("result")
                entry.update(passed=result.get("pass") if isinstance(result, dict) else None,
                             keyed=key is not None, at=isinstance(body.get("at"), str))
                error = _result_error(body)
                if error:
                    site.record(**entry, status=422, error=error)
                    return self.send_json(422, {"statusMessage": error})
                with site.lock:
                    stored = session["results"].get(key) if key is not None else None
                    if stored is None and session["finished"]:
                        conflict = True
                    else:
                        conflict = False
                        if stored is None:
                            session["attempts"] += 1
                            stored = {
                                "id": sid, "attempt": session["attempts"], "passed": result["pass"],
                                "finished_at": _rfc3339(_now()), "url": f"{site.url}/labs/sessions/{sid}",
                            }
                            if key is not None:
                                session["results"][key] = stored
                            # Every submit ends a lab's attempt; a playground only on a pass.
                            session["finished"] = session["kind"] == "lab" or result["pass"]
                            replay = False
                        else:
                            replay = True
                if conflict:
                    site.record(**entry, status=409)
                    return self.send_json(409, {"statusMessage": "This attempt has already ended"})
                site.record(**entry, status=200, replay=replay)
                return self.send_json(200, stored)

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

    @keyword
    def site_fails_next_request(self, path, status=503):
        """Answer the next request to path (as recorded, e.g. /api/cli/guest-runs or
        /api/cli/lab-sessions/{id}/results) with status, a server error by default."""
        with self.site.lock:
            self.site.failures.setdefault(path, []).append(int(status))

    @keyword
    def site_request_count(self, path):
        """How many requests to path the fake site has recorded."""
        return len(self._matching(path)[0])

    @keyword
    def wait_until_site_has_received(self, path, timeout=60, **fields):
        """Site Should Have Received, retried until timeout (seconds): for what the
        CLI sends from its detached background sender."""
        deadline = time.monotonic() + float(timeout)
        while True:
            hits, seen = self._matching(path, **fields)
            if hits:
                return hits[-1]
            if time.monotonic() >= deadline:
                raise AssertionError(f"the fake site got no {path} with {fields} within {timeout}s; it got: {seen}")
            time.sleep(0.5)

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

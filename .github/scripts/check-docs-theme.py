"""Browser checks for a built docs site, served under a mike-style /dev/ prefix."""

import argparse
import json
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from threading import Thread

from playwright.sync_api import sync_playwright


class DocsHandler(SimpleHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/versions.json":
            payload = json.dumps([
                {"version": "dev", "title": "dev", "aliases": []},
                {"version": "1.0", "title": "1.0", "aliases": ["latest"]},
            ]).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(payload)
            return
        if self.path.startswith("/dev/"):
            self.path = self.path[len("/dev"):]
        super().do_GET()

    def log_message(self, *_):
        pass


def check_page(page, url, screenshot):
    page.goto(url)
    page.locator(".md-content h1").wait_for()
    assert page.locator(".md-content h1").count() == 1, "Expected one page heading"
    assert page.evaluate("document.documentElement.scrollWidth <= innerWidth"), f"Page overflow: {url}"
    assert page.locator(".md-content").inner_text().strip()
    page.screenshot(path=str(screenshot), full_page=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--site-dir", required=True, type=Path)
    parser.add_argument("--screenshots", required=True, type=Path)
    parser.add_argument("--browser", help="Optional installed Chromium executable")
    args = parser.parse_args()
    args.screenshots.mkdir(parents=True, exist_ok=True)
    server = ThreadingHTTPServer(("127.0.0.1", 0), partial(DocsHandler, directory=str(args.site_dir.resolve())))
    worker = Thread(target=server.serve_forever, daemon=True)
    worker.start()
    base = f"http://127.0.0.1:{server.server_port}/dev/"
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(executable_path=args.browser)
            context = browser.new_context(viewport={"width": 1440, "height": 1000})
            page = context.new_page()
            errors = []
            page.on("pageerror", lambda error: errors.append(str(error)))
            check_page(page, base, args.screenshots / "home-desktop.png")
            page.locator(".astro-hero").screenshot(path=str(args.screenshots / "hero-preview.png"))
            assert page.locator(".md-version__current").inner_text().strip() == "dev"
            assert page.locator(".grid.cards li").count() == 4
            orbit = page.locator(".astro-orbit")
            assert orbit.evaluate("el => getComputedStyle(el).animationName") != "none"
            page.get_by_role("button", name="Pause animations").click()
            assert orbit.evaluate("el => getComputedStyle(el).animationPlayState") == "paused"
            page.reload()
            assert page.get_by_role("button", name="Play animations").is_visible()
            page.get_by_role("button", name="Play animations").click()
            page.get_by_role("link", name="Install the CLI ↗").click()
            assert page.url.startswith(base + "getting-started/installation/")
            page.locator(".md-code__button[data-md-type='copy']").first.wait_for(state="attached")
            check_page(page, base + "getting-started/installation/", args.screenshots / "installation-desktop.png")
            check_page(page, base + "reference/cli/astrona_run/", args.screenshots / "reference-desktop.png")
            search = page.locator(".md-search__input")
            search.fill("keep-context")
            page.locator(".md-search-result__item").first.wait_for()
            page.keyboard.press("Escape")
            # Mobile navigation remains Material's drawer, not a custom replacement.
            page.set_viewport_size({"width": 375, "height": 812})
            check_page(page, base, args.screenshots / "home-mobile.png")
            page.locator(".md-header label[for='__drawer']").click()
            assert page.locator("#__drawer").is_checked()
            page.locator(".md-sidebar--primary label").filter(has_text="Getting Started").first.click()
            page.locator(".md-sidebar--primary").get_by_role("link", name="Installation", exact=True).click()
            assert "/getting-started/installation/" in page.url
            check_page(page, base + "getting-started/installation/", args.screenshots / "installation-mobile.png")
            check_page(page, base + "reference/cli/astrona_run/", args.screenshots / "reference-mobile.png")
            page.emulate_media(reduced_motion="reduce")
            check_page(page, base, args.screenshots / "home-reduced-motion.png")
            assert page.locator(".astro-orbit").evaluate("el => getComputedStyle(el).animationName") == "none"
            assert page.get_by_role("button", name="Reduced motion enabled").is_disabled()
            assert not errors, errors
            context.close()
            # No JS and no remote fonts must still leave a usable document.
            offline = browser.new_context(java_script_enabled=False, viewport={"width": 375, "height": 812})
            offline.route("https://fonts.googleapis.com/**", lambda route: route.abort())
            static_page = offline.new_page()
            check_page(static_page, base, args.screenshots / "home-no-js.png")
            assert static_page.get_by_role("link", name="Install the CLI ↗").is_visible()
            assert not static_page.locator(".astro-motion").is_visible()
            offline.close()
            browser.close()
    finally:
        server.shutdown()
        server.server_close()
        worker.join()
    print("Docs checks passed: layout, navigation, search, copy controls, motion, no-JS, /dev/ links.")


if __name__ == "__main__":
    main()

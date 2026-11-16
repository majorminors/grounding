"""Render web pages with crawl4ai for grounding, on a Unix socket.

grounding's "auto" renderer climbs to a headless browser when a page
needs one. That browser can be crawl4ai
(github.com/unclecode/crawl4ai), run as a service of its own beside
grounding, under a systemd network filter (IPAddressDeny of loopback,
private and mesh ranges), so the page's JavaScript cannot reach the
host's loopback services, private networks or a mesh (grounding README
"The browser renderer: crawl4ai").

crawl4ai's own server lives only in its Docker image, with Redis and
supervisord. grounding needs one thing of it, "render this URL and give
me the HTML", so this file serves that from crawl4ai's library: the
subset of the server's API grounding's client already speaks.

  GET  /health  ->  {"status": "ok"}
  POST /crawl   {"urls": [URL], ...}  ->  {"success": true, "results": [
                   {"success", "status_code", "html", "url",
                    "redirected_url", "error_message"}]}

It listens on a Unix socket, never a port: a unit under the filter
cannot accept TCP on loopback, and the socket's directory group says
who may connect, so there is no token. The socket is made 0660.

This product includes software developed by UncleCode
(https://x.com/unclecode) as part of the Crawl4AI project
(https://github.com/unclecode/crawl4ai).

Usage: render_pages_with_crawl4ai_on_a_unix_socket.py SOCKET_PATH
"""

import asyncio
import os
import sys
from urllib.parse import urlsplit

from aiohttp import web

# Pages rendered at once: each is a browser tab, and the unit's memory
# should be capped (2G is enough for this).
PAGES_RENDERED_AT_ONCE = 2
# How long a page may take to load, in milliseconds.
PAGE_LOAD_TIME_LIMIT_MILLISECONDS = 30_000
# The most HTML handed back; grounding refuses a page over 5 MB anyway.
LARGEST_HTML_RETURNED_IN_CHARACTERS = 10_000_000
# The most a request body may be: one URL and a few settings.
LARGEST_REQUEST_BODY_IN_BYTES = 64 * 1024


class CrawlerWithItsOwnBrowser:
    """crawl4ai's AsyncWebCrawler, one browser kept open for every
    request, imported only when the service starts so tests can stand
    in for it."""

    def __init__(self):
        self.crawler = None

    async def start(self):
        from crawl4ai import AsyncWebCrawler, BrowserConfig

        self.crawler = AsyncWebCrawler(config=BrowserConfig(headless=True))
        await self.crawler.start()

    async def close(self):
        if self.crawler is not None:
            await self.crawler.close()

    async def render(self, url):
        from crawl4ai import CacheMode, CrawlerRunConfig

        # grounding caches; crawl4ai's cache would outlive grounding's.
        result = await self.crawler.arun(
            url,
            config=CrawlerRunConfig(
                cache_mode=CacheMode.BYPASS,
                page_timeout=PAGE_LOAD_TIME_LIMIT_MILLISECONDS,
            ),
        )
        return {
            "success": bool(result.success),
            "status_code": result.status_code,
            "html": result.html or "",
            "url": url,
            "redirected_url": getattr(result, "redirected_url", None) or url,
            "error_message": result.error_message or "",
        }


def url_or_refusal(url):
    """The URL if it is http or https with a host, else None. grounding
    has already checked it is public; the network filter is the guard."""
    if not isinstance(url, str):
        return None
    parts = urlsplit(url)
    if parts.scheme not in ("http", "https") or not parts.hostname:
        return None
    return url


def make_application(crawler):
    pages_rendering = asyncio.Semaphore(PAGES_RENDERED_AT_ONCE)

    async def health(request):
        return web.json_response({"status": "ok"})

    async def crawl(request):
        try:
            body = await request.json()
        except Exception:
            return web.json_response({"success": False, "error": "the body is not JSON"}, status=400)
        urls = body.get("urls") if isinstance(body, dict) else None
        if not isinstance(urls, list) or len(urls) != 1 or url_or_refusal(urls[0]) is None:
            return web.json_response(
                {"success": False, "error": "send exactly one http or https URL in urls"}, status=400
            )
        url = urls[0]
        async with pages_rendering:
            try:
                result = await crawler.render(url)
            except Exception as error:  # a page that broke the browser
                result = {
                    "success": False,
                    "status_code": None,
                    "html": "",
                    "url": url,
                    "redirected_url": url,
                    "error_message": f"the browser failed: {type(error).__name__}: {str(error)[:300]}",
                }
        if len(result["html"]) > LARGEST_HTML_RETURNED_IN_CHARACTERS:
            result["html"] = result["html"][:LARGEST_HTML_RETURNED_IN_CHARACTERS]
        return web.json_response({"success": True, "results": [result]})

    application = web.Application(client_max_size=LARGEST_REQUEST_BODY_IN_BYTES)
    application.router.add_get("/health", health)
    application.router.add_post("/crawl", crawl)

    async def start_the_browser(application):
        await crawler.start()

    async def close_the_browser(application):
        await crawler.close()

    application.on_startup.append(start_the_browser)
    application.on_cleanup.append(close_the_browser)
    return application


async def serve_on_unix_socket(application, socket_path):
    """Serve until stopped, the socket made 0660 so its group connects."""
    runner = web.AppRunner(application)
    await runner.setup()
    if os.path.exists(socket_path):
        os.unlink(socket_path)  # left by a run that did not end cleanly
    site = web.UnixSite(runner, socket_path)
    await site.start()
    os.chmod(socket_path, 0o660)
    print(f"rendering pages with crawl4ai on {socket_path}", file=sys.stderr, flush=True)
    try:
        await asyncio.Event().wait()
    finally:
        await runner.cleanup()


def main(arguments):
    if len(arguments) != 2:
        print("usage: render_pages_with_crawl4ai_on_a_unix_socket.py SOCKET_PATH", file=sys.stderr)
        return 2
    try:
        asyncio.run(serve_on_unix_socket(make_application(CrawlerWithItsOwnBrowser()), arguments[1]))
    except KeyboardInterrupt:
        pass
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))

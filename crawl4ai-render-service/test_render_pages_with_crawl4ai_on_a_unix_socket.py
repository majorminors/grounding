"""Tests for the render service, with a stand-in for crawl4ai, over a
real Unix socket. Needs aiohttp only:
    python3 -m unittest crawl4ai-render-service/test_render_pages_with_crawl4ai_on_a_unix_socket.py
"""

import asyncio
import json
import os
import stat
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import aiohttp  # noqa: E402

import render_pages_with_crawl4ai_on_a_unix_socket as service  # noqa: E402


class StandInCrawler:
    def __init__(self, fail_on=None):
        self.started = self.closed = False
        self.rendered = []
        self.fail_on = fail_on
        self.rendering_now = self.most_at_once = 0

    async def start(self):
        self.started = True

    async def close(self):
        self.closed = True

    async def render(self, url):
        self.rendering_now += 1
        self.most_at_once = max(self.most_at_once, self.rendering_now)
        await asyncio.sleep(0.05)
        self.rendering_now -= 1
        if url == self.fail_on:
            raise RuntimeError("Target page crashed")
        self.rendered.append(url)
        return {"success": True, "status_code": 200, "html": "<p>rendered</p>",
                "url": url, "redirected_url": url + "#landed", "error_message": ""}


class RenderServiceOverAUnixSocket(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        # A short path: macOS limits a socket's path to 104 bytes.
        self.directory = tempfile.mkdtemp(prefix="c4a")
        self.socket_path = os.path.join(self.directory, "render.sock")
        self.crawler = StandInCrawler(fail_on="https://crash.example/")
        self.server = asyncio.create_task(
            service.serve_on_unix_socket(service.make_application(self.crawler), self.socket_path))
        for _ in range(100):
            if os.path.exists(self.socket_path):
                break
            await asyncio.sleep(0.02)
        self.session = aiohttp.ClientSession(connector=aiohttp.UnixConnector(path=self.socket_path))

    async def asyncTearDown(self):
        await self.session.close()
        self.server.cancel()
        try:
            await self.server
        except asyncio.CancelledError:
            pass
        os.rmdir(self.directory) if not os.listdir(self.directory) else None

    async def post(self, body):
        async with self.session.post("http://render/crawl", data=json.dumps(body)) as response:
            return response.status, await response.json()

    async def test_the_socket_is_0660_and_the_browser_started(self):
        self.assertEqual(stat.S_IMODE(os.stat(self.socket_path).st_mode), 0o660)
        self.assertTrue(self.crawler.started)

    async def test_health_answers(self):
        async with self.session.get("http://render/health") as response:
            self.assertEqual(await response.json(), {"status": "ok"})

    async def test_crawl_answers_in_the_shape_groundings_client_reads(self):
        status, answer = await self.post({"urls": ["https://a.example/page"], "crawler_config": {}})
        self.assertEqual(status, 200)
        result = answer["results"][0]
        self.assertTrue(answer["success"] and result["success"])
        self.assertEqual((result["status_code"], result["html"], result["redirected_url"]),
                         (200, "<p>rendered</p>", "https://a.example/page#landed"))

    async def test_a_crash_is_an_unsuccessful_result_not_a_dead_service(self):
        status, answer = await self.post({"urls": ["https://crash.example/"]})
        self.assertEqual(status, 200)
        self.assertFalse(answer["results"][0]["success"])
        self.assertIn("Target page crashed", answer["results"][0]["error_message"])
        status, answer = await self.post({"urls": ["https://a.example/after"]})
        self.assertTrue(answer["results"][0]["success"])

    async def test_refuses_what_is_not_one_http_url(self):
        for body in ({"urls": ["file:///etc/passwd"]}, {"urls": ["javascript:alert(1)"]},
                     {"urls": ["https://a.example/", "https://b.example/"]}, {"urls": []}, {"nope": 1}):
            status, answer = await self.post(body)
            self.assertEqual(status, 400, body)
            self.assertFalse(answer["success"])
        self.assertEqual(self.crawler.rendered, [])

    async def test_renders_at_most_two_pages_at_once(self):
        await asyncio.gather(*(self.post({"urls": [f"https://a.example/{n}"]}) for n in range(6)))
        self.assertEqual(self.crawler.most_at_once, service.PAGES_RENDERED_AT_ONCE)

    async def test_html_handed_back_is_capped(self):
        original = service.LARGEST_HTML_RETURNED_IN_CHARACTERS
        service.LARGEST_HTML_RETURNED_IN_CHARACTERS = 5
        try:
            _, answer = await self.post({"urls": ["https://a.example/"]})
            self.assertEqual(answer["results"][0]["html"], "<p>re")
        finally:
            service.LARGEST_HTML_RETURNED_IN_CHARACTERS = original


if __name__ == "__main__":
    unittest.main()

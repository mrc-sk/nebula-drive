#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""邀请码管理面板 —— 纯页面插件的静态 HTTP 服务（协议 v2）。

只做一件事：把同目录的 index.html 提供给宿主反代的 iframe。
页面本身是同源的（/ext/<plugin>/* 由宿主反代，浏览器 origin 仍是站点域名），
因此页面 JS 直接读取 localStorage 的 nebula_token 并调用宿主 /api/admin/* 接口，
鉴权由宿主 Auth+AdminOnly 中间件兜底；插件本身不落库、不存任何密钥、
不提供任何未鉴权的写入口。

这里刻意只暴露 index.html，不开放目录列举，避免把 server.py / 契约文件读出去。
"""
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))
HTML_PATH = os.path.join(HERE, "index.html")


class Handler(BaseHTTPRequestHandler):
    def _send(self, code, body, ctype):
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        # 插件页面每次都取最新，避免改版后被缓存
        self.send_header("Cache-Control", "no-store, no-cache, must-revalidate")
        self.send_header("X-Content-Type-Options", "nosniff")
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(body)

    def _page(self):
        path = self.path.split("?", 1)[0]
        if path in ("", "/", "/index.html", "/panel", "/panel/"):
            try:
                with open(HTML_PATH, "rb") as f:
                    self._send(200, f.read(), "text/html; charset=utf-8")
            except OSError:
                self._send(500, b"index.html missing", "text/plain; charset=utf-8")
        elif path == "/health":
            self._send(200, b"ok", "text/plain; charset=utf-8")
        else:
            self._send(404, b"not found", "text/plain; charset=utf-8")

    def do_GET(self):
        self._page()

    def do_HEAD(self):
        self._page()

    def log_message(self, *args):
        # 不打访问日志，避免污染宿主日志
        pass


def main():
    port = int(os.environ.get("NB_PLUGIN_PORT", "0"))
    host = os.environ.get("NB_PLUGIN_HOST", "127.0.0.1")
    if not port:
        raise SystemExit("NB_PLUGIN_PORT 未下发")
    ThreadingHTTPServer((host, port), Handler).serve_forever()


if __name__ == "__main__":
    main()

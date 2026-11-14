#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
NebulaDrive 本地小模型网关 —— OpenAI 兼容 /v1/chat/completions

设计约束：
  * 只监听 127.0.0.1，不对外暴露（工单站是唯一调用方）。
  * 模型常驻内存，启动时预热；llama_cpp 的 Llama 不是线程安全的，
    用一把锁把推理串行化，2 核小机器上反而更稳、不会 OOM。
  * 输入截断 + max_tokens 上限，防止超长工单撑爆 1024/1536 上下文，
    也防止单次请求把 CPU 占满导致网盘主站变慢。
  * 不依赖任何 Web 框架，只用 Python 标准库 + llama_cpp。
"""
import json
import os
import sys
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from llama_cpp import Llama

MODEL_PATH = os.environ.get("LLM_MODEL", "/opt/llm/model.gguf")
MODEL_NAME = os.environ.get("LLM_NAME", "qwen2.5-0.5b-instruct")
PORT = int(os.environ.get("LLM_PORT", "8083"))
HOST = "127.0.0.1"
N_CTX = 1536
N_THREADS = 2
MAX_INPUT_CHARS = 1500
MAX_TOKENS_CAP = 320

# 默认站点事实兜底：仅当调用方未提供 system 消息时前置注入。
# 事实由同目录 system.txt 提供（部署侧维护），网关本身保持通用 OpenAI 兼容。
DEFAULT_SYSTEM_FILE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "system.txt")
GUARD_FILE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "guard.txt")
SAFE_FALLBACK = (
    "关于账号与登录：本站公开注册已关闭，账号由管理员在后台创建；"
    "邮件服务未启用，没有自助找回密码入口，也没有客服电话或在线客服。"
    "忘记密码或需要账号，请直接联系本站点管理员在后台处理。"
)


def default_system():
    try:
        with open(DEFAULT_SYSTEM_FILE, encoding="utf-8") as f:
            return f.read().strip()[:MAX_INPUT_CHARS]
    except OSError:
        return ""


def guard_terms():
    """输出黑名单：每行一个诱导型短语，命中即判为编造。"""
    try:
        with open(GUARD_FILE, encoding="utf-8") as f:
            return [ln.strip() for ln in f if ln.strip() and not ln.startswith("#")]
    except OSError:
        return []


_llm = None
_lock = threading.Lock()


def get_llm():
    global _llm
    if _llm is None:
        _llm = Llama(
            model_path=MODEL_PATH,
            n_ctx=N_CTX,
            n_threads=N_THREADS,
            n_batch=256,
            verbose=False,
        )
    return _llm


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):
        sys.stderr.write("[llm] %s - %s\n" % (self.address_string(), fmt % args))

    def _json(self, code, obj):
        body = json.dumps(obj, ensure_ascii=False).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        p = self.path.rstrip("/")
        if p in ("/health", "/v1/health"):
            return self._json(200, {"ok": True, "model": MODEL_NAME,
                                    "loaded": _llm is not None, "backend": "llama.cpp"})
        if p == "/v1/models":
            return self._json(200, {"object": "list", "data": [
                {"id": MODEL_NAME, "object": "model", "owned_by": "local"}]})
        self._json(404, {"error": {"message": "not found"}})

    def do_POST(self):
        if self.path.rstrip("/") != "/v1/chat/completions":
            return self._json(404, {"error": {"message": "not found"}})
        try:
            n = int(self.headers.get("Content-Length", "0"))
            body = json.loads(self.rfile.read(n).decode("utf-8"))
        except Exception as e:
            return self._json(400, {"error": {"message": "bad request: %s" % e}})

        msgs = []
        for m in body.get("messages", []):
            role = m.get("role", "user")
            if role not in ("system", "user", "assistant"):
                role = "user"
            msgs.append({"role": role,
                         "content": str(m.get("content", ""))[:MAX_INPUT_CHARS]})
        if not msgs:
            msgs = [{"role": "user", "content": "你好"}]
        # 调用方没给 system 时，前置注入站点事实兜底，杜绝无约束自由发挥。
        if not any(m["role"] == "system" for m in msgs):
            guard = default_system()
            if guard:
                msgs.insert(0, {"role": "system", "content": guard})

        try:
            max_tokens = min(int(body.get("max_tokens", 256)), MAX_TOKENS_CAP)
        except (TypeError, ValueError):
            max_tokens = 256
        max_tokens = max(16, max_tokens)
        try:
            temperature = max(0.0, min(1.0, float(body.get("temperature", 0.4))))
        except (TypeError, ValueError):
            temperature = 0.4

        t0 = time.time()
        try:
            with _lock:
                out = get_llm().create_chat_completion(
                    messages=msgs, max_tokens=max_tokens,
                    temperature=temperature, top_p=0.9,
                )
        except Exception as e:
            return self._json(500, {"error": {"message": "推理失败: %s" % e}})

        text = out["choices"][0]["message"]["content"].strip()
        # 确定性输出护栏：命中黑名单即替换为安全兜底，杜绝小模型编造功能/联系方式。
        if any(g in text for g in guard_terms()):
            text = SAFE_FALLBACK
        usage = out.get("usage", {}) or {}
        resp = {
            "id": "chatcmpl-" + uuid.uuid4().hex[:16],
            "object": "chat.completion",
            "created": int(time.time()),
            "model": MODEL_NAME,
            "choices": [{
                "index": 0,
                "message": {"role": "assistant", "content": text},
                "finish_reason": "stop",
            }],
            "usage": {
                "prompt_tokens": usage.get("prompt_tokens", 0),
                "completion_tokens": usage.get("completion_tokens", 0),
                "total_tokens": usage.get("total_tokens", 0),
            },
        }
        sys.stderr.write("[llm] ok %.1fs in=%s out=%s\n" % (
            time.time() - t0, usage.get("prompt_tokens"), usage.get("completion_tokens")))
        self._json(200, resp)


def main():
    print("[llm] 预热加载模型: %s" % MODEL_PATH, flush=True)
    get_llm()
    print("[llm] ready on %s:%d  model=%s  ctx=%d threads=%d"
          % (HOST, PORT, MODEL_NAME, N_CTX, N_THREADS), flush=True)
    ThreadingHTTPServer((HOST, PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()

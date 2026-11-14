#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""敏感文件标记 —— NebulaDrive 官方插件（协议 v2）"""
import json
import os
import sys
import time

NAME = 'nd-sensitive-tag'
VERSION = '1.0.0'
HOOKS = ['onContentScan', 'onFileUploadAfter']
CONFIG = {}
DATA_DIR = os.environ.get("NEBULA_PLUGIN_DATA", os.path.dirname(os.path.abspath(__file__)))


def cfg(key, default):
    """读取宿主在 init 下发的配置（plugins.config），未配置时回退默认值。"""
    v = CONFIG.get(key, default)
    return default if v in (None, "") else v


def log(level, message):
    return {"level": level, "message": str(message)[:300]}


def emit(obj):
    sys.stdout.write(json.dumps(obj, ensure_ascii=False) + "\n")
    sys.stdout.flush()


# ---------------------------------------------------------------- 插件逻辑
DEFAULT_WORDS = ["机密", "内部", "私密", "身份证", "银行卡",
                 "password", "secret", "confidential"]


def on_hook(hook, ctx):
    if hook not in ("onContentScan", "onFileUploadAfter"):
        return {}
    text = str(ctx.get("fileName") or ctx.get("name") or ctx.get("content") or "").lower()
    words = cfg("keywords", None)
    words = [str(w) for w in words] if isinstance(words, list) else DEFAULT_WORDS
    hit = [w for w in words if str(w).lower() in text]
    if not hit:
        return {}
    return {"ctxPatch": {"sensitive": True, "tags": ["需复核"]},
            "log": [log("warn", "命中敏感关键词 %s，已标记待复核: %s"
                        % (",".join(hit[:5]), text[:60]))]}
# ---------------------------------------------------------------- /插件逻辑

def handle(hook, ctx):
    try:
        r = on_hook(hook, ctx or {}) or {}
        return r if isinstance(r, dict) else {}
    except Exception as e:
        # 插件异常绝不能拖垮宿主业务：吞掉异常，只回一条日志
        return {"log": [log("error", "插件异常: %s" % e)]}


def rpc(req):
    rid = req.get("id")
    method = req.get("method", "")
    params = req.get("params") or {}
    if method == "init":
        global CONFIG
        c = params.get("config")
        if isinstance(c, str):
            try:
                c = json.loads(c)
            except Exception:
                c = {}
        if isinstance(c, dict):
            CONFIG = c
        return {"jsonrpc": "2.0", "id": rid, "result": {
            "ok": True, "name": NAME, "version": VERSION, "hooks": HOOKS}}
    if method == "ping":
        return {"jsonrpc": "2.0", "id": rid, "result": {"ok": True}}
    if method == "fire":
        return {"jsonrpc": "2.0", "id": rid,
                "result": handle(params.get("hook", ""), params.get("ctx") or {})}
    if method == "shutdown":
        return {"jsonrpc": "2.0", "id": rid, "result": {"ok": True}}
    return {"jsonrpc": "2.0", "id": rid, "result": {}}


def main():
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
        except Exception:
            continue
        try:
            resp = rpc(req)
        except Exception as e:
            resp = {"jsonrpc": "2.0", "id": req.get("id"),
                    "error": {"code": -32603, "message": str(e)}}
        if resp is not None:
            emit(resp)
        if req.get("method") == "shutdown":
            break


if __name__ == "__main__":
    main()

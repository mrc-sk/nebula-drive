#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""上传安全守卫 —— NebulaDrive 官方插件（协议 v2）"""
import json
import os
import sys
import time

NAME = 'nd-upload-guard'
VERSION = '1.0.0'
HOOKS = ['onFileUploadBefore']
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
DEFAULT_BLOCK = [".exe", ".bat", ".cmd", ".scr", ".vbs", ".ps1", ".sh",
                 ".php", ".jsp", ".asp", ".dll", ".so", ".msi", ".com", ".pif"]


def on_hook(hook, ctx):
    if hook != "onFileUploadBefore":
        return {}
    name = str(ctx.get("fileName") or ctx.get("name") or "")
    low = name.lower()
    ext = os.path.splitext(low)[1]
    blocked = cfg("blockedExtensions", None)
    exts = ([str(x).lower() for x in blocked]
            if isinstance(blocked, list) else DEFAULT_BLOCK)
    if ext and ext in exts:
        return {"decision": "deny",
                "reason": "出于安全考虑，不允许上传 %s 类型的文件" % ext,
                "log": [log("warn", "拦截危险类型: %s" % name[:80])]}
    if ".." in name or "/" in name or "\\" in name or "\x00" in name:
        return {"decision": "deny", "reason": "文件名包含非法路径字符",
                "log": [log("warn", "拦截路径穿越文件名: %s" % name[:80])]}
    try:
        limit = int(cfg("maxBytes", 0))
        size = int(ctx.get("size") or ctx.get("fileSize") or 0)
        if limit > 0 and size > limit:
            return {"decision": "deny",
                    "reason": "文件超过单文件上限 %d MB" % (limit // 1048576),
                    "log": [log("warn", "拦截超大文件: %d bytes" % size)]}
    except (TypeError, ValueError):
        pass
    return {}
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

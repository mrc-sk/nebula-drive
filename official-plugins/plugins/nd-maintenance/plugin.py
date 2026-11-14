#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""维护模式与站点公告 —— NebulaDrive 官方插件（协议 v2）"""
import json
import os
import sys
import time

NAME = 'nd-maintenance'
VERSION = '1.0.0'
HOOKS = ['onRequestBefore', 'onUIPageOpen']
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
FLAG = os.path.join(DATA_DIR, "maintenance.flag")


def on_hook(hook, ctx):
    try:
        with open(FLAG, encoding="utf-8") as f:
            notice = f.read().strip()
    except Exception:
        notice = ""
    if not os.path.isfile(FLAG):
        return {}
    if hook == "onUIPageOpen":
        if not notice:
            return {}
        return {"ctxPatch": {"siteNotice": notice},
                "log": [log("info", "已注入站点公告")]}
    if hook == "onRequestBefore":
        path = str(ctx.get("path") or ctx.get("url") or "")
        role = str(ctx.get("role") or "")
        if "/api/admin" in path or role in ("admin", "administrator"):
            return {}  # 管理员永远放行，防止把运维自己锁在外面
        if bool(cfg("maintenanceEnabled", True)):
            return {"decision": "deny",
                    "reason": notice or "站点正在维护中，请稍后再试",
                    "log": [log("warn", "维护模式拦截: %s" % path[:80])]}
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

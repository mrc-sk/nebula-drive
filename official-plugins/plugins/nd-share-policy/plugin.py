#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""分享链接策略 —— NebulaDrive 官方插件（协议 v2）"""
import json
import os
import sys
import time

NAME = 'nd-share-policy'
VERSION = '1.0.0'
HOOKS = ['onShareCreateBefore']
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
def on_hook(hook, ctx):
    if hook != "onShareCreateBefore":
        return {}
    has_pwd = bool(ctx.get("password") or ctx.get("sharePassword"))
    expire = ctx.get("expire") or ctx.get("expiresAt") or ctx.get("expireAt")
    logs = []
    if bool(cfg("requirePassword", True)) and not has_pwd:
        return {"decision": "deny",
                "reason": "创建分享链接必须设置访问密码",
                "log": [log("warn", "分享被拦截：未设置密码")]}
    if bool(cfg("forbidPermanent", True)) and not expire:
        try:
            days = int(cfg("defaultDays", 7))
        except (TypeError, ValueError):
            days = 7
        patch = {"expire": int(time.time()) + days * 86400}
        logs.append(log("info", "已强制设置分享有效期 %d 天" % days))
        return {"ctxPatch": patch, "log": logs}
    return {"log": logs}
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

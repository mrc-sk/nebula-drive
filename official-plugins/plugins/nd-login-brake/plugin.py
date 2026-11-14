#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""登录爆破抑制 —— NebulaDrive 官方插件（协议 v2）"""
import json
import os
import sys
import time

NAME = 'nd-login-brake'
VERSION = '1.0.0'
HOOKS = ['onAuthBeforeLogin', 'onAuthLoginFailed', 'onAuthLoginSuccess']
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
_FAILS = {}
_LOCK = {}


def on_hook(hook, ctx):
    key = str(ctx.get("ip") or ctx.get("username") or ctx.get("email") or "?")
    now = time.time()
    try:
        window = int(cfg("windowSeconds", 60))
        maxfail = int(cfg("maxFailures", 5))
        ban = int(cfg("banSeconds", 300))
    except (TypeError, ValueError):
        window, maxfail, ban = 60, 5, 300
    if hook == "onAuthBeforeLogin":
        until = _LOCK.get(key, 0)
        if until > now:
            return {"decision": "deny",
                    "reason": "登录失败次数过多，请 %d 秒后重试" % int(until - now),
                    "log": [log("warn", "临时锁定中: %s" % key)]}
        return {}
    if hook == "onAuthLoginSuccess":
        _FAILS.pop(key, None)
        _LOCK.pop(key, None)
        return {}
    if hook == "onAuthLoginFailed":
        rec = [t for t in _FAILS.get(key, []) if now - t < window]
        rec.append(now)
        _FAILS[key] = rec
        if len(rec) >= maxfail:
            _LOCK[key] = now + ban
            return {"log": [log("warn", "%s 连续登录失败，临时锁定 %ds" % (key, ban))]}
        return {"log": [log("info", "登录失败计数 %d/%d" % (len(rec), maxfail))]}
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

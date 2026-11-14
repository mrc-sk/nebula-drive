#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""注册邀请码 —— NebulaDrive 官方插件（协议 v2）"""
import json
import os
import sys
import time

NAME = 'nd-invite-code'
VERSION = '1.0.0'
HOOKS = ['onAuthRegisterBefore']
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
CODES_FILE = os.path.join(DATA_DIR, "codes.txt")


def _codes():
    c = cfg("codes", "")
    if isinstance(c, list):
        base = [str(x).strip() for x in c]
    elif isinstance(c, str):
        base = [x.strip() for x in c.replace(",", "\n").splitlines() if x.strip()]
    else:
        base = []
    try:
        if os.path.isfile(CODES_FILE):
            with open(CODES_FILE, encoding="utf-8") as f:
                base += [x.strip() for x in f
                         if x.strip() and not x.strip().startswith("#")]
    except Exception:
        pass
    return [x for x in base if x]


def on_hook(hook, ctx):
    if hook != "onAuthRegisterBefore":
        return {}
    codes = _codes()
    if not codes:
        return {}  # 未配置码表：放行，不擅自锁死注册
    given = ""
    for k in ("inviteCode", "invite_code", "code", "regCode", "registrationCode"):
        if ctx.get(k):
            given = str(ctx[k]).strip()
            break
    if not given:
        return {"decision": "deny",
                "reason": "本站需要注册码才能注册，请填写有效注册码",
                "log": [log("warn", "注册被拦截：缺少注册码")]}
    if given not in codes:
        return {"decision": "deny",
                "reason": "注册码无效，请核对后重试",
                "log": [log("warn", "注册被拦截：注册码无效")]}
    return {"log": [log("info", "注册码校验通过")]}
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

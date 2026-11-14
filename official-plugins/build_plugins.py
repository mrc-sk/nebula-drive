#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
NebulaDrive 官方插件生成器（本地构建期使用）

生成 10 个纯钩子型 Python 插件的完整三文件契约：
  plugin.json / register.go / co.BEGG / plugin.py / README.md / store-meta.json

设计原则：
  * 全部订阅真实存在的钩子（由 Go 侧 LoadContract 二次校验）。
  * 默认策略「宽松、不误伤」：装了不会立刻锁站，需管理员显式配置或放置标志文件才生效。
  * 不启动任何 HTTP 页面服务（co.BEGG 不写 pages/command），零额外端口。
  * stdout 只走 JSON-RPC，日志走返回的 log 字段，绝不 print 调试信息。
"""
import json
import os
import stat

BASE = os.path.dirname(os.path.abspath(__file__))
PLUGINS_DIR = os.path.join(BASE, "plugins")
AUTHOR = "NebulaDrive Official"
LICENSE = "AGPL-3.0-only"
HOMEPAGE = "https://cloud.berrywing.top/"

SKELETON = '''#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""%(title)s —— NebulaDrive 官方插件（协议 v2）"""
import json
import os
import sys
import time

NAME = %(name)r
VERSION = %(version)r
HOOKS = %(hooks)r
CONFIG = {}
DATA_DIR = os.environ.get("NEBULA_PLUGIN_DATA", os.path.dirname(os.path.abspath(__file__)))


def cfg(key, default):
    """读取宿主在 init 下发的配置（plugins.config），未配置时回退默认值。"""
    v = CONFIG.get(key, default)
    return default if v in (None, "") else v


def log(level, message):
    return {"level": level, "message": str(message)[:300]}


def emit(obj):
    sys.stdout.write(json.dumps(obj, ensure_ascii=False) + "\\n")
    sys.stdout.flush()


# ---------------------------------------------------------------- 插件逻辑
%(handle)s
# ---------------------------------------------------------------- /插件逻辑

def handle(hook, ctx):
    try:
        r = on_hook(hook, ctx or {}) or {}
        return r if isinstance(r, dict) else {}
    except Exception as e:
        # 插件异常绝不能拖垮宿主业务：吞掉异常，只回一条日志
        return {"log": [log("error", "插件异常: %%s" %% e)]}


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
'''

README = '''# %(title)s

%(desc)s

| 项目 | 值 |
| --- | --- |
| 插件名 | `%(name)s` |
| 版本 | %(version)s |
| 作者 | %(author)s |
| 协议 | %(license)s |
| 订阅钩子 | %(hooklist)s |

## 功能

%(detail)s

## 配置

安装后在后台「插件 → 配置」里填写 JSON（对应 `plugins.config`）。**不填任何配置时插件保持宽松/关闭状态，不会一装上就改变站点行为。

%(configdoc)s

## 启用前须知

本插件以子进程方式运行，拥有宿主同等的文件与网络权限。请先在测试环境验证，确认策略符合预期后再在生产环境启用。

官方插件使用 Ed25519 签名，站点信任库中已内置官方公钥；安装时会在后台显示「已由受信任的发布者签名」。
'''

# ---------------------------------------------------------------- 10 个插件定义
PLUGINS = [
    {
        "name": "nd-invite-code", "title": "注册邀请码", "version": "1.0.0",
        "desc": "为开放注册补上注册码校验：未填或填错注册码一律拒绝，码表由管理员维护。",
        "tags": ["注册", "安全", "准入"],
        "hooks": ["onAuthRegisterBefore"],
        "permissions": ["auth:read"],
        "detail": "NebulaDrive 本体没有内置邀请码功能。本插件订阅 `onAuthRegisterBefore` 决策钩子，\n"
                  "从后台配置或数据目录 `codes.txt` 读取有效注册码；未配置任何码时自动放行，\n"
                  "避免装上就把注册入口锁死。配合站点的「关闭注册」设置可实现两种准入模式。",
        "configdoc": '```json\n{"codes": ["BERRY-2026", "TEAM-ONLY"]}\n```\n'
                     '也可在插件数据目录新建 `codes.txt`，一行一个码，`#` 开头为注释。\n'
                     '宿主传入的字段名兼容 `inviteCode` / `invite_code` / `code` / `regCode`。',
        "handle": '''CODES_FILE = os.path.join(DATA_DIR, "codes.txt")


def _codes():
    c = cfg("codes", "")
    if isinstance(c, list):
        base = [str(x).strip() for x in c]
    elif isinstance(c, str):
        base = [x.strip() for x in c.replace(",", "\\n").splitlines() if x.strip()]
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
    return {"log": [log("info", "注册码校验通过")]}''',
    },
    {
        "name": "nd-upload-guard", "title": "上传安全守卫", "version": "1.0.0",
        "desc": "拦截可执行文件、脚本与路径穿越文件名，并可限制单文件大小。",
        "tags": ["上传", "安全", "文件"],
        "hooks": ["onFileUploadBefore"],
        "permissions": ["file:read"],
        "detail": "在 `onFileUploadBefore` 决策钩子上做三件事：按扩展名黑名单拒绝可执行文件/脚本、\n"
                  "拒绝含 `..` 或路径分隔符的文件名、按 `maxBytes` 限制单文件上限。默认只启用\n"
                  "前两项，大小限制需显式配置才生效。",
        "configdoc": '```json\n{"blockedExtensions": [".exe", ".sh", ".php"], "maxBytes": 524288000}\n```\n'
                     '`blockedExtensions` 不填时使用内置黑名单（.exe/.bat/.cmd/.scr/.vbs/.ps1/.sh/.php/.jsp/.asp/.dll/.so/.msi/.com/.pif）；\n'
                     '`maxBytes` 为 0 或省略表示不限制。',
        "handle": '''DEFAULT_BLOCK = [".exe", ".bat", ".cmd", ".scr", ".vbs", ".ps1", ".sh",
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
    if ".." in name or "/" in name or "\\\\" in name or "\\x00" in name:
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
    return {}''',
    },
    {
        "name": "nd-filename-clean", "title": "文件名规范化", "version": "1.0.0",
        "desc": "自动清洗上传与重命名时的非法字符、路径片段与过长文件名。",
        "tags": ["文件", "规范化", "安全"],
        "hooks": ["onFileUploadBefore", "onFileRename"],
        "permissions": ["file:read"],
        "detail": "变换型插件：把 Windows 非法字符、控制字符、路径分隔符与 `..` 统一替换，\n"
                  "去掉首尾空格与点，并截到 `maxLength`。通过 `ctxPatch.fileName` 回写，\n"
                  "不改变原有拒绝逻辑，可与「上传安全守卫」叠加使用。",
        "configdoc": '```json\n{"maxLength": 200}\n```\n默认 200 字符。',
        "handle": '''def _clean(name):
    s = str(name).replace("\\\\", "_").replace("/", "_")
    s = "".join(ch for ch in s if ord(ch) >= 32 and ch not in '<>:"|?*')
    s = s.replace("..", "_").strip().strip(".")
    try:
        n = int(cfg("maxLength", 200))
    except (TypeError, ValueError):
        n = 200
    return s[:n] or "unnamed"


def on_hook(hook, ctx):
    if hook not in ("onFileUploadBefore", "onFileRename"):
        return {}
    name = str(ctx.get("fileName") or ctx.get("name") or ctx.get("newName") or "")
    if not name:
        return {}
    cleaned = _clean(name)
    if cleaned == name:
        return {}
    return {"ctxPatch": {"fileName": cleaned},
            "log": [log("info", "文件名规范化: %s -> %s" % (name[:60], cleaned[:60]))]}''',
    },
    {
        "name": "nd-quota-watch", "title": "配额用量预警", "version": "1.0.0",
        "desc": "当用户存储用量接近配额上限时写入预警信息，便于提前扩容或提醒。",
        "tags": ["配额", "运维", "通知"],
        "hooks": ["onFileUploadAfter", "onUserQuotaChange"],
        "permissions": ["storage:read"],
        "detail": "通知/变换型插件：计算 `used / maxStorage` 占比，超过阈值时通过\n"
                  "`ctxPatch.quotaWarning` 注入提示并写入插件日志。默认阈值 80%，\n"
                  "不会拦截任何上传。",
        "configdoc": '```json\n{"threshold": 80}\n```\n阈值单位为百分比。',
        "handle": '''def on_hook(hook, ctx):
    if hook not in ("onFileUploadAfter", "onUserQuotaChange"):
        return {}
    used = ctx.get("used") or ctx.get("usedStorage") or 0
    total = ctx.get("maxStorage") or ctx.get("quota") or ctx.get("total") or 0
    try:
        used, total = float(used), float(total)
    except (TypeError, ValueError):
        return {}
    if total <= 0:
        return {}
    pct = used / total * 100
    try:
        threshold = float(cfg("threshold", 80))
    except (TypeError, ValueError):
        threshold = 80
    if pct < threshold:
        return {}
    msg = "存储空间已使用 %.1f%%，接近配额上限" % pct
    return {"ctxPatch": {"quotaWarning": msg},
            "log": [log("warn", "用户 %s %s" % (ctx.get("userId", "?"), msg))]}''',
    },
    {
        "name": "nd-login-brake", "title": "登录爆破抑制", "version": "1.0.0",
        "desc": "同一 IP / 账号短时间连续登录失败后临时锁定，抑制暴力破解。",
        "tags": ["登录", "安全", "限流"],
        "hooks": ["onAuthBeforeLogin", "onAuthLoginFailed", "onAuthLoginSuccess"],
        "permissions": ["auth:read"],
        "detail": "在 `onAuthLoginFailed` 计数、`onAuthBeforeLogin` 决策拦截、\n"
                  "`onAuthLoginSuccess` 清零。默认 60 秒内失败 5 次锁定 300 秒。\n"
                  "计数保存在插件进程内存中（重启即清零），属于轻量级第一道防线，\n"
                  "不替代网关层限流。",
        "configdoc": '```json\n{"windowSeconds": 60, "maxFailures": 5, "banSeconds": 300}\n```',
        "handle": '''_FAILS = {}
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
    return {}''',
    },
    {
        "name": "nd-share-policy", "title": "分享链接策略", "version": "1.0.0",
        "desc": "强制分享链接设置密码与有效期，禁止无保护的永久公开链接。",
        "tags": ["分享", "安全", "策略"],
        "hooks": ["onShareCreateBefore"],
        "permissions": ["share:read"],
        "detail": "默认要求所有新建分享带访问密码；未声明有效期的分享自动补上\n"
                  "`expire`（默认 7 天），避免产生永久裸链。属于决策 + 变换型插件。",
        "configdoc": '```json\n{"requirePassword": true, "forbidPermanent": true, "defaultDays": 7}\n```\n'
                     '把 `requirePassword` 设为 false 可只启用有效期策略。',
        "handle": '''def on_hook(hook, ctx):
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
    return {"log": logs}''',
    },
    {
        "name": "nd-audit-trail", "title": "关键操作审计", "version": "1.0.0",
        "desc": "把登录、上传、删除、分享等关键事件以 JSONL 追加写入独立审计日志。",
        "tags": ["审计", "运维", "日志"],
        "hooks": ["onFileUploadAfter", "onFileDelete", "onAuthLoginSuccess",
                  "onShareCreateAfter", "onUserDelete"],
        "permissions": ["audit:write"],
        "detail": "通知型插件：订阅 5 个关键事件，脱敏摘要后写入插件专属数据目录\n"
                  "`audit.log`（每行一条 JSON）。日志与宿主数据目录隔离，便于单独备份与外送。",
        "configdoc": "无需配置。日志路径为插件数据目录下的 `audit.log`；\n"
                     "可用 `deskctl` 风格的本地命令或直接 `tail -f` 查看。",
        "handle": '''AUDIT = os.path.join(DATA_DIR, "audit.log")
KEEP = ("userId", "userName", "fileId", "fileName", "ip",
        "shareId", "size", "path", "ticketNo")


def on_hook(hook, ctx):
    if hook not in HOOKS:
        return {}
    rec = {"ts": time.strftime("%Y-%m-%d %H:%M:%S"), "hook": hook,
           "actor": ctx.get("userId") or ctx.get("userName") or "-"}
    for k in KEEP:
        if k in ctx and k not in rec:
            rec[k] = str(ctx[k])[:200]
    try:
        with open(AUDIT, "a", encoding="utf-8") as f:
            f.write(json.dumps(rec, ensure_ascii=False) + "\\n")
    except Exception as e:
        return {"log": [log("error", "审计写入失败: %s" % e)]}
    return {"log": [log("info", "已记录 %s" % hook)]}''',
    },
    {
        "name": "nd-webdav-guard", "title": "WebDAV 写保护", "version": "1.0.0",
        "desc": "按需把 WebDAV 切成只读，或保护指定目录不被 WebDAV 写入/删除。",
        "tags": ["WebDAV", "安全", "策略"],
        "hooks": ["onWebDAVPut", "onWebDAVDelete"],
        "permissions": ["webdav:read"],
        "detail": "默认完全放行（不改动任何行为）。管理员可开启只读模式、禁止 WebDAV 删除，\n"
                  "或用 `denyPaths` 保护指定路径前缀，防止误同步覆盖重要目录。",
        "configdoc": '```json\n{"readonly": false, "forbidDelete": false, "denyPaths": "/系统,/归档"}\n```\n'
                     '`denyPaths` 为逗号分隔的路径前缀。',
        "handle": '''def on_hook(hook, ctx):
    if hook not in ("onWebDAVPut", "onWebDAVDelete"):
        return {}
    if bool(cfg("readonly", False)):
        return {"decision": "deny",
                "reason": "当前 WebDAV 处于只读模式，禁止写入与删除",
                "log": [log("warn", "WebDAV 只读拦截: %s" % hook)]}
    if hook == "onWebDAVDelete" and bool(cfg("forbidDelete", False)):
        return {"decision": "deny",
                "reason": "管理员已禁止通过 WebDAV 删除文件",
                "log": [log("warn", "WebDAV 删除被拦截")]}
    path = str(ctx.get("path") or ctx.get("uri") or "")
    raw = cfg("denyPaths", "")
    prefixes = ([str(x).strip() for x in raw.split(",")] if isinstance(raw, str)
                else [str(x) for x in raw] if isinstance(raw, list) else [])
    for p in prefixes:
        if p and path.startswith(p):
            return {"decision": "deny",
                    "reason": "该目录受保护，禁止通过 WebDAV 写入",
                    "log": [log("warn", "受保护目录拦截: %s" % path[:80])]}
    return {}''',
    },
    {
        "name": "nd-sensitive-tag", "title": "敏感文件标记", "version": "1.0.0",
        "desc": "按关键词扫描文件名，命中后自动打上「需复核」标签，不拦截上传。",
        "tags": ["内容", "合规", "标记"],
        "hooks": ["onContentScan", "onFileUploadAfter"],
        "permissions": ["content:read"],
        "detail": "变换型插件：匹配到关键词时通过 `ctxPatch` 写入 `sensitive=true` 与\n"
                  "`tags`，并记一条告警日志。默认词表覆盖「机密 / 内部 / 私密 / 身份证 /\n"
                  "银行卡 / password / secret」，可自行替换。不阻断任何业务。",
        "configdoc": '```json\n{"keywords": ["机密", "内部", "合同"]}\n```',
        "handle": '''DEFAULT_WORDS = ["机密", "内部", "私密", "身份证", "银行卡",
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
                        % (",".join(hit[:5]), text[:60]))]}''',
    },
    {
        "name": "nd-maintenance", "title": "维护模式与站点公告", "version": "1.0.0",
        "desc": "放置标志文件即进入维护模式（管理员仍可进），并可向页面注入公告。",
        "tags": ["运维", "公告", "维护"],
        "hooks": ["onRequestBefore", "onUIPageOpen"],
        "permissions": ["request:read"],
        "detail": "默认完全不生效。管理员在插件数据目录创建 `maintenance.flag`（文件内容即公告文案）后：\n"
                  "`onRequestBefore` 拒绝普通用户请求但放行后台 `/api/admin` 路径；\n"
                  "`onUIPageOpen` 通过 `ctxPatch.siteNotice` 注入公告。删除文件即恢复。",
        "configdoc": '```json\n{"maintenanceEnabled": true}\n```\n'
                     '标志文件路径：插件数据目录 `maintenance.flag`。\n'
                     '**务必保留管理员放行规则**，否则会把自己也挡在外面。',
        "handle": '''FLAG = os.path.join(DATA_DIR, "maintenance.flag")


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
    return {}''',
    },
]


def write(path, content, executable=False):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8", newline="\n") as f:
        f.write(content)
    if executable:
        os.chmod(path, os.stat(path).st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)


def main():
    os.makedirs(PLUGINS_DIR, exist_ok=True)
    for p in PLUGINS:
        d = os.path.join(PLUGINS_DIR, p["name"])
        os.makedirs(d, exist_ok=True)

        manifest = {
            "name": p["name"],
            "title": p["title"],
            "version": p["version"],
            "author": AUTHOR,
            "description": p["desc"],
            "entry": "plugin.py",
            "protocolVersion": 2,
            "hooks": p["hooks"],
            "permissions": p["permissions"],
            "license": LICENSE,
            "homepage": HOMEPAGE,
        }
        write(os.path.join(d, "plugin.json"),
              json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")

        hooks_go = ", ".join('"%s"' % h for h in p["hooks"])
        files = ["plugin.json", "register.go", "co.BEGG", "plugin.py", "README.md", "store-meta.json"]
        install_go = ", ".join('"%s"' % f for f in files)
        decl = (
            "package p_%s\n\n"
            "// register.go —— 纯声明文件：宿主只用 go/parser 读语法树，不编译、不执行。\n"
            "// 由 NebulaDrive 官方插件发布工具生成。\n"
            "var Spec = PluginSpec{\n"
            "    Name:            %r,\n"
            "    Title:           %r,\n"
            "    Version:         %r,\n"
            "    Author:          %r,\n"
            "    Description:     %r,\n"
            "    ProtocolVersion: 2,\n"
            "    Hooks:           []string{%s},\n"
            "    Provides:        []string{},\n"
            "    Install:         []string{%s},\n"
            "    Runtime:         \"python\",\n"
            "}\n"
        ) % (p["name"].replace("-", "_"), p["name"], p["title"], p["version"],
             AUTHOR, p["desc"], hooks_go, install_go)
        # Go 源码里字符串用双引号，不能用 Python repr 的单引号
        decl = decl.replace("%r", "%s")
        q = lambda s: '"' + str(s).replace('\\', '\\\\').replace('"', '\\"') + '"'
        decl = (
            "package p_%s\n\n"
            "// register.go —— 纯声明文件：宿主只用 go/parser 读语法树，不编译、不执行。\n"
            "// 由 NebulaDrive 官方插件发布工具生成。\n"
            "var Spec = PluginSpec{\n"
            "    Name:            %s,\n"
            "    Title:           %s,\n"
            "    Version:         %s,\n"
            "    Author:          %s,\n"
            "    Description:     %s,\n"
            "    ProtocolVersion: 2,\n"
            "    Hooks:           []string{%s},\n"
            "    Provides:        []string{},\n"
            "    Install:         []string{%s},\n"
            "    Runtime:         \"python\",\n"
            "}\n"
        ) % (p["name"].replace("-", "_"), q(p["name"]), q(p["title"]),
             q(p["version"]), q(AUTHOR), q(p["desc"]), hooks_go, install_go)
        write(os.path.join(d, "register.go"), decl)

        begg = {"runtime": "python", "requires": ["python3"]}
        write(os.path.join(d, "co.BEGG"),
              json.dumps(begg, ensure_ascii=False, indent=2) + "\n")

        py = SKELETON % {
            "title": p["title"], "name": p["name"], "version": p["version"],
            # 模板里是 %(hooks)r，此处必须传真正的 list，
            # 由 %r 生成 Python 列表字面量；传字符串会被再 repr 一次变成字符串。
            "hooks": p["hooks"], "handle": p["handle"],
        }
        write(os.path.join(d, "plugin.py"), py, executable=True)

        write(os.path.join(d, "README.md"), README % {
            "title": p["title"], "desc": p["desc"], "name": p["name"],
            "version": p["version"], "author": AUTHOR, "license": LICENSE,
            "hooklist": ", ".join("`%s`" % h for h in p["hooks"]),
            "detail": p["detail"], "configdoc": p["configdoc"],
        })

        meta = {"title": p["title"], "description": p["desc"],
                "tags": p["tags"], "author": AUTHOR, "license": LICENSE}
        write(os.path.join(d, "store-meta.json"),
              json.dumps(meta, ensure_ascii=False, indent=2) + "\n")

        print("生成 %-22s hooks=%d" % (p["name"], len(p["hooks"])))
    print("共 %d 个插件 -> %s" % (len(PLUGINS), PLUGINS_DIR))


if __name__ == "__main__":
    main()

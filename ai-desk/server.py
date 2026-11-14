#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
NebulaDrive 智能 AI 工单站  ——  安全版

安全边界（重要）：
  * 公开页只提供「提交工单 / 查询工单」，不含任何管理或配置入口。
  * 管理控制台挂在随机隐藏路径 /<admin_path>，必须带正确令牌才返回页面，
    否则一律 404（不暴露存在性）。令牌不出现在任何公开 HTML / JS 里。
  * 管理 API 只认 X-Admin-Token 请求头，不接受 URL 参数（避免落到访问日志）。
  * AI 网关配置**没有 Web 写入口**，完全由服务器本地 `deskctl.py` 维护。
  * 本地模型服务只监听 127.0.0.1，不对公网开放。

零第三方依赖（仅 Python 3 标准库），数据存 SQLite。
用 `python3 deskctl.py url` 取得管理控制台完整地址。
"""
import os
import re
import sys
import json
import html
import sqlite3
import secrets
import datetime
import threading
import urllib.request
import urllib.error
from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler
from urllib.parse import urlparse, parse_qs

BASE = os.path.dirname(os.path.abspath(__file__))
DATA_DIR = os.path.join(BASE, "data")
DB_PATH = os.path.join(DATA_DIR, "desk.db")
CFG_PATH = os.path.join(BASE, "config.json")
PORT = int(os.environ.get("AI_DESK_PORT", "8081"))

DEFAULT_CFG = {
    "site_name": "NebulaDrive 智能工单站",
    "admin_token": "",
    "admin_path": "",
    "ai": {
        "enabled": False,
        "base_url": "http://127.0.0.1:8083/v1",
        "api_key": "",
        "model": "qwen2.5-0.5b-instruct",
        "timeout": 90,
    },
}

FAQ = [
    (("配额", "空间", "1gb", "容量", "不够用", "满了", "上传失败"),
     "网盘目前为每位用户分配 1 GB 空间。请到「设置 → 存储」查看已用量；若提示空间不足，可先清理回收站与历史版本。管理员如需调整额度，在后台「用户组」修改 default 组的配额即可。"),
    (("注册", "账号", "注册码", "登录不了", "无法登录", "没有账号"),
     "当前站点已关闭公开注册，仅允许已有账号登录。若你需要账号，请联系管理员在后台「用户管理」中创建；后续如启用注册码，会在注册页要求填写邀请码。"),
    (("密码", "忘记", "重置", "改密码"),
     "本站未启用邮件服务，也没有「找回密码」自助入口、客服电话或在线客服。忘记密码或需要账号，请直接联系本站点管理员在后台重置；请勿在工单中填写密码。"),
    (("分享", "外链", "提取码", "链接失效", "下载不了"),
     "分享链接失效通常有三个原因：链接过期、提取码错误、或文件被移动/删除。请重新生成分享链接，并确认有效期设置；若仍失败，请附上链接与报错信息继续回复本工单。"),
    (("webdav", "挂载", "同步", "客户端"),
     "WebDAV 需在后台「用户组」中开启对应权限，挂载地址一般为 https://域名/dav，使用账号密码登录。若使用 Windows 资源管理器挂载失败，建议改用 RaiDrive 等客户端。"),
    (("插件", "扩展", "co.begg", "钩子", "安装失败"),
     "插件需包含 plugin.json、register.go、co.BEGG 三个声明文件，且后台「插件」总开关处于开启状态。安装失败请先查看插件日志；v2 插件协议会向下兼容 v1。详细规范见项目 Wiki 的《插件制作手册》。"),
    (("慢", "速度", "卡顿", "超时", "502", "打不开"),
     "服务以单实例运行。若出现访问缓慢，请告知具体时间点与操作，我们会查看服务日志与负载；大文件上传建议使用分片/断点续传。"),
    (("发票", "付费", "套餐", "价格", "购买"),
     "当前为自用/内测部署，未开启付费与套餐购买。如有商用需求请在工单中说明，管理员会与你联系。"),
]

GENERIC_REPLY = (
    "工单已收到，感谢反馈。当前智能答复由本地知识库生成：\n"
    "请补充以下信息以便更快定位——①出现问题的时间点；②你使用的浏览器/客户端版本；"
    "③具体操作步骤与报错原文（可截图）；④涉及的账号（请勿在此填写密码）。\n"
    "管理员会在后台看到本工单并跟进处理。"
)

# ---- V26-11.1 答复准确性护栏 ----
# 0.5B 级小模型无法可靠遵守「否定约束」（会把禁用词当成建议复述），因此：
#   1) 事实/凭证/商务类问题 → 确定性走人工编写的知识库，完全不经过模型；
#   2) 其余问题仍由本地小模型组织语言，但输出经黑名单拦截，命中即回退知识库。
# 配置（注册、邮件、付费）变更后，请同步更新 FAQ 与 ACCOUNT_FALLBACK。
SITE_FACTS = (
    "你是 NebulaDrive 网盘的技术支持助手。\n"
    "【站点事实】自托管内测部署，公开注册已关闭，账号由管理员创建，每用户 1 GB；"
    "账户与凭证相关事宜一律由管理员在后台处理。\n"
    "只依据官方知识库作答，简洁友好、分点、200 字以内；不清楚就请用户补充信息或联系管理员。"
)

# 命中这些词的工单属于「事实敏感类」，直接返回知识库，杜绝模型编造。
FACT_KEYS = (
    "密码", "忘记", "找回", "重置", "改密码", "注册", "注册码", "没有账号", "登录不了", "无法登录",
    "客服", "电话", "热线", "人工客服", "联系方式", "邮箱找回", "邮件服务", "smtp",
    "发票", "付费", "套餐", "价格", "购买",
)

# 模型输出端黑名单：出现这些「诱导型」短语即判定为编造，丢弃并回退知识库。
FORBIDDEN = (
    "拨打客服", "联系客服", "客服电话", "客服热线", "在线人工客服", "在线客服",
    "找回密码：", "找回密码按钮", "点击链接进行验证", "发送一封", "发送邮件",
    "邮件重置", "邮件找回", "通过邮箱找回", "邮箱验证", "拨打", "致电",
)

ACCOUNT_FALLBACK = (
    "关于账号与登录：本站公开注册已关闭，账号由管理员在后台创建；"
    "邮件服务未启用，没有自助找回密码入口，也没有客服电话或在线客服。"
    "忘记密码或需要账号，请直接联系本站点管理员在后台处理；请勿在工单中填写密码。"
)


def now():
    return datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")


def esc(s):
    return html.escape(str(s or ""), quote=True)


def load_cfg():
    cfg = json.loads(json.dumps(DEFAULT_CFG))
    try:
        with open(CFG_PATH, "r", encoding="utf-8") as f:
            saved = json.load(f)
        for k, v in saved.items():
            if isinstance(v, dict) and isinstance(cfg.get(k), dict):
                cfg[k].update(v)
            else:
                cfg[k] = v
    except (FileNotFoundError, ValueError):
        pass
    dirty = False
    if not cfg.get("admin_token"):
        cfg["admin_token"] = secrets.token_urlsafe(24)
        dirty = True
    if not cfg.get("admin_path"):
        cfg["admin_path"] = "console-" + secrets.token_hex(8)
        dirty = True
    if dirty:
        save_cfg(cfg)
    return cfg


def save_cfg(cfg):
    os.makedirs(BASE, exist_ok=True)
    tmp = CFG_PATH + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        json.dump(cfg, f, ensure_ascii=False, indent=2)
    os.replace(tmp, CFG_PATH)
    try:
        os.chmod(CFG_PATH, 0o600)
    except OSError:
        pass


def init_db():
    os.makedirs(DATA_DIR, exist_ok=True)
    con = sqlite3.connect(DB_PATH)
    con.executescript("""
    CREATE TABLE IF NOT EXISTS tickets (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        ticket_no TEXT UNIQUE, title TEXT NOT NULL, content TEXT NOT NULL,
        category TEXT DEFAULT '其他', contact TEXT DEFAULT '',
        status TEXT DEFAULT '待处理', ai_reply TEXT DEFAULT '', ai_source TEXT DEFAULT '',
        created_at TEXT, updated_at TEXT
    );
    CREATE TABLE IF NOT EXISTS replies (
        id INTEGER PRIMARY KEY AUTOINCREMENT, ticket_id INTEGER,
        author TEXT, content TEXT, created_at TEXT
    );
    CREATE INDEX IF NOT EXISTS idx_tickets_no ON tickets(ticket_no);
    """)
    con.commit()
    con.close()


def db():
    con = sqlite3.connect(DB_PATH)
    con.row_factory = sqlite3.Row
    return con


def kb_hits(title, content, category):
    text = (title + " " + content).lower()
    return [ans for keys, ans in FAQ if any(k in text for k in keys)]


def local_reply(title, content, category):
    hits = kb_hits(title, content, category)
    if hits:
        return ("根据你描述的问题，本地知识库匹配到以下指引：\n\n"
                + "\n\n".join("· " + h for h in hits[:3])
                + "\n\n若以上未解决，请直接回复本工单补充细节，管理员会人工跟进。",
                "local-kb")
    return (GENERIC_REPLY, "local-kb")


def ai_reply(cfg, title, content, category):
    text = (title + " " + content).lower()

    # 护栏 1：事实/凭证/商务类问题，确定性走知识库，不经模型（100% 不编造）。
    if any(k in text for k in FACT_KEYS):
        hits = kb_hits(title, content, category)
        if hits:
            return ("根据你描述的问题，本地知识库匹配到以下指引：\n\n"
                    + "\n\n".join("· " + h for h in hits[:3])
                    + "\n\n若以上未解决，请直接回复本工单补充细节，管理员会人工跟进。",
                    "local-kb")
        return ACCOUNT_FALLBACK, "local-kb"

    ai = cfg.get("ai", {})
    if ai.get("enabled"):
        try:
            # 非事实类问题才交给本地小模型组织语言，KB 要点作为事实依据注入。
            kb_reply, _ = local_reply(title, content, category)
            prompt = (
                SITE_FACTS + "\n\n"
                "请用简洁、友好、可执行的中文回答用户的工单，分点给出排查步骤；"
                "只依据上面的站点事实与下面的官方知识库作答；"
                "如信息不足，明确列出需要用户补充的内容。回答控制在 200 字以内。\n\n"
                "【官方知识库要点，请优先据此作答】\n" + kb_reply[:700] + "\n\n"
                f"分类：{category}\n标题：{title}\n描述：{content}"
            )
            payload = json.dumps({
                "model": ai.get("model", "qwen2.5-0.5b-instruct"),
                "messages": [
                    {"role": "system", "content": SITE_FACTS},
                    {"role": "user", "content": prompt},
                ],
                "temperature": 0.4,
                "max_tokens": 320,
            }).encode("utf-8")
            url = ai.get("base_url", "").rstrip("/") + "/chat/completions"
            headers = {"Content-Type": "application/json"}
            if ai.get("api_key"):
                headers["Authorization"] = "Bearer " + ai["api_key"]
            req = urllib.request.Request(url, data=payload, headers=headers)
            with urllib.request.urlopen(req, timeout=int(ai.get("timeout", 90))) as resp:
                data = json.loads(resp.read().decode("utf-8"))
            out = data["choices"][0]["message"]["content"].strip()
            # 护栏 2：输出黑名单，命中即丢弃模型结果、回退知识库。
            if out and not any(f in out for f in FORBIDDEN):
                return out, "ai:" + ai.get("model", "local")
            if out:
                fb, _ = local_reply(title, content, category)
                return fb, "local-kb"
        except Exception as e:
            fallback, _ = local_reply(title, content, category)
            return ("【本地模型暂不可用，已自动切换到知识库】\n" + str(e)[:100]
                    + "\n\n" + fallback, "ai-fallback")
    return local_reply(title, content, category)


def new_ticket_no():
    return "ND-" + datetime.datetime.now().strftime("%Y%m%d") + "-" + secrets.token_hex(3).upper()


# ---------------------------------------------------------------- 公开页面
PUBLIC_PAGE = r"""<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>__SITE_NAME__</title>
<style>
*{box-sizing:border-box}body{margin:0;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;background:#0f1420;color:#e6ebf5;line-height:1.65}
a{color:#6ea8ff;text-decoration:none}button,input,select,textarea{font-family:inherit}
.wrap{max-width:820px;margin:0 auto;padding:24px 18px 60px}
header{padding:22px 0 18px;border-bottom:1px solid #222b3d}
h1{font-size:21px;margin:0;font-weight:650}.sub{color:#8b97ad;font-size:13px;margin-top:5px}
.badge{display:inline-block;padding:3px 10px;border-radius:20px;font-size:12px;background:#1c2740;color:#8ab4ff;border:1px solid #2b3a5e;float:right;margin-top:-30px}
nav{display:flex;gap:8px;margin:20px 0}
nav button{background:#1a2132;color:#aebbd2;border:1px solid #29334a;padding:9px 18px;border-radius:9px;cursor:pointer;font-size:14px}
nav button.on{background:#2b5bd7;border-color:#2b5bd7;color:#fff}
.card{background:#161d2c;border:1px solid #232d42;border-radius:14px;padding:22px;margin-bottom:16px}
.card h2{font-size:16px;margin:0 0 16px;font-weight:600}
label{display:block;font-size:13px;color:#9aa8c0;margin:12px 0 6px}
input,select,textarea{width:100%;background:#0f1522;border:1px solid #2b3550;color:#e6ebf5;border-radius:9px;padding:11px 12px;font-size:14px;outline:none}
textarea{min-height:120px;resize:vertical}
.btn{background:#2b5bd7;color:#fff;border:0;padding:11px 24px;border-radius:9px;cursor:pointer;font-size:14px;font-weight:600;margin-top:16px}
.btn.ghost{background:#1c2436;border:1px solid #33405c;color:#c3cee2}
.row{display:flex;gap:12px;flex-wrap:wrap}.row>*{flex:1;min-width:180px}
.msg{padding:12px 14px;border-radius:9px;margin:14px 0;font-size:14px;display:none}
.msg.ok{background:#12331f;border:1px solid #215c36;color:#8ce0a8;display:block}
.msg.err{background:#3a1a1c;border:1px solid #6b2b2f;color:#ff9b9f;display:block}
.ticket{border:1px solid #29334a;border-radius:11px;padding:15px 17px;margin-bottom:11px;background:#121a29}
.ticket .t{font-weight:600;font-size:15px}.ticket .m{color:#8794ab;font-size:12.5px;margin-top:5px}
.pill{display:inline-block;font-size:11.5px;padding:2px 9px;border-radius:20px;margin-left:8px;background:#24304a;color:#a8bcde}
.detail{white-space:pre-wrap;font-size:14px;background:#0f1522;border:1px solid #232d42;border-radius:10px;padding:15px;margin-top:12px}
.ai-box{background:linear-gradient(135deg,#15233f,#182a4a);border:1px solid #2e4a7d;border-radius:10px;padding:15px;margin-top:12px}
.ai-box .lbl{font-size:12px;color:#8ab4ff;font-weight:600;margin-bottom:8px}
.hidden{display:none}.muted{color:#7c889e;font-size:12.5px}
.foot{color:#5f6b80;font-size:12px;text-align:center;margin-top:34px}
</style></head><body><div class="wrap">
<header><h1>__SITE_NAME__</h1><div class="sub">提交问题后自动给出智能答复，管理员会在后台跟进</div>
<span class="badge" id="aiBadge">状态检测中</span></header>
<nav>
<button id="tabSubmit" class="on" onclick="tab('submit')">提交工单</button>
<button id="tabQuery" onclick="tab('query')">查询工单</button>
</nav>
<section id="pane-submit" class="card">
<h2>描述你遇到的问题</h2>
<div class="row"><div><label>分类</label><select id="fCat"><option>使用问题</option><option>账号与登录</option><option>上传下载</option><option>存储与配额</option><option>插件与扩展</option><option>功能建议</option><option>其他</option></select></div>
<div><label>联系方式（选填，便于回复）</label><input id="fContact" placeholder="邮箱 / 用户名，请勿填密码"></div></div>
<label>标题</label><input id="fTitle" placeholder="一句话概括问题">
<label>详细描述</label><textarea id="fContent" placeholder="出现了什么、你做了什么、期望是什么、报错原文…"></textarea>
<button class="btn" onclick="submitTicket()">提交并获取智能答复</button>
<div id="submitMsg" class="msg"></div>
<div id="aiResult" class="ai-box hidden"><div class="lbl" id="aiLbl"></div><div class="detail" id="aiText"></div><div class="muted" id="aiNo" style="margin-top:10px"></div></div>
</section>
<section id="pane-query" class="card hidden">
<h2>按工单号查询</h2>
<div class="row"><div><input id="qNo" placeholder="例如 ND-20261108-AB12CD"></div><div style="flex:0 0 auto"><button class="btn ghost" onclick="queryTicket()">查询</button></div></div>
<div id="queryResult"></div>
</section>
<div class="foot">NebulaDrive 智能工单站 · 数据仅存于本机 · 请勿在工单中提交密码</div>
</div>
<script>
function tab(n){['submit','query'].forEach(function(x){
 document.getElementById('pane-'+x).classList.toggle('hidden',x!==n);
 document.getElementById('tab'+x.charAt(0).toUpperCase()+x.slice(1)).classList.toggle('on',x===n);});}
function msg(id,ok,t){var e=document.getElementById(id);e.className='msg '+(ok?'ok':'err');e.textContent=t;}
async function api(path,opt){var r=await fetch(path,opt||{});var d=await r.json().catch(function(){return {}});if(!r.ok)throw new Error(d.error||('HTTP '+r.status));return d;}
function esc(s){return String(s==null?'':s).replace(/[&<>"']/g,function(c){return {'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]});}
async function submitTicket(){
 var b={title:document.getElementById('fTitle').value.trim(),content:document.getElementById('fContent').value.trim(),category:document.getElementById('fCat').value,contact:document.getElementById('fContact').value.trim()};
 if(!b.title||!b.content){msg('submitMsg',false,'请填写标题和详细描述');return;}
 try{var d=await api('/api/tickets',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(b)});
  msg('submitMsg',true,'提交成功，工单号 '+d.ticket_no+'（请记下以便查询）');
  var box=document.getElementById('aiResult');box.classList.remove('hidden');
  document.getElementById('aiLbl').textContent=d.ai_source.indexOf('ai:')===0?'智能答复 · 本地模型':'智能答复 · 本地知识库';
  document.getElementById('aiText').textContent=d.ai_reply;
  document.getElementById('aiNo').textContent='工单号：'+d.ticket_no+' ｜ 提交时间：'+d.created_at;
  document.getElementById('fTitle').value='';document.getElementById('fContent').value='';
 }catch(e){msg('submitMsg',false,'提交失败：'+e.message);}}
async function queryTicket(){var n=document.getElementById('qNo').value.trim();if(!n)return;
 try{var d=await api('/api/tickets/'+encodeURIComponent(n));
  var s='<div class="ticket"><div class="t">'+esc(d.title)+'<span class="pill">'+esc(d.status)+'</span></div><div class="m">'+esc(d.ticket_no)+' · '+esc(d.created_at)+' · '+esc(d.category)+'</div></div>';
  s+='<div class="detail">'+esc(d.content)+'</div>';
  if(d.ai_reply)s+='<div class="ai-box"><div class="lbl">智能答复</div><div class="detail">'+esc(d.ai_reply)+'</div></div>';
  (d.replies||[]).forEach(function(r){s+='<div class="card" style="margin-top:10px;padding:13px"><b style="font-size:13px;color:#8ab4ff">'+esc(r.author)+'</b><span class="muted"> · '+esc(r.created_at)+'</span><div class="detail">'+esc(r.content)+'</div></div>';});
  document.getElementById('queryResult').innerHTML=s;
 }catch(e){document.getElementById('queryResult').innerHTML='<div class="msg err">未找到该工单</div>';}}
fetch('/api/status').then(function(r){return r.json()}).then(function(d){
 document.getElementById('aiBadge').textContent=d.ai_active?'本地模型已启用':'智能答复已就绪';}).catch(function(){});
</script></body></html>"""

# ---------------------------------------------------------------- 管理控制台（隐藏路径）
ADMIN_PAGE = r"""<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>工单管理控制台</title>
<style>
*{box-sizing:border-box}body{margin:0;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;background:#0f1420;color:#e6ebf5;line-height:1.65}
.wrap{max-width:1080px;margin:0 auto;padding:24px 18px 60px}
h1{font-size:20px;margin:0 0 6px}.sub{color:#8b97ad;font-size:13px;margin-bottom:18px}
.card{background:#161d2c;border:1px solid #232d42;border-radius:12px;padding:18px;margin-bottom:14px}
table{width:100%;border-collapse:collapse;font-size:13.5px}th,td{text-align:left;padding:9px 8px;border-bottom:1px solid #232d42}th{color:#8b97ad;font-weight:500}
tbody tr{cursor:pointer}tbody tr:hover{background:#1b2537}
.detail{white-space:pre-wrap;font-size:14px;background:#0f1522;border:1px solid #232d42;border-radius:10px;padding:14px;margin-top:10px}
.ai-box{background:#15233f;border:1px solid #2e4a7d;border-radius:10px;padding:14px;margin-top:10px}
select,textarea,input{width:100%;background:#0f1522;border:1px solid #2b3550;color:#e6ebf5;border-radius:9px;padding:10px;font-size:14px;font-family:inherit}
textarea{min-height:70px;resize:vertical}label{display:block;font-size:13px;color:#9aa8c0;margin:12px 0 6px}
.btn{background:#2b5bd7;color:#fff;border:0;padding:10px 20px;border-radius:9px;cursor:pointer;font-size:14px;margin-top:12px}
.muted{color:#7c889e;font-size:12px}.pill{display:inline-block;font-size:11px;padding:2px 8px;border-radius:20px;background:#24304a;color:#a8bcde}
.note{background:#1a2132;border:1px solid #33405c;border-radius:9px;padding:12px;font-size:12.5px;color:#a9b7cf;margin-bottom:16px}
</style></head><body><div class="wrap">
<h1>工单管理控制台</h1><div class="sub">非公开入口 · 请妥善保管当前地址，不要转发</div>
<div class="note">AI 网关配置不在此处修改。请在服务器本地执行 <b>python3 /opt/ai-desk/deskctl.py --help</b> 查看/修改配置。</div>
<div class="card"><h2 style="font-size:15px;margin:0 0 12px">工单列表 <span class="muted" id="cnt"></span></h2>
<table><thead><tr><th>工单号</th><th>标题</th><th>状态</th><th>答复来源</th><th>时间</th></tr></thead><tbody id="list"></tbody></table></div>
<div class="card" id="detailCard" style="display:none"></div>
<script>
var TOKEN='__TOKEN__';
function esc(s){return String(s==null?'':s).replace(/[&<>"']/g,function(c){return {'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]});}
async function api(p,o){o=o||{};o.headers=Object.assign({'X-Admin-Token':TOKEN},o.headers||{});var r=await fetch(p,o);var d=await r.json().catch(function(){return{}});if(!r.ok)throw new Error(d.error||('HTTP '+r.status));return d;}
async function load(){var d=await api('/api/admin/tickets');document.getElementById('cnt').textContent='共 '+d.total+' 条';
 document.getElementById('list').innerHTML=d.items.map(function(x){return '<tr onclick="show(\''+esc(x.ticket_no)+'\')"><td>'+esc(x.ticket_no)+'</td><td>'+esc(x.title)+'</td><td><span class="pill">'+esc(x.status)+'</span></td><td>'+esc(x.ai_source)+'</td><td>'+esc(x.created_at)+'</td></tr>';}).join('');}
async function show(no){var d=await api('/api/admin/tickets/'+encodeURIComponent(no));
 var h='<h2 style="font-size:15px;margin:0">'+esc(d.title)+' <span class="pill">'+esc(d.status)+'</span></h2><div class="muted">'+esc(d.ticket_no)+' · 联系方式：'+esc(d.contact||'未填')+'</div><div class="detail">'+esc(d.content)+'</div>';
 if(d.ai_reply)h+='<div class="ai-box"><b style="font-size:12px;color:#8ab4ff">智能答复 · '+esc(d.ai_source)+'</b><div class="detail">'+esc(d.ai_reply)+'</div></div>';
 (d.replies||[]).forEach(function(r){h+='<div class="detail"><b>'+esc(r.author)+'</b> · '+esc(r.created_at)+'\n'+esc(r.content)+'</div>';});
 h+='<label>状态</label><select id="st"><option>待处理</option><option>处理中</option><option>已解决</option><option>已关闭</option></select><label>追加回复</label><textarea id="rp"></textarea><button class="btn" onclick="save(\''+esc(d.ticket_no)+'\')">保存</button>';
 var c=document.getElementById('detailCard');c.innerHTML=h;c.style.display='block';document.getElementById('st').value=d.status;c.scrollIntoView({behavior:'smooth'});}
async function save(no){await api('/api/admin/tickets/'+encodeURIComponent(no),{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({status:document.getElementById('st').value,reply:document.getElementById('rp').value})});load();}
load().catch(function(e){document.getElementById('cnt').textContent='加载失败：'+e.message;});
</script></body></html>"""


class Handler(BaseHTTPRequestHandler):
    server_version = "AIDesk/2.0"

    def log_message(self, fmt, *args):
        sys.stderr.write("[%s] %s\n" % (now(), fmt % args))

    def _send(self, code, body, ctype="application/json; charset=utf-8"):
        data = body.encode("utf-8") if isinstance(body, str) else body
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(data)))
        self.send_header("Cache-Control", "no-store")
        self.send_header("X-Content-Type-Options", "nosniff")
        self.end_headers()
        try:
            self.wfile.write(data)
        except (BrokenPipeError, ConnectionResetError):
            pass

    def _json(self, code, obj):
        self._send(code, json.dumps(obj, ensure_ascii=False))

    def _not_found(self):
        self._send(404, "Not Found", "text/plain; charset=utf-8")

    def _body(self):
        n = int(self.headers.get("Content-Length", 0) or 0)
        if n <= 0:
            return {}
        try:
            return json.loads(self.rfile.read(min(n, 2 * 1024 * 1024)).decode("utf-8"))
        except Exception:
            return {}

    def _is_admin(self):
        """只认请求头，杜绝令牌出现在 URL / 日志 / Referer 里。"""
        cfg = load_cfg()
        token = self.headers.get("X-Admin-Token", "")
        return bool(token) and secrets.compare_digest(token, cfg["admin_token"])

    def _ticket_dict(self, row, include_replies=False):
        d = dict(row)
        if include_replies:
            con = db()
            d["replies"] = [dict(r) for r in con.execute(
                "SELECT author,content,created_at FROM replies WHERE ticket_id=? ORDER BY id", (row["id"],))]
            con.close()
        d.pop("id", None)
        return d

    # ------------------------------------------------------------ GET
    def do_GET(self):
        u = urlparse(self.path)
        p, q = u.path, parse_qs(u.query)
        cfg = load_cfg()

        if p in ("/", "/index.html"):
            self._send(200, PUBLIC_PAGE.replace("__SITE_NAME__", esc(cfg["site_name"])),
                       "text/html; charset=utf-8")
            return
        if p == "/health":
            self._json(200, {"ok": True, "time": now()})
            return
        if p == "/api/status":
            self._json(200, {"ai_active": bool(cfg["ai"].get("enabled"))})
            return

        # 隐藏管理控制台：路径不对或令牌不对都返回 404，不暴露存在性
        if p.rstrip("/") == "/" + cfg["admin_path"]:
            token = q.get("token", [""])[0]
            if not token or not secrets.compare_digest(token, cfg["admin_token"]):
                return self._not_found()
            self._send(200, ADMIN_PAGE.replace("__TOKEN__", cfg["admin_token"]),
                       "text/html; charset=utf-8")
            return

        # 公开查询：凭工单号
        m = re.match(r"^/api/tickets/([A-Za-z0-9\-]+)$", p)
        if m:
            con = db()
            row = con.execute("SELECT * FROM tickets WHERE ticket_no=?", (m.group(1),)).fetchone()
            con.close()
            if not row:
                return self._json(404, {"error": "工单不存在"})
            self._json(200, self._ticket_dict(row, include_replies=True))
            return

        # ---- 以下全部为管理 API，强制请求头令牌 ----
        if not self._is_admin():
            return self._not_found()
        if p == "/api/admin/tickets":
            con = db()
            rows = con.execute("SELECT * FROM tickets ORDER BY id DESC LIMIT 300").fetchall()
            con.close()
            self._json(200, {"total": len(rows), "items": [self._ticket_dict(r) for r in rows]})
            return
        m = re.match(r"^/api/admin/tickets/([A-Za-z0-9\-]+)$", p)
        if m:
            con = db()
            row = con.execute("SELECT * FROM tickets WHERE ticket_no=?", (m.group(1),)).fetchone()
            con.close()
            if not row:
                return self._json(404, {"error": "工单不存在"})
            self._json(200, self._ticket_dict(row, include_replies=True))
            return
        # 配置没有任何 Web 读取/写入口：查看与修改一律用服务器本机的 deskctl.py，
        # 公网侧连"是否存在配置页"都不应被探测到。
        self._not_found()

    # ------------------------------------------------------------ POST
    def do_POST(self):
        p = urlparse(self.path).path
        if p != "/api/tickets":
            return self._not_found()
        b = self._body()
        title = str(b.get("title", "")).strip()[:200]
        content = str(b.get("content", "")).strip()[:20000]
        category = str(b.get("category", "其他")).strip()[:40]
        contact = str(b.get("contact", "")).strip()[:120]
        if not title or not content:
            return self._json(400, {"error": "标题和描述不能为空"})
        reply, source = ai_reply(load_cfg(), title, content, category)
        no = new_ticket_no()
        con = db()
        con.execute(
            "INSERT INTO tickets(ticket_no,title,content,category,contact,status,ai_reply,ai_source,created_at,updated_at)"
            " VALUES(?,?,?,?,?,?,?,?,?,?)",
            (no, title, content, category, contact, "待处理", reply, source, now(), now()))
        con.commit()
        con.close()
        self._json(201, {"ticket_no": no, "ai_reply": reply, "ai_source": source, "created_at": now()})

    # ------------------------------------------------------------ PUT
    def do_PUT(self):
        p = urlparse(self.path).path
        if not self._is_admin():
            return self._not_found()
        m = re.match(r"^/api/admin/tickets/([A-Za-z0-9\-]+)$", p)
        if m:
            b = self._body()
            status = str(b.get("status", "")).strip()[:20]
            reply = str(b.get("reply", "")).strip()[:10000]
            con = db()
            row = con.execute("SELECT id FROM tickets WHERE ticket_no=?", (m.group(1),)).fetchone()
            if not row:
                con.close()
                return self._json(404, {"error": "工单不存在"})
            if status:
                con.execute("UPDATE tickets SET status=?,updated_at=? WHERE id=?", (status, now(), row["id"]))
            if reply:
                con.execute("INSERT INTO replies(ticket_id,author,content,created_at) VALUES(?,?,?,?)",
                            (row["id"], "管理员", reply, now()))
                con.execute("UPDATE tickets SET updated_at=? WHERE id=?", (now(), row["id"]))
            con.commit()
            con.close()
            return self._json(200, {"ok": True})
        # 注意：没有 /api/admin/config 写入口 —— 配置只能由本地 deskctl.py 修改
        self._not_found()

    def do_OPTIONS(self):
        self.send_response(204)
        self.send_header("Allow", "GET, POST, PUT, OPTIONS")
        self.end_headers()


def main():
    init_db()
    cfg = load_cfg()
    print("[AI-Desk] 控制台: http://127.0.0.1:%d/%s?token=%s"
          % (PORT, cfg["admin_path"], cfg["admin_token"]), flush=True)
    print("[AI-Desk] AI: %s" % ("本地模型" if cfg["ai"].get("enabled") else "知识库模式"), flush=True)
    print("[AI-Desk] 监听 0.0.0.0:%d" % PORT, flush=True)
    srv = ThreadingHTTPServer(("0.0.0.0", PORT), Handler)
    srv.daemon_threads = True
    srv.serve_forever()


if __name__ == "__main__":
    main()

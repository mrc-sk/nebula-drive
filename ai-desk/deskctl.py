#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
deskctl.py —— 工单站本地运维命令行（只在服务器本机运行，无 Web 入口）

  python3 deskctl.py url                 显示管理控制台完整地址
  python3 deskctl.py config             查看 AI 配置（Key 脱敏）
  python3 deskctl.py set-ai --enable    启用本地模型
  python3 deskctl.py set-ai --disable   关闭模型，回到知识库模式
  python3 deskctl.py set-ai --model qwen2.5-0.5b-instruct --timeout 90
  python3 deskctl.py set-ai --base-url http://127.0.0.1:8083/v1
  python3 deskctl.py test               测试本地模型网关连通性
  python3 deskctl.py tickets            列出最近工单
  python3 deskctl.py status             服务与配置总览
"""
import os
import sys
import json
import socket
import sqlite3
import urllib.request

BASE = os.path.dirname(os.path.abspath(__file__))
CFG = os.path.join(BASE, "config.json")
DB = os.path.join(BASE, "data", "desk.db")
PORT = int(os.environ.get("AI_DESK_PORT", "8081"))


def load():
    with open(CFG, "r", encoding="utf-8") as f:
        return json.load(f)


def save(cfg):
    tmp = CFG + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        json.dump(cfg, f, ensure_ascii=False, indent=2)
    os.replace(tmp, CFG)
    os.chmod(CFG, 0o600)


def cmd_url(_):
    c = load()
    print("管理控制台（勿转发）：")
    print("  http://127.0.0.1:%d/%s?token=%s" % (PORT, c["admin_path"], c["admin_token"]))
    print("\n如需从本机远程访问，先建 SSH 隧道：")
    print("  ssh -L %d:127.0.0.1:%d root@<服务器> " % (PORT, PORT))
    print("  然后浏览器打开上面的地址。")


def cmd_config(_):
    c = load()
    ai = dict(c["ai"])
    ai["api_key"] = "<已设置>" if ai.get("api_key") else "<空（本地网关无需 Key）>"
    print(json.dumps({"site_name": c["site_name"], "ai": ai}, ensure_ascii=False, indent=2))


def cmd_status(_):
    c = load()
    print("站点名称 :", c["site_name"])
    print("监听端口 :", PORT)
    print("AI 模式  :", "本地模型" if c["ai"].get("enabled") else "本地知识库")
    print("网关地址 :", c["ai"].get("base_url"))
    print("模型名称 :", c["ai"].get("model"))
    try:
        with urllib.request.urlopen("http://127.0.0.1:%d/health" % PORT, timeout=5) as r:
            print("工单服务 :", json.loads(r.read().decode()) )
    except Exception as e:
        print("工单服务 : 不可达 -", e)
    try:
        con = sqlite3.connect(DB)
        n = con.execute("SELECT count(*) FROM tickets").fetchone()[0]
        con.close()
        print("工单总数 :", n)
    except Exception as e:
        print("工单总数 : 读取失败 -", e)


def cmd_set_ai(args):
    c = load()
    ai = c["ai"]
    i = 0
    while i < len(args):
        a = args[i]
        if a == "--enable":
            ai["enabled"] = True
        elif a == "--disable":
            ai["enabled"] = False
        elif a in ("--model", "--base-url", "--api-key", "--timeout") and i + 1 < len(args):
            k = {"--model": "model", "--base-url": "base_url",
                 "--api-key": "api_key", "--timeout": "timeout"}[a]
            v = args[i + 1]
            ai[k] = int(v) if k == "timeout" else v
            i += 1
        else:
            print("未知参数:", a)
            return 1
        i += 1
    save(c)
    print("已保存，服务会在下一次请求自动生效（无需重启）：")
    cmd_config([])
    return 0


def cmd_test(_):
    c = load()
    url = c["ai"]["base_url"].rstrip("/") + "/chat/completions"
    payload = json.dumps({
        "model": c["ai"].get("model", "local"),
        "messages": [{"role": "user", "content": "用一句话说明你是什么模型"}],
        "max_tokens": 60, "temperature": 0.3,
    }).encode("utf-8")
    print("请求:", url)
    try:
        req = urllib.request.Request(url, data=payload, headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(req, timeout=int(c["ai"].get("timeout", 90))) as r:
            d = json.loads(r.read().decode("utf-8"))
        print("模型回复:", d["choices"][0]["message"]["content"].strip())
        return 0
    except Exception as e:
        print("测试失败:", e)
        print("请确认本地模型服务在运行：systemctl status llm-local")
        return 1


def cmd_tickets(_):
    con = sqlite3.connect(DB)
    con.row_factory = sqlite3.Row
    rows = con.execute("SELECT ticket_no,title,status,ai_source,created_at FROM tickets ORDER BY id DESC LIMIT 30").fetchall()
    con.close()
    if not rows:
        print("暂无工单")
        return
    print("%-24s %-10s %-14s %-20s %s" % ("工单号", "状态", "来源", "时间", "标题"))
    for r in rows:
        print("%-24s %-10s %-14s %-20s %s" % (
            r["ticket_no"], r["status"], r["ai_source"], r["created_at"], r["title"][:40]))


CMDS = {"url": cmd_url, "config": cmd_config, "status": cmd_status,
        "set-ai": cmd_set_ai, "test": cmd_test, "tickets": cmd_tickets}


def main():
    if len(sys.argv) < 2 or sys.argv[1] in ("-h", "--help", "help"):
        print(__doc__)
        return 0
    fn = CMDS.get(sys.argv[1])
    if not fn:
        print("未知命令:", sys.argv[1]); print(__doc__)
        return 1
    return fn(sys.argv[2:]) or 0


if __name__ == "__main__":
    sys.exit(main())

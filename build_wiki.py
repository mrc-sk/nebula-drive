#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""生成 NebulaDrive 项目 Wiki 静态站（使用 Python-Markdown 渲染）。"""
import os
import re
import json
import html
import shutil

import markdown

SRC_ROOT = "/workspace/nebula-drive-plugin-v2"
OUT = "/workspace/wiki-site"

# (slug, 分类, 标题, 路径, 简介)
DOCS = [
    ("overview", "入门", "项目概览 README", f"{SRC_ROOT}/README.md", "NebulaDrive 是什么、功能清单与快速开始"),
    ("license", "入门", "AGPL 许可通俗说明", f"{SRC_ROOT}/AGPL_通俗说明.md", "开源协议怎么用、二次开发要注意什么"),

    ("plugin-manual", "插件开发", "插件制作手册", f"{SRC_ROOT}/docs/插件制作手册.md", "从零做一个插件：三文件契约、钩子、打包与调试"),
    ("plugin-v2-guide", "插件开发", "插件平台 v2 完全指南", f"{SRC_ROOT}/docs/插件平台v2-完全指南.md", "108 个钩子、注册型 Provider、v1 兼容与真接线点"),
    ("plugin-system-guide", "插件开发", "插件系统完全指南", f"{SRC_ROOT}/docs/插件系统完全指南.md", "插件运行时、进程外通信与生命周期"),
    ("pbclus-spec", "插件开发", "PBClus 格式规范", f"{SRC_ROOT}/docs/PBClus-格式规范.md", "插件包格式与分发规范"),
    ("plugin-hotload", "插件开发", "插件热加载：进度与接续", f"{SRC_ROOT}/docs/插件热加载-进度与接续.md", "热加载实现状态与后续计划"),
    ("plugin-builder", "插件开发", "可视化插件制作器使用说明", "/workspace/插件制作器-使用说明.md", "Python 桌面版：拖拽控件、21 套模板、首次教程"),

    ("changelog", "版本与交付", "CHANGELOG 变更记录", f"{SRC_ROOT}/CHANGELOG.md", "各版本功能与修复明细"),
    ("ai-era-delivery", "版本与交付", "智能时代交付说明", "/workspace/智能时代-交付说明.md", "AI 钩子、智能搜索、代码空间与网关设计"),
    ("v2-delivery", "版本与交付", "插件平台 v2 交付说明", "/workspace/nebula-drive-plugin-v2-交付说明.md", "v2 改造范围、验证结论与文件清单"),

    ("audit", "开发档案", "不足总结与改进建议", "/workspace/NebulaDrive-不足总结与改进建议.md", "站长视角与架构视角的审计结论"),
    ("code-review", "开发档案", "CODE REVIEW 代码审查", f"{SRC_ROOT}/CODE_REVIEW.md", "P0/P1 问题清单与修复思路"),
    ("fixlog", "开发档案", "FIXLOG 修复记录", f"{SRC_ROOT}/FIXLOG.md", "逐条缺陷根因与修复过程"),
    ("backlog", "开发档案", "新功能占位清单", f"{SRC_ROOT}/docs/新功能占位清单-明天接续.md", "未完成事项与接续指引"),

    ("ops-manual", "运维", "站点部署与运维手册", "/workspace/wiki-src/站点部署与运维手册.md", "六个服务、域名 HTTPS、本地模型、插件市场、备份与加固"),
    ("ticket-desk", "运维", "智能 AI 工单站使用说明", "/workspace/wiki-src/AI工单站使用说明.md", "安全版：隐藏控制台、本机 CLI 配置、本地小模型与知识库兜底"),
    ("plugin-store", "运维", "官方插件市场与插件清单", "/workspace/wiki-src/官方插件市场与插件清单.md", "10 个官方签名插件、信任库、安装启用与二次开发"),
]

CATEGORY_ORDER = ["入门", "插件开发", "版本与交付", "开发档案", "运维"]
CATEGORY_ICON = {"入门": "▶", "插件开发": "◈", "版本与交付": "▤", "开发档案": "▣", "运维": "⚙"}

CSS = r"""
*{box-sizing:border-box}
body{margin:0;background:#0f1420;color:#dfe6f2;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;line-height:1.75}
a{color:#7fb0ff;text-decoration:none}a:hover{text-decoration:underline}
#sidebar{position:fixed;left:0;top:0;bottom:0;width:270px;background:#131a29;border-right:1px solid #232d42;overflow-y:auto;padding:20px 14px 40px}
#sidebar .brand{font-size:16px;font-weight:700;color:#fff;padding:4px 10px 16px;border-bottom:1px solid #232d42;margin-bottom:14px}
#sidebar .brand small{display:block;color:#7e8ba3;font-weight:400;font-size:11.5px;margin-top:4px}
#search{width:100%;background:#0d1320;border:1px solid #2b3550;color:#dfe6f2;border-radius:8px;padding:9px 11px;font-size:13px;outline:none;margin-bottom:8px}
#search:focus{border-color:#4a7ae8}
#searchResults{font-size:12.5px;margin-bottom:12px}
#searchResults a{display:block;padding:6px 9px;border-radius:6px;color:#b9c8e2}
#searchResults a:hover{background:#1d2941;text-decoration:none}
#searchResults .sr-cat{color:#6f7c93;font-size:11px}
.nav-group{margin-top:16px}
.nav-group h4{font-size:11.5px;color:#6f7d96;text-transform:uppercase;letter-spacing:1px;margin:0 0 7px 10px;font-weight:600}
.nav-group a{display:block;padding:7px 10px;border-radius:7px;color:#a9b7cf;font-size:13.5px;margin-bottom:2px}
.nav-group a:hover{background:#1b2437;color:#fff;text-decoration:none}
.nav-group a.active{background:#22407e;color:#fff}
#main{margin-left:270px;padding:30px 46px 90px;max-width:1180px}
.crumb{color:#77849b;font-size:12.5px;margin-bottom:18px}
.crumb span{color:#a9b7cf}
h1{font-size:27px;color:#fff;margin:0 0 8px;font-weight:680;letter-spacing:.3px}
.desc{color:#8895ac;font-size:14px;margin:0 0 26px;padding-bottom:20px;border-bottom:1px solid #232d42}
.md h1,.md h2,.md h3,.md h4{color:#fff;font-weight:640;line-height:1.4;margin:30px 0 12px}
.md h1{font-size:23px}.md h2{font-size:20px;border-bottom:1px solid #232d42;padding-bottom:9px}
.md h3{font-size:17px}.md h4{font-size:15px}
.md p{margin:12px 0}.md ul,.md ol{padding-left:24px;margin:12px 0}.md li{margin:5px 0}
.md strong{color:#fff}
.md blockquote{margin:16px 0;padding:12px 18px;background:#152039;border-left:3px solid #3d6bd8;border-radius:0 8px 8px 0;color:#b7c5de}
.md blockquote p{margin:4px 0}
.md code{background:#1b2436;color:#9fd0ff;padding:2px 6px;border-radius:5px;font-size:12.8px;font-family:"SFMono-Regular",Consolas,"Liberation Mono",monospace}
.md pre{background:#0c1220;border:1px solid #232d42;border-radius:10px;padding:16px 18px;overflow-x:auto;margin:16px 0}
.md pre code{background:none;color:#c6d3e8;padding:0;font-size:12.8px;line-height:1.65}
.md table{width:100%;border-collapse:collapse;margin:18px 0;font-size:13.5px;display:block;overflow-x:auto}
.md th,.md td{border:1px solid #27324a;padding:9px 12px;text-align:left}
.md th{background:#1a2438;color:#b9c8e2;font-weight:600;white-space:nowrap}
.md tr:nth-child(even){background:#131b2b}
.md hr{border:0;border-top:1px solid #232d42;margin:28px 0}
.md img{max-width:100%;border-radius:8px}
.foot{margin-top:50px;padding-top:18px;border-top:1px solid #232d42;color:#657189;font-size:12px}
.card-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(230px,1fr));gap:14px;margin:22px 0}
.card{background:#161e2e;border:1px solid #26314a;border-radius:12px;padding:18px}
.card h3{margin:0 0 7px;font-size:15px;color:#fff}
.card p{margin:0;color:#8d9ab2;font-size:13px}
.card a{font-size:13px}
.hero{background:linear-gradient(135deg,#16233d,#1a2c4e);border:1px solid #2a3f6b;border-radius:14px;padding:26px 28px;margin-bottom:26px}
.hero h1{margin:0 0 8px}.hero p{color:#a6b7d6;margin:0;font-size:14px}
.stat{display:inline-block;background:#1c2a45;color:#9dc0ff;border-radius:16px;padding:3px 11px;font-size:12px;margin:10px 6px 0 0}
@media(max-width:860px){#sidebar{position:static;width:100%;height:auto;max-height:320px}#main{margin-left:0;padding:22px 18px 60px}}
"""

JS = r"""
let DATA=[];
fetch('search.json').then(r=>r.json()).then(d=>{DATA=d}).catch(()=>{});
const box=document.getElementById('searchResults'), input=document.getElementById('search');
input.addEventListener('input',()=>{
  const q=input.value.trim().toLowerCase();
  if(!q){box.innerHTML='';return;}
  const hits=DATA.filter(x=>(x.title+' '+x.cat+' '+x.text).toLowerCase().includes(q)).slice(0,12);
  box.innerHTML=hits.length?hits.map(x=>'<a href="pages/'+x.slug+'.html"><span class="sr-cat">'+x.cat+'</span> · '+x.title+'</a>').join(''):'<div style="color:#6f7c93;padding:6px 10px">无匹配结果</div>';
});
"""

PAGE_TPL = r"""<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>__TITLE__ · NebulaDrive Wiki</title>
<link rel="stylesheet" href="__ROOT__assets/style.css"></head><body>
<aside id="sidebar">
<div class="brand">NebulaDrive Wiki<small>项目文档 · 部署运维 · 插件开发</small></div>
<input id="search" placeholder="搜索文档…" autocomplete="off">
<div id="searchResults"></div>
__NAV__
</aside>
<main id="main">
<div class="crumb"><a href="__ROOT__index.html">首页</a> / <span>__CAT__</span> / __TITLE__</div>
__BODY__
<div class="foot">NebulaDrive Wiki · 由项目文档自动构建 · 最后生成 __DATE__</div>
</main>
<script src="__ROOT__assets/app.js"></script></body></html>"""

INDEX_TPL = r"""<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>NebulaDrive Wiki</title>
<link rel="stylesheet" href="assets/style.css"></head><body>
<aside id="sidebar">
<div class="brand">NebulaDrive Wiki<small>项目文档 · 部署运维 · 插件开发</small></div>
<input id="search" placeholder="搜索文档…" autocomplete="off">
<div id="searchResults"></div>
__NAV__
</aside>
<main id="main">
<div class="hero">
<h1>NebulaDrive 项目 Wiki</h1>
<p>网盘主程序、插件平台 v2、智能时代能力与本次服务器部署的统一文档入口。</p>
<div><span class="stat">__NDOCS__ 篇文档</span><span class="stat">__NCATS__ 个分类</span><span class="stat">全文搜索</span></div>
</div>
<div style="margin:0 0 26px;padding:14px 18px;border-radius:10px;background:#12233a;border:1px solid #2b4c6b;border-left:4px solid #6ce0a8">
<div style="font-weight:800;color:#6ce0a8;letter-spacing:.04em;margin-bottom:6px">📌 置顶 · 当前版本 V26-11.1（上线热修复）</div>
<div style="font-size:13.5px;line-height:1.8;color:#cddcf0">上线前终检后的小修版本：对外物料去除源站信息、AI 工单答复补充站点事实约束、版本口径统一。<b style="color:#eef3ff">产品功能与部署零改动，主程序无需重装</b>。详见 <a href="pages/changelog.html" style="color:#48d6ec">CHANGELOG 变更记录</a> 顶部置顶条目。</div>
</div>
<h1 style="font-size:20px">线上服务</h1>
<div class="card-grid">
<div class="card"><h3>☁ 网盘主站</h3><p>文件存储、分享、WebDAV、插件与 AI 能力</p><a href="http://__IP__/">→ 打开主站（80 端口）</a></div>
<div class="card"><h3>◈ 智能 AI 工单站</h3><p>提交问题、自动答复、后台跟进</p><a href="http://__IP__:8081/">→ 提交 / 管理工单</a></div>
<div class="card"><h3>▤ 项目 Wiki</h3><p>就是你现在看到的这个站点</p><a href="index.html">→ 文档首页</a></div>
</div>
<h1 style="font-size:20px;margin-top:30px">从哪里开始</h1>
<div class="card-grid">
<div class="card"><h3>刚接手运维？</h3><p>端口、服务、管理员、配额、注册限制、备份与加固</p><a href="pages/ops-manual.html">→ 站点部署与运维手册</a></div>
<div class="card"><h3>要写插件？</h3><p>三文件契约、108 个钩子、打包与调试</p><a href="pages/plugin-manual.html">→ 插件制作手册</a></div>
<div class="card"><h3>想用可视化工具？</h3><p>Python 桌面制作器、21 套模板</p><a href="pages/plugin-builder.html">→ 制作器使用说明</a></div>
<div class="card"><h3>了解架构决策？</h3><p>审计结论、代码审查与修复记录</p><a href="pages/audit.html">→ 不足总结与改进建议</a></div>
</div>
<h1 style="font-size:20px;margin-top:30px">全部文档</h1>
__CATALOG__
<div class="foot">NebulaDrive Wiki · 由项目文档自动构建 · 最后生成 __DATE__</div>
</main>
<script src="assets/app.js"></script></body></html>"""


def esc(s):
    return html.escape(str(s or ""), quote=True)


def build_nav(active_slug, root):
    parts = []
    for cat in CATEGORY_ORDER:
        items = [d for d in DOCS if d[1] == cat]
        if not items:
            continue
        links = []
        for slug, _cat, title, _path, _desc in items:
            cls = ' class="active"' if slug == active_slug else ""
            links.append(f'<a{cls} href="{root}pages/{slug}.html">{esc(title)}</a>')
        parts.append('<div class="nav-group"><h4>%s %s</h4>%s</div>'
                     % (CATEGORY_ICON.get(cat, "·"), esc(cat), "".join(links)))
    parts.append('<div class="nav-group"><h4>· 首页</h4><a href="%sindex.html">Wiki 首页</a></div>' % root)
    return "".join(parts)


def fix_links(text, basename_map):
    def repl(m):
        target = m.group(1)
        if target.startswith(("http://", "https://", "#", "mailto:")):
            return m.group(0)
        clean = target.split("#")[0].strip()
        anchor = "#" + target.split("#", 1)[1] if "#" in target else ""
        base = os.path.basename(clean)
        if base.lower().endswith(".md") and base in basename_map:
            return f"]({basename_map[base]}{anchor})"
        return m.group(0)
    return re.sub(r"\]\(([^)]+)\)", repl, text)


def main():
    if os.path.isdir(OUT):
        shutil.rmtree(OUT)
    os.makedirs(os.path.join(OUT, "pages"))
    os.makedirs(os.path.join(OUT, "assets"))

    basename_map = {}
    for d in DOCS:
        basename_map[os.path.basename(d[3])] = d[0] + ".html"

    with open(os.path.join(OUT, "assets", "style.css"), "w", encoding="utf-8") as f:
        f.write(CSS)
    with open(os.path.join(OUT, "assets", "app.js"), "w", encoding="utf-8") as f:
        f.write(JS)

    search, catalog, ok, missing = [], [], 0, []
    import datetime
    date = datetime.date.today().isoformat()

    for slug, cat, title, path, desc in DOCS:
        if not os.path.isfile(path):
            missing.append(path)
            continue
        with open(path, "r", encoding="utf-8", errors="replace") as f:
            raw = f.read()
        raw = fix_links(raw, basename_map)
        body = markdown.markdown(raw, extensions=["tables", "fenced_code", "toc", "sane_lists", "attr_list"])
        page = (PAGE_TPL
                .replace("__TITLE__", esc(title))
                .replace("__CAT__", esc(cat))
                .replace("__ROOT__", "../")
                .replace("__NAV__", build_nav(slug, "../"))
                .replace("__BODY__", '<article class="md"><p class="desc">%s</p>%s</article>' % (esc(desc), body))
                .replace("__DATE__", date))
        with open(os.path.join(OUT, "pages", slug + ".html"), "w", encoding="utf-8") as f:
            f.write(page)
        plain = re.sub(r"<[^>]+>", " ", body)
        plain = re.sub(r"\s+", " ", html.unescape(plain))[:3000]
        search.append({"slug": slug, "title": title, "cat": cat, "text": plain})
        ok += 1

    # 首页分类目录
    for cat in CATEGORY_ORDER:
        items = [d for d in DOCS if d[1] == cat and os.path.isfile(d[3])]
        if not items:
            continue
        lis = "".join('<li><a href="pages/%s.html">%s</a> — <span style="color:#8895ac">%s</span></li>'
                      % (slug, esc(t), esc(desc)) for slug, _c, t, _p, desc in items)
        catalog.append('<div class="card" style="margin:14px 0"><h3>%s %s</h3><ul style="margin:8px 0 0;padding-left:20px;font-size:13.5px;line-height:2">%s</ul></div>'
                       % (CATEGORY_ICON.get(cat, "·"), esc(cat), lis))

    index = (INDEX_TPL
             .replace("__NAV__", build_nav(None, ""))
             .replace("__IP__", "YOUR_SERVER_IP")
             .replace("__NDOCS__", str(ok))
             .replace("__NCATS__", str(len(CATEGORY_ORDER)))
             .replace("__CATALOG__", "".join(catalog))
             .replace("__DATE__", date))
    with open(os.path.join(OUT, "index.html"), "w", encoding="utf-8") as f:
        f.write(index)
    with open(os.path.join(OUT, "search.json"), "w", encoding="utf-8") as f:
        json.dump(search, f, ensure_ascii=False)

    print("生成完成: %d 篇文档, 输出 %s" % (ok, OUT))
    if missing:
        print("缺失素材: %s" % missing)
    total = sum(os.path.getsize(os.path.join(dp, fn))
                for dp, _dn, fns in os.walk(OUT) for fn in fns)
    print("总大小: %.1f KB" % (total / 1024))


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
把 docs/plugin-wiki/ 下的 Markdown wiki 渲染成一个自包含、好看的 HTML 文档站。
无第三方依赖：内置一套针对本 wiki 语法的 Markdown->HTML 转换器。

用法：python build_wiki.py
产出：docs/plugin-wiki/index.html
"""
import os, re, html, json

HERE = os.path.dirname(os.path.abspath(__file__))

# 文件名 -> 侧栏 key / 标题
SECTIONS = [
    ("README.md",          "overview",   "开始之前"),
    ("01-architecture.md", "architecture","01 · 架构"),
    ("02-manifest.md",      "manifest",   "02 · 清单 plugin.json"),
    ("03-protocol.md",      "protocol",   "03 · 协议 JSON-RPC"),
    ("04-hooks.md",         "hooks",      "04 · 钩子清单"),
    ("05-signing-trust.md", "trust",      "05 · 签名与信任"),
    ("06-tutorial.md",      "tutorial",   "06 · 上手教程"),
    ("07-examples.md",      "examples",   "07 · 官方示例"),
    ("08-admin.md",         "admin",      "08 · 管理端与 API"),
]

FNAME_TO_KEY = {f: k for f, k, _ in SECTIONS}


def slug(s: str) -> str:
    s = s.strip().lower()
    s = re.sub(r"[^\w\u4e00-\u9fff\- ]+", "", s)
    s = s.replace(" ", "-")
    s = re.sub(r"-+", "-", s).strip("-")
    return s or "h"


def inline(text: str) -> str:
    # 先抽离行内代码，避免其中的 ** / [..](..) 被误处理
    codes = []
    def stash(m):
        codes.append(m.group(1))
        return f"\x00{len(codes)-1}\x00"
    text = re.sub(r"`([^`]+)`", stash, text)
    # 链接 [text](url)
    text = re.sub(r"\[([^\]]+)\]\(([^)]+)\)", lambda m: _link(m.group(1), m.group(2)), text)
    # 粗体 **x**
    text = re.sub(r"\*\*([^*]+)\*\*", r"<strong>\1</strong>", text)
    # 还原代码
    def unstash(m):
        i = int(m.group(1))
        return "<code>" + html.escape(codes[i]) + "</code>"
    text = re.sub(r"\x00(\d+)\x00", unstash, text)
    return text


def _link(text, url):
    # 站内侧链 *.md -> 对应 section 锚点；外部(../ 或 http) 保留
    if url.endswith(".md") or ".md#" in url:
        base = url.split("#")[0]
        frag = "#" + url.split("#", 1)[1] if "#" in url else ""
        bn = os.path.basename(base)
        key = FNAME_TO_KEY.get(bn)
        if key:
            return f'<a href="#sec-{key}">{text}</a>'
    return f'<a href="{html.escape(url)}">{text}</a>'


def esc(t):
    return html.escape(t, quote=False)


def split_rows(line):
    line = line.strip()
    if line.startswith("|"):
        line = line[1:]
    if line.endswith("|"):
        line = line[:-1]
    return [c.strip() for c in line.split("|")]


def is_table_sep(line):
    if "|" not in line:
        return False
    s = line.strip().strip("|")
    return bool(re.match(r"^[\s:\-|]+$", s)) and "-" in s


def render_markdown(md: str, key: str):
    lines = md.split("\n")
    out = []
    nav = []          # (level, text, anchor)
    i = 0
    in_fence = False
    fence_buf = []
    fence_lang = ""

    def flush_fence():
        nonlocal fence_buf, fence_lang
        lang = fence_lang or ""
        code = "\n".join(fence_buf)
        out.append(f'<pre class="code" data-lang="{esc(lang)}"><code>{esc(code)}</code></pre>')
        fence_buf = []
        fence_lang = ""

    while i < len(lines):
        line = lines[i]
        # 围栏代码
        m = re.match(r"^```(\w+)?\s*$", line.strip())
        if m:
            if not in_fence:
                in_fence = True
                fence_lang = m.group(1) or ""
            else:
                flush_fence()
                in_fence = False
            i += 1
            continue
        if in_fence:
            fence_buf.append(line)
            i += 1
            continue

        # 空行
        if line.strip() == "":
            i += 1
            continue

        # 标题
        hm = re.match(r"^(#{1,4})\s+(.*)$", line)
        if hm:
            level = len(hm.group(1))
            txt = hm.group(2).strip()
            anchor = slug(txt)
            if level == 1:
                anchor = key
            else:
                anchor = f"{key}-{anchor}"
            cls = {1: "h1", 2: "h2", 3: "h3", 4: "h4"}[level]
            out.append(f'<{cls} id="{anchor}">{inline(txt)}</{cls}>')
            if level in (1, 2):
                nav.append((level, txt, anchor))
            i += 1
            continue

        # 表格
        if "|" in line and i + 1 < len(lines) and is_table_sep(lines[i + 1]):
            hdr = split_rows(line)
            # 跳过分隔行
            j = i + 2
            rows = []
            while j < len(lines) and "|" in lines[j] and lines[j].strip():
                rows.append(split_rows(lines[j]))
                j += 1
            cells = "".join(f"<th>{inline(c)}</th>" for c in hdr)
            thead = f"<thead><tr>{cells}</tr></thead>"
            body = ""
            for r in rows:
                tds = "".join(f"<td>{inline(c)}</td>" for c in r)
                body += f"<tr>{tds}</tr>"
            out.append(f'<div class="table-wrap"><table>{thead}<tbody>{body}</tbody></table></div>')
            i = j
            continue

        # 引用
        if line.lstrip().startswith(">"):
            buf = []
            while i < len(lines) and lines[i].lstrip().startswith(">"):
                buf.append(re.sub(r"^\s*>\s?", "", lines[i]))
                i += 1
            inner = "".join(f"<p>{inline(b)}</p>" for b in buf if b.strip() != "")
            out.append(f'<blockquote>{inner}</blockquote>')
            continue

        # 分隔线
        if re.match(r"^(\-{3,}|\*{3,})\s*$", line.strip()):
            out.append("<hr>")
            i += 1
            continue

        # 列表（有序 / 无序，支持缩进嵌套）
        if re.match(r"^\s*([\-\*]|\d+\.)\s+", line):
            ordered = bool(re.match(r"^\s*\d+\.\s+", line))
            buf = []
            while i < len(lines) and re.match(r"^\s*([\-\*]|\d+\.)\s+", lines[i]):
                buf.append(lines[i])
                i += 1
            out.append(render_list(buf, ordered))
            continue

        # 普通段落（合并连续非空白、非特殊行）
        buf = [line]
        i += 1
        while (i < len(lines) and lines[i].strip() != "" and not lines[i].lstrip().startswith((">", "#", "-", "*"))
               and not re.match(r"^\s*\d+\.\s+", lines[i])
               and not re.match(r"^```", lines[i])
               and not is_table_sep(lines[i])):
            buf.append(lines[i])
            i += 1
        para = " ".join(x.strip() for x in buf)
        out.append(f"<p>{inline(para)}</p>")

    if in_fence:
        flush_fence()
    return "\n".join(out), nav


def render_list(buf, ordered):
    # 计算缩进层级
    items = []
    for ln in buf:
        ind = len(ln) - len(ln.lstrip(" "))
        m = re.match(r"^\s*([\-\*]|\d+\.)\s+(.*)$", ln)
        items.append((ind, m.group(2)))
    # 简单两层级处理：按缩进分桶
    max_ind = max(ind for ind, _ in items) if items else 0
    top = [x for x in items if x[0] <= 2]
    nested = [x for x in items if x[0] > 2]
    def make(lst):
        tag = "ol" if ordered else "ul"
        lis = "".join(f"<li>{inline(t)}</li>" for _, t in lst)
        return f"<{tag}>{lis}</{tag}>"
    if nested and top:
        # 把 nested 追加到最后一个 top item（常见：说明紧跟在条目下）
        # 这里采用：若嵌套项紧跟在某条目后，则并入该条目
        result_items = []
        i = 0
        while i < len(items):
            ind, t = items[i]
            # 收集后续缩进更大的作为子列表
            sub = []
            j = i + 1
            while j < len(items) and items[j][0] > ind:
                sub.append(items[j])
                j += 1
            inner = inline(t)
            if sub:
                sub_ordered = bool(re.match(r"^\s*\d+\.\s+", sub[0][1])) if False else ordered
                # 子列表沿用父级有序性
                inner += make(sub)
            result_items.append(f"<li>{inner}</li>")
            i = j
        tag = "ol" if ordered else "ul"
        return f"<{tag}>" + "".join(result_items) + f"</{tag}>"
    return make(items)


def build():
    sections_html = []
    nav_top = []
    for fname, key, title in SECTIONS:
        path = os.path.join(HERE, fname)
        with open(path, encoding="utf-8") as f:
            md = f.read()
        body, nav = render_markdown(md, key)
        sections_html.append(f'<section id="sec-{key}" class="section">{body}</section>')
        # 该 section 的二级导航
        subs = [n for n in nav if n[0] == 2]
        nav_top.append((key, title, subs))

    sidebar = []
    for key, title, subs in nav_top:
        items = "".join(f'<li><a href="#{aid}" class="sub">{esc(t)}</a></li>' for _, t, aid in subs)
        sidebar.append(
            f'<div class="nav-group">'
            f'<a href="#sec-{key}" class="nav-top">{esc(title)}</a>'
            f'<ul class="nav-sub">{items}</ul>'
            f'</div>'
        )
    sidebar_html = "\n".join(sidebar)
    content_html = "\n".join(sections_html)

    tpl = HTML_TEMPLATE.replace("__SIDEBAR__", sidebar_html).replace("__CONTENT__", content_html)
    out_path = os.path.join(HERE, "index.html")
    with open(out_path, "w", encoding="utf-8") as f:
        f.write(tpl)
    print("written", out_path, os.path.getsize(out_path), "bytes")


HTML_TEMPLATE = r"""<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>NebulaDrive 插件开发 Wiki</title>
<style>
:root{
  --bg:#0B1020; --bg2:#0F1626; --panel:#121A2E; --panel2:#16203A;
  --text:#E6EAF2; --muted:#9AA6BE; --faint:#6B7690;
  --indigo:#818CF8; --cyan:#22D3EE; --purple:#A855F7; --green:#34D399;
  --border:rgba(255,255,255,.08); --border2:rgba(255,255,255,.14);
  --code-bg:#0A0F1E;
  --maxw:880px;
}
*{box-sizing:border-box}
html,body{margin:0;padding:0}
body{
  background:
    radial-gradient(1200px 600px at 80% -10%, rgba(129,140,248,.10), transparent 60%),
    radial-gradient(900px 500px at -10% 10%, rgba(34,211,238,.08), transparent 55%),
    radial-gradient(700px 500px at 50% 120%, rgba(168,85,247,.08), transparent 60%),
    var(--bg);
  color:var(--text);
  font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Hiragino Sans GB","Microsoft YaHei",sans-serif;
  line-height:1.75; font-size:16px;
  -webkit-font-smoothing:antialiased;
}
a{color:var(--cyan);text-decoration:none}
a:hover{text-decoration:underline}
.layout{display:flex;min-height:100vh}
/* ---- sidebar ---- */
.sidebar{
  width:300px;flex:0 0 300px;position:sticky;top:0;height:100vh;overflow-y:auto;
  background:linear-gradient(180deg,rgba(18,26,46,.95),rgba(11,16,32,.95));
  border-right:1px solid var(--border);padding:22px 16px 60px;backdrop-filter:blur(6px);
}
.sidebar .brand{display:flex;align-items:center;gap:10px;margin-bottom:6px;padding:0 6px}
.sidebar .logo{width:30px;height:30px;border-radius:8px;
  background:conic-gradient(from 200deg,var(--indigo),var(--cyan),var(--purple),var(--indigo));
  box-shadow:0 0 18px rgba(129,140,248,.45)}
.sidebar .brand b{font-size:15px;letter-spacing:.3px}
.sidebar .ver{font-size:11px;color:var(--faint);padding:0 6px 14px}
.nav-group{margin:6px 0}
.nav-top{display:block;font-weight:600;font-size:13.5px;color:var(--text);
  padding:7px 10px;border-radius:8px;transition:.15s}
.nav-top:hover{background:var(--panel2);text-decoration:none;color:#fff}
.nav-sub{list-style:none;margin:2px 0 2px;padding:0 0 0 10px;
  border-left:1px solid var(--border)}
.nav-sub li a{display:block;font-size:12.5px;color:var(--muted);padding:4px 10px;border-radius:6px}
.nav-sub li a:hover{background:var(--panel2);color:#fff;text-decoration:none}
.nav-top.active,.nav-sub li a.active{color:#fff;background:linear-gradient(90deg,rgba(129,140,248,.22),transparent)}
.nav-sub li a.active{box-shadow:inset 2px 0 0 var(--cyan)}
/* ---- main ---- */
.main{flex:1;min-width:0;display:flex;justify-content:center;padding:48px 28px 120px}
.content{width:100%;max-width:var(--maxw)}
.section{scroll-margin-top:24px}
.section + .section{margin-top:8px}
h1{font-size:30px;line-height:1.25;margin:.2em 0 .6em;
  background:linear-gradient(90deg,var(--indigo),var(--cyan));-webkit-background-clip:text;background-clip:text;color:transparent}
h2{font-size:22px;margin:1.8em 0 .7em;padding-bottom:.3em;border-bottom:1px solid var(--border);color:#fff}
h3{font-size:18px;margin:1.5em 0 .5em;color:var(--indigo)}
h4{font-size:15.5px;margin:1.2em 0 .4em;color:var(--cyan)}
p{margin:.7em 0}
ul,ol{margin:.7em 0;padding-left:1.4em}
li{margin:.3em 0}
.h2{scroll-margin-top:20px}
hr{border:none;border-top:1px solid var(--border);margin:2em 0}
blockquote{margin:1em 0;padding:12px 16px;border-left:3px solid var(--purple);
  background:rgba(168,85,247,.08);border-radius:0 8px 8px 0;color:var(--muted)}
blockquote p{margin:.3em 0}
code{font-family:"JetBrains Mono",Consolas,"SFMono-Regular",Menlo,monospace;
  font-size:.88em;background:rgba(255,255,255,.06);padding:.12em .4em;border-radius:5px;
  border:1px solid var(--border)}
pre.code{position:relative;margin:1em 0;background:var(--code-bg);border:1px solid var(--border2);
  border-radius:10px;padding:16px 16px 14px;overflow-x:auto}
pre.code code{background:none;border:none;padding:0;font-size:13px;line-height:1.6;color:#cdd6f4}
pre.code[data-lang]:not([data-lang=""])::before{content:attr(data-lang);position:absolute;top:8px;right:12px;
  font-size:10px;letter-spacing:.5px;text-transform:uppercase;color:var(--faint)}
.table-wrap{overflow-x:auto;margin:1em 0;border:1px solid var(--border);border-radius:10px}
table{border-collapse:collapse;width:100%;font-size:14px}
th,td{padding:9px 13px;text-align:left;border-bottom:1px solid var(--border)}
thead th{background:linear-gradient(180deg,rgba(129,140,248,.16),rgba(129,140,248,.06));
  color:#fff;font-weight:600;border-bottom:1px solid var(--border2)}
tbody tr:nth-child(odd){background:rgba(255,255,255,.02)}
tbody tr:hover{background:rgba(34,211,238,.06)}
/* top bar inside content */
.topbar{position:sticky;top:0;z-index:5;margin:-48px -28px 24px;padding:14px 28px;
  background:rgba(11,16,32,.7);backdrop-filter:blur(8px);border-bottom:1px solid var(--border);
  display:none}
@media (max-width:880px){
  .sidebar{display:none}
  .main{padding:24px 16px 80px}
  .topbar{display:block}
}
</style>
</head>
<body>
<div class="layout">
  <aside class="sidebar">
    <div class="brand"><div class="logo"></div><b>NebulaDrive</b></div>
    <div class="ver">插件开发 Wiki · V26-10.0-d</div>
    __SIDEBAR__
  </aside>
  <div class="main">
    <div class="content">
      __CONTENT__
    </div>
  </div>
</div>
<script>
// 侧栏高亮：滚动到哪个 section / 子标题，就高亮对应导航项
const groups = document.querySelectorAll('.nav-group');
const tops = [...document.querySelectorAll('.nav-top')];
const subs = [...document.querySelectorAll('.nav-sub li a')];
const map = new Map();
subs.forEach(a=>map.set(a.getAttribute('href').slice(1), a));
tops.forEach(a=>map.set(a.getAttribute('href').slice(1), a));
const targets = [...document.querySelectorAll('section[id^="sec-"] h2, section[id^="sec-"] h3, section[id^="sec-"]')];
const obs = new IntersectionObserver((entries)=>{
  entries.forEach(e=>{
    if(e.isIntersecting){
      const id = e.target.id;
      map.forEach(v=>v.classList.remove('active'));
      const el = map.get(id);
      if(el){ el.classList.add('active');
        const top = el.classList.contains('sub') ? el.closest('.nav-group').querySelector('.nav-top') : el;
        top && top.classList.add('active');
      }
    }
  });
},{rootMargin:'-10% 0px -75% 0px',threshold:0});
targets.forEach(t=>obs.observe(t));
// 默认高亮第一项
if(tops[0]) tops[0].classList.add('active');
</script>
</body>
</html>
"""


if __name__ == "__main__":
    build()

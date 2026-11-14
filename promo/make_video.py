#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""NebulaDrive 宣传片 v2：30 镜 × 8 秒 = 4 分钟。
每屏只讲一件事（减负），画面随 120 BPM 节拍脉冲，切镜带光扫冲击。
Pillow 逐帧渲染 + ffmpeg rawvideo 管道编码；配乐见 make_bgm.py。
"""
import math
import os
import subprocess
import sys
from functools import lru_cache

import numpy as np
from PIL import Image, ImageDraw, ImageFont

W, H, FPS = 1280, 720, 24
SHOT_DUR = 8.0
BEAT = 0.5                      # 120 BPM
OUT = "/workspace/promo/NebulaDrive-宣传片.mp4"
BGM = "/workspace/promo/assets/bgm.wav"
ASSETS = "/workspace/promo/assets"

TOP = np.array([7, 10, 24], np.float32)
BOT = np.array([15, 21, 46], np.float32)
PURPLE = (147, 116, 255)
CYAN = (72, 214, 236)
ORANGE = (255, 183, 96)
WHITE = (238, 243, 255)
MUTED = (146, 160, 198)
GREEN = (108, 224, 168)
LINE = (92, 108, 160)
F_SANS = "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc"
F_BOLD = "/usr/share/fonts/opentype/noto/NotoSansCJK-Bold.ttc"

# 30 镜，每镜 8 秒；sub = 屏幕字幕，main/lead = 画面标题
SCENES = [
    dict(t="序章", key="logo",
         sub="把数据，放进自己的星云",
         main="NebulaDrive", lead="PRIVATE CLOUD · PLUGIN PLATFORM v2 · LOCAL AI"),

    dict(t="定位", key="cards",
         sub="自托管 · 可扩展 · AI 就绪",
         main="不只是网盘", lead="一套可以被社区无限扩展的私有云底座",
         items=[("◈", "自托管", ["数据落在自己的服务器", "不依赖任何外部账户"], PURPLE),
                ("◈", "可扩展", ["大功能交给社区插件", "108 个事件钩子真接线"], CYAN),
                ("◈", "AI 就绪", ["三档智能可插拔", "本地小模型兜底"], ORANGE)]),

    dict(t="技术底座", key="big",
         sub="一条命令启动，数据全部落在自己的磁盘",
         main="单文件，就能跑", lead="Go 编译产物内嵌完整前端，部署不需要额外装依赖",
         big="1", unit="个二进制文件", accent=CYAN,
         points=["单文件 Linux x86_64，约 65 MB", "前端静态资源已内嵌，无需 Nginx",
                 "systemd 托管，开机自启"]),

    dict(t="已经上线", key="shot",
         sub="cloud.berrywing.top · 公网可直接访问",
         main="已经上线", lead="不是 Demo，是一台真实在跑的服务器",
         shot="home", url="https://cloud.berrywing.top/", accent=CYAN,
         points=["域名已解析到 Cloudflare", "首页实测返回 200"]),

    dict(t="HTTPS 与回源", key="steps",
         sub="域名解析到 Cloudflare，回源转发到本机 80 端口",
         main="HTTPS 与回源", lead="Cloudflare Full 模式，源站 443 终止 TLS",
         items=[("Cloudflare", "公网入口 / 证书", CYAN),
                ("源站 443", "socat 终止 TLS", PURPLE),
                ("网盘 :80", "nebuladrive 进程", GREEN)]),

    dict(t="三站点", key="cards",
         sub="六个 systemd 服务开机自启",
         main="三站点，一次部署", lead="网盘 / 工单 / 文档，各司其职",
         items=[("◈", "网盘主站", ["cloud.berrywing.top", "文件 / 分享 / 后台"], PURPLE),
                ("◈", "AI 工单站", ["提交 / 查询工单", "本地小模型自动答复"], CYAN),
                ("◈", "项目 Wiki", ["18 篇文档 · 全文搜索", "侧边栏 + 分类导航"], ORANGE)]),

    dict(t="文件管理", key="shot",
         sub="列表与网格双视图，操作都在一个界面",
         main="文件管理", lead="从上传到回收站，完整闭环",
         shot="files", url="cloud.berrywing.top / files", accent=PURPLE,
         points=["上传与新建文件夹", "分享列表 / 离线下载", "回收站与侧边导航"]),

    dict(t="上传与分享", key="cards",
         sub="链接策略可强制密码与有效期",
         main="上传与分享", lead="分享不是把文件丢到公网就完事",
         items=[("↑", "上传", ["断点与批量", "类型与大小可守卫"], CYAN),
                ("↗", "分享链接", ["可强制密码", "可强制有效期"], PURPLE),
                ("↺", "回收站", ["误删可恢复", "不占立即释放"], ORANGE)]),

    dict(t="更多接入", key="big",
         sub="官方插件还能把 WebDAV 切成只读",
         main="WebDAV 与更多", lead="离线下载、回收站、只读保护",
         big="3", unit="种接入方式", accent=ORANGE,
         points=["WebDAV 挂载到桌面客户端", "离线下载由服务端代拉",
                 "写保护插件保护指定目录"]),

    dict(t="配额", key="big",
         sub="套餐额度优先于用户组，扩容只改一处",
         main="每用户 1 GB", lead="由 default 用户组统一分配配额",
         big="1 GB", unit="每用户配额", accent=CYAN,
         points=["新用户默认进 default 组", "用户组与套餐双层额度"]),

    dict(t="注册准入", key="big",
         sub="需要开放时，由官方邀请码插件做准入校验",
         main="注册默认关闭", lead="项目没有内置注册码，就不开放自助注册",
         big="OFF", unit="security.allow_register = false", accent=ORANGE,
         points=["注册接口默认返回「未开放注册」", "只能登录，杜绝批量灌号"]),

    dict(t="安全默认值", key="cards",
         sub="登录 / 上传 / 审计，三道常见风险都有对应插件",
         main="安全是默认值", lead="不是装完再补，而是上线就收紧",
         items=[("◈", "登录", ["失败过多临时锁定", "抑制暴力破解"], ORANGE),
                ("◈", "上传", ["拦截可执行与脚本", "限制单文件大小"], CYAN),
                ("◈", "审计", ["关键事件写独立日志", "JSONL 追加留存"], PURPLE)]),

    dict(t="插件平台 v2", key="big",
         sub="升 v2、自动兼容 v1，全部真接线",
         main="把大功能交给社区", lead="修 bug、加功能之外，能力缺口由插件来补",
         big="v2", unit="插件协议", accent=PURPLE,
         points=["通知 / 决策 / 变换 三档全开", "注册型 Provider 提供能力",
                 "进程外热加载，可单独启停"]),

    dict(t="108 钩子", key="big",
         sub="从登录、上传到分享、WebDAV、AI，全都可订阅",
         main="108 个事件钩子", lead="不是留了名字，是全部真接线、已接入 108",
         big="108", unit="个钩子", accent=CYAN,
         points=["覆盖 9 大分组，含 9 个 AI 钩子", "v1 握手自动兼容老插件"]),

    dict(t="通知型", key="big",
         sub="适合审计日志、统计、告警与消息推送",
         main="① 通知型钩子", lead="事件发生后再告诉插件，不影响主流程",
         big="①", unit="Notify", accent=CYAN,
         points=["只订阅、不拦截、不改写", "官方「关键操作审计」即此类型"]),

    dict(t="决策型", key="big",
         sub="返回 deny 即拦截，注册邀请码就靠它",
         main="② 决策型钩子", lead="能不能继续，由插件说了算",
         big="②", unit="Decision", accent=ORANGE,
         points=["插件可返回 deny + 原因", "上传安全守卫 / 邀请码校验"]),

    dict(t="变换型", key="big",
         sub="文件名规范化、敏感标记都属这一类",
         main="③ 变换型钩子", lead="CtxPatch 直接改写上下文",
         big="③", unit="Transform", accent=PURPLE,
         points=["补丁合并回主流程上下文", "文件名规范化 / 内容标记"]),

    dict(t="Provider 与兼容", key="cards",
         sub="协议 v2 自动兼容 v1，老插件不用改",
         main="注册型 Provider", lead="插件不只是监听，还能对外提供能力",
         n=2,
         items=[("◈", "注册型 Provider", ["AI Provider / 页面 Provider", "由宿主按需调用"], PURPLE),
                ("◈", "v1 平滑升级", ["握手下发兼容版本", "旧插件无需重新打包"], GREEN)]),

    dict(t="三文件契约", key="files",
         sub="plugin.json + register.go + co.BEGG",
         main="三文件契约", lead="写插件，只要三个文件",
         items=[("plugin.json", "元信息 / 入口 / 订阅钩子", PURPLE),
                ("register.go", "纯声明不编译，AST 只读解析", CYAN),
                ("co.BEGG", "JSON 声明运行时 / 页面 / UI", ORANGE)]),

    dict(t="任意语言", key="cards",
         sub="0.0.0.0 自动替换为域名 / IP / 默认 IP",
         main="任意语言，任意布局", lead="Python、PHP、HTTP 页面都能接进来",
         items=[("Py", "Python", ["JSON-RPC over stdio", "零 pip 依赖"], CYAN),
                ("PHP", "页面插件", ["按解释器探测启动", "HTTP / HTTPS 页面"], PURPLE),
                ("▣", "任意目录", ["ASS/ 等布局均可", "/ext/* 反向代理"], ORANGE)]),

    dict(t="默认安全", key="big",
         sub="确认免责后才拉起进程，/ext/* 才会生效",
         main="默认关闭，开启前免责", lead="插件系统全局开关默认不打开",
         big="SAFE", unit="Default Off", accent=GREEN,
         points=["关闭时不拉起任何插件进程", "扩展页面一律返回 404"]),

    dict(t="可视化制作器", key="cards",
         sub="21 套模板 · 108 钩子选择器 · 首次启动新手教程",
         main="可视化插件制作器", lead="拖拽控件，也能直接写代码",
         items=[("▣", "21 套模板", ["决策 / 通知 / 页面", "AI Provider 全覆盖"], PURPLE),
                ("◉", "钩子选择器", ["按分组勾选", "自动生成 register.go"], CYAN),
                ("✦", "跨平台窗口", ["Python + tkinter", "Windows / Linux 通用"], ORANGE)]),

    dict(t="官方插件市场", key="shot",
         sub="后台直接看到清单、版本与说明",
         main="官方插件市场", lead="10 个官方插件，签名后一键安装",
         shot="store", url="cloud.berrywing.top / admin / plugins", accent=PURPLE,
         points=["商店页可见 10 张插件卡", "已安装 / 商店 / 扩展点分栏"]),

    dict(t="10 个官方插件", key="list",
         sub="覆盖准入、安全、运维、合规与内容",
         main="10 个，都要有用", lead="不是凑数，每个都对应一个真实痛点",
         items=["注册邀请码", "上传安全守卫", "文件名规范化", "配额用量预警",
                "登录爆破抑制", "分享链接策略", "关键操作审计", "WebDAV 写保护",
                "敏感文件标记", "维护模式与公告"]),

    dict(t="签名与信任", key="big",
         sub=".pbclus 打包分发，com.json 驱动商店",
         main="签名与信任", lead="Ed25519 官方签名 + 信任库校验发布者",
         big="Ed25519", unit="签名算法", accent=CYAN,
         points=["官方密钥写入 plugin-trust.json", "安装时显示「受信任的发布者」"]),

    dict(t="AI 智能时代", key="shot",
         sub="L0 本地算法 · L1 模型网关 · L3 插件接管",
         main="AI 智能时代", lead="要不要 AI、用哪一档，由你决定",
         shot="ai", url="cloud.berrywing.top / admin / ai", accent=ORANGE,
         points=["后台开关默认关闭", "调用顺序：插件 → 网关 → 本地"]),

    dict(t="智能体验", key="cards",
         sub="后台可选开启，审计不记录 prompt 正文",
         main="智能体验", lead="搜索、文件、代码空间都能更聪明",
         items=[("⌕", "搜索框", ["智能推荐", "近期常用"], CYAN),
                ("{ }", "代码空间", ["后台可选开启", "智能代码补全"], PURPLE),
                ("✦", "AI 钩子", ["9 个 onAI* 事件", "ProviderAI 可接管"], ORANGE)]),

    dict(t="本地 AI 工单站", key="shot",
         sub="知识库要点注入 → 小模型组织语言 → 异常自动回退",
         main="本地 AI 工单站", lead="小模型跑在本机，配置页不对外",
         shot="desk", url="NebulaDrive / 智能工单站", accent=CYAN,
         points=["公开页只有提交 / 查询", "管理台挂随机隐藏路径"]),

    dict(t="项目 Wiki", key="shot",
         sub="18 篇文档 · 5 个分类 · 全文搜索",
         main="项目 Wiki", lead="部署、开发、运维，一处可查",
         shot="wiki", url="NebulaDrive / 项目 Wiki", accent=PURPLE,
         points=["插件制作手册与部署手册", "search.json 全文检索"]),

    dict(t="开始使用", key="outro",
         sub="你的数据，你的星云",
         main="现在就打开浏览器", lead="进入你的星云"),
]
TOTAL = len(SCENES) * SHOT_DUR

SHOT_FILE = {
    "home": "home.png", "files": "admin_admin_plugins.png", "plugins": "plugins.png",
    "store": "store.png", "ai": "aisettings.png", "desk": "desk.png", "wiki": "wiki.png",
}


# ---------------- 基础工具 ----------------
def clamp(v):
    return max(0.0, min(1.0, v))


def ease(x):
    x = clamp(x)
    return 1 - (1 - x) ** 3


def stagger(p, i, delay=0.075, dur=0.24, start=0.06):
    # 基础可见度 0.22：切镜瞬间画面不空，元素再依次点亮
    return 0.22 + 0.78 * clamp((p - (start + i * delay)) / dur)


def beat(t):
    return math.exp(-((t % BEAT) * 10.0))


@lru_cache(maxsize=64)
def font(size, bold=True):
    return ImageFont.truetype(F_BOLD if bold else F_SANS, size, index=2)


@lru_cache(maxsize=800)
def text_img(s, size, color, bold):
    f = font(size, bold)
    bb = f.getbbox(s)
    im = Image.new("RGBA", (max(1, bb[2] - bb[0] + 14), max(1, bb[3] - bb[1] + 14)), (0, 0, 0, 0))
    ImageDraw.Draw(im).text((7 - bb[0], 7 - bb[1]), s, font=f, fill=(*color, 255))
    return im


def with_alpha(im, k):
    if k >= 0.999:
        return im
    o = im.copy()
    o.putalpha(o.getchannel("A").point(lambda v: int(v * k)))
    return o


def text(L, pos, s, size=28, color=WHITE, bold=True, alpha=1.0, align="left"):
    im = text_img(s, size, tuple(color), bold)
    x, y = pos
    if align == "center":
        x -= im.width / 2
    L.alpha_composite(with_alpha(im, clamp(alpha)), (int(x), int(y)))


def rrect(L, box, r=16, fill=(20, 28, 58, 210), outline=(110, 130, 190, 90), width=1):
    ImageDraw.Draw(L).rounded_rectangle(box, radius=r, fill=fill, outline=outline, width=width)


def chip(L, x, y, s, color=PURPLE, alpha=1.0, size=19):
    im = text_img(s, size, WHITE, True)
    w, h = im.width + 26, im.height + 8
    k = clamp(alpha)
    ImageDraw.Draw(L).rounded_rectangle((x, y, x + w, y + h), radius=h // 2,
                                        fill=(*color, int(56 * k)),
                                        outline=(*color, int(150 * k)), width=1)
    L.alpha_composite(with_alpha(im, k), (x + 13, y - 2))
    return w


def bullet(L, x, y, s, p, i, color=CYAN, size=22, dot=True):
    k = stagger(p, i, dur=0.20)
    if k <= 0:
        return
    if dot:
        ImageDraw.Draw(L).ellipse((x, y + 11, x + 9, y + 20), fill=(*color, int(255 * k)))
    text(L, (x + (26 if dot else 0), y), s, size=size, color=WHITE, alpha=k)


def caption(L, s, p):
    a = (0.35 + 0.65 * clamp(p / 0.06)) * clamp((1 - p) / 0.06)
    if a <= 0:
        return
    ImageDraw.Draw(L).rounded_rectangle((W // 2 - 470, 616, W // 2 + 470, 672),
                                        radius=12, fill=(10, 15, 34, int(165 * a)))
    text(L, (W // 2, 627), s, size=29, color=WHITE, alpha=a, align="center")


def heading(L, main, lead, p):
    a = 0.40 + 0.60 * ease(p / 0.10)
    text(L, (W // 2, 96 - (1 - a) * 16), main, size=48, color=WHITE, alpha=a, align="center")
    if lead:
        text(L, (W // 2, 164 - (1 - a) * 8), lead, size=21, color=MUTED,
             bold=False, alpha=0.30 + 0.70 * clamp((p - 0.02) / 0.12), align="center")


@lru_cache(maxsize=16)
def load_shot(name):
    return Image.open(os.path.join(ASSETS, SHOT_FILE[name])).convert("RGB")


def browser(L, name, box, p, url, accent):
    x, y, w, h = box
    bar = 30
    ih = h - bar
    k = 0.30 + 0.70 * clamp(p / 0.10)
    d = ImageDraw.Draw(L)
    glow = int(120 * k + 55 * beat(0))
    d.rounded_rectangle((x - 3, y - 3, x + w + 3, y + h + 3), radius=16,
                        fill=(16, 22, 46, int(225 * k)),
                        outline=(*accent, min(255, glow)), width=2)
    d.rounded_rectangle((x, y, x + w, y + bar), radius=10, fill=(28, 36, 68, int(230 * k)))
    for j, c in enumerate(((255, 110, 120), (255, 200, 90), (110, 225, 150))):
        d.ellipse((x + 14 + j * 18, y + 12, x + 22 + j * 18, y + 20),
                  fill=(*c, int(200 * k)))
    text(L, (x + 80, y + 3), url, size=14, color=MUTED, bold=False, alpha=k)

    src = load_shot(name)
    aw, ah = src.size
    scale = 1.0 + 0.055 * ease(p)
    ar = w / ih
    cw, ch = aw / scale, aw / scale / ar
    if ch > ah / scale:
        ch, cw = ah / scale, ah / scale * ar
    cx, cy = aw / 2, ah / 2 - 8 * math.sin(p * math.pi)
    crop = (int(cx - cw / 2), int(cy - ch / 2), int(cx + cw / 2), int(cy + ch / 2))
    im = src.crop(crop).resize((w, ih), Image.Resampling.BILINEAR).convert("RGBA")
    mask = Image.new("L", (w, ih), 0)
    ImageDraw.Draw(mask).rounded_rectangle((0, 0, w - 1, ih + 10), radius=10, fill=255)
    im.putalpha(mask.point(lambda v: int(v * k)))
    L.alpha_composite(im, (x, y + bar))


# ---------------- 主体类型（每屏只讲一件事） ----------------
def body_logo(L, sc, p, t):
    a = 0.30 + 0.70 * ease(p / 0.16)
    cx, cy = W // 2, 306
    d = ImageDraw.Draw(L, "RGBA")
    pul = 1 + 0.10 * beat(t)
    for i in range(80):
        ang = i * 2.39996
        rad = 34 + (i % 9) * 44
        rr = rad * (1 - 0.60 * ease(p / 0.55)) + 6
        x = cx + math.cos(ang + t * 0.16) * rr * pul
        y = cy + math.sin(ang + t * 0.16) * rr * 0.60 * pul
        c = PURPLE if i % 3 else CYAN
        d.ellipse((x - 2, y - 2, x + 2, y + 2), fill=(*c, int(200 * a)))
    for rad, al in ((98, 100), (142, 58), (192, 30)):
        d.ellipse((cx - rad * pul, cy - rad * 0.6 * pul,
                   cx + rad * pul, cy + rad * 0.6 * pul),
                  outline=(*PURPLE, int(al * a)), width=2)
    text(L, (W // 2, 280 - (1 - a) * 18), sc["main"], size=76,
         color=WHITE, alpha=a, align="center")
    text(L, (W // 2, 384), "把数据，放进自己的星云", size=30, color=CYAN,
         bold=False, alpha=clamp((p - 0.14) / 0.20), align="center")
    text(L, (W // 2, 448), sc["lead"], size=16, color=MUTED, bold=False,
         alpha=clamp((p - 0.28) / 0.22), align="center")


def body_cards(L, sc, p, t):
    items = sc["items"]
    n = sc.get("n", 3)
    x0, total_w, gap = 64, 1152, 28
    cw = (total_w - gap * (n - 1)) // n
    y, h = 236, 252
    for i, (icon, title, lines, color) in enumerate(items):
        k = stagger(p, i, delay=0.10, dur=0.26)
        if k <= 0:
            continue
        x = x0 + i * (cw + gap)
        yy = y + int((1 - ease(k)) * 26)
        rrect(L, (x, yy, x + cw, yy + h), r=20,
              fill=(19, 27, 56, int(216 * k)), outline=(*color, int(110 * k)))
        text(L, (x + 26, yy + 20), icon, size=34, color=color, alpha=k)
        text(L, (x + 26, yy + 76), title, size=27, color=WHITE, alpha=k)
        for j, ln in enumerate(lines):
            text(L, (x + 26, yy + 130 + j * 34), ln, size=19, color=MUTED,
                 bold=False, alpha=k)


def body_big(L, sc, p, t):
    a = 0.35 + 0.65 * ease(p / 0.12)
    big, unit, color = sc["big"], sc["unit"], sc["accent"]
    size = 128 if len(big) <= 3 else (78 if len(big) <= 7 else 58)
    if big in ("①", "②", "③"):
        size = 150
    # 左：巨幅数字/词
    rrect(L, (64, 232, 566, 512), r=22,
          fill=(19, 27, 56, int(210 * a)), outline=(*color, int(110 * a)))
    text(L, (315, 286), big, size=size, color=color, alpha=a, align="center")
    text(L, (315, 430), unit, size=22, color=MUTED, bold=False, alpha=a, align="center")
    # 右：2~3 条要点（大字）
    x = 606
    rrect(L, (x, 232, 1216, 512), r=22,
          fill=(18, 26, 54, int(196 * a)), outline=(*LINE, int(90 * a)))
    text(L, (x + 30, 254), "要点", size=20, color=color, alpha=a)
    d = ImageDraw.Draw(L)
    d.line((x + 30, 292, 1186, 292), fill=(*LINE, int(120 * a)), width=1)
    for i, s in enumerate(sc["points"]):
        bullet(L, x + 30, 310 + i * 62, s, p, i + 1, color=color, size=21)


def body_shot(L, sc, p, t):
    accent = sc["accent"]
    browser(L, sc["shot"], (64, 222, 790, 376), p, sc["url"], accent)
    pts = sc["points"]
    x, y, w, h = 884, 222, 332, 376
    a = stagger(p, 0, dur=0.22)
    rrect(L, (x, y, x + w, y + h), r=18,
          fill=(18, 26, 54, int(202 * a)), outline=(*accent, int(100 * a)))
    text(L, (x + 24, y + 22), "这一屏看什么", size=20, color=accent, alpha=a)
    ImageDraw.Draw(L).line((x + 24, y + 62, x + w - 24, y + 62),
                           fill=(*LINE, int(120 * a)), width=1)
    for i, s in enumerate(pts):
        bullet(L, x + 24, y + 82 + i * 68, s, p, i + 1, color=accent, size=19)


def body_files(L, sc, p, t):
    for i, (name, desc, color) in enumerate(sc["items"]):
        k = stagger(p, i, delay=0.11, dur=0.28)
        if k <= 0:
            continue
        x = 64 + i * 392
        y = 244 + int((1 - ease(k)) * 26)
        rrect(L, (x, y, x + 364, y + 230), r=20,
              fill=(19, 27, 56, int(220 * k)), outline=(*color, int(125 * k)))
        ImageDraw.Draw(L).rounded_rectangle((x + 22, y + 22, x + 76, y + 58),
                                            radius=8, fill=(*color, int(60 * k)))
        text(L, (x + 32, y + 24), str(i + 1), size=24, color=color, alpha=k)
        text(L, (x + 22, y + 88), name, size=27, color=WHITE, alpha=k)
        text(L, (x + 22, y + 142), desc, size=18, color=MUTED, bold=False, alpha=k)
    text(L, (W // 2, 512), "register.go 纯声明不编译 · co.BEGG 为 JSON",
         size=21, color=GREEN, alpha=clamp((p - 0.30) / 0.22), align="center")


def body_steps(L, sc, p, t):
    items = sc["items"]
    d = ImageDraw.Draw(L, "RGBA")
    cw, gap, x0, y, h = 344, 56, 64, 252, 214
    for i, (title, sub, color) in enumerate(items):
        k = stagger(p, i, delay=0.10, dur=0.24)
        if k <= 0:
            continue
        x = x0 + i * (cw + gap)
        rrect(L, (x, y, x + cw, y + h), r=18,
              fill=(19, 27, 56, int(216 * k)), outline=(*color, int(120 * k)))
        text(L, (x + cw // 2, y + 58), title, size=27, color=WHITE, alpha=k, align="center")
        text(L, (x + cw // 2, y + 116), sub, size=19, color=MUTED,
             bold=False, alpha=k, align="center")
        if i < len(items) - 1:
            ax = x + cw + 8
            d.line((ax, y + h // 2, ax + 40, y + h // 2),
                   fill=(*CYAN, int(170 * k)), width=3)
            d.polygon([(ax + 40, y + h // 2 - 8), (ax + 52, y + h // 2),
                       (ax + 40, y + h // 2 + 8)], fill=(*CYAN, int(190 * k)))
    text(L, (W // 2, 500), "源站自签证书即可满足 Full 回源，也可换 Cloudflare Origin CA",
         size=20, color=GREEN, alpha=clamp((p - 0.30) / 0.22), align="center")


def body_list(L, sc, p, t):
    items = sc["items"]
    for i, s in enumerate(items):
        k = stagger(p, i, delay=0.035, dur=0.20, start=0.05)
        if k <= 0:
            continue
        col, row = i // 5, i % 5
        x = 64 + col * 592
        y = 238 + row * 66
        color = (PURPLE, CYAN, ORANGE, GREEN)[i % 4]
        rrect(L, (x, y, x + 560, y + 56), r=12,
              fill=(19, 27, 56, int(205 * k)), outline=(*color, int(85 * k)))
        text(L, (x + 18, y + 14), f"{i+1:02d}", size=22, color=color, alpha=k)
        text(L, (x + 74, y + 13), s, size=22, color=WHITE, alpha=k)
    text(L, (W // 2, 570), "全部为 v2 三文件契约的 Python 纯钩子插件，零 pip 依赖",
         size=19, color=MUTED, bold=False, alpha=clamp((p - 0.42) / 0.20), align="center")


def body_outro(L, sc, p, t):
    a = 0.35 + 0.65 * ease(p / 0.12)
    d = ImageDraw.Draw(L, "RGBA")
    pul = 1 + 0.08 * beat(t)
    for j in range(3):
        rad = (150 + j * 64) * pul
        d.ellipse((W // 2 - rad, 300 - rad * 0.6, W // 2 + rad, 300 + rad * 0.6),
                  outline=(*PURPLE, int((76 - j * 18) * a)), width=2)
    text(L, (W // 2, 236 - (1 - a) * 18), sc["main"], size=32,
         color=MUTED, bold=False, alpha=a, align="center")
    text(L, (W // 2, 306), "cloud.berrywing.top", size=60,
         color=WHITE, alpha=a, align="center")
    text(L, (W // 2, 400), "从一块硬盘，到一整套可扩展的云", size=27, color=CYAN,
         bold=False, alpha=clamp((p - 0.12) / 0.20), align="center")
    x = 344
    for s, c in (("自托管", PURPLE), ("可扩展", CYAN), ("AI 就绪", ORANGE)):
        x += chip(L, x, 472, s, c, clamp((p - 0.22) / 0.22)) + 16
    text(L, (W // 2, 544), "NebulaDrive · V26-11.1", size=18, color=MUTED,
         bold=False, alpha=clamp((p - 0.30) / 0.22), align="center")


BODY = {"logo": body_logo, "cards": body_cards, "big": body_big, "shot": body_shot,
        "files": body_files, "steps": body_steps, "list": body_list, "outro": body_outro}


# ---------------- 背景（跟随 120 BPM 脉冲） ----------------
def make_base():
    yy = np.linspace(0, 1, H, dtype=np.float32)[:, None, None]
    arr = TOP[None, None, :] * (1 - yy) + BOT[None, None, :] * yy
    arr = np.repeat(arr, W, axis=1)
    X = np.linspace(0, 1, W, dtype=np.float32)[None, :]
    Y = np.linspace(0, 1, H, dtype=np.float32)[:, None]
    g1 = np.exp(-(((X - 0.16) ** 2 + (Y - 0.22) ** 2) / 0.10))[..., None]
    g2 = np.exp(-(((X - 0.86) ** 2 + (Y - 0.80) ** 2) / 0.13))[..., None]
    G1 = g1 * np.array([30, 16, 66], np.float32)
    G2 = g2 * np.array([6, 34, 58], np.float32)
    im = Image.fromarray(np.clip(arr + G1 + G2, 0, 255).astype(np.uint8), "RGB")
    d = ImageDraw.Draw(im, "RGBA")
    for gx in range(0, W, 64):
        d.line((gx, 0, gx, H), fill=(120, 140, 200, 9), width=1)
    for gy in range(0, H, 64):
        d.line((0, gy, W, gy), fill=(120, 140, 200, 9), width=1)
    return np.asarray(im, np.float32), G1, G2


BASE_NP, G1, G2 = make_base()
rng = np.random.default_rng(20261114)
STARS = [(int(rng.integers(0, W)), int(rng.integers(0, H)), int(rng.integers(1, 3)),
          float(rng.uniform(0.25, 0.9)), float(rng.uniform(0, 6.28))) for _ in range(150)]


def bg_frame(t):
    pul = beat(t)
    arr = BASE_NP.copy()
    arr += (G1 * (0.30 * pul)) + (G2 * (0.34 * pul))
    cx = (0.12 + 0.76 * (0.5 + 0.5 * math.sin(t * 0.19))) * W
    xs = np.arange(W, dtype=np.float32)
    band = np.exp(-((xs - cx) ** 2) / (2 * 210 ** 2))[None, :, None]
    arr += band * np.array([9 + 8 * pul, 6 + 5 * pul, 18 + 12 * pul], np.float32)
    im = Image.fromarray(np.clip(arr, 0, 255).astype(np.uint8), "RGB").convert("RGBA")
    d = ImageDraw.Draw(im, "RGBA")
    for x, y, r, b, ph in STARS:
        yy = (y + t * (3 + b * 7)) % H
        a = int((65 + 110 * b * (0.6 + 0.4 * math.sin(t * 1.2 + ph))) * (0.82 + 0.35 * pul))
        d.ellipse((x, yy, x + r, yy + r), fill=(190, 205, 255, min(255, a)))
    return im


def cut_flash(L, t):
    """每 8 秒切镜瞬间的一道横向光扫，配合鼓点。"""
    cut = t % SHOT_DUR
    if cut >= 0.32:
        return
    k = (1 - cut / 0.32) ** 1.6
    band = Image.new("RGBA", (300, H), (0, 0, 0, 0))
    d = ImageDraw.Draw(band)
    for i in range(0, 300, 5):
        a = int(62 * k * math.sin(math.pi * i / 300))
        d.rectangle((i, 0, i + 5, H), fill=(190, 208, 255, a))
    L.alpha_composite(band, (int((cut / 0.32) * (W + 300)) - 300, 0))


def hud(img, t, sc, idx):
    d = ImageDraw.Draw(img)
    d.text((44, 26), "NEBULADRIVE", font=font(15, True), fill=MUTED)
    d.text((44, 46), f"{idx+1:02d} / {len(SCENES):02d}   {sc['t']}", font=font(19, True), fill=WHITE)
    d.text((1236, 30), f"{int(t)//60:02d}:{int(t)%60:02d} / {int(TOTAL)//60:02d}:{int(TOTAL)%60:02d}",
           font=font(15, False), fill=MUTED, anchor="ra")
    # 进度条 + 章节刻度 + 节拍点
    d.rectangle((44, 686, 1236, 687), fill=(52, 64, 104))
    pw = int(1192 * t / TOTAL)
    d.rectangle((44, 685, 44 + pw, 688), fill=CYAN)
    for i in range(1, len(SCENES)):
        x = 44 + int(1192 * i / len(SCENES))
        d.line((x, 682, x, 691), fill=(110, 128, 178), width=1)
    r = 3 + int(3 * beat(t))
    d.ellipse((44 + pw - r, 687 - r, 44 + pw + r, 687 + r), fill=WHITE)
    d.text((44, 694), "PROMO FILM · 2026", font=font(12, False), fill=(100, 114, 152))
    d.text((1236, 694), "cloud.berrywing.top", font=font(12, False),
           fill=(100, 114, 152), anchor="ra")


def render_frame(t):
    idx = min(len(SCENES) - 1, int(t / SHOT_DUR))
    sc = SCENES[idx]
    p = clamp((t - idx * SHOT_DUR) / SHOT_DUR)

    img = bg_frame(t)
    L = Image.new("RGBA", (W, H), (0, 0, 0, 0))
    if sc["key"] not in ("logo", "outro"):
        heading(L, sc["main"], sc.get("lead"), p)
    BODY[sc["key"]](L, sc, p, t)
    caption(L, sc["sub"], p)
    cut_flash(L, t)

    # 硬切：镜头内元素自带基础可见度与点亮动画，配合光扫转场，画面始终饱满
    img = Image.alpha_composite(img, L).convert("RGB")
    hud(img, t, sc, idx)
    return img


def main():
    os.makedirs(os.path.dirname(OUT), exist_ok=True)
    cmd = [
        "ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
        "-f", "rawvideo", "-pix_fmt", "rgb24", "-s", f"{W}x{H}", "-r", str(FPS), "-i", "-",
        "-i", BGM,
        "-c:v", "libopenh264", "-b:v", "3400k", "-g", "48", "-pix_fmt", "yuv420p",
        "-c:a", "aac", "-b:a", "160k", "-shortest", "-movflags", "+faststart", OUT,
    ]
    proc = subprocess.Popen(cmd, stdin=subprocess.PIPE)
    frames = int(TOTAL * FPS)
    print(f"渲染 {frames} 帧 / {TOTAL:.0f}s / {len(SCENES)} 镜 / {W}x{H}@{FPS}", flush=True)
    for i in range(frames):
        proc.stdin.write(render_frame(i / FPS).tobytes())
        if i % (FPS * 20) == 0:
            print(f"  {i/FPS:6.1f}s / {TOTAL:.0f}s  ({i*100//frames}%)", flush=True)
    proc.stdin.close()
    rc = proc.wait()
    print("ffmpeg exit:", rc, "| size:", round(os.path.getsize(OUT) / 1024 / 1024, 1), "MB", flush=True)
    return rc


if __name__ == "__main__":
    sys.exit(main())

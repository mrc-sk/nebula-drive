"""结构化校验 i18n key：按命名空间块解析，检查新页面用到的 key 是否真在对应命名空间里。

比grep 靠谱：grep '^    key:' 会跨命名空间命中同名 key。

检查两件事：
  1) 五个语言文件（en/zh-CN/zh-TW/ja/ko）逐命名空间、逐 key 完全一致；
  2) 三个「我的」侧栏页面引用的 key 都真实存在于对应命名空间。

漏一个 key 的表现是界面直接显示 "ns_mine.emptyTrash" 这种原始串，
tsc 与 vite 都发现不了，只能靠这个检查拦。

用法：python check_i18n.py（CI 里在 frontend/ 下跑，本地亦然）
"""
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
LANGS = ["en", "zh-CN", "zh-TW", "ja", "ko"]
SRC = os.path.join(HERE, "src", "i18n", "%s.ts")
PAGES = [
    os.path.join(HERE, "src", "pages", "ShareList.tsx"),
    os.path.join(HERE, "src", "pages", "TasksOffline.tsx"),
    os.path.join(HERE, "src", "pages", "Trash.tsx"),
]

def parse_ns(path):
    """返回 {ns_name: set(keys)}。按 '  ns: {' 定位块，收集块内 '    key:'。"""
    text = open(path, encoding="utf-8").read()
    out = {}
    cur = None
    depth = None
    for line in text.split("\n"):
        m = re.match(r"^  ([A-Za-z_][A-Za-z0-9_]*):\s*\{", line)
        if m:
            cur = m.group(1)
            out[cur] = set()
            depth = 1
            continue
        if cur is None:
            continue
        depth += line.count("{") - line.count("}")
        if depth <= 0:
            cur = None
            continue
        mk = re.match(r"^    ([A-Za-z_][A-Za-z0-9_]*):", line)
        if mk:
            out[cur].add(mk.group(1))
    return out

ns = {lg: parse_ns(SRC % lg) for lg in LANGS}
# 五个命名空间的 key 集合必须完全一致
base = ns["en"]
bad = False
for lg in LANGS[1:]:
    if set(ns[lg].keys()) != set(base.keys()):
        print("命名空间集合不一致: en vs %s -> %s" % (lg, set(base) ^ set(ns[lg])))
        bad = True
    for n in base:
        if ns[lg].get(n, set()) != base[n]:
            print("  [%s] ns %s key 差异: %s" % (lg, n, base[n] ^ ns[lg].get(n, set())))
            bad = True
print("五语言命名空间/key 全对称" if not bad else "!! 存在不对称")

print("\n=== 三个新页面引用的 key ===")
missing = 0
for f in PAGES:
    text = open(f, encoding="utf-8").read()
    keys = sorted(set(re.findall(r"t\('([A-Za-z_][A-Za-z0-9_]*\.[A-Za-z_][A-Za-z0-9_]*)'", text)))
    # 动态 key：t(`ns.${x}`) —— 打印出来人工确认覆盖
    dyn = sorted(set(re.findall(r"t\(`([A-Za-z_][A-Za-z0-9_]*)\.\$\{", text)))
    print("\n--- %s ---" % os.path.basename(f))
    for k in keys:
        n, key = k.split(".", 1)
        where = [lg for lg in LANGS if key in ns[lg].get(n, set())]
        if len(where) == 5:
            print("  OK    %-24s 五语言齐全" % k)
        else:
            missing += 1
            miss = [lg for lg in LANGS if key not in ns[lg].get(n, set())]
            print("  MISS  %-24s 缺失于 %s" % (k, ",".join(miss)))
    for n in dyn:
        print("  DYN   %s.*  (动态拼接，需人工确认覆盖)" % n)

print("\n%s" % ("全部 key 齐全" if missing == 0 else "有 %d 个 key 缺失" % missing))
sys.exit(1 if (bad or missing) else 0)

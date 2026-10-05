#!/bin/sh
# 插件热加载端到端冒烟测试
#
# 验证真实运行时行为（不是 mock）：
#   1. 启动二进制（真实进程）
#   2. 安装示例插件 referer-guard
#   3. 同意插件协议
#   4. 启用插件 → 验证子进程真的被拉起（列表里 status=running + pid）
#   5. 上传一个文件，用带/不带 Referer 的请求下载 → 验证 403 拦截
#   6. 禁用插件 → 验证子进程被杀、下载恢复 200
#   7. 验证钩子清单接口返回真实 count
#
# 关键：每一步都断言 HTTP 状态码/响应体，而不是只看"没报错"。

set -e

BASE="http://127.0.0.1:18099"
DATA="./.smoke-plugindata"
BIN="./.smoke-nebula"
PLUGIN_SRC="../examples/plugins/referer-guard"

PASS=0
FAIL=0

ok()   { PASS=$((PASS+1)); echo "  [PASS] $1"; }
bad()  { FAIL=$((FAIL+1)); echo "  [FAIL] $1"; }
check(){ if [ "$2" = "$3" ]; then ok "$1 ($2)"; else bad "$1: got=$2 want=$3"; fi; }

curl() { command curl -sS --noproxy '*' "$@"; }

cleanup() {
    [ -n "$SRVPID" ] && kill "$SRVPID" 2>/dev/null || true
    sleep 1
    [ -n "$SRVPID" ] && kill -9 "$SRVPID" 2>/dev/null || true
    rm -rf "$DATA"
}
trap cleanup EXIT

echo
echo "============================================"
echo "  插件热加载冒烟测试"
echo "============================================"
echo

# ---- 1. 构建 ----
echo "[1/7] 构建"
if [ ! -x "$BIN" ]; then
    GOOS=$(uname -s | tr 'A-Z' 'a-z')
    (cd . && go build -o "../$BIN" .) || { echo "构建失败"; exit 1; }
fi
ok "二进制就绪: $BIN"

# ---- 2. 启动 ----
echo "[2/7] 启动服务"
mkdir -p "$DATA"
(cd "$DATA" && "../$BIN" -data . -listen 127.0.0.1:18099 > server.log 2>&1) &
SRVPID=$!
for i in $(seq 1 40); do
    if curl -s -o /dev/null "$BASE/api/install" 2>/dev/null; then break; fi
    sleep 0.5
done
if ! curl -s -o /dev/null "$BASE/api/install"; then
    echo "服务未启动，日志："; tail -30 "$DATA/server.log"; exit 1
fi
ok "服务已启动 pid=$SRVPID"

# ---- 3. 安装系统 ----
echo "[3/7] 初始化安装"
curl -s -X POST "$BASE/api/install" -H 'Content-Type: application/json' -d '{
  "admin":{"userName":"admin","email":"a@b.c","password":"Admin12345"},
  "db":{"type":"sqlite","file":"data/nebula.db"},
  "system":{"siteName":"Smoke","uploadPath":"uploads"}
}' > /dev/null
TOKEN=$(curl -s -X POST "$BASE/api/auth/login" -H 'Content-Type: application/json' \
  -d '{"userName":"admin","password":"Admin12345"}' \
  | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
if [ -z "$TOKEN" ]; then echo "登录失败"; exit 1; fi
ok "管理员登录成功"

AUTH="Authorization: Bearer $TOKEN"

# ---- 4. 协议关卡 ----
echo "[4/7] 插件协议关卡"
BEFORE=$(curl -s -H "$AUTH" "$BASE/api/admin/plugins/agreement" | grep -o '"accepted":[a-z]*' | head -1)
check "初始未同意协议" "$BEFORE" '"accepted":false'

# 未同意时启用应被拒（虽然插件还没装，先验证关卡在）
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "$AUTH" \
  "$BASE/api/admin/plugins/nonexist/enable")
check "未同意时启用被拒 403" "$CODE" "403"

VER=$(curl -s -H "$AUTH" "$BASE/api/admin/plugins/agreement" | grep -o '"version":[0-9]*' | head -1 | cut -d: -f2)
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "$AUTH" \
  -H 'Content-Type: application/json' \
  -d "{\"version\":$VER,\"accept\":true}" "$BASE/api/admin/plugins/agreement")
check "同意协议 200" "$CODE" "200"

AFTER=$(curl -s -H "$AUTH" "$BASE/api/admin/plugins/agreement" | grep -o '"accepted":[a-z]*' | head -1)
check "已同意协议" "$AFTER" '"accepted":true'

# ---- 5. 装插件 ----
echo "[5/7] 安装示例插件"
# 编译示例插件（独立 module）
(cd "$PLUGIN_SRC" && go build -o "../../backend/.smoke-plug/referer-guard" .) \
    || { echo "示例插件编译失败"; exit 1; }
STAGE="$DATA/stage"
mkdir -p "$STAGE"
cp "$PLUGIN_SRC/plugin.json" "$STAGE/"
cp "$PLUGIN_SRC/main.go" "$STAGE/" 2>/dev/null || true
cp "../../backend/.smoke-plug/referer-guard" "$STAGE/"

CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "$AUTH" \
  -H 'Content-Type: application/json' \
  -d "{\"source\":\"local\",\"path\":\"$PWD/$STAGE\"}" \
  "$BASE/api/admin/plugins/install")
check "安装插件 200" "$CODE" "200"

# ---- 6. 启用（热加载核心）----
echo "[6/7] 启用插件（热加载）"
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "$AUTH" \
  "$BASE/api/admin/plugins/referer-guard/enable")
check "启用 200" "$CODE" "200"

sleep 1
LIST=$(curl -s -H "$AUTH" "$BASE/api/admin/plugins")
echo "$LIST" | grep -q '"status":"running"' \
    && ok "插件进程运行中" || bad "插件未进入 running 状态"
echo "$LIST" | grep -o '"pid":[0-9]*' | head -1 | grep -qv '"pid":0' \
    && ok "子进程有真实 pid" || bad "pid 为 0，进程未拉起"

# 钩子计数应反映真实注册
HOOKS=$(curl -s -H "$AUTH" "$BASE/api/admin/plugins/hooks")
echo "$HOOKS" | grep -q '"name":"onAntiLeech","inProcess":0,"outProcess":1' \
    && ok "onAntiLeech 计数=1（进程外）" \
    || bad "onAntiLeech 计数不对: $(echo "$HOOKS" | grep -o '"name":"onAntiLeech"[^}]*')"

# 验证子进程真的存在（不是只改了数据库）
PLUGPID=$(echo "$LIST" | sed -n 's/.*"pid":\([0-9]*\).*/\1/p' | head -1)
if [ -n "$PLUGPID" ] && [ "$PLUGPID" -gt 0 ] 2>/dev/null; then
    if kill -0 "$PLUGPID" 2>/dev/null; then
        ok "子进程 $PLUGPID 真实存活"
    else
        bad "子进程 $PLUGPID 不存在"
    fi
fi

# ---- 7. 拦截行为 ----
echo "[7/7] 验证拦截与热卸载"
UP=$(curl -s -H "$AUTH" -F "file=@$PLUGIN_SRC/plugin.json" "$BASE/api/files/upload" \
     | sed -n 's/.*"id":\([0-9]*\).*/\1/p' | head -1)
if [ -z "$UP" ]; then
    # 扩展名可能不在白名单，换个 txt
    echo "plugin json allowed?" > "$DATA/t.txt"
    UP=$(curl -s -H "$AUTH" -F "file=@$DATA/t.txt" "$BASE/api/files/upload" \
         | sed -n 's/.*"id":\([0-9]*\).*/\1/p' | head -1)
fi
if [ -z "$UP" ]; then
    bad "上传失败，跳过拦截验证"
else
    # 插件默认配置只允许 localhost —— 带外站 Referer 应被拦
    sleep 1
    CODE=$(curl -s -o /dev/null -w '%{http_code}' -H "$AUTH" \
      -H "Referer: https://evil.example.net/x" "$BASE/api/files/$UP/download")
    if [ "$CODE" = "403" ]; then
        ok "外站 Referer 被插件拦截 403（热加载真实生效）"
    else
        bad "外站 Referer 未被拦截: got=$CODE want=403"
    fi
    # 本地 Referer 应放行
    CODE=$(curl -s -o /dev/null -w '%{http_code}' -H "$AUTH" \
      -H "Referer: http://localhost:3000/x" "$BASE/api/files/$UP/download")
    if [ "$CODE" = "200" ]; then
        ok "本地 Referer 放行 200"
    else
        bad "本地 Referer 被误拦: got=$CODE want=200"
    fi
fi

# 热卸载：禁用后应恢复放行，且主服务不重启
SRV_START=$(ps -o lstart= -p "$SRVPID" 2>/dev/null || echo "")
curl -s -X POST -H "$AUTH" "$BASE/api/admin/plugins/referer-guard/disable" > /dev/null
sleep 1
LIST2=$(curl -s -H "$AUTH" "$BASE/api/admin/plugins")
echo "$LIST2" | grep -q '"status":"stopped"' \
    && ok "禁用后进程已停止" || bad "禁用后状态非 stopped"
kill -0 "$SRVPID" 2>/dev/null && ok "主服务未重启（热卸载生效）" || bad "主服务挂了"

CODE=$(curl -s -o /dev/null -w '%{http_code}' -H "$AUTH" \
  -H "Referer: https://evil.example.net/x" "$BASE/api/files/$UP/download")
if [ "$CODE" = "200" ]; then
    ok "禁用后拦截消失（插件已真正卸载）"
else
    bad "禁用后仍被拦截: got=$CODE"
fi

# 子进程应已消失
if [ -n "$PLUGPID" ] && kill -0 "$PLUGPID" 2>/dev/null; then
    bad "插件子进程 $PLUGPID 仍在运行（未被杀）"
else
    ok "插件子进程已回收"
fi

echo
echo "============================================"
echo "  通过 $PASS / 失败 $FAIL"
echo "============================================"
[ "$FAIL" -eq 0 ]

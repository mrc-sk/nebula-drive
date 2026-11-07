#!/bin/sh
# 插件热加载端到端冒烟测试
#
# 验证真实运行时行为（不是 mock）：
#   1. 启动二进制（真实进程）
#   2. 安装系统 + 管理员登录
#   3. 安装后重启（让 bootstrap 与插件宿主就绪）
#   4. 同意插件协议 → 协议关卡强制生效
#   5. 安装并启用示例插件 referer-guard → 验证子进程真的被拉起（status=running + pid）
#   6. 拦截行为：登录下载与**公开分享**（download 与 preview 两条路径）
#      都按 Referer 判定，外站 403 / 本地 200
#   7. 禁用插件 → 子进程被杀、登录下载与分享下载都恢复 200
#   8. 扩展鉴权 onApiAuth（HMAC 签名）：未配密钥一律拒绝（fail closed）；
#      配好密钥后有效签名放行、篡改/缺签名/过期时间戳一律 403；
#      并断言插件配置在数据库里是**密文**（端到端：写 → 加密落库 → 解密下发）
#   9. PBClus 插件包：打包工具能打出真容器（魔术字不是 PK），
#      管理端能装这个包并拉起进程；zip 改名成 .pbclus 被明确拒绝
#  9.4 启用插件后**重启服务**，断言插件自动恢复运行 ——
#      这条钉住"安装侧与加载侧必须用同一个数据目录"（曾经不一致，
#      表现为"插件重启后消失"），同时覆盖 PBClus 安装 → 启用 → 重启恢复
#  10. 插件命令行 onCLI：子命令被识别、退出码约定成立、不启动 HTTP 服务
#
# 关键：每一步都断言 HTTP 状态码/响应体，而不是只看"没报错"。

set -e

# 全部路径基于脚本自身所在目录解析。
#
# 原先用 "./" / "../" 拼相对路径，结果是：`-o "../$BIN"` 把二进制建到了
# **仓库根**，而运行处又按 `"../$BIN"` 去数据目录的上一级找 —— 找不到，
# 报 `.././.smoke-nebula: No such file or directory`，整个脚本在第 2 步就死了
# （也就是这个冒烟测试此前从未真正跑到过插件部分）。
# 另外 Windows 下 Go 产物带 .exe，不带后缀时 bash 起不来。
HERE=$(cd "$(dirname "$0")" && pwd)

BASE="http://127.0.0.1:18099"
DATA="$HERE/.smoke-plugindata"
PLUGIN_SRC="$HERE/../examples/plugins/referer-guard"

case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*) EXE=".exe" ;;
    *)                    EXE="" ;;
esac
BIN="$HERE/.smoke-nebula$EXE"

PASS=0
FAIL=0

ok()   { PASS=$((PASS+1)); echo "  [PASS] $1"; }
bad()  { FAIL=$((FAIL+1)); echo "  [FAIL] $1"; }
check(){ if [ "$2" = "$3" ]; then ok "$1 ($2)"; else bad "$1: got=$2 want=$3"; fi; }

curl() { command curl -sS --noproxy '*' "$@"; }

# jsonpath 把 POSIX 路径转成「JSON 字符串里合法的 Windows 路径」。
#
# 为什么需要：Git Bash 下 $PWD/$DATA 是 /e/nebula-drive/... 这种 MSYS 路径，
# 直接塞进 JSON 传给服务端，端上按原生路径解析就找不到目录 ——
# 表现为「安装插件 400」，日志里看不出是路径格式问题。
# cygpath 只在 MSYS/Cygwin 下存在，其他平台原样返回即可。
# 反斜杠还要再转义一层，否则 JSON 会把 \n 之类当转义序列。
jsonpath() {
    p="$1"
    if command -v cygpath > /dev/null 2>&1; then p=$(cygpath -w "$p"); fi
    printf '%s' "$p" | sed 's/\\/\\\\/g'
}

# proc_alive 判断进程是否存活。
#
# Windows 下插件 pid 是**原生 PID**，而 Git Bash 的 kill -0 只认 MSYS pid，
# 对原生 pid 一律返回失败 —— 症状是插件明明在跑（下载确实被 403 拦截了），
# 脚本却报「子进程不存在」。必须按平台分流。
proc_alive() {
    case "$(uname -s)" in
        MINGW*|MSYS*|CYGWIN*) tasklist //FI "PID eq $1" 2>/dev/null | grep -q "$1" ;;
        *)                    kill -0 "$1" 2>/dev/null ;;
    esac
}

cleanup() {
    # MSYS/Git Bash 的 PID 与 Windows 进程未必一一对应，只 kill 子 shell 会留下
    # 真实进程继续占着端口和 $DATA（症状是 rm 报 "Device or resource busy"）。
    [ -n "$SRVPID" ] && kill "$SRVPID" 2>/dev/null || true
    sleep 1
    case "$(uname -s)" in
        MINGW*|MSYS*|CYGWIN*)
            taskkill //F //IM ".smoke-nebula.exe" > /dev/null 2>&1 || true
            # 插件子进程也要收干净，否则会留着孤儿进程
            taskkill //F //IM "referer-guard.exe" > /dev/null 2>&1 || true
            ;;
    esac
    [ -n "$SRVPID" ] && kill -9 "$SRVPID" 2>/dev/null || true
    # || true 不能省：set -e 下 EXIT trap 里任何一条返回非零的命令都会
    # **覆盖脚本的真实退出码**，把"断言失败"变成"看起来是清理失败"。
    rm -rf "$DATA" || true
}
trap cleanup EXIT

echo
echo "============================================"
echo "  插件热加载冒烟测试"
echo "============================================"
echo
# 打印环境：CI 里失败时先看这行，能立刻区分"环境不对"与"断言失败"。
echo "环境: $(uname -srm) | bash ${BASH_VERSION:-?} | $(go version 2>/dev/null || echo 'go 不可用')"
echo

# ---- 1. 构建 ----
echo "[1/10] 构建"
(cd "$HERE" && go build -o "$BIN" .) || { echo "构建失败"; exit 1; }
ok "二进制就绪: $BIN"

# ---- 2. 启动 ----
echo "[2/10] 启动服务"
mkdir -p "$DATA"
# 刻意**不传** -data：数据目录取默认值 "data"。
# 传 `-data .` 时 conf 只 MkdirAll(".")，而安装向导提交的库路径是
# "data/nebula.db"（相对 CWD 解析）—— 父目录 data/ 不存在，sqlite 会报
# `unable to open database file: out of memory (14)`，报错极具误导性。
(cd "$DATA" && "$BIN" -listen 127.0.0.1:18099 > server.log 2>&1) &
SRVPID=$!
for i in $(seq 1 40); do
    if curl -s -o /dev/null "$BASE/api/install/status" 2>/dev/null; then break; fi
    sleep 0.5
done
if ! curl -s -o /dev/null "$BASE/api/install/status"; then
    echo "服务未启动，日志："; tail -30 "$DATA/server.log"; exit 1
fi
ok "服务已启动 pid=$SRVPID"

# ---- 3. 安装系统 ----
echo "[3/10] 初始化安装"
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

# ---- 3b. 安装后重启 ----
#
# 必须重启，否则后面所有插件接口都是 503「插件宿主未就绪」。
#
# 原因：main.go 的 bootstrap()（db 初始化、版本化迁移、插件宿主 pluginset.InitPlugins、
# JWT）只在**启动时** conf.IsInstalled() 为 true 才执行；而首次安装是在服务
# 运行中通过 POST /api/install 完成的，此时 bootstrap 早就跳过了。
# 所以「装完不重启」= 插件功能不可用。这是产品的既有行为，不是本脚本的问题，
# 但脚本必须按真实部署方式重启一次才能测到插件链路。
echo "[3b/10] 安装后重启（让 bootstrap 跑到）"
kill "$SRVPID" 2>/dev/null || true
sleep 2
case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*) taskkill //F //IM ".smoke-nebula.exe" > /dev/null 2>&1 || true ;;
esac
(cd "$DATA" && "$BIN" -listen 127.0.0.1:18099 >> server.log 2>&1) &
SRVPID=$!
for i in $(seq 1 40); do
    if curl -s -o /dev/null "$BASE/api/health" 2>/dev/null; then break; fi
    sleep 0.5
done
TOKEN=$(curl -s -X POST "$BASE/api/auth/login" -H 'Content-Type: application/json' \
  -d '{"userName":"admin","password":"Admin12345"}' \
  | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
if [ -z "$TOKEN" ]; then echo "重启后登录失败"; tail -30 "$DATA/server.log"; exit 1; fi
AUTH="Authorization: Bearer $TOKEN"
if curl -s -H "$AUTH" "$BASE/api/admin/plugins/hooks" | grep -q '"protocolVersion"'; then
    ok "重启后插件宿主已就绪"
else
    bad "重启后插件宿主仍未就绪（enable 会 503）"
fi

# ---- 4. 协议关卡 ----
echo "[4/10] 插件协议关卡"
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
echo "[5/10] 安装示例插件"
# 编译示例插件（独立 module）。产物直接落 $DATA —— 不往仓库里塞临时目录，
# 也避免再出现"建到别处、运行时找不到"的相对路径问题。
PLUGIN_BIN="$DATA/referer-guard$EXE"
(cd "$PLUGIN_SRC" && go build -o "$PLUGIN_BIN" .) \
    || { echo "示例插件编译失败"; exit 1; }
STAGE="$DATA/stage"
mkdir -p "$STAGE"
cp "$PLUGIN_SRC/plugin.json" "$STAGE/"
cp "$PLUGIN_SRC/main.go" "$STAGE/" 2>/dev/null || true
cp "$PLUGIN_BIN" "$STAGE/"

CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "$AUTH" \
  -H 'Content-Type: application/json' \
  -d "{\"source\":\"local\",\"path\":\"$(jsonpath "$STAGE")\"}" \
  "$BASE/api/admin/plugins/install")
check "安装插件 200" "$CODE" "200"

# ---- 6. 启用（热加载核心）----
echo "[6/10] 启用插件（热加载）"
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "$AUTH" \
  "$BASE/api/admin/plugins/referer-guard/enable")
check "启用 200" "$CODE" "200"

sleep 1
LIST=$(curl -s -H "$AUTH" "$BASE/api/admin/plugins")
echo "$LIST" | grep -q '"status":"running"' \
    && ok "插件进程运行中" || bad "插件未进入 running 状态"
echo "$LIST" | grep -o '"pid":[0-9]*' | head -1 | grep -qv '"pid":0' \
    && ok "子进程有真实 pid" || bad "pid 为 0，进程未拉起"

# 钩子计数应反映真实注册。
#
# 不要匹配 `"name":"onAntiLeech","inProcess":0,"outProcess":1` 这种连续子串：
# 后端用 gin.H（map）返回，encoding/json 对 map 的键**按字母序**输出，
# 字段顺序是 blockable / count / doc / inProcess / mode / name / outProcess / ...
# —— name 与 inProcess 之间隔着别的键，连续匹配永远不可能命中。
# 先把各钩子对象拆开，再在 onAntiLeech 那一段里找 outProcess。
HOOKS=$(curl -s -H "$AUTH" "$BASE/api/admin/plugins/hooks")
ANTI=$(echo "$HOOKS" | sed 's/},{/}\n{/g' | grep '"name":"onAntiLeech"')
if echo "$ANTI" | grep -q '"outProcess":1'; then
    ok "onAntiLeech 计数=1（进程外）"
else
    bad "onAntiLeech 计数不对: $ANTI"
fi

# 验证子进程真的存在（不是只改了数据库）
PLUGPID=$(echo "$LIST" | sed -n 's/.*"pid":\([0-9]*\).*/\1/p' | head -1)
if [ -n "$PLUGPID" ] && [ "$PLUGPID" -gt 0 ] 2>/dev/null; then
    if proc_alive "$PLUGPID"; then
        ok "子进程 $PLUGPID 真实存活"
    else
        bad "子进程 $PLUGPID 不存在"
    fi
fi

# ---- 7. 拦截行为 ----
echo "[7/10] 验证拦截与热卸载"
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

# 分享链接同样必须被防盗链覆盖。
#
# 这是本轮修掉的真实缺口：onAntiLeech 原先只在登录下载（file.go）触发，
# /api/shares/:id/download 与 /preview **完全不触发** ——
# 装了防盗链插件的站点，分享链接依然可以被任意第三方站点盗刷，
# 而分享恰恰是最需要防盗链的场景。
#
# download 与 preview 都要测：preview 虽然用 inline，返回的仍是完整文件内容，
# 只拦 download 等于留了一条绕过防盗链的通道。
#
# 注意分享是**匿名**访问：不带 Authorization（接收方通常没账号）。
SHARE=""
if [ -n "$UP" ]; then
    SHARE=$(curl -s -X POST -H "$AUTH" -H 'Content-Type: application/json' \
      -d "{\"fileId\":$UP}" "$BASE/api/shares" \
      | sed -n 's/.*"id":\([0-9]*\).*/\1/p' | head -1)
    if [ -z "$SHARE" ]; then
        bad "创建分享失败，跳过分享防盗链验证"
    else
        ok "创建分享 id=$SHARE"
        for kind in download preview; do
            CODE=$(curl -s -o /dev/null -w '%{http_code}' \
              -H "Referer: https://evil.example.net/x" \
              "$BASE/api/shares/$SHARE/$kind")
            check "分享 $kind：外站 Referer 被拦截 403" "$CODE" "403"
            CODE=$(curl -s -o /dev/null -w '%{http_code}' \
              -H "Referer: http://localhost:3000/x" \
              "$BASE/api/shares/$SHARE/$kind")
            check "分享 $kind：本地 Referer 放行 200" "$CODE" "200"
        done
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

# 禁用后分享下载同样应恢复放行（证明分享路径走的是同一个钩子，
# 而不是另加了一层写死的限制）
if [ -n "${SHARE:-}" ]; then
    CODE=$(curl -s -o /dev/null -w '%{http_code}' \
      -H "Referer: https://evil.example.net/x" "$BASE/api/shares/$SHARE/download")
    check "禁用后分享下载恢复 200" "$CODE" "200"
fi

# 子进程应已消失。
# 必须判 $PLUGPID -gt 0 且走 proc_alive：Windows 下原生 PID 用 kill -0 永远为假。
if [ -n "$PLUGPID" ] && [ "$PLUGPID" -gt 0 ] 2>/dev/null && proc_alive "$PLUGPID"; then
    bad "插件子进程 $PLUGPID 仍在运行（未被杀）"
else
    ok "插件子进程已回收"
fi

# ---- 8. 扩展鉴权 onApiAuth（HMAC 签名）----
echo "[8/10] 扩展鉴权 onApiAuth（真实 HMAC 签名）"
#
# 这个扩展点有三件事必须同时成立，缺一件就是不安全的：
#   1. 钩子要真的派发到子进程 —— 否则插件作者以为接上了，实际收不到；
#   2. 配置不全时**不得**放开鉴权（fail closed）——
#      装了插件却把 401 变成 200，等于把开放 API 直接敞开；
#   3. 有效签名要能放行、篡改要能拦住。
#
# 下面先验 2（未配置密钥时一律拒绝），再配好密钥验 1 与 3。
HMAC_SRC="$HERE/../examples/plugins/hmac-auth"
HMAC_SECRET="smoke-hmac-secret-9f3a"
if [ -d "$HMAC_SRC" ]; then
    HMAC_BIN="$DATA/hmac-auth$EXE"
    (cd "$HMAC_SRC" && go build -o "$HMAC_BIN" .) \
        || { bad "hmac-auth 编译失败"; }

    if [ -f "$HMAC_BIN" ]; then
        HSTAGE="$DATA/stage-hmac"
        mkdir -p "$HSTAGE"
        cp "$HMAC_SRC/plugin.json" "$HSTAGE/"
        cp "$HMAC_BIN" "$HSTAGE/"

        CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "$AUTH" \
          -H 'Content-Type: application/json' \
          -d "{\"source\":\"local\",\"path\":\"$(jsonpath "$HSTAGE")\"}" \
          "$BASE/api/admin/plugins/install")
        check "安装 hmac-auth 200" "$CODE" "200"

        # ---- 8.1 未配置密钥：一律拒绝（fail closed）----
        #
        # 注意期望值是 **403 而不是 401**：插件已启用但配置不全，
        # 它对每个请求都明确返回 blocked。这正是我们要的 ——
        # "配错了"绝不能退化成"放开"。
        CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "$AUTH" \
          "$BASE/api/admin/plugins/hmac-auth/enable")
        check "启用 hmac-auth（尚无密钥）200" "$CODE" "200"

        CODE=$(curl -s -o /dev/null -w '%{http_code}' \
          -H "Authorization: Bearer totally-bogus-token" "$BASE/api/v1/files")
        check "未配密钥时请求被拒（fail closed，403）" "$CODE" "403"

        # 禁用后再写配置：配置在**启用时**生效，先配后启顺序最清晰
        curl -s -X POST -H "$AUTH" "$BASE/api/admin/plugins/hmac-auth/disable" > /dev/null

        # ---- 8.2 配好密钥并启用 ----
        cat > "$DATA/hmacconf.json" <<EOF
{"config":"{\"sharedSecret\":\"$HMAC_SECRET\",\"principalUserId\":1,\"toleranceSeconds\":300}"}
EOF
        CODE=$(curl -s -o /dev/null -w '%{http_code}' -X PUT -H "$AUTH" \
          -H 'Content-Type: application/json' \
          --data-binary @"$DATA/hmacconf.json" \
          "$BASE/api/admin/plugins/hmac-auth/config")
        check "写入 hmac 配置 200" "$CODE" "200"

        CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "$AUTH" \
          "$BASE/api/admin/plugins/hmac-auth/enable")
        check "启用 hmac-auth（已配密钥）200" "$CODE" "200"
        sleep 1

        # ---- 8.2b 配置必须是加密落库的（端到端）----
        #
        # 这条把整条链路串起来：管理端接口写配置 → 落库为密文 →
        # 启用时解密下发给插件 → 插件用**明文密钥**校验签名成功（8.4 验证）。
        # 任何一环把明文/密文搞反，这里或 8.4 就会红。
        #
        # 用 grep 查数据库文件而不是 sqlite3：不引入额外依赖，
        # 而且"文件里都不该出现明文"比"某一列是密文"更严格。
        HDB="$DATA/data/nebula.db"
        leak=0
        for f in "$HDB" "$HDB-wal" "$HDB-shm"; do
            [ -f "$f" ] || continue
            if grep -aq "$HMAC_SECRET" "$f"; then leak=1; fi
        done
        if [ "$leak" = "1" ]; then
            bad "数据库文件中出现明文插件密钥（配置未加密落库）"
        else
            ok "插件配置加密落库（数据库文件里无明文密钥）"
        fi

        # ---- 8.3 钩子真的派发到子进程 ----
        # 断言的是**插件自己打的那行日志**，而不是宿主侧的自述。
        HLOGS=$(curl -s -H "$AUTH" \
          "$BASE/api/admin/plugins/logs?name=hmac-auth&limit=300")
        if echo "$HLOGS" | grep -q 'onApiAuth'; then
            ok "插件收到 onApiAuth 事件（钩子真的派发到子进程）"
        else
            bad "插件未收到 onApiAuth：$(echo "$HLOGS" | head -c 300)"
        fi

        # ---- 8.4 真实签名：有效放行 / 篡改拦截 ----
        #
        # 规范串：METHOD\nPATH\nQUERY\nTIMESTAMP\nBODY_SHA256_HEX
        # 这里的实现是**客户端参考实现**，与插件内部刻意分开写 ——
        # 插件改了规范串而客户端不改，第 8.4 条就会红。
        TS=$(date +%s)
        BODYHASH=$(printf '' | sha256sum | cut -d' ' -f1)
        CANON=$(printf 'GET\n/api/v1/files\n\n%s\n%s' "$TS" "$BODYHASH")
        SIG=$(printf '%s' "$CANON" | openssl dgst -sha256 -hmac "$HMAC_SECRET" -hex \
              | sed 's/.*= *//')

        CODE=$(curl -s -o /dev/null -w '%{http_code}' \
          -H "X-Nebula-Signature: $SIG" -H "X-Nebula-Timestamp: $TS" \
          "$BASE/api/v1/files")
        check "有效签名放行 200" "$CODE" "200"

        # 篡改：签名是对 /api/v1/files 算的，请求却带上 query
        CODE=$(curl -s -o /dev/null -w '%{http_code}' \
          -H "X-Nebula-Signature: $SIG" -H "X-Nebula-Timestamp: $TS" \
          "$BASE/api/v1/files?tampered=1")
        check "篡改查询串被拦截 403" "$CODE" "403"

        # 不带签名头同样拒绝（否则不带签名就能绕过这层保护）
        CODE=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/v1/files")
        check "缺签名头被拦截 403" "$CODE" "403"

        # 过期时间戳被拦截（防重放）
        OLD_TS=$((TS - 3600))
        OLD_CANON=$(printf 'GET\n/api/v1/files\n\n%s\n%s' "$OLD_TS" "$BODYHASH")
        OLD_SIG=$(printf '%s' "$OLD_CANON" | openssl dgst -sha256 -hmac "$HMAC_SECRET" -hex \
                  | sed 's/.*= *//')
        CODE=$(curl -s -o /dev/null -w '%{http_code}' \
          -H "X-Nebula-Signature: $OLD_SIG" -H "X-Nebula-Timestamp: $OLD_TS" \
          "$BASE/api/v1/files")
        check "过期时间戳被拦截 403（防重放）" "$CODE" "403"

        # ---- 8.5 清理 ----
        curl -s -X POST -H "$AUTH" "$BASE/api/admin/plugins/hmac-auth/disable" > /dev/null
    fi
else
    bad "找不到 hmac-auth 示例插件源码"
fi

# ---- 9. PBClus 插件包（打包 → 校验 → 安装 → 启用）----
echo "[9/10] PBClus 插件包"

# 打包工具
PACK_BIN="$HERE/../tools/pbclus-pack$EXE"
mkdir -p "$HERE/../tools"
if (cd "$HERE" && go build -o "$PACK_BIN" ./cmd/pbclus-pack) 2>/dev/null; then
    ok "打包工具编译成功"
else
    bad "打包工具编译失败"
fi

# 造一个"另一个插件"的目录：只改 name，**不动 entry** ——
# 一开始用 sed 's/"referer-guard"/.../' 把 name 与 entry 一起换了，
# 结果 entry 指向 referer-guard-pkg 而二进制叫 referer-guard，
# 打包器（正确地）拒绝："entry 指向的文件不存在"。
PKG_STAGE="$DATA/pkgstage"
mkdir -p "$PKG_STAGE"
sed 's/"name"[[:space:]]*:[[:space:]]*"referer-guard"/"name": "referer-guard-pkg"/' \
    "$PLUGIN_SRC/plugin.json" > "$PKG_STAGE/plugin.json"
cp "$PLUGIN_BIN" "$PKG_STAGE/referer-guard$EXE"

# grep 兜底：确认真的换掉了 name，否则后面会以"插件名冲突"这种别的原因失败
if grep -q '"name": "referer-guard-pkg"' "$PKG_STAGE/plugin.json"; then
    ok "暂存目录的清单 name 已改"
else
    bad "暂存目录的清单 name 没改成功"
fi

PKG="$DATA/referer-guard-pkg-1.0.0.pbclus"
# 注意 `|| PACK_CODE=$?`：脚本开头是 set -e，直接写 $? 会在赋值前就被终止
PACK_CODE=0
(cd "$PKG_STAGE" && "$PACK_BIN" pack . -o "$PKG" --force > /dev/null 2>&1) || PACK_CODE=$?
check "打包成 .pbclus" "$PACK_CODE" "0"

VERIFY_CODE=0
"$PACK_BIN" verify "$PKG" > /dev/null 2>&1 || VERIFY_CODE=$?
check "包完整性校验通过" "$VERIFY_CODE" "0"

# 魔术字必须是 PBClus，而不是 PK（zip）—— 这条断言直接守住
# "不是 zip 改后缀"这个要求。
MAGIC=$(head -c 6 "$PKG")
if [ "$MAGIC" = "PBClus" ]; then
    ok "魔术字是 PBClus"
else
    bad "魔术字不对: $MAGIC"
fi
# 第 7、8 字节应是 0x1A 0x0A（防文本模式糟蹋的标记）
TRAIL2=$(head -c 8 "$PKG" | tail -c 2 | od -An -tx1 | tr -d ' \n')
if [ "$TRAIL2" = "1a0a" ]; then
    ok "魔术字尾部标记正确（1a0a）"
else
    bad "魔术字尾部标记不对: $TRAIL2"
fi
if head -c 2 "$PKG" | grep -q '^PK'; then
    bad "文件以 PK 开头，说明它其实是 zip"
else
    ok "文件不是 zip 结构"
fi

# ---- 9.2 平台标注 ----
#
# 打包时入口是原生可执行文件，所以它会被如实标注成**当前平台**。
# 这一步验证的是"包不会谎称自己通用"——带原生二进制却标成通用，
# 装到别的平台会以"入口文件不存在"失败，与真实原因相距很远。
PLATFORM_OUT=$("$PACK_BIN" inspect "$PKG" 2>/dev/null | sed -n 's/^平台 *//p')
if [ -n "$PLATFORM_OUT" ] && [ "$PLATFORM_OUT" != "any" ]; then
    ok "包如实标注了平台: $PLATFORM_OUT"
else
    bad "带原生二进制的包应标注具体平台，实际: [$PLATFORM_OUT]"
fi

# ---- 9.3 签名链路 ----
#
# keygen → 签名打包 → （无信任库）校验 → 加信任库 → 再校验。
# 关键断言是"没给信任库时不能说成受信任"——把"签名有效"讲成"可信"
# 是这类工具最危险的表述。
SIGN_KEY="$DATA/smoke-signing-key.json"
SIGNED="$DATA/referer-guard-pkg-signed.pbclus"
TRUST="$DATA/smoke-trust.json"

KEYGEN_CODE=0
"$PACK_BIN" keygen -o "$SIGN_KEY" --name "smoke" --force > /dev/null 2>&1 || KEYGEN_CODE=$?
check "生成签名密钥" "$KEYGEN_CODE" "0"
if [ -f "$SIGN_KEY" ]; then
    # 私钥文件不该是人人可读的（Windows 上权限位不生效，只做存在性检查）
    ok "密钥文件已生成"
else
    bad "密钥文件没生成"
fi

SIGN_CODE=0
(cd "$PKG_STAGE" && "$PACK_BIN" pack . --sign "$SIGN_KEY" -o "$SIGNED" --force > /dev/null 2>&1) || SIGN_CODE=$?
check "签名打包" "$SIGN_CODE" "0"

VERIFY_SIG=0
"$PACK_BIN" verify "$SIGNED" > /dev/null 2>&1 || VERIFY_SIG=$?
check "签名包完整性校验通过" "$VERIFY_SIG" "0"

# 没有信任库时：必须说"有效但无法判断是否可信"，不能只说"有效"
OUT_NOKEY=$("$PACK_BIN" verify "$SIGNED" 2>&1)
if echo "$OUT_NOKEY" | grep -q 'Ed25519 有效'; then
    ok "报了签名算法与有效性"
else
    bad "应报签名有效，实际: $OUT_NOKEY"
fi
if echo "$OUT_NOKEY" | grep -q '无法判断是否可信'; then
    ok "没有信任库时明确说无法判断可信性"
else
    bad "没有信任库时不该暗示可信，实际: $OUT_NOKEY"
fi

TRUST_CODE=0
"$PACK_BIN" trust add --key "$SIGN_KEY" --name "smoke 发布者" -f "$TRUST" --force > /dev/null 2>&1 || TRUST_CODE=$?
check "把公钥加入信任库" "$TRUST_CODE" "0"

"$PACK_BIN" trust list -f "$TRUST" 2>/dev/null | grep -q "smoke 发布者" \
    && ok "信任库列表能列出发布者" || bad "信任库列表没列出发布者"

OUT_KEY=$("$PACK_BIN" verify "$SIGNED" --trusted-keys "$TRUST" 2>&1)
echo "$OUT_KEY" | grep -q '受信任' \
    && ok "带信任库时判定为受信任" || bad "带信任库时应判定受信任，实际: $OUT_KEY"

# 篡改签名块 → 必须判无效
#
# 用 od+dd 精确定位并翻转一个字节（只用 POSIX 工具，不引入 python 依赖）。
# 位置落在签名（64 字节）区间内：文件末尾往前 48(尾部) + 60。
cp "$SIGNED" "$DATA/tampered-sig.pbclus"
SIZE=$(wc -c < "$SIGNED" | tr -d ' ')
OFF=$((SIZE - 48 - 60))
ORIG=$(od -An -tu1 -j "$OFF" -N 1 "$SIGNED" | tr -d ' \n')
NEW=$(( ORIG ^ 255 ))
printf "$(printf '\\%03o' "$NEW")" | dd of="$DATA/tampered-sig.pbclus" bs=1 seek="$OFF" count=1 conv=notrunc 2>/dev/null

TAMPER_CODE=0
"$PACK_BIN" verify "$DATA/tampered-sig.pbclus" > /dev/null 2>&1 || TAMPER_CODE=$?
if [ "$TAMPER_CODE" -ne 0 ]; then
    ok "签名被改后校验失败（返回 $TAMPER_CODE）"
else
    bad "签名被改后仍报成功"
fi
# 同时：内容本身仍然自洽（说明失败**只**来自签名，不是别的原因）
if "$PACK_BIN" inspect "$DATA/tampered-sig.pbclus" > /dev/null 2>&1; then
    ok "签名被改后仍能读取结构（失败只来自签名）"
else
    bad "签名被改不该影响结构性读取"
fi


# 通过管理端 API 安装这个包（source=local 指向**文件**而不是目录）
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "$AUTH" \
  -H 'Content-Type: application/json' \
  -d "{\"source\":\"local\",\"path\":\"$(jsonpath "$PKG")\"}" \
  "$BASE/api/admin/plugins/install")
check "安装 .pbclus 200" "$CODE" "200"

CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "$AUTH" \
  "$BASE/api/admin/plugins/referer-guard-pkg/enable")
check "启用（从包安装的插件）200" "$CODE" "200"

sleep 1
LIST=$(curl -s -H "$AUTH" "$BASE/api/admin/plugins")
echo "$LIST" | sed 's/},{/}\n{/g' | grep '"name":"referer-guard-pkg"' | grep -q '"status":"running"' \
    && ok "包安装的插件进程运行中" || bad "包安装的插件未进入 running 状态"
echo "$LIST" | sed 's/},{/}\n{/g' | grep '"name":"referer-guard-pkg"' | grep -o '"pid":[0-9]*' | grep -qv '"pid":0' \
    && ok "包安装的插件有真实 pid" || bad "包安装的插件 pid 为 0"

# zip 改成 .pbclus 必须被明确拒绝。
#
# 识别只看前 4 字节（PK\x03\x04），所以这里 printf 一个就够了 ——
# 不用为了造一个真 zip 而引入 zip/python 依赖。
printf 'PK\003\004this-is-a-zip-body' > "$DATA/renamed.pbclus"
RESP=$(curl -s -X POST -H "$AUTH" -H 'Content-Type: application/json' \
  -d "{\"source\":\"local\",\"path\":\"$(jsonpath "$DATA/renamed.pbclus")\"}" \
  "$BASE/api/admin/plugins/install")
if echo "$RESP" | grep -q 'zip'; then
    ok "zip 改名成 .pbclus 被拒绝并说明原因"
else
    bad "zip 改名后应被拒绝，实际响应: $RESP"
fi

# 收尾：卸载这个包安装的插件，别影响后面的断言
curl -s -X POST -H "$AUTH" "$BASE/api/admin/plugins/referer-guard-pkg/disable" > /dev/null

# ---- 9.4 四个新插件：安装与启用 ----
#
# 验证"插件是能用的"最低标准不是"能编译"，而是：
#   1. 能装进真实服务器（清单校验、解包、落库全过）；
#   2. 启用后子进程真的被拉起（status=running + 真实 pid）；
#   3. 它们声明的钩子真的注册到了宿主（否则事件永远到不了）。
# 第 3 点特别重要：插件"看起来在跑"但钩子没注册，功能等于零，
# 而界面上完全看不出来 —— 只有查钩子计数能发现。
echo "[9.4] 四个新插件：安装与启用"
NEW_PLUGINS="notify-webhook backup-mirror download-limiter collab-redirect"
for p in $NEW_PLUGINS; do
    NP_SRC="$HERE/../examples/plugins/$p"
    if [ ! -d "$NP_SRC" ]; then
        bad "缺少插件目录: $p"
        continue
    fi
    NP_STAGE="$DATA/newstage/$p"
    mkdir -p "$NP_STAGE"
    cp "$NP_SRC/plugin.json" "$NP_STAGE/"
    BUILD_CODE=0
    (cd "$NP_SRC" && go build -o "$NP_STAGE/$p$EXE" .) || BUILD_CODE=$?
    check "$p 编译成功" "$BUILD_CODE" "0"
    if [ "$BUILD_CODE" -ne 0 ]; then continue; fi

    CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "$AUTH" \
      -H 'Content-Type: application/json' \
      -d "{\"source\":\"local\",\"path\":\"$(jsonpath "$NP_STAGE")\"}" \
      "$BASE/api/admin/plugins/install")
    check "$p 安装 200" "$CODE" "200"

    CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "$AUTH" \
      "$BASE/api/admin/plugins/$p/enable")
    check "$p 启用 200" "$CODE" "200"
done

sleep 2
LIST_ALL=$(curl -s -H "$AUTH" "$BASE/api/admin/plugins")
for p in $NEW_PLUGINS; do
    SEG=$(echo "$LIST_ALL" | sed 's/},{/}\n{/g' | grep "\"name\":\"$p\"")
    if echo "$SEG" | grep -q '"status":"running"'; then
        ok "$p 运行中"
    else
        bad "$p 未进入 running: $SEG"
    fi
    echo "$SEG" | grep -o '"pid":[0-9]*' | grep -qv '"pid":0' \
        && ok "$p 有真实 pid" || bad "$p 没有进程"
done

# 钩子计数：每个插件声明的钩子都必须有**进程外** handler。
# 只断言"钩子出现在列表里"不够（列表本来就列全部钩子），
# 必须断言 outProcess 不为 0 —— 否则"插件在跑但事件到不了"这种状态查不出来。
HOOKS_NEW=$(curl -s -H "$AUTH" "$BASE/api/admin/plugins/hooks")
check_out_hook() {
    h="$1"; owner="$2"
    SEG=$(echo "$HOOKS_NEW" | sed 's/},{/}\n{/g' | grep "\"name\":\"$h\"")
    if [ -z "$SEG" ]; then
        bad "$h 没出现在钩子列表里"
    elif echo "$SEG" | grep -q '"outProcess":0'; then
        bad "$h 没有进程外 handler（$owner 的钩子没注册上）"
    else
        ok "$h 有进程外 handler（$owner）"
    fi
}
check_out_hook onEmail notify-webhook
check_out_hook onBackup backup-mirror
check_out_hook onRestore backup-mirror
check_out_hook onCollabOpen collab-redirect
check_out_hook onAntiLeech download-limiter
check_out_hook onCLI "新插件均实现"

# ---- 9.4b 写入真实配置 ----
#
# 这一组配置是为了让 §10.1 的 onCLI 断言有意义：未配置时这几个插件
# 各自会报告"未配置"（那是**正确的**诚实行为，但测不出功能）。
# 给它们真实配置后，onCLI 输出才反映真实解析结果。
#
# 注意接口约定：config 字段是**字符串**（内含 JSON），所以内层要转义引号。
# notify-webhook 刻意**不配置**：用来断言"没配置时它诚实报错而不是假装成功"。
# 注意 `&` 在 JSON 里**不需要**转义。我第一版写成 `\&`，
# 结果整份配置不是合法 JSON，接口直接 400（而断言立刻抓住了它）。
# 这个细节值得留在脚本里：配置文件里手写 JSON 时最容易犯的就是"过度转义"。
CFG_COLLAB='{\"editorURL\":\"http://office.local/editor?fileId={{fileId}}&name={{fileName}}\",\"extensions\":[\"docx\"]}'
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X PUT -H "$AUTH" \
  -H 'Content-Type: application/json' -d "{\"config\":\"$CFG_COLLAB\"}" \
  "$BASE/api/admin/plugins/collab-redirect/config")
check "collab-redirect 配置写入 200" "$CODE" "200"

CFG_LIMITER='{\"maxPerMinute\":3,\"maxPerHour\":10}'
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X PUT -H "$AUTH" \
  -H 'Content-Type: application/json' -d "{\"config\":\"$CFG_LIMITER\"}" \
  "$BASE/api/admin/plugins/download-limiter/config")
check "download-limiter 配置写入 200" "$CODE" "200"

CFG_MIRROR='{\"targets\":[\"mirror\"],\"keep\":3}'
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X PUT -H "$AUTH" \
  -H 'Content-Type: application/json' -d "{\"config\":\"$CFG_MIRROR\"}" \
  "$BASE/api/admin/plugins/backup-mirror/config")
check "backup-mirror 配置写入 200" "$CODE" "200"

# ---- 9.5 重启后插件必须回来 ----
#
# 这一节针对一个真实存在过的 bug：安装侧用**进程工作目录**拼插件目录，
# 而加载侧用 `conf.DataDir()`。两者不一致时，"安装 + 启用"当场完全正常
# （控制器两处用自己的那套路径，自洽），但**重启后启动加载在另一个目录里
# 找不到插件**，且没有任何提示。
#
# 旧脚本抓不到它 —— 它只在安装向导之后重启一次，那时还没有任何插件，
# 装完插件后又从不重启。所以这里补上"装完插件再重启"这一步。
echo "[9.5] 重启后插件仍被加载"
# 先显式启用一个插件：§7 结束时 referer-guard 已被禁用、§9 收尾也禁用了
# 包安装的那个，所以**重启前根本没有启用的插件**。
# 不先启用就断言"重启后还在跑"，测的其实是空气。
#
# 这里刻意用**包安装的那个**（referer-guard-pkg）：它同时覆盖
# "PBClus 安装 → 启用 → 重启后自动恢复"这条完整链路。
curl -s -X POST -H "$AUTH" "$BASE/api/admin/plugins/referer-guard-pkg/enable" > /dev/null
sleep 1
BEFORE_RESTART=$(curl -s -H "$AUTH" "$BASE/api/admin/plugins")
echo "$BEFORE_RESTART" | sed 's/},{/}\n{/g' | grep '"name":"referer-guard-pkg"' \
  | grep -q '"status":"running"' \
    && ok "重启前插件运行中（作为对照）" || bad "重启前插件就没跑起来，后面的断言无意义"

kill "$SRVPID" 2>/dev/null || true
sleep 2
case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*) taskkill //F //IM ".smoke-nebula.exe" > /dev/null 2>&1 || true ;;
esac

(cd "$DATA" && "$BIN" -listen 127.0.0.1:18099 >> server.log 2>&1) &
SRVPID=$!
for i in $(seq 1 40); do
    if curl -s -o /dev/null "$BASE/api/health" 2>/dev/null; then break; fi
    sleep 0.5
done
# 重启后重新登录拿 token（旧 token 可能因密钥/过期而失效）
TOKEN=$(curl -s -X POST "$BASE/api/auth/login" -H 'Content-Type: application/json' \
  -d '{"userName":"admin","password":"Admin12345"}' \
  | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
AUTH="Authorization: Bearer $TOKEN"
if [ -z "$TOKEN" ]; then
    bad "重启后登录失败"
else
    ok "重启后已重新登录"
fi

# 这里要等一会儿：宿主是在启动流程里异步拉起已启用插件的
sleep 3
AFTER_RESTART=$(curl -s -H "$AUTH" "$BASE/api/admin/plugins")
echo "$AFTER_RESTART" | sed 's/},{/}\n{/g' | grep '"name":"referer-guard-pkg"' \
  | grep -q '"status":"running"' \
    && ok "重启后插件自动恢复运行" \
    || bad "重启后插件没有自动加载（安装侧与加载侧的数据目录可能不一致）"
echo "$AFTER_RESTART" | sed 's/},{/}\n{/g' | grep '"name":"referer-guard-pkg"' \
  | grep -o '"pid":[0-9]*' | grep -qv '"pid":0' \
    && ok "重启后插件有真实 pid" || bad "重启后插件没有进程"

# 四个新插件也必须一起回来。
# 只测一个插件不够：如果崩在某个插件的加载上（比如它 init 返回失败），
# 只有逐个断言才能看出是哪个插件拖垮了重启恢复。
for p in $NEW_PLUGINS; do
    SEG=$(echo "$AFTER_RESTART" | sed 's/},{/}\n{/g' | grep "\"name\":\"$p\"")
    if echo "$SEG" | grep -q '"status":"running"'; then
        ok "$p 重启后自动恢复"
    else
        bad "$p 重启后没有恢复: $SEG"
    fi
done

# 插件目录必须落在**数据目录**下，而不是进程工作目录。
# 这条直接钉住路径一致性（也就是上面那个 bug 的根因）。
if [ -d "$DATA/data/plugins/referer-guard-pkg" ]; then
    ok "插件目录位于数据目录下（\$DATA/data/plugins）"
else
    bad "插件目录不在数据目录下 —— 安装侧与加载侧可能又分叉了"
fi

# ---- 9.6 信任管理 API（前端「签名与信任」页的后端）----
#
# 前端界面无法在这里点，但它依赖的四个接口可以在**真实服务器**上跑一遍。
# 重点验证"界面拿不到就得靠猜"的两处：私钥必须被拒、坏信任库不许被覆盖。
echo "[9.6] 信任管理 API"

TRUST_STATE=$(curl -s -H "$AUTH" "$BASE/api/admin/plugins/trust")
echo "$TRUST_STATE" | grep -q '"mode"' \
    && ok "读取信任状态 200（含策略）" || bad "读取信任状态失败: $TRUST_STATE"
echo "$TRUST_STATE" | grep -q '"path"' \
    && ok "返回里带信任库路径（界面要展示给管理员）" || bad "缺少 path 字段"

# 加公钥：用第 9.3 节生成的密钥里的公钥
PUBKEY=$(sed -n 's/.*"publicKey": "\([^"]*\)".*/\1/p' "$SIGN_KEY" | head -1)
if [ -n "$PUBKEY" ]; then
    ADD_CODE=0
    ADD_RESP=$(curl -s -X POST -H "$AUTH" -H 'Content-Type: application/json' \
      -d "{\"publicKey\":\"$PUBKEY\",\"name\":\"smoke 发布者\"}" \
      "$BASE/api/admin/plugins/trust") || ADD_CODE=$?
    check "加公钥 200" "$ADD_CODE" "0"
    echo "$ADD_RESP" | grep -q '"keyId"' \
        && ok "返回 keyId" || bad "加公钥没返回 keyId: $ADD_RESP"
    # 取出 keyId 供后面的断言用。
    #
    # 不要用"名字里含 smoke"来判断列表里有没有这条记录：响应里还有
    # **信任库路径**，而本脚本的数据目录就叫 .smoke-plugindata ——
    # 那样的断言会被路径命中，变成永远为真。
    ADDED_KEYID=$(echo "$ADD_RESP" | sed -n 's/.*"keyId":"\([^"]*\)".*/\1/p' | head -1)

    # 重复添加必须 409 且提示 force（界面据此弹确认）
    DUP_CODE=0
    DUP_RESP=$(curl -s -X POST -H "$AUTH" -H 'Content-Type: application/json' \
      -d "{\"publicKey\":\"$PUBKEY\",\"name\":\"重复\"}" \
      "$BASE/api/admin/plugins/trust") || DUP_CODE=$?
    if echo "$DUP_RESP" | grep -q 'force'; then
        ok "重复添加被拒且提示 force"
    else
        bad "重复添加应提示 force，实际: $DUP_RESP"
    fi

    # **私钥**必须被拒（64 字节 base64）。这条守住"别把私钥发出去"。
    PRIV=$(sed -n 's/.*"privateKey": "\([^"]*\)".*/\1/p' "$SIGN_KEY" | head -1)
    if [ -n "$PRIV" ]; then
        PBODY=$(curl -s -X POST -H "$AUTH" -H 'Content-Type: application/json' \
          -d "{\"publicKey\":\"$PRIV\",\"name\":\"误发私钥\"}" \
          "$BASE/api/admin/plugins/trust")
        if echo "$PBODY" | grep -q '私钥'; then
            ok "粘贴私钥被拒，且说明原因"
        else
            bad "粘贴私钥必须被拒并说明，实际: $PBODY"
        fi
        echo "$PBODY" | grep -q 'pub.json' \
            && ok "错误里指明该用公钥文件" || bad "应指明用 *.pub.json: $PBODY"
    else
        bad "没能从密钥文件里取出私钥做这条断言"
    fi

    # 列表里应能看到这枚密钥（按 keyId 断言，见上面对"别用名字"的说明）
    TRUST_LIST=$(curl -s -H "$AUTH" "$BASE/api/admin/plugins/trust")
    if [ -n "$ADDED_KEYID" ] && echo "$TRUST_LIST" | grep -q "$ADDED_KEYID"; then
        ok "信任库列表里能看到刚加的密钥（$ADDED_KEYID）"
    else
        bad "列表里没有刚加的密钥 $ADDED_KEYID，实际响应: $TRUST_LIST"
    fi

    # 改策略：非法值 400
    CODE=$(curl -s -o /dev/null -w '%{http_code}' -X PUT -H "$AUTH" \
      -H 'Content-Type: application/json' -d '{"mode":"Require!"}' \
      "$BASE/api/admin/plugins/trust/mode")
    check "非法策略被拒 400" "$CODE" "400"

    # 切到 off 应成功（让后面的断言不受签名策略影响）
    CODE=$(curl -s -o /dev/null -w '%{http_code}' -X PUT -H "$AUTH" \
      -H 'Content-Type: application/json' -d '{"mode":"off"}' \
      "$BASE/api/admin/plugins/trust/mode")
    check "切策略到 off 200" "$CODE" "200"

    # 信任库文件损坏时不许被静默覆盖（这条无法用正常请求造出坏文件，
    # 所以在数据目录里直接写坏它，再确认接口拒绝而不是重建）
    TRUST_FILE="$DATA/data/plugin-trust.json"
    cp "$TRUST_FILE" "$TRUST_FILE.bak" 2>/dev/null || true
    printf '{"keys":[{"keyId":"tooshort"}]}' > "$TRUST_FILE"
    BAD_CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "$AUTH" \
      -H 'Content-Type: application/json' \
      -d "{\"publicKey\":\"$PUBKEY\",\"name\":\"x\"}" \
      "$BASE/api/admin/plugins/trust")
    check "坏信任库被拒 409（不静默重建）" "$BAD_CODE" "409"
    if grep -q 'tooshort' "$TRUST_FILE"; then
        ok "坏文件保持原样（没有被覆盖）"
    else
        bad "坏信任库文件被覆盖了 —— 会抹掉管理员已有的信任关系"
    fi
    # 列表要如实报错，不能显示成"空库"
    curl -s -H "$AUTH" "$BASE/api/admin/plugins/trust" | grep -q '"error"' \
        && ok "列表如实报出信任库错误" || bad "列表把坏库显示成了空库"
    # 还原成好的信任库，避免影响后续
    [ -f "$TRUST_FILE.bak" ] && mv "$TRUST_FILE.bak" "$TRUST_FILE"
fi

# ---- 10. 插件命令行（onCLI）----
echo "[10/10] 插件命令行 onCLI"
#
# `nebula plugin <cmd>` 不启动 HTTP 服务：初始化依赖 → 派发 onCLI → 退出。
# 验证两件事：
#   1. 子命令真的被识别（而不是被 flag 解析当成未知参数忽略掉）；
#   2. 退出码约定成立（脚本要能靠退出码判断成功/失败）。
#
# **必须先停掉服务**：同一个数据目录被两个进程同时打开 SQLite 会互相干扰。
kill "$SRVPID" 2>/dev/null || true
sleep 2
case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*) taskkill //F //IM ".smoke-nebula.exe" > /dev/null 2>&1 || true ;;
esac

CLI_CODE=0
(cd "$DATA" && "$BIN" plugin hello > cli-ok.log 2>&1) || CLI_CODE=$?
check "插件子命令退出码 0（无插件处理该命令也算成功）" "$CLI_CODE" "0"

# 用法错误：没有给命令名 → 退出码 1
CLI_CODE=0
(cd "$DATA" && "$BIN" plugin > cli-usage.log 2>&1) || CLI_CODE=$?
check "缺少命令名时退出码 1（用法错误）" "$CLI_CODE" "1"

# 用法提示要写进 stderr（否则脚本里 `nebula plugin 2>/dev/null` 会什么都不显示）
if grep -q '用法' "$DATA/cli-usage.log" 2>/dev/null; then
    ok "缺命令名时给出了用法提示"
else
    bad "缺命令名时没有用法提示"
fi

# 子命令**不得**启动 HTTP 服务：端口应立刻可被占用
if (cd "$DATA" && "$BIN" plugin hello > /dev/null 2>&1) ; then
    if curl -s -o /dev/null --max-time 1 "$BASE/api/health" 2>/dev/null; then
        bad "插件子命令意外启动了 HTTP 服务（应只初始化依赖后退出）"
    else
        ok "插件子命令未启动 HTTP 服务"
    fi
fi

# ---- 10.1 新插件的 onCLI 真实往返 ----
#
# 这是"插件真的能用"的最强单条证据：宿主 → 子进程（onCLI）→ ctxPatch → 命令行打印。
# 它同时证明了：插件被加载、协议握手成功、事件派发到子进程、
# 回写能穿过 IPC 回到宿主、以及插件的 onCLI 实现真的在响应命令名。
#
# 只断言退出码为 0 是不够的 —— 没有任何插件响应某个命令时退出码也是 0
# （§10 开头那条就是这种情况）。所以这里必须断言**输出非空**。
echo "[10.1] 新插件的 onCLI 往返"
for p in backup-mirror download-limiter collab-redirect; do
    CLI_OUT="$DATA/cli-$p.log"
    CODE=0
    (cd "$DATA" && "$BIN" plugin "$p" > "$CLI_OUT" 2>&1) || CODE=$?
    check "$p 的 onCLI 退出码 0" "$CODE" "0"
    if [ -s "$CLI_OUT" ]; then
        ok "$p 的 onCLI 有输出（事件真的派发到子进程了）"
    else
        bad "$p 的 onCLI 没有任何输出 —— 命令没被插件接住"
    fi
done

# notify-webhook 刻意**没配置**：没配 webhook 时它必须**诚实失败**，
# 而不是打印一句"完成"然后什么都不发。退出码 1 + 明确提示才是对的行为。
NW_CODE=0
(cd "$DATA" && "$BIN" plugin notify-webhook > "$DATA/cli-notify-webhook.log" 2>&1) || NW_CODE=$?
check "notify-webhook 未配置时退出码 1（诚实失败）" "$NW_CODE" "1"
if grep -qE '未配置|webhook' "$DATA/cli-notify-webhook.log" 2>/dev/null; then
    ok "notify-webhook 未配置时说明了原因与下一步"
else
    bad "notify-webhook 未配置时应说明原因: $(head -3 "$DATA/cli-notify-webhook.log" 2>/dev/null)"
fi

# 再抽两条做**内容**断言，确认拿到的是这个插件自己的输出而不是别人的。
if grep -qE 'limit|限额|上限|maxPer' "$DATA/cli-download-limiter.log" 2>/dev/null; then
    ok "download-limiter 的 onCLI 输出了限流参数（配置被读到）"
else
    bad "download-limiter 的 onCLI 输出不符合预期: $(head -3 "$DATA/cli-download-limiter.log" 2>/dev/null)"
fi
# collab-redirect 写了 editorURL，应打印渲染后的 URL（说明模板解析在工作）
if grep -qE 'http' "$DATA/cli-collab-redirect.log" 2>/dev/null; then
    ok "collab-redirect 的 onCLI 打印了渲染后的 URL"
else
    bad "collab-redirect 的 onCLI 没有渲染示例: $(head -3 "$DATA/cli-collab-redirect.log" 2>/dev/null)"
fi
# backup-mirror 写了 target，应能在输出里看到它
if grep -q 'mirror' "$DATA/cli-backup-mirror.log" 2>/dev/null; then
    ok "backup-mirror 的 onCLI 输出了配置的 target"
else
    bad "backup-mirror 的 onCLI 没有输出 target: $(head -3 "$DATA/cli-backup-mirror.log" 2>/dev/null)"
fi

echo
echo "============================================"
echo "  通过 $PASS / 失败 $FAIL"
echo "============================================"
[ "$FAIL" -eq 0 ]

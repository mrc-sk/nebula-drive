#!/bin/sh
# 存储策略密钥「静态加密」端到端冒烟测试
#
# 为什么需要它：policies.config 里存 SFTP 密码 / OSS / COS / S3 密钥，
# 加密靠 models.Encrypted 的 driver.Valuer。而 database/sql **只对实现了
# Valuer 的「值」**调用它 —— 传裸 string 时 Valuer 完全不参与，明文直接落库，
# 且没有任何报错（接口 200、GORM 不抱怨、日志无异常）。
#
# 这类 bug 包级测试很容易漏：models/encrypted_test.go 的
# TestEncryptedUpdateMapPath 传的就是已带类型的值，覆盖不到错误写法。
# 所以这里从**真实 HTTP 接口**打进去，再直接读**数据库原始列**断言密文。
#
# 检查项：
#   1. 启动真实二进制（真实进程 + 真实 SQLite）
#   2. 走安装向导 + 管理员登录
#   3. 创建存储策略（Create 路径）→ 原始列必须带 enc:v1: 前缀
#   4. **编辑**策略（Update 路径，就是出过事的那条）→ 原始列必须仍是密文
#   5. 数据库文件里不得出现明文密钥
#   6. 解密链路仍然可用（改完策略还能正常上传/下载）
#
# 第 4 步是本脚本存在的理由。历史上 controllers/admin_ex.go 的 UpdatePolicy
# 在这里传了裸 string，管理员编辑一次策略密钥就变明文，直到下次重启
# 才被 MigrateSensitiveFields 兜底修回。详见 FIXLOG.md 第 17 节。

set -e

# 全部路径基于脚本自身所在目录解析。
# 不用 "./" / "../" 这类相对路径拼：脚本在谁的 CWD 下运行、$BIN 又带不带 "./"，
# 两者一组合就会把产物建到别处（"No such file or directory" 且日志里看不出原因）。
HERE=$(cd "$(dirname "$0")" && pwd)

BASE="http://127.0.0.1:18098"
DATA="$HERE/.smoke-policydata"

# Windows（Git Bash）下 Go 产物带 .exe，否则 bash 起不来这个进程
case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*) EXE=".exe" ;;
    *)                    EXE="" ;;
esac
BIN="$HERE/.smoke-nebula$EXE"

# 两个互不相同的哨兵串：分别验证 Create 与 Update 两条路径
SECRET_CREATE="SMOKE_CREATE_SECRET_9f3a"
SECRET_UPDATE="SMOKE_UPDATE_SECRET_c71b"

PASS=0
FAIL=0

ok()   { PASS=$((PASS+1)); echo "  [PASS] $1"; }
bad()  { FAIL=$((FAIL+1)); echo "  [FAIL] $1"; }
check(){ if [ "$2" = "$3" ]; then ok "$1 ($2)"; else bad "$1: got=$2 want=$3"; fi; }

curl() { command curl -sS --noproxy '*' "$@"; }

SRVPID=""
cleanup() {
    # MSYS/Git Bash 的 PID 与 Windows 进程未必一一对应，只 kill 子 shell 会留下
    # 真实的 nebula 进程继续占着端口和 $DATA（症状是 rm 报 "Device or resource busy"）。
    # 所以 Windows 下按映像名兜底。
    [ -n "$SRVPID" ] && kill "$SRVPID" 2>/dev/null || true
    sleep 1
    case "$(uname -s)" in
        MINGW*|MSYS*|CYGWIN*) taskkill //F //IM ".smoke-nebula.exe" > /dev/null 2>&1 || true ;;
    esac
    [ -n "$SRVPID" ] && kill -9 "$SRVPID" 2>/dev/null || true
    # || true 不能省：set -e 下 EXIT trap 里任何一条返回非零的命令都会
    # **覆盖脚本的真实退出码**，把"断言失败"变成"看起来是清理失败"。
    rm -rf "$DATA" || true
}
trap cleanup EXIT

echo
echo "============================================"
echo "  存储策略密钥静态加密冒烟测试"
echo "============================================"
echo
# 打印环境：CI 里失败时先看这行，能立刻区分"环境不对"与"断言失败"。
echo "环境: $(uname -srm) | bash ${BASH_VERSION:-?} | $(go version 2>/dev/null || echo 'go 不可用')"
echo

# ---- 0. 前置检查 ----
#
# sqlite3 是本脚本**核心断言**的依赖（读数据库原始列）。缺了它只有两种选择：
#   - 本地缺依赖：跳过（默认）—— 开发机不装也能跑其余检查，不阻塞人
#   - CI 里缺依赖：**必须失败**。否则门禁会变成"永远绿色"的假门禁，
#     正是这个脚本要防的那类问题（见 FIXLOG 第 18 节：一个坏掉的冒烟脚本
#     不会发出任何信号，于是坏了好几个月没人知道）。
# CI 通过 SMOKE_REQUIRE_SQLITE3=1 声明"这里不允许静默降级"。
if ! command -v sqlite3 > /dev/null 2>&1; then
    if [ "${SMOKE_REQUIRE_SQLITE3:-0}" = "1" ]; then
        echo "[FAIL] 缺少 sqlite3，而 SMOKE_REQUIRE_SQLITE3=1 —— 不允许静默跳过"
        echo "       本脚本的核心断言是直接读数据库原始列，缺少该工具则覆盖为零。"
        exit 1
    fi
    echo "跳过：本机没有 sqlite3，无法读取数据库原始列"
    echo "（这个检查正是本脚本的核心，缺失时宁可跳过也不要假装通过）"
    exit 0
fi

# ---- 1. 构建 ----
echo "[1/6] 构建"
(cd "$HERE" && go build -o "$BIN" .) || { echo "构建失败"; exit 1; }
ok "二进制就绪: $BIN"

# ---- 2. 启动 ----
echo "[2/6] 启动服务"
mkdir -p "$DATA"
# 刻意**不传** -data：让数据目录取默认值 "data"。
#
# 传 `-data .` 时 conf 只会 MkdirAll(".")，而安装向导提交的库路径是
# "data/nebula.db"（相对 CWD 解析）—— 父目录 data/ 不存在，sqlite 报
# `unable to open database file: out of memory (14)`，这个报错极具误导性。
# 用默认数据目录时 conf 会建出 ./data，与真实安装路径完全一致。
(cd "$DATA" && "$BIN" -listen 127.0.0.1:18098 > server.log 2>&1) &
SRVPID=$!
for i in $(seq 1 40); do
    if curl -s -o /dev/null "$BASE/api/install/status" 2>/dev/null; then break; fi
    sleep 0.5
done
if ! curl -s -o /dev/null "$BASE/api/install/status"; then
    echo "服务未启动，日志："; tail -30 "$DATA/server.log"; exit 1
fi
ok "服务已启动 pid=$SRVPID"

# ---- 3. 安装 + 登录 ----
echo "[3/6] 初始化安装"
curl -s -X POST "$BASE/api/install" -H 'Content-Type: application/json' -d '{
  "admin":{"userName":"admin","email":"a@b.c","password":"Admin12345"},
  "db":{"type":"sqlite","file":"data/nebula.db"},
  "system":{"siteName":"Smoke","uploadPath":"smokeuploads"}
}' > /dev/null
TOKEN=$(curl -s -X POST "$BASE/api/auth/login" -H 'Content-Type: application/json' \
  -d '{"userName":"admin","password":"Admin12345"}' \
  | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
if [ -z "$TOKEN" ]; then echo "登录失败"; exit 1; fi
ok "管理员登录成功"

AUTH="Authorization: Bearer $TOKEN"

DB=$(find "$DATA" -name '*.db' | head -1)
if [ -z "$DB" ]; then echo "找不到 SQLite 文件"; exit 1; fi
ok "数据库: $DB"

raw_config() { sqlite3 "$DB" "SELECT config FROM policies WHERE id=$1;"; }

# 防御性第二道：整个数据库文件（含 wal/shm）里不得出现明文密钥。
#
# 逐个文件判断而不是用 `grep ... glob`：glob 不匹配时 grep 会报错退出，
# 那样会把"文件根本没匹配上"误判成"没有明文"，变成假通过。
db_leaks() {
    for f in "$DB" "$DB-wal" "$DB-shm"; do
        [ -f "$f" ] || continue
        if grep -aq "$1" "$f"; then return 0; fi
    done
    return 1
}

# ---- 4. 创建路径 ----
echo "[4/6] 创建存储策略（Create 路径）"
cat > "$DATA/create.json" <<EOF
{"name":"smoke-sftp","type":"sftp","config":"{\"password\":\"$SECRET_CREATE\"}","isDefault":false}
EOF
CREATED=$(curl -s -X POST -H "$AUTH" -H 'Content-Type: application/json' \
  -d @"$DATA/create.json" "$BASE/api/admin/policies")
PID=$(echo "$CREATED" | sed -n 's/.*"id":\([0-9]*\).*/\1/p' | head -1)
if [ -z "$PID" ]; then bad "创建策略失败: $CREATED"; else ok "创建策略 id=$PID"; fi

RAW1=$(raw_config "$PID")
case "$RAW1" in
    enc:v1:*) ok "Create 后原始列为密文" ;;
    # 空值单独判：读库失败（例如 sqlite 被占用）会返回空串，
    # 若不区分就会打印"原始列为明文: "，把一个环境问题误报成安全事件。
    "")       bad "读不到 policies.config 原始值（数据库被占用或查询失败？）" ;;
    *)        bad "Create 后原始列为明文: $RAW1" ;;
esac

# ---- 5. 更新路径（核心断言）----
echo "[5/6] 编辑存储策略（Update 路径 —— 出过事的那条）"
cat > "$DATA/update.json" <<EOF
{"name":"smoke-sftp","type":"sftp","config":"{\"password\":\"$SECRET_UPDATE\"}","isDefault":false}
EOF
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X PUT -H "$AUTH" \
  -H 'Content-Type: application/json' \
  -d @"$DATA/update.json" "$BASE/api/admin/policies/$PID")
check "更新策略 200" "$CODE" "200"

RAW2=$(raw_config "$PID")
case "$RAW2" in
    enc:v1:*) ok "Update 后原始列仍为密文" ;;
    "")       bad "读不到 policies.config 原始值（数据库被占用或查询失败？）" ;;
    *)        bad "Update 后原始列为**明文**（密钥泄露）: $RAW2" ;;
esac

if db_leaks "$SECRET_UPDATE"; then
    bad "数据库文件中出现明文更新密钥 $SECRET_UPDATE"
else
    ok "数据库文件中无明文更新密钥"
fi
if db_leaks "$SECRET_CREATE"; then
    bad "数据库文件中出现明文创建密钥 $SECRET_CREATE"
else
    ok "数据库文件中无明文创建密钥"
fi

# 顺带把上传实际使用的策略 1（local）也改一次 —— 第 6 步要靠它证明解密链路。
# upload 路径（controllers/file.go）写死了 handlerForPolicyID(1)，改不了。
cat > "$DATA/update1.json" <<EOF
{"name":"本地存储","type":"local","config":"{\"path\":\"smokeuploads\"}","isDefault":true}
EOF
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X PUT -H "$AUTH" \
  -H 'Content-Type: application/json' \
  -d @"$DATA/update1.json" "$BASE/api/admin/policies/1")
check "更新策略 1（上传实际使用）200" "$CODE" "200"
case "$(raw_config 1)" in
    enc:v1:*) ok "策略 1 Update 后原始列仍为密文" ;;
    "")       bad "读不到策略 1 的 config 原始值（数据库被占用或查询失败？）" ;;
    *)        bad "策略 1 Update 后原始列为明文: $(raw_config 1)" ;;
esac

# ---- 6. 解密链路仍然可用 ----
echo "[6/6] 解密链路（改完策略还能正常上传/下载）"
#
# 为什么用「上传 + 下载」而不是「读接口拿配置」：
# models.Policy.Config 的 tag 是 `json:"-"`，接口**刻意不返回**配置
# （不把密钥发给客户端是对的），所以没法从 HTTP 响应里观察解密结果。
#
# 但 filesystem.New(p.Type, p.Config.String()) 需要配置是**明文 JSON**：
# 若 Config.String() 返回的是 "enc:v1:..."，JSON 解析会失败 → 上传 500。
# 所以上传成功本身就是解密链路的端到端证明。
echo "smoke-policy-config-body" > "$DATA/payload.txt"
UP=$(curl -s -H "$AUTH" -F "file=@$DATA/payload.txt" "$BASE/api/files/upload" \
     | sed -n 's/.*"id":\([0-9]*\).*/\1/p' | head -1)
if [ -z "$UP" ]; then
    bad "上传失败（策略配置可能未正确解密）"
else
    ok "上传成功 file id=$UP"
    DL=$(curl -s -H "$AUTH" "$BASE/api/files/$UP/download")
    if [ "$DL" = "smoke-policy-config-body" ]; then
        ok "下载内容一致（解密后的策略配置真实可用）"
    else
        bad "下载内容不一致: $DL"
    fi
fi

echo
echo "============================================"
echo "  通过 $PASS / 失败 $FAIL"
echo "============================================"
[ "$FAIL" -eq 0 ]

# NebulaDrive 修复日志

> 配套文档：`CODE_REVIEW.md`（完整审计报告，含问题编号与原始行号）
> 本文件记录**已落地的代码改动**，每条对应审查报告中的编号。

---

## 一、修复总览

| 编号 | 问题 | 严重度 | 状态 | 主要改动文件 |
|---|---|---|---|---|
| P0-1 | 路径穿越（本地/SFTP/WebDAV 远端） | P0 | ✅ 已修 | `filesystem/fs.go`、`filesystem/sftp.go` |
| P0-2 | WebDAV PUT 覆盖场景丢弃请求体 | P0 | ✅ 已修 | `webdav/webdav.go` |
| P0-3 | WebDAV PUT 绕过扩展名/魔术字节/配额/审计 | P0 | ✅ 已修 | `webdav/webdav.go`、`internal/service/outbound.go` |
| P0-4 | WebDAV COPY/LOCK/UNLOCK/PROPPATCH 伪造 200 | P0 | ✅ 已修 | `webdav/webdav.go` |
| P0-5 | CORS `AllowAllOrigins: true` | P0 | ✅ 已修 | `routers/router.go` |
| P0-6 | 离线下载 SSRF（无校验、无超时、无体积上限） | P0 | ✅ 已修 | `controllers/task.go`、`internal/service/outbound.go` |
| P1-1 | `Breadcrumb` 零鉴权越权枚举路径 | P1 | ✅ 已修 | `controllers/file.go` |
| P1-2 | 标签搜索 SQL 语法错误（`owner_id ?` 缺 `=`） | P1 | ✅ 已修 | `controllers/search.go` |
| P1-3 | 清理搜索历史 SQL 在 MySQL 报 1093 | P1 | ✅ 已修 | `controllers/search.go` |
| P1-4 | 多步写入全程零事务（13 处） | P1 | ✅ 已修 | `install.go`、`file.go`、`collab.go`、`task.go`、`webdav.go`、`plan.go` |
| P1-5 | 配额 TOCTOU 竞态 | P1 | ✅ 已修 | `internal/service/quota.go` + 全部写入点 |
| P1-6 | 分片上传：map 竞争 / 会话泄漏 / 采信前端声明大小 | P1 | ✅ 已修 | `controllers/file.go` |
| P1-7 | `CancelTask` 重复 close channel panic | P1 | ✅ 已修 | `controllers/task.go` |
| P2-2 | `Purge` / `cleanupTrash` 重复扣减配额 | P2 | ✅ 已修 | `controllers/file.go` |
| P2-7 | 敏感字段明文入库 | P1 | ✅ 已修 | `internal/cryptox`、`models/encrypted.go`、`models/migrate_sensitive.go` |
| S-1 | 改密码不吊销会话（旧 token 仍可用 7 天） | P1 | ✅ 已修 | `controllers/auth.go` |
| S-2 | `CreateUser` 缺密码强度校验（可建弱密码账号） | P1 | ✅ 已修 | `controllers/admin.go` |
| S-3 | JWT `Parse` 未显式限定签名算法 | P2 | ✅ 已修 | `pkg/jwt/jwt.go` |
| — | 测试驱动与生产不一致（且需 CGO，测试跑不起来） | 严重 | ✅ 已修 | `pkg/testutil/db.go` |
| — | 测试连接不关闭，Windows 上清理失败致误判 FAIL | 中 | ✅ 已修 | `pkg/testutil/db.go`、`pkg/db/db_test.go`、`controllers/install_test.go` |
| — | `db.Init` 覆盖全局实例时泄漏旧连接池 | 中 | ✅ 已修 | `pkg/db/db.go` |

> 验证状态：
> - 后端：`go build` ✅ ｜ `go vet` ✅ ｜ `go test -race` 双平台（ubuntu + windows）✅（全量测试通过）
> - 前端：GitHub Actions `npm ci` + `npm run build`（`tsc -b` 严格类型检查 + `vite` 生产构建）✅，
>   并校验 `backend/frontend_dist` 与构建产物一致 ✅
>
> **审查清单 15 项 + 追加加固 3 项（S-1~S-3）已全部完成**；后续两处非缺陷改进
> （`UpdateUser` 自操作保护、前端 CI）亦已补上，仅剩"5 项进程内存状态外置到 Redis"按需处理。

---

## 二、逐项说明

### P0-1 路径穿越

**根因**：所有本地/远端 handler 直接把用户可控名称拼进 `filepath.Join(root, name)`，
`path.Clean` 在 Windows 下不识别 `\`，因此 `..\..\windows\win.ini` 可逃逸出存储根目录。

**修复**：新增统一的名称归一化 + 前缀校验函数。

```go
// filesystem/fs.go
var (
    ErrPathTraversal      = errors.New("path traversal detected")
    ErrInvalidObjectName  = errors.New("invalid object name")
)

// SafeLocalPath 把不可信的对象名解析为存储根目录内的绝对路径。
// 任何逃逸尝试都返回 ErrPathTraversal。
func SafeLocalPath(root, name string) (string, error) {
    if strings.TrimSpace(name) == "" {
        return "", ErrInvalidObjectName
    }
    clean := strings.ReplaceAll(name, "\\", "/") // Windows 反斜杠归一化
    clean = strings.TrimLeft(clean, "/")
    if clean == "" {
        return "", ErrInvalidObjectName
    }
    for _, seg := range strings.Split(clean, "/") {
        if seg == "" {
            continue
        }
        if seg == "." || seg == ".." {
            return "", ErrPathTraversal
        }
        if strings.ContainsAny(seg, "\x00") || strings.HasSuffix(seg, ":") {
            return "", ErrInvalidObjectName
        }
    }
    joined := filepath.Join(root, filepath.FromSlash(clean))
    absRoot, err := filepath.Abs(root)
    if err != nil { return "", err }
    absPath, err := filepath.Abs(joined)
    if err != nil { return "", err }
    // 双重保险：解析后的绝对路径必须仍在根内
    if absPath != absRoot && !strings.HasPrefix(absPath, absRoot+string(filepath.Separator)) {
        return "", ErrPathTraversal
    }
    return absPath, nil
}
```

`Local` handler 的 Put / Get / GetRange / Delete / Size / Copy 六个方法全部改为
经 `SafeLocalPath` 取路径；`SFTPHandler.remotePath` 与 `WebDAVRemoteHandler.fullPath`
改成返回 `(string, error)` 并做同样的归一化 + 前缀校验（远端路径额外逐段 `url.PathEscape`）。

**防御层次**：段级黑名单（`..` / `:` / NUL）→ 反斜杠归一化 → 绝对路径前缀比对。三层独立成立，
任一层被绕过仍有下一层兜底。

---

### P0-6 SSRF

**根因**：`http.Get(url)` 直接请求用户提供的 URL，无 scheme 限制、无 DNS 校验、无超时、
`io.ReadAll` 无上限。可打内网、可读云元数据 `169.254.169.254`、可耗尽内存。

**修复**：新增 `internal/service/outbound.go`，提供受控出站客户端。

```go
// 禁止的目标地址：回环 / 私有 / 链路本地 / 组播 / 未指定 / CGNAT / IPv4 映射 IPv6
func IsForbiddenIP(ip net.IP) bool

// 请求前校验：scheme 白名单 + DNS 解析 + 所有 A 记录逐一校验
func ValidateOutboundURL(raw string) (*url.URL, error)

// 拨号时二次校验 —— 抵御 DNS Rebinding（TOCTOU）
func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error)

const (
    outboundTimeout           = 30 * time.Second
    MaxOutboundResponseBytes  = 64 << 20 // 64MB
    maxRedirects              = 3
)
```

`controllers/task.go` 的两处调用点已切换：

| 原代码 | 现代码 |
|---|---|
| `http.Get(url)` in `downloadBytes` | `service.FetchBytes(rawURL, service.MaxOutboundResponseBytes)` |
| `http.DefaultClient.Get(url)` in `runHTTPTask` | `service.OutboundClient().Get(url)` |

`FetchBytes` 内部用 `io.LimitReader(body, maxBytes+1)` 读取，超限显式报错而非静默截断。
重定向也被限制为 3 跳，且每一跳都重新走 `ValidateOutboundURL`。

---

### P0-5 CORS

**根因**：`AllowAllOrigins: true` + `AllowHeaders: ["*"]`，任意站点可跨源调用全部 API。

**修复**：改为 `AllowOriginFunc` 白名单判定。

```go
// routers/router.go
func allowedOrigin(origin string) bool {
    // 1) 本机开发来源（localhost / 127.0.0.1 / [::1] / *.localhost）放行
    // 2) settings: cors.allowed_origins 中显式配置的来源放行
    // 3) 其余一律拒绝
}
```

额外允许来源从 `settings` 表读取（键 `cors.allowed_origins`，逗号/分号/换行/空格分隔），
带 60 秒缓存避免每请求查库。默认值为空 —— 即默认只允许本机来源与同源请求。

`AllowCredentials` 从 `false` 改为 `true`，但**仅在白名单来源下生效**，这是安全前提已经建立之后的必要修正（前端需要携带 Cookie）。

---

### P0-2 / P0-3 WebDAV PUT

**根因**：覆盖已存在文件时直接 `c.String(200, "OK")` 丢弃请求体 —— 客户端以为成功，数据全丢。
新建文件时则完全跳过 HTTP 上传路径的「扩展名白名单 → 魔术字节 → 配额 → 审计」四道关卡。

**修复**：

```go
// 覆盖场景：读新内容 → 写新物理对象 → 改 File 指向 → 释放旧物理对象
if remaining == "" {
    body, mimeType, ext, err := readDAVUpload(c, node.Name)   // 走统一校验链
    ...
    h.Put(bytes.NewReader(body), newSrc, int64(len(body)))
    delta := int64(len(body)) - node.Size
    if delta > 0 { service.AddStorageTx(db.Get(), u.ID, delta) }  // 原子配额
    db.Get().Model(&models.File{}).Where("id = ?", node.ID).Updates(updates)
    storage.Retain(node.PolicyID, newSrc, ...)
    storage.ReleaseObject(oldPolicyID, oldSrc, oh)   // 旧对象 refs-1，归零自动删
    c.Status(204)   // 覆盖成功返回 204 No Content（RFC 4918）
    return
}
```

新增的 `readDAVUpload` 是 HTTP 上传校验链在 WebDAV 侧的复用入口：

```go
func readDAVUpload(c *gin.Context, name string) ([]byte, string, string, error) {
    ext := strings.ToLower(path.Ext(name))
    if !service.IsAllowedExtension(ext) {          // ① 扩展名白名单
        return nil, "", "", fmt.Errorf("file type not allowed: %s", ext)
    }
    maxBytes := int64(service.MaxUploadBytes())
    body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBytes+1))
    if int64(len(body)) > maxBytes {                // ② 体积上限
        return nil, "", "", fmt.Errorf("file too large (limit %d bytes)", maxBytes)
    }
    if service.MagicCheckEnabled() && service.IsImageExt(ext) {
        if err := service.CheckImageMagicErr(body, ext); err != nil {  // ③ 魔术字节
            return nil, "", "", err
        }
    }
    return body, mimeTypeFromExt(ext), ext, nil
}
```

写入成功后调用 `auditDAVWrite` 落 `audit_logs`，audit 的 `Detail` 字段里带 `"channel": "webdav"`，
与 HTTP 上传共用同一张表、同一套查询。

配额部分同时把原来「读 storage → 比较 → 事后 `UpdateColumn("storage", storage+size)`」
替换为 `service.AddStorageTx`（见 P1-5）。

---

### P0-4 WebDAV LOCK / UNLOCK / COPY / PROPPATCH

**根因**：`case "COPY", "PROPPATCH", "LOCK", "UNLOCK": c.Status(200)` —— 对客户端谎报成功。
依赖锁做并发保护的客户端会静默覆盖彼此的数据；COPY 后文件根本不存在。

**修复**：

- **LOCK**：实现进程内排他锁表 `davLocks`，返回完整 RFC 4918 lockdiscovery 响应体与
  `Lock-Token` / `Timeout` 头。
- **UNLOCK**：校验 `Lock-Token` 头，确认持锁人匹配后释放，否则 409。
- **锁生效**：`PUT` / `DELETE` / `MOVE` 入口处调用 `davCheckLock`，目标路径被他人持锁时返回 **423 Locked**。
- **COPY**：真正实现递归复制（`davCopyNode`），支持目录、拒绝覆盖已存在目标（412）、
  复制物理对象后走 `storage.Retain` 与原子配额。
- **PROPPATCH**：不再假成功。按 RFC 4918 §9.2 返回 **207 Multi-Status**，每个属性标记
  403 Forbidden 并附 `responsedescription`，明确告知客户端本服务不支持写属性。

---

### P1-1 Breadcrumb 越权

**根因**：`Breadcrumb` 无任何归属校验，任意登录用户传别人的目录 ID 即可枚举完整路径树
（配合 ID 递增可批量遍历全站目录结构）。

**修复**：

```go
func Breadcrumb(c *gin.Context) {
    u := middleware.CurrentUser(c)
    ...
    const maxDepth = 128
    for cur != 0 && depth < maxDepth {
        ...
        if !u.IsAdmin && f.OwnerID != u.ID {
            c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "无权访问该路径"})
            return
        }
        ...
    }
}
```

链路上**每个节点**都校验归属（不只是终点），并限制最大深度 128 防止伪造环形父指针造成死循环。

---

### P1-5 配额 TOCTOU

**根因**：`ensureQuota` 是「先 SELECT storage → 在 Go 里比较 → 稍后 UPDATE 累加」。
并发 N 个请求同时通过比较，然后各自累加 → 实际用量可超出额度 N 倍。

**修复**：`internal/service/quota.go` 引入把校验与写入合并为**单条条件 UPDATE** 的实现。

```go
// 核心：把「比较」交给数据库，由 WHERE 条件保证原子性
res := tx.Model(&models.User{}).
    Where("id = ? AND (storage + ?) <= ?", userID, delta, maxStorage).
    UpdateColumn("storage", gorm.Expr("storage + ?", delta))
if res.RowsAffected == 0 {
    return fmt.Errorf("%w: have %d bytes, limit %d bytes (%s), tried to add %d bytes",
        ErrQuotaExceeded, u.Storage, maxStorage, label, delta)
}
```

`RowsAffected == 0` 有两种含义（条件不满足 / 用户不存在），代码通过二次查询区分，
不会把「用户不存在」误报成「超额」。

已接入的写入点（全部改为「先写物理 → 占额度 → 失败回滚物理」的顺序）：

| 文件 | 位置 | 处理 |
|---|---|---|
| `controllers/file.go` | `Rapid` 秒传 | 占额度失败则回滚 File 行 |
| `controllers/file.go` | `Upload` 秒传分支 | 同上 |
| `controllers/file.go` | `Upload` 正常分支 | 失败则删 File 行 + `h.Delete` 物理对象 |
| `controllers/file.go` | `ChunkInit` 秒传 | 同上 |
| `controllers/file.go` | `ChunkMerge` | 失败则删物理对象 |
| `controllers/file.go` | `BatchCopy` | 失败则删记录 + 删物理对象，`continue` 下一个 |
| `controllers/task.go` | aria2 完成任务 | 失败则删记录 + 清理临时文件，任务标失败 |
| `controllers/task.go` | HTTP 回退下载 | 同上 |
| `webdav/webdav.go` | `PUT` 新建 / 覆盖 | 新建失败删记录+物理；覆盖失败删新物理 |

---

### P1-6 分片上传

三个独立问题：

**(a) `meta.Chunks` 在锁外被写**
`chunkUploadsMu` 只保护 map 本身，不保护 `meta` 的内部字段。
多个分片并发上传时同时写 `meta.Chunks[idx]` → 并发 map write，Go 运行时直接 panic。

修复：给 `chunkUploadMeta` 加 `mu sync.Mutex`，`Chunks` / `ChunksBytes` / `LastTouched`
的读写全部持有 `meta.mu`。

**(b) 会话与临时分片永久泄漏**
`chunkUploads` 只增不减，中断的上传（关闭浏览器、网络断）会一直占着 map 条目和
`uploads/chunks/` 下的临时文件。

修复：新增 `LastTouched` 字段 + `chunkGC()` 后台协程，每 30 分钟扫描一次，
回收超过 24 小时（`chunkSessionTTL`）未触碰的会话及其分片文件。

**(c) 采信前端声明的文件大小**
`meta.Size` 完全来自 `ChunkInit` 的请求体。攻击者可声明 1KB 实际传 10GB，绕过配额。

修复：

```go
// ChunkUpload：分片序号必须在 [0, ChunkCount) 内，且单分片不超过 chunkSize + 1MB
if idx < 0 || idx >= meta.ChunkCount { 400 }
if file.Size > maxChunkBytes { 413 }

// 累加真实落盘字节数
meta.ChunksBytes += written

// ChunkMerge：以真实字节数为准，与声明值不符直接拒绝
if meta.Size > 0 && actualBytes != meta.Size {
    400 "size mismatch: declared %d, actual %d"
}
ensureQuota(u.ID, actualBytes)   // 配额预检也用真实值
h.Put(tee, sourceName, actualBytes)  // 写入时也用真实值
```

合并前对分片表做一次持锁快照 `chunksCopy`，避免合并过程中并发上传导致 map 变异。

---

### P1-7 CancelTask 幂等性

**根因**：任务已结束但仍在 `dlJobs` 中时二次调用 `CancelTask` 会
`close(j.cancel)` 已关闭的 channel → panic 打挂 goroutine。

**修复**：

```go
// 原子摘除：只有成功摘除的那一方负责 close
dlMutex.Lock()
j, ok := dlJobs[uint(id)]
if ok {
    delete(dlJobs, uint(id))
}
dlMutex.Unlock()
if ok { ...; close(j.cancel) }
```

同时补上原本缺失的越权校验（`t.OwnerID != u.ID && !u.IsAdmin` → 403）和
「任务不存在」的 404，并把状态更新加上 `AND status = 1` 条件，
避免取消一个已完成的任务把它的 `status` 从「完成」改写成「已取消」。

---

### P2-2 重复扣减配额

**根因**：`Purge` 与 `cleanupTrash` 都是「先 `Delete` 行 → 再 `addStorage(-Size)`」或者反过来的
非原子组合。重复点击 / 并发执行会对同一条记录扣减两次，用户已用容量被扣成负数。

**修复**：先做**条件原子删除**，只有真正删掉行的那一次调用才继续记账。

```go
res := db.Get().Unscoped().Where("id = ?", f.ID).Delete(&models.File{})
if res.RowsAffected == 0 {
    // 已被其他路径删除，直接跳过，不重复扣减
    return
}
// 只有走到这里才释放物理对象 + 扣配额
```

`cleanupTrash` 同样改为 `Where("id = ? AND deleted_at IS NOT NULL")` 条件删除。

补充说明：本项目的软删除**不**释放配额（回收站仍占额度），所以 `Purge` / `cleanupTrash`
各扣一次是正确语义，问题只在于缺少幂等保护。

---

### P2-7 敏感字段明文入库

**根因**：五处敏感字段全部明文存库，其中两处的代码注释还写着"加密"：

| 字段 | 内容 | 原注释 |
|---|---|---|
| `User.TwoFactor` | TOTP 密钥 | `// TOTP secret，加密` ❌ |
| `Policy.Config` | 存储配置 JSON（含 SFTP 密码 / OSS / COS 密钥） | `// JSON 加密` ❌ |
| `OAuthApp.ClientSecret` | OAuth 客户端密钥 | 无 |
| `AccessToken.Token` | OAuth 访问令牌 | 无 |
| `PersonalAccessToken.Token` | 个人访问令牌 | 无 |

DB 一旦泄露（备份被拖、SQL 注入、管理员账号失守），攻击者可直接：
接管任何已开启 2FA 的账号、以明文密码连接用户的 SFTP 存储、冒用 OAuth/PAT 令牌。

#### 关键判断：两类字段必须用不同方案

这是本次改造最需要想清楚的一点 —— 把所有字段一律加密会引入两个真实故障：

| 类别 | 字段 | 方案 | 依据 |
|---|---|---|---|
| **需读回原值** | TwoFactor / ClientSecret / Policy.Config | AES-256-GCM **可逆加密** | TOTP 验证需要 secret；Config 要解析出密码去连接存储 |
| **只需比对** | AccessToken.Token / PAT.Token | SHA-256 **单向哈希** | 令牌创建时展示一次，之后所有场景都只是比对 |

**为什么令牌不能用可逆加密**：

1. AES-GCM 带随机 nonce，每次加密结果都不同 → `WHERE token = ?` 永远查不到
2. 相同明文得到不同密文 → `uniqueIndex` 失效

单向哈希同时解决了这两点，而且**安全性更高**：哈希是确定性的所以索引和查询照常工作，
而数据库泄露也无法直接冒用令牌（明文和可逆加密都做不到这一点）。

#### 为什么不用 GORM hook

原本打算用 `BeforeSave` / `AfterFind` 钩子，但项目里 2FA 的开关代码是：

```go
db.Get().Model(u).Update("two_factor", req.Secret)   // ← 值来自 map，不是 struct 字段
```

**钩子只对 struct 字段生效**，这条路径的值根本不经过 struct，钩子拦不住，
会静默写入明文。改用 `database/sql` 的 `driver.Valuer` / `sql.Scanner` 自定义类型
（`models.Encrypted`），它位于**所有**写入路径的必经之处。

这个选择是踩坑后确定的：先按 hook 写完，grep 时才发现 `auth.go` 有两处 `Update` 用法。

#### 平滑升级：密文带版本前缀

密文形如 `enc:v1:<base64>`。前缀的作用是**区分密文与存量明文**：

```go
func Decrypt(s string) (string, error) {
    if s == "" || !IsEncrypted(s) {
        return s, nil        // 无前缀 → 按明文原样返回
    }
    return conf.DecryptString(strings.TrimPrefix(s, prefix))
}
```

这让"直接上线加密"成为可能 —— 存量数据照常可读（老用户的 2FA 不会失效），
在后续写入时顺带升级为密文，不需要停机做全量迁移。

`models.MigrateSensitiveFields()` 负责存量转换：**分批 500 条、幂等、失败不阻塞启动**，
并在 `main.go` 的 `bootstrap()` 中调用（紧跟 `storage.BackfillFileObjects()`）。

#### 连带必须修的三处

1. **`User.TwoFactor` 字段长度 64 → 255**。TOTP secret 约 32 字符，
   AES-GCM 加密后（+12 字节 nonce +16 字节 tag）base64 约 80 字符，64 装不下。
2. **OAuth 的 `WHERE client_secret = ?` 失效**（库里现在是密文）。
   改为按 `client_id` 查出记录（Scanner 自动解密）后在内存中比对，
   并用 `crypto/subtle.ConstantTimeCompare` —— 顺带消除了时序攻击面。
3. **`testutil.SetupDB` 必须 `conf.Load()`**。密钥未加载时 `EncryptString` 报错，
   而 Valuer 是 fail closed 的（加密失败即让整个写操作失败），
   结果会是所有创建 User / Policy 的测试全部报错。让测试也走真实加解密路径。

#### 测试策略：必须用原始 SQL 断言

新增的测试用 `rawColumn()` 绕过 GORM 的 Scanner 直接读库：

```go
func TestEncryptedRoundTrip(t *testing.T) {
    // ... 写入 TwoFactor
    raw := rawColumn(t, "users", "two_factor", u.ID)
    if !strings.HasPrefix(raw, "enc:v1:") { t.Fatalf("落库应为密文，实际 = %q", raw) }
    if strings.Contains(raw, secret)       { t.Fatal("密文中不应出现明文") }
}
```

**这个辅助函数是整个测试文件存在的原因**：如果只用 GORM 读回再断言，
即使 Valuer 根本没生效（库里就是明文），Scanner 也会"正确"地返回原文，
测试照样全绿 —— 变成假阳性。加密类改动尤其容易栽在这里。

共新增 10 个测试：5 个覆盖加解密（含存量明文兼容、`Update(map)` 路径、幂等），
5 个覆盖迁移（端到端转换、哈希化、幂等、不动新格式数据、状态探针）。

---

### P1-2 标签搜索 SQL 语法错误

`controllers/search.go:102`：

```go
// 修复前 —— 缺少 "="，SQL 直接语法错误，整个标签筛选功能不可用
Where("file_tags.tag IN ? AND file_tags.owner_id ?", tags, u.ID)

// 修复后
Where("file_tags.tag IN ? AND file_tags.owner_id = ?", tags, u.ID)
```

---

### P1-3 搜索历史清理在 MySQL 报 1093

`controllers/search.go` 的 `SaveSearchHistory`：

```sql
-- 修复前：MySQL 不允许在 DELETE 的子查询里引用被删除的同一张表 → 错误 1093
DELETE FROM search_histories
WHERE user_id = ? AND id NOT IN (
  SELECT id FROM search_histories WHERE user_id = ? ORDER BY id DESC LIMIT 20
)
```

这个 bug 有个**隐蔽之处**：SQLite 允许这种写法，所以本地开发完全不会暴露；
一旦部署到 MySQL/生产环境，每次保存搜索历史都报错。

修复方式改为「先定位第 20 条，再删比它更早的」——无子查询，SQLite / MySQL / PostgreSQL 通用：

```go
var keep models.SearchHistory
if err := db.Get().Where("user_id = ?", u.ID).
    Order("id desc").Offset(19).Limit(1).Find(&keep).Error; err == nil && keep.ID > 0 {
    db.Get().Where("user_id = ? AND id < ?", u.ID, keep.ID).Delete(&models.SearchHistory{})
}
```

注意用 `Find` 而非 `First`：GORM 的 `First` 会自动追加 `ORDER BY id`，与显式的 `Order("id desc")` 语义冲突。
`keep.ID > 0` 的判断保证记录不足 20 条时不做任何删除（用 `NOT IN` 空列表会误删全部）。

---

### P1-4 多步写入全程零事务

**根因**：审查时全项目 `grep -r "\.Transaction(" --include=*.go .` **零命中**。
所有「建文件记录 + 记配额」「归档版本 + 更新主记录」都是若干次独立写入，
任一步失败就留下无法自动修复的中间态。

**受影响位置（13 处，已全部改造）**：

| 位置 | 事务包裹的内容 |
|---|---|
| `install.go` `Install` | 默认组 ×2 + 管理员 + 存储策略 + 默认套餐 |
| `file.go` `Rapid` | File 记录 + 配额占用 |
| `file.go` `Upload` 秒传分支 | File 记录 + 配额占用 |
| `file.go` `Upload` 正常分支 | File 记录 + 配额占用 |
| `file.go` `ChunkInit` | File 记录 + 配额占用 |
| `file.go` `ChunkMerge` | File 记录 + 配额占用 |
| `file.go` `UploadVersion` | 版本归档 + File 主记录更新 + 配额差值 |
| `file.go` `RestoreVersion` | 版本归档 + File 切换到目标版本 + 配额 + 删除已恢复版本 |
| `file.go` `BatchCopy` | 每条副本的 File 记录 + 配额 |
| `collab.go` `CollabSave` | 版本归档 + File 更新 + 配额差值 |
| `task.go` aria2 下载完成 | File 记录 + 配额 |
| `task.go` HTTP 回退下载完成 | File 记录 + 配额 |
| `webdav.go` `put` 新建 / `put` 覆盖 / `davCopyNode` | File 记录 + 配额（覆盖时是增量 delta） |

#### 事务边界的三条原则

改造中最容易犯错的是「什么该放进事务」。这里遵循三条：

**1. 物理对象写入不进事务。**
`h.Put(...)` 写的是磁盘/对象存储，数据库回滚管不到它。正确顺序是
「先写物理 → 事务内写 DB/配额 → 事务失败则手动删物理对象」：

```go
if err := h.Put(src, sourceName, size); err != nil { /* 失败即返回，无副作用 */ }

if err := db.Get().Transaction(func(tx *gorm.DB) error {
    if err := tx.Create(&f).Error; err != nil { return err }
    return service.AddStorageTx(tx, u.ID, f.Size)
}); err != nil {
    _ = h.Delete(sourceName)   // 补偿：清理已落盘但无人引用的物理对象
    writeQuotaOrServerError(c, err)
    return
}
```

**2. DDL 不进事务。**
`models.AutoMigrate()` 必须留在事务外 —— MySQL 的 `CREATE/ALTER TABLE` 会**隐式提交**
当前事务，把它包进 `db.Transaction` 只会得到一个「看起来有事务、实际没有」的假象。
`Install` 里这一条尤其重要，因为它是整个安装流程的第一步。

**3. 文件写入不进事务。**
`conf.Save(cfg)` 写的是磁盘配置文件，放在事务提交之后。

#### Install 的额外处理：幂等化

光有事务还不够。`Install` 的失败点不止在事务内 —— 事务提交后还有 `conf.Save`。
如果这一步失败，数据库已经写好了但 `installed=false`，用户重试时会撞上：

- `createAdmin` 撞用户名唯一索引 → **永久卡死，只能手工删库**

两个配套修复：

```go
// ① 同名用户已存在时更新，而不是报错。
//    能走到这里说明 conf.IsInstalled() 仍为 false（否则被 InstalledBlock 拦下），
//    即系统处于「上次安装中断」状态，此时更新才是符合安装意图的行为。
err = tx.Where("user_name = ?", a.UserName).First(&existing).Error
if err == nil {
    return tx.Model(&models.User{}).Where("id = ?", existing.ID).Updates(...).Error
}
```

```go
// ② 默认组逐个确保，而不是「组1在就整体跳过」。
//    原实现：if 组1 不存在 { 建组1; 建组2 } —— 若建组1成功、建组2失败，
//    重试时组1已存在 → 整个 if 跳过 → 组2 永远缺失。
if err := ensureGroup(tx, 1, "default", 10<<30, true, true); err != nil { return err }
return ensureGroup(tx, 2, "admin", -1, true, true)
```

配合这两点，`Install` 现在可以在任意中断点安全重试。

#### 顺带清掉的隐患

- `reserveStorage` 包装函数已无调用者，删除；所有记账统一走 `service.AddStorageTx(tx, ...)`
- 新增两个统一的错误响应helper，把「配额不足」与「服务端故障」区分开：
  - HTTP：`writeQuotaOrServerError` → 配额 400 / 其他 500
  - WebDAV：`writeDAVQuotaOrServerError` → 配额 **507 Insufficient Storage**（RFC 4918）/ 其他 500

  这样运维看日志能立刻分辨是「用户超额度」还是「数据库出问题」，而不是清一色 500。

---

## 三、新增文件

| 文件 | 作用 |
|---|---|
| `backend/internal/service/outbound.go` | SSRF 防御、受控出站客户端、上传白名单/魔术字节校验、设置读取注入 |
| `backend/internal/service/quota.go` | 原子配额占用（`AddStorageTx` / `ReserveQuota` / `CheckQuota`） |
| `backend/internal/cryptox/cryptox.go` | 敏感字段加解密（带 `enc:v1:` 前缀以区分存量明文）+ 令牌单向哈希 |
| `backend/models/encrypted.go` | `Encrypted` 类型：`driver.Valuer` / `sql.Scanner` 自动加解密 |
| `backend/models/migrate_sensitive.go` | 存量敏感字段的幂等批量转换 |

**新增包 `internal/service` 的设计意图**：把「HTTP 上传路径」和「WebDAV 路径」共用的
安全逻辑（白名单、魔术字节、配额、出站校验）收敛到单一实现，从根上消除
「两套路径两套规则」这一本次审计中所有 P0 问题的共同成因。

为避免 `service` 反向依赖 `controllers`，设置读取与配额解析都通过**注入**接入：

```go
service.SetSettingGetter(func(key string) (string, bool) { ... })
service.SetQuotaProvider(func(tx *gorm.DB, userID uint) (int64, string) { ... })
```

未注入时 `quota.go` 有 `defaultQuota` 兜底（直接按用户组额度算），
所以 service 包可以独立编译与测试。

---

## 四、验证结果

环境：Go 1.25.5 windows/amd64（全局安装于 `C:\Program Files\Go`）

```bash
cd backend
export GOPROXY=off GOTOOLCHAIN=local GOFLAGS=-mod=mod

go build -p 8 ./...   # EXIT=0（首次 1m47s，增量 8.4s）
go vet ./...          # EXIT=0（零问题）
go test ./...         # EXIT=0（全部通过）
```

测试结果：

```
ok      .../conf          ok      .../models
ok      .../controllers   ok      .../pkg/db
ok      .../filesystem    ok      .../pkg/jwt
ok      .../middleware    ok      .../pkg/plugin
ok      .../pkg/tls       ok      .../pkg/util
ok      .../routers       ok      .../service
```

> **编译速度提示**：`GOPROXY` 必须设成可达的镜像（如 `https://goproxy.cn,direct`），
> 或在依赖已下载后设为 `off`。默认的 `proxy.golang.org` 在部分网络下会长时间挂起，
> 表现为「编译几十分钟没反应」——实际是卡在模块下载，不是在编译。
> 可用 `go build -x` 确认，或观察 `go env GOCACHE` 目录大小是否增长来区分。

---

## 五、顺带修掉的测试基础设施缺陷

这一节的问题不在原 `CODE_REVIEW.md` 清单里，是在「让测试真正跑起来」的过程中暴露的。

### 5.1 测试驱动与生产驱动不一致（严重）

| | 驱动 | 是否需要 CGO |
|---|---|---|
| 生产代码 `pkg/db/db.go` | `github.com/glebarez/sqlite`（纯 Go） | 否 |
| 测试 `pkg/testutil/db.go` | `gorm.io/driver/sqlite` | **是** |

两个后果：

1. **测试在无 C 编译器的环境完全跑不起来。** Windows 上 `CGO_ENABLED` 默认为 0，
   整个测试套件报 `go-sqlite3 requires cgo to work` 直接失败。精简 CI 容器同理。
2. **测试跑的引擎和线上不是同一个。** 这是更要命的一点：`glebarez/sqlite` 底层是
   `modernc.org/sqlite`（纯 Go 移植），与 `mattn/go-sqlite3` 虽都兼容 SQLite 语法，
   但版本和边界行为存在差异。**测试通过不代表线上通过。**

第 2 点正是本项目 P1-3 能潜伏到生产的机制：那条 `DELETE ... WHERE id NOT IN (SELECT ... FROM 同一张表)`
在 SQLite 上是合法的，本地测试全绿；一到 MySQL 就报 ER_1093。
如果测试驱动与生产一致，这类差异至少能在更早的阶段被感知。

**修复**：`pkg/testutil/db.go` 改用 `github.com/glebarez/sqlite`，与生产对齐。

### 5.2 测试连接从不关闭，Windows 上导致清理失败

`SetupDB` 打开的 SQLite 连接没有关闭逻辑。测试结束 `t.TempDir()` 清理时，
Windows 拒绝删除仍被句柄占用的文件：

```
TempDir RemoveAll cleanup: unlinkat ...\test.db: The process cannot access
the file because it is being used by another process.
```

报错位置在 `testing.go:1369` 的 cleanup 阶段 —— 也就是说**断言全部通过，测试却被判 FAIL**。
这个问题一直被 5.1 掩盖着：驱动加载失败时测试在打开数据库之前就挂了，根本走不到清理。

**修复**：`SetupDB` 与 `TestInitSQLite` 注册 `t.Cleanup` 关闭底层 `*sql.DB`。

> **实现要点（容易踩坑）**：`t.Cleanup` 是 **LIFO** 执行。必须在 `t.TempDir()`
> **之后**注册关闭逻辑，这样「关连接」才会先于「删临时目录」执行。
> 顺序写反的话，现象与没修完全一样。

### 5.3 `db.Init` 覆盖全局实例时不关闭旧连接池

```go
// 修复前：旧连接池直接泄漏（既不 Close 也不释放）
DB = g
```

安装向导的「测试连接」按钮每次点击都会调用 `TestDB` → `db.Init`，换一个 DSN 试一次，
每次泄漏一个连接池。

**修复**：覆盖前先 `DB.DB().Close()`，并新增导出的 `db.Close()` 供测试清理与进程优雅退出使用。

---

## 6. 追加加固（S-1 ~ S-3）

上一轮 15 项修完后做了一轮针对性复查，又找出 3 处。共同特征：
**都属于「机制已存在但某个入口漏了接上」**，而不是缺功能。

### 6.1 S-1 改密码不吊销会话（最严重）

`ChangePassword` 改完密码就 `return`，**不碰 `sessions` 表**。

而会话有效性判定在 `middleware/auth.go:40`：

```go
db.Get().Where("session_id = ? AND expires_at > ?", claims.SessionID, time.Now()).First(&sess)
```

只查 `session_id` + 过期时间，**与密码完全解耦**。后果：

> 用户发现账号被入侵 → 改密码 → 攻击者手上的 token 在「记住登录」的 **7 天里照常可用**。

「我改密码了」这个补救动作对已入侵场景**完全无效**。项目的单设备登录机制
（登录时 `Delete(user_id)` 清旧会话）做得不错，但改密码这条路没接上。

**修复**：新增 `revokeAllSessions()`，删掉该用户全部会话 + 为当前浏览器签发新
token 并重设 cookie。**全删而不是只删其他会话**——保留任何旧 token 都等于留后门。
当前浏览器通过新 token 无感续上，前端无需改动（`ProfileSettings.tsx` 改完只提示 OK）。

配套动作：
- 写 `AuditLog`（`action: change_password`）——这类操作必须可追溯
- 吊销失败时返回 500 而非 200。不能假装成功——密码已改但旧 token 仍有效，
  报个错比给个假成功安全

**测试有效性做了变异验证**：把 `Delete` 那行注释掉后重跑，
`TestChangePasswordRevokesAllSessions` 报「实际 4 个 —— 旧会话未吊销」并 FAIL。
若只断言「密码改了」而不查 session，删掉整个吊销逻辑测试照样通过 —— 漏洞仍在。

另加两个反向测试：原密码错误、新密码不达标时**都不得吊销会话**
（否则可被用来强制把受害者踢下线）。

### 6.2 S-2 `CreateUser` 缺密码强度校验

密码策略 `validatePassword()` 有两个调用点（注册、改密码），但**第三个入口漏了**：

| 入口 | 修复前 | 修复后 |
|---|---|---|
| `auth.go Register` | ✅ 校验 | ✅ |
| `auth.go ChangePassword` | ✅ 校验 | ✅ |
| `admin.go CreateUser` | ❌ 只有 `binding:"required"`（非空） | ✅ 校验 |
| `install.go` 初始管理员 | ✅（安装期，走默认值） | ✅ |

意味着管理端可以创建 `123` 这种弱密码账号，绕过系统自身策略。

**修复**：在 `bcrypt.GenerateFromPassword` 之前插入 `validatePassword`。

这个漏洞的**副作用帮了忙**：`handlers_test.go` 的 `TestCreateUser` 原本用
`"Secret1"`（7 位）当密码，修复后立刻被拒报 400。这正是修复生效的直接证据 ——
改完测试数据为 `Secret123` 后恢复。全仓其他测试密码（`Abcdefg1`）本来就合规。

### 6.3 S-3 JWT `Parse` 未显式限定签名算法

`keyfunc` 只 `return secret`，没检查 `t.Method`：

```go
// 修复后
if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
    return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
}
```

**当前并无实际漏洞** —— `golang-jwt/v5` 对非对称算法会因密钥类型不匹配
（`[]byte` vs 公钥）而拒绝。但这属于「依赖库的隐式行为做安全保证」，
显式白名单是不换库版本、不重构就不失守的第二道防线。补了两个伪造 token 测试
（`alg=none` 与 `alg=RS256`）锁住行为。

### 6.4 顺带确认没问题的地方

复查时也排除了几个**看起来可疑但实际正确**的点，避免误改：

- **IP 封禁是否共享**：`IsIPBanned` 查的是 `models.IPBan` **数据库表**，多副本共享 ✓。
  内存的只有 `loginFailures` 触发计数（凑满 10 次才写 DB）。
  README 的「部署形态」小节已按这个准确语义写。
- **路由鉴权**：`admin := api.Group("/admin", middleware.Auth(true), middleware.AdminOnly())` ✓
- **`UpdateUser` 能否改密码**：结构体里**没有**密码字段 ✓
- **自助删除保护**：`DeleteUser:124` 有 `if cur != nil && uint(id) == cur.ID` ✓

---

## 7. 试用包（packaging/）

给非开发者体验用的一套东西。三平台各一个自包含压缩包，对方解压即跑，
**不需要装 Go / Node / 数据库**。

### 为什么能做到单二进制
`backend/web.go` 用 `//go:embed all:frontend_dist` 把前端打进了 exe。
构建加 `-trimpath -ldflags="-s -w"`：剥离符号表与调试信息，
65 MB → 47 MB（**-27%**），前端资源静态校验确认已内嵌、无 `.tsx` 源码泄漏。

### 端口冲突处理：逻辑放在 Go 里而不是 bat/sh
最初想用 `netstat | find` 在脚本里探测空闲端口。两个问题：

1. `find ":5212 "` 是**子串匹配**，会把 `52120`/`52121` 误判成 5212 被占用。
   虽然当前 netstat 列宽对齐时不会撞上，但跨机器/跨 locale 不保证。
2. batch/shell 的语法无法在本机可靠验证（跨 shell 调用 cmd 被安全策略拦截）。

改为在二进制里加 `-autoport`，用 `net.Listen` **实际尝试绑定**确定端口，
`start.sh` 只剩一行 `exec ./nebula -autoport -open`。
新增 `backend/main_test.go` 覆盖 `findFreePort` 的跳号与边界。

`-autoport` 优先级高于配置文件里的 `listen`——试用场景的核心诉求是
「一定能起来」，而端口冲突是启动失败最常见的原因。

### 实测发现并修掉的问题
1. **启动横幅 URL 是大写 `HTTP://`** —— Windows 上部分程序无法识别。
   实测截图确认后加 `strings.ToLower`。这是 `printStartupBanner` 加进去后
   第一次实际运行才暴露的。
2. **`start.sh` 会把 `Exec format error` 漏给用户** —— 下错版本时
   （拿了 Windows 包）文件存在但跑不了，用户完全看不懂这个报错。
   改为**读文件头魔数**判断：ELF(7f454c46) / Mach-O(cffaedfe) / PE(4d5a)，
   并检查 ELF 位数与 `getconf LONG_BIT` 是否一致。
   注意：**靠退出码判断不可靠** —— 实测 Git Bash 会把执行失败转成退出码 0。
3. 试用说明里的功能声明**逐条 grep 核实**（i18n 实为 5 种含韩语、
   版本上限 `file.max_versions=10`、回收站 `trash.retention_days=30`
   且每 6 小时扫一次）。不核实的功能说明等于给朋友的假承诺。

### 验证边界（必须说清楚）
- **Windows**：全新目录实测启动成功、端口自动选择、HTTP 200、前端页面正常、
  安装守卫返回 503（正确引导到安装向导）
- **Linux / macOS**：仅完成交叉编译 + ELF 头校验 + `sh -n` 语法检查 +
  错误路径实测。**本机无 Linux 环境（WSL 空、无 Docker），
  未做真机运行验证**

`packaging/试用说明.md` 覆盖安装向导步骤、7 类可试功能、6 个常见问题、
以及能力边界（单实例设计、WebDAV PROPPATCH 限制、LOCK 进程内）。

---

## 8. 遗留项与本轮补充

均为**非缺陷**的改进；其中两项已在本轮补上，一项按需处理。

### 已补强

- **`UpdateUser` 自操作保护**（原 #1）—— 管理员不能把自己 `is_admin` 置 false，也不能把
  `status` 改成非 0（即 `auth.go` 的"已封禁"），否则一次误操作就永久失去后台访问权限。
  与 `DeleteUser` 的"不能删除自己"形成完整闭环。`controllers/admin_selflock_test.go`
  用 4 个用例覆盖（自降级拦截 / 自封禁拦截 / 自改中性字段放行 / 降级他人放行），并做了
  **变异验证**：临时去掉拦截后两个用例即 FAIL，证明守卫真实生效。
- **前端 CI**（原 #3）—— 新增 `.github/workflows/frontend-ci.yml`：node 20 + `npm ci` +
  `npm run build`（`tsc -b` 严格类型检查 + `vite` 生产构建）。额外加 **`frontend_dist` 同步守卫**：
  防止改了前端源码却没重建并提交 `backend/frontend_dist`，导致二进制内嵌的是过期页面。
  已交叉验证 node 22 / 24 构建产物哈希完全一致（esbuild 版本锁定，与 node 版本无关），
  故 CI 的 node 20 不会出现工具链漂移误报。

### 仍待处理（按需）

- **5 项进程内存状态** —— WebDAV 锁、登录失败计数、验证码阈值、通用限流、分片会话。
  多副本部署需外置到 Redis，README「部署形态」已列明。

---

## 9. 前端代码分割（性能优化）

`frontend/src/App.tsx` 原本把所有页面（含后台 8 个页面 + echarts）静态打进单一 **3.15 MB chunk**
（gzip 981 KB）。改为路由级 `React.lazy` + 顶层 `<Suspense>`：

- 所有页面组件改为 `lazy(() => import('...'))`，`<Routes>` 外包一层
  `<Suspense fallback={<FullScreenLoading/>}>`（页面均为 default 导出，可直接 lazy）。
- 首屏主 chunk 从 3.15 MB 降到 **291 KB（gzip 98 KB）**；echarts（Dashboard 1.15 MB）、
  播放器/文档预览（Files 1.53 MB）等重依赖改为**按需加载、可独立缓存**，不再阻塞首屏。
- `tsc -b` 类型检查 + `vite build` 均通过；`go:embed all:frontend_dist` 已随新 chunk 重新编译通过，
  后端 `go build` 验证无误（前端_dist 由 3 文件增至 49 文件，旧单 chunk 已删）。

**未做的进一步优化**（按需）：Files 页仍 1.53 MB，因其内联了 plyr / hls.js / mammoth / xlsx /
jszip 等预览依赖。若想继续压，可在 Files 内对这些预览/播放组件再做二级 `lazy` 拆分。

---

## 10. 点阵加载动画接入（前端体验）

用户提供桌面 `JS.txt`（一个自包含 IIFE Web Component `<dot-motion-loader>`，canvas 点阵动画），
选择接入为全局加载动画。落地方式：

- 来源文件保留在桌面不动；项目内副本放到 `frontend/public/dot-motion-loader.js`。Vite 的 `public/`
  会原样拷到产物根目录，**dev 与内嵌二进制（Go `go:embed`）下均能用**，无需改 `web.go`。
- `frontend/index.html` 的 `<head>` 内用 `<script defer src="/dot-motion-loader.js"></script>`
  注册：`customElements.define` 在脚本执行时自动注册，保证 React 渲染前已可用；`defer` 不阻塞 HTML 解析。
- `frontend/src/App.tsx` 的 `FullScreenLoading()` 把原 `Loader2` 旋转圈换成
  `<dot-motion-loader style={{ width: '100px' }} />`。组件 `:host` 默认 48px、`canvas{width:100%}`，
  用内联 width 放大到 100px 居中；canvas 透明（`clearRect` 而非填背景），只绘制点阵——
  暗色底点 + `primary` 紫色辉光活动点，在暗色主题上自然融合，无黑块。
- 新增 `frontend/src/types/dot-motion-loader.d.ts`，给该自定义元素补齐 JSX 类型，避免
  `tsc -b` 报 `Property 'dot-motion-loader' does not exist on type JSX.IntrinsicElements`。
  （lucide 的 `Loader2` 仅 `FullScreenLoading` 使用，已从 import 移除；`X`/`Info` 仍被使用。）

验证：`npm run build`（`tsc -b` 严格类型检查 + `vite` 生产构建）通过；`dot-motion-loader.js`
已正确进入 `backend/frontend_dist` 根目录；`go build ./...`（含 `go:embed all:frontend_dist`）
通过。`frontend_dist` 同步守卫（CI）继续有效：源码与内嵌产物一致。

### 修正：底点透明（同节补充）

初版用了桌面 `JS.txt`，其点阵 `background` 为 `[0,0,0,1]`（不透明黑底点），在暗色加载区会显出
黑点网格。用户反馈后改用 `Downloads/loader-1.js`（`background:[0,0,0,0]`，alpha=0）：底点不再绘制，
仅保留 `primary` 紫色辉光活动点，暗色主题下干净无黑点。两文件为同一工具导出，组件名/`:host`/绘制
逻辑完全一致，故 `index.html` 注册与 `App.tsx` 用法无需改动——仅替换 `public/dot-motion-loader.js`
并重 build 即可。提交 `28252b7`。

---

## 11. 全栈集成测试 + Windows 存储路径修复（提交 `ce1ad77`）

### 背景
`controllers/*_test.go` 全部用 `newTestRouter()`：把单个 handler 挂到空 `gin.New()`、手动
`c.Set(CtxUserKey, u)` 注入用户。这绕过了完整的认证 / 安装守卫 / 权限中间件链——而 CODE_REVIEW
里修的 P0 恰好**全部**发生在这些中间件与 WebDAV 层。也就是说：修复本身没有测试保护。

### 新增 `backend/routers/integration_test.go`（package routers_test）
启动 `routers.Setup()` 完整引擎（Recovery→SecurityHeaders→Gzip→CORS→IPGuard→InstallGuard→
StaticCache + Auth + WebDAV + 全部 REST 路由），用 `httptest` 真实发 HTTP 请求，走生产完全一致的路径。

打通完整引擎有三个必备前提（都曾卡住）：
1. `conf.Save()` 是唯一能把 `installed` 翻 true 的公开途径（`markInstalled` 私有），
   否则 `InstallGuard` 把所有 `/api` 业务路由挡成 503；
2. 必须 seed Group(1 default/开 WebDAV、2 admin/-1) + Policy(1 local) + 品牌与上传安全 settings，
   并注入 `service.SetSettingGetter` / `SetQuotaProvider` + `jwt.Init()` + `SeedDefaultPlans()`；
3. **两套认证路径不同**——REST 走 cookie `nebula_token` / `Bearer` → JWT → 校验 `sessions` 表；
   WebDAV 走 **HTTP Basic**（`webdav.basicAuth`）查库 + bcrypt 比对 + `Group.WebDAVEnabled`，
   与 session/JWT 无关。测试需分别构造。

覆盖 5 组已修 P0：WebDAV 路径穿越、WebDAV PUT 覆盖、SSRF 拒绝、权限矩阵、文件生命周期
（上传→复制→软删→purge，全程校验 DB + 物理文件 + 配额三者一致）。

### 顺带修掉的真实数据错位缺陷
`FileLifecycle` 起初挂在"物理文件丢失"上。排查发现不是测试写法问题，而是**生产缺陷**：
存储策略 config 由字符串拼接生成 `{"path":"C:\Users\...\003"}`，反斜杠在 JSON 中不是合法转义，
`json.Unmarshal` 直接失败；而代码写的是 `_ = json.Unmarshal(...)` **把错误吞了**，
`cfg.Path` 为空后静默回落到**相对目录** `uploads`——相对路径按**进程 CWD** 解析。
于是 Windows 上所有上传都落到与配置声明无关的位置，文件错乱且极易丢失，**全程不报错**。

- `models.LocalPolicyConfig(path)`：改用 `json.Marshal` 正确转义，替换全部 3 处拼接
  （`main.ensureDefaults` / `install.ensureDefaultPolicy` / 测试）；
- `filesystem.New`：不再吞掉 Unmarshal 错误——解析失败时优先回退 `conf.UploadPath`
  （绝对路径）并打日志，仍拿不到才显式报错，**绝不静默落到相对目录**；
- `filesystem` 原有测试自己也用了同样错误的拼接写法，被新 fail-closed 逻辑拦下，一并修正；
- 新增 `TestLocalConfigJSONEscaping`，双向锁死"正确转义可写入目标目录"与"畸形 config 不得变相对目录"。

验证：`go build ./...` 通过，`go test ./...` 全绿（19 包）。`-race` 本机不可跑
（`CGO_ENABLED=0` 且无 gcc），CI 已配 `CGO_ENABLED=1` + gcc 前置检查，新测试自动纳入。

### 两个测试侧踩坑（非被测代码 bug）
- **PROPFIND 的 `Depth`**：不传时 `propfind()` 默认 `depth="0"`（符合 RFC 4918 §9.1，只返回资源
  自身、不返回子项）。断言"应列出子文件"必须显式传 `Depth: 1`。
- **gofmt**：仓库既有文件是 CRLF，`gofmt -l` 会把几乎所有文件都列出来（噪声）。
  不可全量 `gofmt -w`（会生成覆盖全仓库的无关 diff），只对新建文件执行。

---

## 12. 打包三平台 V26-10.0-b

### 版本号规则（本次确立）
`V<两位年号>-<月>.<版本>` + 可选后缀：测试版 `-b` / 热修复 `-PR` / 特殊 `-S`。
本次为 2026 年 10 月，改动含新增功能与修复但未发正式版，故取 `V26-10.0-b`。
（历史 tag 为另一套 `V1-0.0.x-beta` 命名，两套并存，本次按新规则。）

### 产物（`release/`，已被 `.gitignore` 忽略）
`NebulaDrive-V26-10.0-b-{windows,linux,darwin}-amd64`，各含目录 + zip
（15.36 / 15.11 / 15.38 MB）。构建：`CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"`，
在 Windows 上直接交叉编译，未走 `packaging/build-release.bat`——它版本取自 `git describe --tags`，
无新 tag 时会退化成 `dev`（上次打包就因此产出了 `-dev` 命名）。

### 打包要点
- **前端同步**：`go:embed` 直接嵌 `backend/frontend_dist`，不同步即打出旧页面。打包前已确认
  `git status -- frontend/ backend/frontend_dist/` 为空（CI `frontend-ci.yml` 有同步守卫）；
- **行尾按平台分别转**（最易踩）：`*.sh` 必须 **LF**（CRLF 会让 Linux 报
  `bad interpreter: /bin/sh^M`，包直接不可用）；`*.bat` 应 **CRLF**。`packaging/` 源脚本本身
  是 CRLF，copy 出来需逐平台转换，已用 `cat -A` 确认 shebang 无 `^M`；
- **zip 权限位**：用 Python `zipfile` 写 `external_attr`，给 `nebula` 与 `*.sh` 置 0755，
  否则 Linux/macOS 用户解压后仍需手动 `chmod +x`。

### 验证（不只看"编译通过"）
- 文件头交叉确认平台：`4d5a`=PE(Windows) / `7f454c46`=ELF(Linux) / `cffaedfe`=Mach-O(macOS)；
- **实跑 Windows 产物**：`/api/health` → `{"installed":false,"ok":true}`；`/` 返回完整 HTML；
  `/dot-motion-loader.js` → 200 / 3508 bytes（证明 `go:embed` 前端确实可用）；
- zip 逐项核对：CRC 全 OK、权限位正确、行尾符合平台预期；
- 清理冒烟测试残留（`data/`、日志），交付目录只含 5 个正式文件。

另：旧的 `NebulaDrive-dev-*` 产物（10-02 打包，**不含**本轮 Windows 路径修复）已删除，避免误用。

---

## 13. 许可证残留清理（MIT → AGPL-3.0）

### 起因
用户在关于页截图里发现「本项目采用 MIT License 开源发布」，指出仓库实际是 AGPL。
核对后确认用户记忆正确：根 `LICENSE` 确实是 **GNU AGPL-3.0**（661 行，16 处 AGPL 关键字），
`README.md` 的 badge 与协议章节也早已改成 AGPL —— 但**前端与打包配置没跟上**。

### 排查方法
全项目 `grep -rniI "MIT"`，逐条甄别而非无脑替换。命中的三类：
- **误报（须保留）**：`package-lock.json` 里几百条 `"license": "MIT"` 是第三方依赖的正常声明；
  `go.mod`/`go.sum` 的 `smithy`/`mapstructure`；`tsconfig.json` 的 `noEmit`；`App.tsx` 的 konami 码。
- **真残留（须修）**：见下表。
- **历史记录（须保留）**：`CODE_REVIEW.md` 三处提到 MIT，是那份报告在记录「原问题」，
  改了反而丢失决策痕迹。

### 实际修正
| 位置 | 修正前 | 修正后 |
|---|---|---|
| `frontend/src/pages/admin/About.tsx` | MIT License + 品牌保留 3 处 | GNU AGPL-3.0 + 网络服务源码开放义务 |
| `frontend/src/pages/admin/Plugins.tsx` | MIT License + 品牌保留 3 处 | 同上 |
| `backend/nfpm.yaml` | `license: MIT` | `license: AGPL-3.0`（SPDX 标识） |
| `backend/nfpm.yaml` | `version: 1.0.0` | `version: 26.10.0` + `release: b` |
| `backend/nfpm.yaml` | homepage 指向 `nebula-drive/nebula` | `mrc-sk/nebula-drive`（与 README 一致） |
| `trailer/tools/render_opening.py` | 片头字卡「MIT 开源」 | 「AGPL-3.0 开源」 |
| `trailer/RELEASE_COPY.md` | 「LICENSE 仍写着 MIT」的待确认告警 | 删除（问题早已解决，属过期告警） |

### 一个容易漏的点：连带死代码
`Plugins.tsx` 原本只在**那两段 MIT 文案**里用到 `brand`（`brand.name` / `brand.author`）。
文案改写后 `brand` 变成完全未使用 —— 虽然 `tsconfig` 的 `noUnusedLocals: false` 不会报错，
但留着就是自己引入的死代码。连带清掉了 `useState` 声明、配套的 `useEffect`，
以及**只被这个 effect 使用的 `getBrand` import**（留着会触发 lint 告警）。
`About.tsx` 的 `brand` 页脚仍在用，不动。

### 关于「品牌保留 3 处」这条附加条款
它原本写在 MIT 之上、要求运行时展示署名。`CODE_REVIEW.md` §5.1 早已判定该条款与 MIT 第 1 条
冲突、解释权不明，并在改用 AGPL-3.0 时**整体废弃**。但前端两处合规声明没跟着删，
等于把一个已经不存在的许可证义务展示给用户 —— 这比单纯的「MIT 拼错」更严重：
用户会以为不保留 3 处品牌就违反协议。现已改写为 AGPL 真正的核心义务
（**网络服务同样触发源码开放**，AGPL 与 GPL 的唯一区别）。

### 验证
- `npx tsc -b` → exit 0（strict 模式）；
- `npm run build` → 15.44s，`About-BKMbMF-o.js` / `Plugins-CfkcX9zP.js` 中 `grep MIT` 为空、
  `grep AGPL-3.0` 命中，确认改动真的进了 bundle；
- `go build ./...` 通过；`go test ./... -count=1` **全绿**（controllers 68.6s、routers 7.8s、
  middleware 10.5s、models 9.5s，其余均 ok）；
- 全项目复扫 `MIT`：自有文件中仅剩 `CODE_REVIEW.md` 的 3 处历史记录（预期保留）。

### 遗留
`V26-10.0-b` 三平台产物打包于本次修正**之前**，二进制里 `go:embed` 的还是写着 MIT 的旧前端。
**重新打包后才能对外分发**，已在 CHANGELOG「已知遗留」标注。

---

## 14. 侧边栏三个入口点击无反应（+ 连带修掉六项既有缺陷）

**日期**：2026-10-03

### 根因：路由从未注册，被 catch-all 静默吞掉
`App.tsx` 里 `/share-list`、`/tasks`、`/trash` 三条路径**一条都没有**。
React Router v6 匹配不到时会落到文件末尾的
`<Route path="*" element={<Navigate to="/" replace />} />`，
被**静默重定向回首页** —— 不抛错、不白屏、URL 变了但内容没变，
所以用户看到的现象就是「点了没反应」。

这类 bug 难定位的原因是它**不留错误痕迹**：控制台干净、Network 正常、
只有点一下才知道没跳。移动端底部 tabbar 的第二个入口指向的也是 `/share-list`，
所以桌面侧栏和移动底栏同时是死的。

修法本身不难，难的是知道该修什么 —— 一开始差点去查后端接口，
实际上后端 `ListShares` / `ListTasks` / `GET /api/files?trash=1` 都好好地在那儿。

### 顺带发现的六个既有缺陷
排查三入口时把相关代码路径都读了一遍，发现的都是**已经上线、只是没人用到那个入口所以没暴露**：

| # | 位置 | 症状 | 根因 |
|---|---|---|---|
| 1 | `api/client.ts` + `admin/Users.tsx` | 管理员用户列表恒为空 | 类型声明成 `items`，后端返回 `list` |
| 2 | `components/NotificationBell.tsx` | 通知列表取不到 | 同上（且原写法 `(r.data as any).items \|\| r.data` 掩盖了类型错误） |
| 3 | `layouts/MainLayout.tsx` | 侧栏「关于」任何语言都显示不出 | `labelKey: '关于'` 写成中文字面量，渲染时查 `ns_admin.关于`；且该 key 压根不存在 |
| 4 | `pages/admin/Tasks.tsx` | 进行中任务显示成 `task.1` | 后端 `Status` 是 **int**，前端 `(tk.status \|\| tk.state \|\| 'pending') as TaskStatus` 硬转字符串枚举 |
| 5 | 后端 `routers/router.go` | 任务「重试」按钮打 404 | 前端调 `POST /api/tasks/:id/retry`，后端从未注册 |
| 6 | `pages/admin/Tasks.tsx` | 轮询空转 | 无活动任务时仍持续轮询；且无卸载守卫 |

**缺陷 4 值得单说**：`status=0` 恰好是 falsy，会落到 `'pending'`，
所以等待中的任务看起来完全正常 —— 是巧合掩盖了类型错误。
一旦 `status=1` 就直接渲染成字面量 `task.1`，徽章配色也回落到灰色 pending。
如果只测「等待中」和「已完成」两种状态，这个 bug 能一直藏着。
现已补显式的 int→枚举映射（`toStatus`），两个任务页共用一份。

### 重构：消除重复
- **抽出 `GuardedPage`**：路由守卫的三段式（`installed === null` / `!installed` / `authBusy` / `!user`）
  原本在 8 处手写了一遍。加第 9 条路由时不想再抄第 9 遍。
- **抽出 `components/Modal.tsx`**：`Files.tsx` 私有一份、`admin/Users.tsx` 导出一份，现统一。
- **抽出 `utils/format.ts`**：`formatSize` / `formatTime` 原本在 `Files.tsx` 里，
  三个新页面各复制一份就是三份重复。
- **`CreateTask` 从 67 行缩到 27 行**，抽出 `startTask` 供创建与重试共用 ——
  否则 aria2 / net-http 的分派逻辑要写两遍，加新下载方式时极易只改一处。

### 三个页面的功能边界（刻意不画空气按钮）
后端能力有明确边界，UI 就只做后端支持的：
- 分享列表：后端 `ListShares` 只返回 `owner_id = 当前用户`，没有「分享给我的」概念；
  `models.Share.Password` 的 tag 是 `json:"-"`，密码永不返回（但 `ExtractCode` 是 `json:"extractCode"`，会返回）；
  `Download` 字段从未自增，显示恒为 0。所以只做：列、复制链接、取消分享。
- 回收站：`trash=1` 时 `parent` 参数被忽略，返回的是**跨目录扁平列表**而非某目录内容；
  `Restore` 每次新建时间戳目录（批量还原 10 个 → 10 个目录）。这两点直接写进 UI 提示，
  否则用户会以为是 bug。后端只有单条 restore/purge，批量操作前端**串行**循环（并发会放大 SQLite 写竞争）。
- 离线下载：与 `/admin/tasks` 调同一接口，区别只在侧栏位置。

### 测试：为什么补集成测试而不是靠手工点
`TestIntegration_TaskRetryAndTrashEndpoints` 用 7 组断言锁住三个入口依赖的后端能力。
其中「Retry 进行中任务返回 400」这条是刻意的：进行中重试会造成同一 URL 重复下载，
这是个容易漏但危害实在的边界。

### 一个新加的门禁：i18n 对称性
本轮新增 40 个 key 跨 5 个语言文件。少一个 key 的表现是界面直接显示
`ns_mine.emptyTrash` 这种原始串 —— **tsc 发现不了**（`t()` 接受任意 string），
**vite 也发现不了**（运行时才渲染）。所以写了 `frontend/check_i18n.py` 并接进 CI。

已做反向验证：故意把 `ko.ts` 的 `emptyTrash` 改名，脚本立刻报出 key 差异且退出码为 1；
还原后恢复 0。（注意：用 `| head` 观察时退出码会被管道吞掉，要看真实退出码得重定向到 `/dev/null`。）

### 验证
- `npx tsc -b` → exit 0；
- `go build ./...` 通过；`go test ./... -count=1` **19 个包全绿**（controllers 32.9s、routers 18.0s、
  middleware 20.4s、models 20.0s）；
- i18n 五语言均 466 行，`ns_mine` / `ns_share` / `task` 三段起始行完全一致；
- **实机冒烟**：跑 Windows 发布产物 → 真实安装 → 登录 → 三个路由逐一确认
  `location.pathname` 未被守卫重定向，各自侧栏高亮正确、面包屑与空状态正常。

### 排查过程中我犯的两个错（都记下来）
1. **用 503 判断「路由已注册」是错的**。`InstallGuard` 是 `r.Use` 注册的**全局**中间件，
   未匹配路由也会经过它，所以不存在和不存在的路由在未安装状态下**都返回 503**。
   正确甄别要在**已安装状态**下做：已注册→401（走鉴权），未注册→404。
2. **Edge 的 `--screenshot=` 必须给绝对路径**，给相对路径会报
   `Failed to write file` 但**仍然返回 rc=0**。这个「成功但无产物」的组合极易误判。

### 遗留
`models.Share.Download` 字段从未自增（前端显示恒为 0），属后端既有缺陷，
本轮未动 —— 修它需要决定统计口径（是否需要定时任务），不在本次范围内。

---

## 15. Linux 分发包：文档教用户执行 stop.sh，但包里没有 stop.sh

**日期**：2026-10-03（给朋友分发 Linux 版时发现）

### 怎么发现的
准备把 Linux 包发给朋友之前，逐条核对了「包内文件」与「说明文档里让用户敲的命令」，
发现 `试用说明.md` 第 30 行写着：

```
- **Linux / macOS 后台运行**（./start.sh --daemon）：执行 ./stop.sh
```

而 `start.sh` 自己也有两处提到 `stop.sh`：

```sh
#   ./start.sh --daemon     后台运行，日志写nebula.log，用 ./stop.sh 停止
echo "  [信息] PID=$PID ，停止服务请执行 ./stop.sh"
```

**但包里只有 `start.sh`，没有 `stop.sh`。**
朋友按文档操作会直接吃到 `./stop.sh: No such file or directory` ——
而且是在他已经开始用之后才发现的那种。

### 根因：两份打包脚本，只有一份记得这文件
- `packaging/build-release.sh` 一直有 `cp packaging/start.sh packaging/stop.sh ...`
- `packaging/repack.py` 只复制了 `starter`（即 `start.sh`）

也就是说 `repack.py` 是一份**平行实现**，两边各写各的。
写它的时候没对照 `build-release.sh` 已经处理过的事，于是漏了。
这类「两份实现各写各的」的地方，review 时只能靠逐文件对照，
不能指望「我照着原意写」—— 原意已经被另一份脚本固化了。

### 修法
1. `repack.py` 加 `SH_SCRIPTS = ["start.sh", "stop.sh"]`，Linux/macOS 包都带上；
2. **`.sh` 自检从「只查启动脚本」改为遍历包内全部 `.sh`** ——
   原来的写法漏掉任何一个脚本都会让用户撞上 `bad interpreter`；
3. `verify_release.py` 加断言：Linux/macOS 包必须含 `stop.sh` 与 `Linux上手说明.md`。
   **这条断言守的是「文档提到的文件必须在包里」** ——
   比检查文件数量有意义得多，数量对但缺某个文件的情况正是这次漏的。

### 顺带：Linux 专属上手说明
跨平台的 `试用说明.md` 讲了功能，但没讲 Linux 用户真正会卡的点。
新增 `packaging/Linux上手说明.md`，只补这些（不重复已有内容）：

- **必须先解压**，不能直接跑压缩包
- `chmod +x` 的时机 —— 明确说明「包里已带权限，是某些解压工具会丢它」
- `uname -m` 确认架构：输出 `aarch64`/`arm64` 说明这个包不适用
- `bad interpreter: /bin/sh^M` 的**真实原因**是文件传输中被改坏，
  不是脚本写错 —— 避免用户去改脚本
- **纯静态链接（`CGO_ENABLED=0`）不依赖 glibc** ——
  所以 CentOS 7 / Ubuntu 16.04 这类老发行版也能跑，
  不用为了它折腾装新系统
- 「只监听 127.0.0.1 是故意的」+ 想改监听地址的代价（无 HTTPS，别直接暴公网）

### 验证
- 三平台重新交叉编译（`CGO_ENABLED=0 GOPROXY=off`），`repack.py` 重新组包，全平台校验通过
- **真实解压测试**：用 Python 解压 zip 模拟 `unzip`，确认 `stop.sh` 在内、
  权限位 755、带 `+x`；`sh -n` 语法检查两个脚本均通过
- 模拟 `nebula` 无执行权限（644）时 `start.sh` 的 ELF 魔数检测：`7f454c46` 判定正确
- `stop.sh` 在无 `.nebula.pid` 时的行为：给出可操作提示（前台运行请Ctrl+C），不是报错
- **完整用户流程冒烟**（13 项全通过）：登录 → 上传 → 分享（含提取码）→
  分享列表 → 任务列表 → 删除进回收站 → 回收站列表 → 还原 → 回收站清空 → 分享详情

## 16. 分享页整条链路都是假的（下载按钮 + 预览区 + 计数字段）

自主巡检时发现的。问题比「`Downloads` 字段不自增」严重得多 ——
分享功能此前只有一个链接能打开，下载和预览都是摆设。

### 根因与修复

| # | 问题 | 根因 | 修复 |
|---|---|---|---|
| 1 | 下载按钮点了没反应 | `Share.tsx` 的按钮**没有 `onClick`**；且后端没有可用的公开下载端点（登录下载接口挂在 `Auth` 后面） | 新增 `GET /api/shares/:id/download`（挂在 `api` 组，无 `Auth`）+ 前端接线 |
| 2 | 预览区显示外部占位图 / 硬编码假代码 | `Preview` 组件图片用 `trae-api-cn.mchost.guru/...text_to_image?prompt=Nebula cloud storage...`，文本写死一段假代码 | 按真实类型接真实内容；新增 `GET /api/shares/:id/preview` |
| 3 | `fileId` 可被构造成越权读取 | 下载端点接受客户端传的 `?fileId=`，不校验即可拖走任意文件 | `fileWithinShare` 逐级校验祖先链（每层 owner 一致 + 未删 + 落到根） |
| 4 | 浏览次数永远慢一拍 | `UpdateColumn` 已把新值写回结构体，代码又 `s.Views++` 了一次 | `gorm.Expr` 数据库侧自增后单独回读 |
| 5 | 下载次数永远为 0 | 全项目没有任何地方写 `Share.Downloads` | 在下载路径自增（数据库侧，并发安全） |
| 6 | 中文文件名下载后乱码 | `Content-Disposition` 只给裸 `filename` | 补 RFC 5987 `filename*` + ASCII 回退名安全过滤 |
| 7 | DB 连不上时每个请求都 panic | `bootstrap()` 里 `db.Init` 失败只打 WARN 就继续，`db.Get()` 返回 nil | 改 `log.Fatalf` 直接退出 |

### 为什么预览要单独一个端点

预览与下载的差别不只是响应体：

- `Content-Disposition` 必须不同。用 `attachment` 时浏览器会忽略 `Content-Type`
  直接弹下载框，`<img src>` 只会拿到一个空壳 —— 图片根本显示不出来。
- 预览**不能**计入 `downloads`，否则所有者看到的下载量虚高且无法排查。
- 预览响应带 `Cache-Control: no-store`：内容受密码/提取码保护，
  留在浏览器缓存里等于绕过校验就能再取一次。

### 为什么 fileId 必须校验整条祖先链

分享树只递归 2 层（`listChildrenLimited(root.OwnerID, &root.ID, 2)`），
深层文件在 UI 树里看不见 —— 所以下载端点**必须**支持按 fileId 取任意深度，
但这正好给了越权构造的入口。

只查一层是不够的：孙目录、深层文件同样能被构造出来。
必须逐级向上，每层满足 `owner_id == 分享所有者 && 未删除`，最后落到分享根 ID。
`maxDepth = 64` 防 parent 成环导致无限回溯。

失败时不区分「不存在」与「越权」：告诉攻击者哪个 ID 存在本身就是信息泄露。

### 为什么 streamFile 要抽出来

登录下载（`ownOrAdmin` 校验）与分享下载（密码/提取码/祖先链校验）都要这套响应逻辑：
对象存储 302 签名跳转、Range 206、普通流式。
各自写一份的话，对象存储跳转与 Range 支持很容易只改一处 ——
而 Range 恰恰是视频拖进度条的前提。

### 测试

`TestIntegration_ShareDownloadAndPreview`（9 组断言）
+ `TestIntegration_ShareDetailViewsIncrements`（3 次连续访问逐次断言）

**反向验证**：把 `fileWithinShare` 的调用改成 `if false` 后，
测试立刻报 `cross-user fileId: got 200, want 404` ——
确认断言真的在守这条边界，不是恰好通过。

### 验证

- `go build ./...` / `go test ./... -count=1`（13 包全绿）/ `npx tsc -b` / i18n 门禁 全通过
- **实机冒烟 14 项**（真跑二进制 + curl）：未认证下载内容一致、`attachment`、
  预览 `inline`、Range 206、`downloads` 计数正确而 preview 不计、中文名 `filename*`
- **浏览器UI 11 项**（系统 Edge + CDP，零依赖）：分享页未被路由守卫重定向、
  `img.src` 指向 preview 端点、图片真的解码成功（`naturalWidth=64`）、
  文本页显示真实内容且不含旧假代码

  过程中抓到并修掉一个自己引入的 bug：图片**成功**时「预览失败」提示也叠在图上
  （原本用 `absolute inset-0` 常驻渲染，忘了默认 `hidden`）。改为条件渲染 +
  切换节点时重置 `imgBroken`。

### 顺带记录的排查经验

- **`UpdateColumn` 会把新值写回内存结构体**（不是「绕过钩子所以不回写」）。
  所以 `UpdateColumn("views", s.Views+1)` 之后再 `s.Views++` 是二次自增。
  这个认知错误一开始让我把测试预期写成了 1，实际行为是 2。
- **`/api/install` 的请求体结构**是 `{admin:{userName,email,password}, db:{type,file}, system:{...}}`，
  管理员在 `admin` 字段下，不在顶层。
- **sqlite 的 `file` 是相对路径**，换个工作目录启动就打不开这个库，
  报的是极具误导性的 `out of memory (14)`。冒烟测试必须以数据目录为 CWD 启动。
- Bash sandbox 隔离了本机回环连接，`curl http://127.0.0.1:<port>` 会报
  `Failed to connect`（此时服务明明在 `netstat` 里 LISTENING），
  需要 `dangerouslyDisableSandbox`。

---

## 17. 改存储策略会把 SFTP 密码/云厂商密钥**以明文写回库**

做插件配置加密的占位调研时，顺手核对「`Policy.Config` 的加密路径是否可照抄」，
结果发现照抄不了 —— 因为它自己就漏了一条写入路径。

### 问题

`controllers/admin_ex.go` 的 `UpdatePolicy` 这样写：

```go
db.Get().Model(&models.Policy{}).Where("id = ?", id).Updates(map[string]any{
    "name": req.Name, "type": req.Type, "config": req.Config, "is_default": req.IsDefault,
})
```

`Policy.Config` 是 `models.Encrypted`，加密靠 `driver.Valuer`（`Value()`）。
但 **`database/sql` 只对实现了 `Valuer` 的「值」调用它** ——
`req.Config` 是裸 `string`，`Valuer` 完全不参与，明文直接落库。

`CreatePolicy`/`UpdatePolicy` 之外的路径没问题：`CreatePolicy` 用了
`models.From(req.Config)`，`User.TwoFactor` 的两处写入
（`controllers/auth.go:214,231`）也都包了 `models.From`，旁边甚至有注释点了这个坑。
**只有这一处漏了。**

### 影响

`policies.config` 里存的是 SFTP 密码、OSS / COS / S3 密钥。管理员在后台
**编辑一次存储策略**，该策略的凭据就在数据库里变成明文 ——
而接口返回 200，GORM 不报任何错，日志也没有异常。

有兜底但不够：`MigrateSensitiveFields() → migratePolicyConfig()` 每次启动都会把
不带 `enc:v1:` 前缀的行转回密文。所以窗口是「编辑之后、下次重启之前」——
不是永久泄露，但在两次重启之间密钥就是裸的，且期间任何一次库文件泄露都直接兑现。

### 为什么既有的测试没拦住

`models/encrypted_test.go` 的 `TestEncryptedUpdateMapPath` 注释写得很准
（「传裸 string，hook 完全拦不住，会静默写入明文」），**但它传的是
`models.From("NEWSECRET")`** —— 是个已带类型的值，所以它验证的是
「正确写法能加密」，而不是「错误写法会被拦」。测试名和注释比它实际覆盖的范围更大，
这种错位最危险：看名字以为这条路径已经有人守着了。

### 修复

```go
"config": models.From(req.Config),
```

一行。另补两个测试锁住：

| 测试 | 作用 |
|---|---|
| `models/encrypted_bare_string_test.go` | 固化「裸 string 不被加密」这条 `database/sql` 约束，防止后来者把 `models.From(...)` 当冗余包装删掉 |
| `controllers/admin_policy_config_test.go` | 直接打 `PUT /policies/:id`，读**原始列**断言 `enc:v1:` 前缀 |

### 验证

两个测试都做过双向验证：把修复改回裸 string 后
`TestUpdatePolicyConfigStaysEncrypted` 报
`policies.config 必须加密落库，实际 = "{\"password\":\"edited-by-admin\"}"（明文泄露）`，
改回 `models.From` 后通过。**测试必须在修复前失败才有价值**。

### 对插件配置加密的直接影响

`controllers/plugins.go` 有**两处**同样的写法：

```go
db.Get().Model(&p).Update("config", raw)          // 第 471 行，InstallPlugin
db.Get().Model(&p).Update("config", body.Config)  // 第 582 行，UpdatePluginConfig
```

今天 `models.Plugin.Config` 还是普通 `string`（字段本身就是明文），所以**现在不是 bug**。
但一旦按计划把它改成 `models.Encrypted`，这两处**必须同时**包成
`models.Encrypted(...)`，否则插件配置（SMTP 密码、HMAC sharedSecret）
会重演本节这个 bug。已写进
`docs/新功能占位清单-明天接续.md` §2.5。

---

## 18. 插件冒烟测试从来没跑到过插件部分

上一节修完需要端到端验证（包级测试读不到"真实 HTTP 接口 + 真实 SQLite 原始列"），
于是先跑现有的 `backend/smoke_plugin.sh`，结果它在**第 2 步**就死了：

```
.././.smoke-nebula: No such file or directory
```

也就是说：仓库里唯一的插件端到端测试**从未真正验证过任何插件行为**，
但它在文档里被当作已有覆盖。

### 18.1 `smoke_plugin.sh` 的四类缺陷

| # | 缺陷 | 后果 |
|---|---|---|
| 1 | `BIN="./.smoke-nebula"` + `go build -o "../$BIN"` → 产物建到**仓库根**，运行时又去数据目录上一级找 | 第 2 步即挂 |
| 2 | 产物不带 `.exe` | Windows/Git Bash 起不来 |
| 3 | `-data .` + 库路径 `data/nebula.db` → `conf` 只 MkdirAll(".")，`./data` 不存在 | sqlite 报误导性的 `out of memory (14)` |
| 4 | 安装插件时把 MSYS 路径 `/e/...` 塞进 JSON | 服务端按原生路径解析找不到目录 → 400 |
| 5 | 钩子计数匹配 `"name":"onAntiLeech","inProcess":0,...` 连续子串 | `gin.H` 是 map，`encoding/json` **按字母序**输出键（…mode/name/outProcess…），该子串永远不可能命中 |
| 6 | `kill -0 "$PLUGPID"` 判断插件子进程 | Windows 下插件 pid 是**原生 PID**，bash 的 `kill -0` 只认 MSYS pid → 明明在跑却报"不存在" |
| 7 | 无 `.exe` 意识、只 kill 子 shell | 残留真实进程占着端口与数据目录（`Device or resource busy`） |

修法：路径全部基于 `$(dirname "$0")` 解析；按 `uname -s` 补 `.exe`；
不传 `-data`（用默认数据目录，与真实安装一致）；加 `jsonpath()`（`cygpath -w`
+ 反斜杠转义）；钩子断言改成先拆分对象再匹配；加 `proc_alive()` 平台分流；
清理时 `taskkill //F //IM` 兜底。

修好后 **20/20 通过**，插件热加载链路（安装 → 启用拉起子进程 → 403 拦截 → 禁用回收子进程）
第一次被真实验证。

### 18.2 顺带发现：装完不重启，插件功能全部 503

修好脚本后卡在「启用 → 503」。查明原因：

`main.go` 的 `bootstrap()`（`db.Init`、版本化迁移、`pluginset.InitPlugins`、`jwt.Init`）
只在**启动时** `conf.IsInstalled()` 为 true 才执行。而首次安装是在服务运行中
通过 `POST /api/install` 完成的 —— 此时 `bootstrap` 早已跳过。

**所以"装完向导不重启"= 插件宿主始终为 nil，所有插件接口 503（前端显示"插件宿主未就绪"）。**

脚本里的处理是**按真实部署方式重启一次**（新增 `[3b/7]` 步骤），并断言重启后
`/api/admin/plugins/hooks` 能返回 `protocolVersion`。

这属于产品既有行为，不是脚本缺陷，因此本轮**只记录不改**：是否要让安装流程
自己补一次宿主初始化（或提示管理员重启），需要产品决策。

### 18.3 新增 `smoke_policy_config.sh`

第 17 节那个明文泄露 bug 的端到端守卫。它从**真实 HTTP 接口**打进去，
再直接读**数据库原始列**断言 `enc:v1:` 前缀：

| 检查 | 说明 |
|---|---|
| Create 后原始列为密文 | 覆盖 `models.From` 正确路径 |
| **Update 后原始列仍为密文** | 覆盖出过事的那条路径 |
| 数据库文件（含 wal/shm）无明文密钥 | 第二道防线，逐个文件判断而非 glob（glob 不匹配会把"没匹配上"误判成"没泄露"） |
| 上传 + 下载内容一致 | 证明**解密链路**仍可用 |

最后一项不能靠"读接口拿配置"：`models.Policy.Config` 的 tag 是 `json:"-"`，
接口**刻意不返回**配置（不把密钥发给客户端是对的）。改用
`filesystem.New(p.Type, p.Config.String())` 需要明文 JSON 这一点来间接验证 ——
若 `Config.String()` 返回密文，JSON 解析失败 → 上传 500。

**双向验证**：把 `admin_ex.go` 改回裸 string，脚本准确报红 3 项、退出码 1；
改回 `models.From` 后 14/14 通过。

### 18.4 教训

**"脚本存在"不等于"脚本在跑"。** 这两处覆盖（插件端到端、密钥静态加密）
在修复前都属于"看上去有、实际零"的状态。冒烟脚本本身也会腐坏，
而它腐坏时**不会发出任何信号** —— CI 不跑它们，没人执行它们，
于是坏了好几个月也没人知道。

### 18.5 把冒烟接进 CI（新增 `.github/workflows/smoke.yml`）

光修好不够 —— 没人定期执行，它们迟早再次腐坏。新增独立 workflow：

| 步骤 | 内容 |
|---|---|
| `Plugin hot-reload smoke` | `bash backend/smoke_plugin.sh` |
| `Policy config encryption smoke` | `SMOKE_REQUIRE_SQLITE3=1 bash backend/smoke_policy_config.sh` |

几个刻意的选择：

- **单独一个 workflow，不塞进 `go-ci.yml`**：go-ci 是 vet + `test -race`（秒到分钟级），
  冒烟要起真实进程、建真实 SQLite、走真实 HTTP。混在一起会让
  "单元测试挂了"和"端到端挂了"难以区分。
- **触发路径含 `examples/plugins/**`**：示例插件是独立 module，`go-ci` 覆盖不到它，
  而冒烟会现场编译并加载它 —— 这里顺带补上了那块空白。
- **`SMOKE_REQUIRE_SQLITE3=1`**：`smoke_policy_config.sh` 的核心断言依赖 `sqlite3`，
  本地缺依赖时默认跳过（不阻塞开发机）；但 CI 里**必须失败**。
  否则门禁会变成"永远绿色"的假门禁 —— 正是本节的病根。已本地验证两个分支：
  严格模式退出码 1、宽松模式退出码 0。
- **EXIT trap 里的 `rm` 补 `|| true`**：`set -e` 下 trap 中任何一条非零命令都会
  **覆盖脚本真实退出码**，把"断言失败"伪装成"清理失败"。
- **开头打印环境行**（`uname` / bash 版本 / go 版本）：CI 失败时先看这行，
  能立刻区分"环境不对"和"断言失败"。
- **读库失败与"确实是明文"分开判**：`sqlite3` 读不到时返回空串，
  若不区分就会打印"原始列为明文: "，把环境问题误报成安全事件。

> ⚠ **未验证项（如实记录）**：本机无 Linux 环境（WSL 无可用发行版），
> 因此 `ubuntu-latest` 上的实际执行**没有跑过**。已做的验证是：
> workflow YAML 可被解析、两个脚本 `bash -n` 语法通过、两者在 Windows/Git Bash
> 下全绿（14/14、20/20），且所有平台相关分支（`.exe`、`taskkill`、`cygpath`、
> `kill -0` vs `tasklist`）都按 `uname` 显式分流。首次在 CI 上跑通前，
> 仍应把它当作"待确认"而非"已验证"。

---

## 19. 占位骨架插件：README 里的承诺终于被验证了

两个占位骨架插件（`smtp-email` / `hmac-auth`）的 README 都写着
"可安装、可启用、能收到事件"，但**从没验证过**。占位最容易骗到自己：
init 握手、hooks 声明、回写任一环节写错，插件就是装不上或静默收不到事件，
而 `go build` 与 `go vet` 都不会有任何反应。

新增 `pkg/plugin/host/example_skeletons_test.go`，走**真实链路**：
编译 → `install.InstallFromDir` → `host.Manager.Load`（真实 init 握手）→ 派发事件。

### 19.1 断言的是协议不变量，不是占位状态

| # | 不变量 | 为什么重要 |
|---|---|---|
| 1 | **空配置也能启用**（`ConfigFor` 为 nil） | 管理员刚装好、还没填配置时不能启不来。占位的首要目标是"能启用" |
| 2 | 声明的钩子在宿主侧 `Count == 1` | init 握手与 manifest 声明真的对上了 |
| 3 | 异步派发立即返回且不报错 | 这是 `onEmail`/`onApiAuth` 留在 `ModeAsync` 的全部理由 |
| 4 | 插件真的处理了事件（能观测到回写） | 防"装上了但事件被静默丢弃" |
| 5 | 绝不返回含 `"blocked"` 的错误 | `onApiAuth` 一旦误报拦截，接上同步门就会把真实请求全部 403 |

刻意**不**断言"返回 `not-implemented`"这类具体值 —— 那是占位状态，
明天填进真实现后就会作废，写进测试只会变成明天要删的噪音。

### 19.2 第一版测试自己写错了：异步钩子不回传结果

第一版在 `m.Dispatch(hook, ctx)` 之后断言 `ctx[patchKey]`，结果两个插件都"没回写"。
查 `host/manager.go` 后确认**是测试错了**：

```go
func (m *Manager) Dispatch(h plugin.HookName, ctx map[string]any) []error {
	if plugin.HookModeOf(h) == plugin.ModeAsync {
		m.dispatchAsync(h, ctx)   // 结果直接丢弃
		return nil
	}
	return m.dispatchSync(h, ctx)
}
```

`onEmail` 与 `onApiAuth` 今天都是 `ModeAsync`，宿主**刻意丢弃**其结果
（见 `hooks.go` 的 Mode 说明与 `Dispatcher` 接口注释）。
要确定性观测必须用 **`DispatchSync`** —— 它无视钩子既定模式、强制等待回传，
正是 `onRateLimit` 的 `RateLimitGate` 用的那个入口。

> 这条坑值得记：**在 `Dispatch` 之后断言异步钩子的 ctx 变更，是一个永远不可能成立的测试**。
> 它不是偶发失败，而是设计使然 —— 写这类测试时必须先看钩子的 Mode。

### 19.3 反向验证（两次）

一个只会绿的测试等于没有测试，所以对新增断言做了两次破坏性验证：

| 破坏方式 | 结果 |
|---|---|
| `plugin.json` 的 `protocolVersion` 改成 2 | ✅ FAIL —— 在清单校验阶段就被拦下 |
| `handleInit` 返回 `OK: false`（装得上但启不了） | ✅ FAIL 且报文精确：`加载 smtp-email 失败（init 握手未通过）: 插件报告初始化失败但未给出原因` |

第二次专门针对第 1 条不变量 —— 它才是"可启用"这个承诺的真正守卫。
验证完已全部还原，`go test ./...` 15 个包全绿。

---

## 20. 公开分享的防盗链缺口已补（onAntiLeech 覆盖分享下载与预览）

第 17 节的调研顺带发现的缺口，本轮实现。

### 问题

`onAntiLeech` 原先全项目只有一个调用点（`controllers/file.go` 的 `Download`），
而公开分享走 `controllers/share.go` 的 `serveShareFile` —— **完全不触发该钩子**。

后果：装了防盗链插件的站点，分享链接依然可以被任意第三方站点盗刷。
而分享恰恰是最需要防盗链的场景：站内下载要登录，分享链接是公开的。

### 修法

在 `serveShareFile` 里、`target` 解析并通过祖先链校验**之后**、
`handlerForFile(&target)` **之前**新增调用点。位置理由是硬的：

- 更早：`target` 与分享还没解析出来，拿不到 `fileId` / `fileName` / `shareId`；
- 更晚：文件已进入 `streamFile` 开始流式输出，再想 403 已经来不及。

### 为什么 preview 也必须过闸（本轮最关键的一个决定）

`preview` 用的是 `Content-Disposition: inline`，看起来"只是预览"，
但它返回的是**完整文件内容**。只拦 `download` 等于留了一条完整的
绕过防盗链的通道 —— 攻击者拿 `?fileId=` 打 preview 就能把文件拖走。

这个判断不靠推理，靠实测：把闸门改成"只拦 download、放行 preview"后，
测试直接报出了证据 ——

```
--- FAIL: TestShareAntiLeechBlocksDownloadAndPreview
    share_antileech_test.go:126: 分享**预览**未被防盗链拦截: status=200 body=hello share
```

于是宿主对两者**一视同仁**地过闸，并把场景通过 `shareKind`
（`download` / `preview`）交给插件自己决定要不要区别对待。
宿主不替插件做这个取舍，但绝不默认放行其中一个出口。

### ctx 契约（分享路径）

```
{userId: 0, userName: "", ip, ua, fileId, fileName, referer,
 shareId, shareKind, shareOwnerId}
```

两个刻意的点：

- **`userId` 恒为 0 且键必须存在**。分享访问者是匿名的，插件不能假设它有值。
  键存在与键缺失是两种语义：键缺失时插件的 `numOf(ctx["userId"])` 拿到 nil，
  与"存在且为 0"不同。测试专门断言了"键存在且为 0"。
- **分享所有者放 `shareOwnerId`**，不与访问者混淆 —— 登录下载那条路径里
  `userId` 就是所有者，两个路径的语义不同，共用同一个字段名会造成误判。

拦截判定走 `plugin.IsBlockedError`（`hooks.go` 声明它是全项目唯一实现）。

### 验证

| 层 | 内容 | 结果 |
|---|---|---|
| 包级（controllers） | 下载与预览都过闸且 403；无插件时照常 200 且内容正确；非 `blocked` 错误不拦截 | 3 个测试全过 |
| 反向验证 ×2 | ①闸门整个关掉 → 下载断言报红；②只拦 download → **预览**断言报红（见上） | 各自 FAIL |
| 端到端（真机） | `smoke_plugin.sh`：真实进程 + 真实子进程插件，创建分享后 download 与 preview 均外站 403 / 本地 200，禁用后恢复 200 | **26/26 通过**（原 20 项 + 新增 6 项） |

示例插件 `referer-guard` 同步更新：审计日志新增 `shareId` / `shareKind`
（才能区分"谁在盗刷分享链接"与"谁在盗刷站内下载"），头部注释写明了
分享路径的 ctx 差异。

### 行为变更提示

装了防盗链插件的站点，**分享链接现在也会被判定**（此前完全不判定）。
本站自身的分享页会带自身 Referer，正常配置下会命中白名单而被放行；
若某站点只想保护下载、放行内联预览，请让插件按 `shareKind` 自行放行 ——
这条已写进 `hooks.go` 的钩子文档与插件 README。

---

## 21. smtp-email 从占位骨架变成真的能发信

第 19 节验证过的骨架，本轮填上真实实现。
背景：宿主**不含任何发信实现**（全仓 `grep -r smtp` 零命中），
`onEmail` 是邮件外发的唯一入口 —— 没有它，站内通知永远到不了邮箱。

### 实现要点

| 点 | 做法 |
|---|---|
| 三种 TLS | `starttls`(587) / `implicit`(465) / `none`(25)，每一步都设 deadline |
| AUTH | 配了 `username` 才认证；服务器没声明 `AUTH` 时**明确报错**，不静默匿名发送 |
| 中文主题 | RFC 2047 编码（`mime.QEncoding`） |
| 中文正文 | `charset=UTF-8` + base64；ASCII 正文走 7bit |
| 头注入 | `Subject`/`To` 的 CR/LF 一律剥离 |
| 密码 | 日志里只出现 `<set:N chars>` |

**没有"跳过证书校验"的开关。** `tlsConfigForHost` 是可注入变量，
但只在测试里被替换；生产默认固定 `MinVersion: TLS1.2` 并按系统根证书校验。
—— 一个"为了测试方便"的跳过选项，最终一定会被用到生产上。

### 单测抓出了实现里的一个真 bug：base64 路径漏了 CRLF 归一化

`buildMessage` 一开始只在 7bit 路径做换行归一化，base64 路径直接编码原文。
后果是收件人解码后拿到 LF 行尾，而 RFC 5321 要求 CRLF ——
base64 是对字节编码，正文里的裸 LF 会被原样编进去。

断言 `正文解码后应等于 toCRLF(原文)` 把它当场抓了出来：

```
--- FAIL: TestSendChineseSubjectAndBody
    main_test.go:263: 正文解码后不一致
```

修法是把归一化提到两条路径之前。**测试没改，改的是实现。**

### 测试策略：两层，缺一不可

字段名是宿主与插件**两个 module 之间**的约定（宿主发 `{"to","subject","body"}`，
插件读 `ctx["to"]` 等）。改名不会有任何编译错误，插件只会静默读不到字段。

| 层 | 文件 | 覆盖 |
|---|---|---|
| 宿主侧契约 | `controllers/notification_email_test.go` | `CreateNotification` 真的触发 `onEmail`，且字段名/内容正确；邮箱为空时不触发 |
| 插件侧协议 | `examples/plugins/smtp-email/main_test.go` | 假 SMTP 服务器做真实往返：中文主题编码与折行解码、正文编码、头注入、三种 TLS、fail closed、错误文案绝不含 `blocked` |
| 完整链路 | `pkg/plugin/host/smtp_email_e2e_test.go` | 宿主下发配置 → 拉起子进程 → 派发 `onEmail` → `net/smtp` → 假 SMTP 服务器 → 断言报文 |

### 反向验证：字段名漂移必须被抓住

把插件的 `ctx["to"]` 改成 `ctx["recipient"]`（模拟两个 module 之间的约定断裂）。

第一版测试在 **16 秒后**才失败，报的是"15s 内没收到邮件" —— 排查方向会被
引到网络/配置上去。原因是插件读不到收件人时直接回 `skipped-no-recipient`、
**根本不连 SMTP**，所以只会等到超时。

改成先判插件自报状态再等收件后：

```
--- FAIL: TestSMTPEmailPlugin_EndToEnd (1.02s)
    smtp_email_e2e_test.go:189: 插件判定收件人为空 —— 宿主发的是 ctx["to"]，
    插件读的键名对不上（两个 module 的字段名约定断了）。
```

**1 秒内给出精确原因。** 失败信息的质量本身就是测试质量的一部分。

### 顺带清理的两处"陈旧文案"

反向验证的日志输出暴露了两条与事实相反的提示，都已修正：

- `handleInit` 还在打 `smtp-email 已就绪（占位骨架）…SMTP 投递尚未实现…`
  → 改为报告 `tls` 模式与失败行为；
- 配置不完整时的 warn 仍写着"当前仍为占位骨架，不会真实发信"
  → 改为"补齐后重启即生效；当前状态下邮件不会发出"。

**实现完成却留着"尚未实现"的日志，比没有日志更有害**：它会让管理员
在已经正常工作（或已经失败）的系统上往错误方向排查。

### 另一处诚实性修正：权限声明

`plugin.json` 原声明了 `write-own-datadir`，但实现没往插件数据目录写任何文件
（失败记录走宿主日志环形缓冲即可，落盘还要担磁盘被写满的风险）。
**声明用不到的权限会高估插件所需权限**，而管理员正是靠这份声明判断风险 ——
已删除该条。

### 验证

| 检查 | 结果 |
|---|---|
| 插件 `go vet` / `go build` / `go test` | ✅ 通过，8 个用例 |
| 宿主 `go build` / `go vet` / `go test ./...` | ✅ 15 个包全绿 |
| `smoke_plugin.sh` | ✅ 26/26 |
| `smoke_policy_config.sh` | ✅ 14/14 |
| 反向验证（字段名漂移） | ✅ 1.02s 精确 FAIL |

示例插件是**独立 module**，`backend` 的 `go test ./...` 覆盖不到 ——
已在 `.github/workflows/smoke.yml` 增加 `Example plugin checks` 步骤
（三个插件逐个 `go vet` + `go test`），否则这些断言又是一处"写了但永不运行"。

---

## 22. 接通 onApiAuth：开放 API 的扩展鉴权

第 3 个预留钩子（`Wired=false`）接通。用途：`/api/v1/*` 在内置鉴权
（PAT / OAuth2）之外接受扩展鉴权方案 —— HMAC 签名、IP 白名单、
第三方令牌等。

### 设计计划被推翻：不需要同步门

第一版计划写的是"照 `onRateLimit` 的做法加一个同步门（`APIAuthGate`）"。
实现时发现**前提不成立**：

`onRateLimit` 之所以要另开门，是因为它有 **7 个调用点**、只有 1 个需要读结果 ——
把整个钩子改成同步会让另外 6 处每次白等一次 IPC 往返。
而 `onApiAuth` 只有**一个**调用点，且它必须读结果。

所以正确做法就是把它本身改成 `ModeSync` + `Blockable: true`，
不需要任何额外的门。**计划里"照抄某个模式"的指令，先要核对前提是否相同。**

### 接线位置与语义

落在 `middleware/api_auth.go`：PAT / OAuth2 **都失败之后**才触发。两个原因：

- 热路径零成本 —— `/api/v1/*` 的每个请求都走这个中间件，
  正常鉴权不该为一次 IPC 往返买单；
- 内置凭据始终优先，插件无法否决一个已经有效的令牌。

插件有两种表达方式：

| 意图 | 做法 | 宿主行为 |
|---|---|---|
| 放行 | 在 ctx 写入 `principalUserId`（>0） | 从库里取出该用户设为当前用户 |
| 拒绝 | 返回含 `blocked` 的 error | 403 |
| 不表态 | 两者都没有 | 保持 401（**fail closed**） |

拒绝用 403、未表态用 401：前者是"扩展鉴权明确说不"，后者是"凭据没通过"。

### ctx 契约

```
{method, path, query, ip, ua, headers, hasAuthorization,
 contentType, contentLength, bodyHash, bodyTruncated}
```

两个刻意的决定：

- **`headers` 不含 `Authorization`。** 内置凭据留在宿主这边；插件要用自定义凭据
  应当用自己的头（HMAC 签名正是这样）。这既减少凭据扩散面，也逼出一个明确约定，
  而不是让插件去猜"Authorization 里的东西算不算已经验证过"。
- **`bodyHash` 有上限（64KB）。** 本中间件在**鉴权失败**时才跑到扩展鉴权，
  而失败路径是攻击者可控的：不限量读 body 等于给了一个内存放大面。
  超限时不读全文，只置 `bodyTruncated=true` 并把 `bodyHash` 置空 ——
  半截数据的哈希没有任何意义，给出去只会让插件拿错误的哈希去校验
  （然后表现为"签名永远不对"）。

`bodyHash` 还有一个必须做对的细节：中间件"顺手看一眼" body 之后，
后续 handler（例如 `/api/v1/files/upload`）仍要读到**完整**内容。
用 `io.MultiReader(已读缓冲, 原body)` 放回，读完与截断两条路径都不丢字节。
测试里专门发了一个带 body 的请求并断言下游读到的内容一字不差。

### 一个只会在"多进程"下暴露的静默失效

`principalUserId` 的值必须兼容多种数字类型：进程内 handler 写进去的是
Go 的 `int`/`uint`，而**进程外插件的回写经 JSON 往返后是 `float64`**。
只认其中一种的后果是"进程外插件放行无效"，而且**不报任何错** ——
这类静默失效正是本项目插件体系里反复出现的一类坑（参见
`referer-guard` 里对 `ctx["userId"]` 的同类处理）。

float64 路径额外加了 2^53 与整数性校验：JSON 无法精确表示更大的整数，
拿到的值可能已被四舍五入成**另一个用户**。`int` 路径没有这个歧义
（它来自进程内、是精确值），所以那里的界限只要求为正 —— 这个不对称
是有意的，测试里也写了注释说明。

### 反向验证（两次，都针对安全属性）

| 破坏方式 | 结果 |
|---|---|
| 把"插件没表态"当成放行 | ✅ FAIL：`插件没表态时应保持 401，实际 200` |
| 忽略插件的 `blocked` 拒绝 | ✅ FAIL：`插件拒绝应 403，实际 401` |

第一条才是要害：装了插件却把 401 变成 200，等于把开放 API 直接敞开。

### 端到端（真机）

`smoke_plugin.sh` 新增第 8 节：真实进程 + 真实子进程插件。

```
[PASS] 安装 hmac-auth 200
[PASS] 启用 hmac-auth 200
[PASS] 无效令牌经扩展鉴权后仍 401（fail closed）
[PASS] 插件收到 onApiAuth 事件（钩子真的派发到子进程）
```

**30/30 通过**（原 26 项 + 新增 4 项）。
"钩子真的派发到子进程"这一条是从宿主的插件日志环形缓冲里读出来的 ——
断言的是插件自己打的那行日志，而不是宿主侧的自述。

### 顺带修掉：hmac-auth 声明的契约是错的

占位阶段我给 `hmac-auth` 写了一份"期望宿主提供"的字段清单
（`timestamp`、`signature`、`appKey`…）。**接通宿主后才发现这些字段宿主根本不发** ——
它们是插件自定义的**凭据头**，应当从 `ctx["headers"]` 里自己取。

这正是"契约探针"的价值所在，但暴露了一个流程问题：
**占位阶段的契约是凭空写的，没有对照任何实现**。已修正：

- `contractFields` 改为与 `middleware/api_auth.go` 实际发送的字段一一对应；
- 新增 `credentialHeaders`（插件自定义头）与 `headerValue()` ——
  后者同样要兼容 `map[string]string`（进程内）与 `map[string]any`（JSON 往返）；
- 头部注释、init 日志、README 全部改为"宿主已接通 + 插件尚未校验"的真实状态，
  并明确 **本插件当前不会放行任何请求**。

### 验证

| 检查 | 结果 |
|---|---|
| `go build` / `go vet` / `go test ./...` | ✅ 15 个包全绿（连续两次） |
| `middleware` 新增 7 个用例 | ✅ 全过 |
| 反向验证 ×2 | ✅ 各自 FAIL |
| `smoke_plugin.sh` | ✅ **30/30** |
| 三个示例插件 `go vet` + `go test` | ✅ 通过 |

> 观察记录（未复现）：一次全量 `go test ./...` 中 `routers` 报 FAIL，
> 同一次运行里 `pkg/plugin/host` 因示例插件编译错误也 FAIL。
> 修好后连续两次全量绿，且 `routers` 单独连跑 3 次全绿。
> 倾向于是并行满载下的偶发（该包会起真实 HTTP 服务）。
> **记录在案，不掩盖** —— 若再次出现需要单独排查。

---

## 23. hmac-auth 从契约探针变成真的会鉴权

第 22 节接通 `onApiAuth` 后，本轮把插件的校验实现补上。
至此 `/api/v1/*` 除了 PAT / OAuth2，还能用**共享密钥 + HMAC-SHA256 签名**调用。

### 端到端测试抓出一个只能靠真实请求发现的 bug

实现完成、包级测试全绿之后，冒烟脚本里的签名请求**一直返回 401**。
现象极具迷惑性：

- 插件进程在跑（`status: running`、真实 pid）；
- 钩子计数正确（`outProcess: 1`、`mode: sync`）；
- 插件日志里**一片空白** —— 连一条"收到 onApiAuth"都没有。

也就是说：**钩子注册了，却从未被派发**。

根因在接线位置：`APIAuth` 原本在

```go
tok := extractBearerToken(c)
if tok == "" {
    c.AbortWithStatusJSON(401, "missing token")
    return          // ← 扩展鉴权在这后面，永远到不了
}
```

而 **HMAC 客户端压根不会带 `Authorization` 头**（签名放在自定义头里）。
于是所有这类请求都在第一步就被 401 挡掉，扩展鉴权形同不存在 ——
而且返回的 401 看起来完全正常，没有任何异常信号。

**修法**：把"缺 token 就早退"改成延迟到最后兜底，让扩展鉴权在
内置方式全部失败（**含根本没带凭据**）之后都能拿到请求：

```go
if tok != "" { /* 1. OAuth2 */ }
if tok != "" { /* 2. PAT    */ }
switch u, decision := pluginAPIAuth(c); decision { /* 3. 扩展鉴权 */ }
if tok == "" { 401 "missing token" } else { 401 "invalid token" }
```

已补回归测试 `TestAPIAuthPluginWithoutAuthorizationHeader` ——
断言"不带 Authorization 的请求必须能走到扩展鉴权"，并在注释里写明了
这个坑的症状，避免以后有人"优化"时又把早退加回去。

> 教训：**包级测试覆盖了所有逻辑分支，却覆盖不了接线位置。**
> 这次能发现，靠的是端到端脚本里那条真实 HTTP 请求。

### 签名规范串

```
METHOD\nPATH\nQUERY\nTIMESTAMP\nBODY_SHA256_HEX
```

五行，**缺一不可**。少任何一行，攻击者就能在不改签名的前提下替换那部分内容。
测试里用"篡改表"逐项验证：改方法 / 路径 / 查询串 / **请求体摘要** / 时间戳 /
签名 / 换密钥，每一项都必须被拒。

反向验证：把 `bodyHashHex` 从规范串里去掉后，篡改表里"改请求体摘要"
当场报红（`该字段没有被纳入规范串`）—— 证明这张表不是摆设。

### 三条安全红线（都有测试）

| 红线 | 做法 |
|---|---|
| 未配 `sharedSecret` / `principalUserId` 时**一律拒绝** | 没有密钥就不存在鉴权；没有 principalUserId 无法认定身份（比"随便挑一个用户"安全） |
| **常量时间比较** | `hmac.Equal`；用 `!=` 会在第一个不同字节处返回，可被逐字节猜签名 |
| **缺签名头也拒绝** | 否则不带签名头就能绕过这层保护；要放行的端点显式写进 `exemptPaths` |

另外两个容易写错的点：

- **`exemptPaths` 按路径段边界匹配**：配置 `/api/v1/health` 匹配
  `/api/v1/health` 与 `/api/v1/health/live`，但**不**匹配 `/api/v1/healthcheck`。
  裸 `strings.HasPrefix` 会意外放行后者 —— 表面只是"多放行一个端点"，
  实际放行了一个没人打算公开的端点。
- **`bodyTruncated` 时必须拒绝**：宿主读 body 有 64KB 上限，
  半截数据的摘要不可信，放行等于允许篡改请求体而签名依然通过。

### 端到端（真机 HTTP）

`smoke_plugin.sh` 第 8 节现在做真实签名（`openssl dgst -sha256 -hmac`
+ `sha256sum`，是**客户端参考实现**，与插件内部刻意分开写）：

```
[PASS] 未配密钥时请求被拒（fail closed，403）
[PASS] 插件收到 onApiAuth 事件（钩子真的派发到子进程）
[PASS] 有效签名放行 200
[PASS] 篡改查询串被拦截 403
[PASS] 缺签名头被拦截 403
[PASS] 过期时间戳被拦截 403（防重放）
```

**36/36 通过**（原 30 项 + 新增 6 项）。

### 顺带修正：一个测试的前提已经过时

第 19 节写的 `TestSkeletonPlugins_LoadAndDispatch` 统一断言
"插件绝不能返回拦截错误" —— 那是 `onApiAuth` 还处于**未接线的异步占位**
状态时的正确期望。接通并实现校验后，它对 hmac-auth 就是**错的期望**
（未配置密钥时**必须**拦截）。

已重写为逐个插件指定 `wantBlocked`，并在注释里写清两个插件期望相反的原因
（`onEmail` 异步不可拦截 vs `onApiAuth` 同步可拦截且要 fail closed）。
测试文件也从 `example_skeletons_test.go` 改名为 `example_plugins_test.go`
—— 它们已经不是骨架了。

### 验证

| 检查 | 结果 |
|---|---|
| `go build` / `go vet` / `go test ./...` | ✅ 15 个包全绿 |
| hmac-auth 单测（含 7 项篡改表） | ✅ 全过 |
| `pkg/plugin/host` 端到端（真实子进程 + 真实签名） | ✅ 4 个用例 |
| 反向验证（bodyHash 不进规范串） | ✅ FAIL |
| `smoke_plugin.sh` | ✅ **36/36** |
| `smoke_policy_config.sh` | ✅ 14/14 |

### 遗留

- 插件配置在数据库里仍是**明文**（宿主侧加密排在计划里）→ **本轮已解决，见第 24 节**。
- `principalUserId` 是单个用户：多个调用方需要各自身份时，
  得按 appKey 映射到不同用户 —— 当前只支持单一身份。

---

## 24. 插件配置改为加密存储 + 配置编辑 UI

第 23 节结尾留下的"插件配置仍是明文"，本轮补掉。

**为什么必须做**：插件配置里装的是 SMTP 密码、HMAC 共享密钥，
与 `policies.config`（SFTP 密码/云厂商密钥）性质完全相同 ——
而后者早就加密了，前者一直漏着。管理端协议里也一直明示"配置是明文存储，
请勿填高敏感凭据"——现在这句话可以删掉了。

### 改法与那个已知陷阱

`models.Plugin.Config` 的类型从 `string` 改成 `models.Encrypted`。
但**改类型本身不等于自动覆盖所有写入路径** —— 这一点第 17 节已经用
真实事故证明过（`Policy.Config` 的 `UpdatePolicy` 传裸 string →
Valuer 不参与 → 明文落库，且接口 200、GORM 不报错）。

所以两处 `Update("config", ...)` 必须**同时**包成 `models.Encrypted(...)`：

| 位置 | 改动 |
|---|---|
| `InstallPlugin`（启用时一并写配置） | `Update("config", raw)` → `Update("config", models.Encrypted(raw))` |
| `UpdatePluginConfig`（管理端改配置） | `Update("config", body.Config)` → `Update("config", models.Encrypted(body.Config))` |

读取侧只有 `pluginset.pluginConfig()`（把配置下发给插件），改成
`p.Config.String()` —— `Scan` 已把密文还原成明文，下游逻辑不变。

### 数据迁移（不是 schema 迁移）

列类型没变（都是 `text`），所以**不需要**版本化 migration；
与其它敏感字段一样走启动时的 `MigrateSensitiveFields`：

- 新增 `migratePluginConfig()`，与 `migratePolicyConfig()` 完全同构
  （读取时 `Scan` 已还原明文，只需原样 `Save`，由 Valuer 完成加密）；
- 纳入 `SensitiveFieldsEncrypted()` 探针统计；
- 迁移汇总日志新增 PluginConfig 一项。

### 守卫（三层，都做了反向验证）

| 层 | 断言 | 反向验证 |
|---|---|---|
| `controllers/plugin_config_encryption_test.go` | `PUT /api/admin/plugins/:name/config` 后读**原始列**必须是 `enc:v1:`；读回应是明文；空配置保持空串；非法 JSON 不入库 | 退回裸 string → FAIL：`必须加密落库，实际 = "…PLUGIN_SMTP_SECRET…"（明文泄露）` |
| `models/migrate_sensitive_test.go` | 存量明文插件配置 → 迁移后原始列无明文，且业务层仍能读出明文 | 已并入既有迁移测试 |
| `smoke_plugin.sh` 第 8.2b 节 | 真机：接口写 → 落库加密 → 下发解密 → 插件用明文密钥校验成功 | 退回裸 string → 36/1：`数据库文件中出现明文插件密钥（配置未加密落库）` |

端到端那条刻意用 **grep 数据库文件**而不是 `sqlite3`：不引入额外依赖，
而且"整个文件里都不该出现明文密钥"比"某一列是密文"更严格。

### 前端：配置编辑 UI

把占位阶段那个**只读**配置块换成真正的编辑器：

- 草稿与已存配置分开。列表刷新会带回新的 `plugin.config`，
  但**只在没有正在编辑时**才覆盖草稿 —— 否则管理员输入到一半，
  一次后台刷新就把内容冲掉。
- 保存前先在前端校验 JSON（后端也会拒），错误提示离输入框更近，
  不用等一次网络往返。
- 进入编辑时若原配置可解析，顺手格式化成缩进 JSON —— 配置多是单行，
  不美化根本看不清层级。
- **前端不做任何加密**：密钥不能发给浏览器，前端加密没有意义。
  界面上标注"已加密存储"，实际处理全在服务端。
- 删掉了"请勿填写高敏感凭据"的警示 —— 那句话现在不成立了。

i18n 同步：删掉 `configReadOnly` / `configPlaintextWarning`，
新增 8 个 key（`configEncrypted` / `configEncryptedHint` / `configEdit` /
`configSave` / `configCancel` / `configSaved` / `configSaveFailed` /
`configInvalidJson`），五语言对称由 `check_i18n.py` 门禁守住。

### 验证

| 检查 | 结果 |
|---|---|
| `go build` / `go vet` / `go test ./...` | ✅ 15 个包全绿 |
| 新增控制器测试 3 个 | ✅ 全过 |
| 迁移测试（含 plugins.config） | ✅ 通过 |
| `npm run build`（tsc 严格 + vite） | ✅ 通过，`frontend_dist` 已同步 |
| `check_i18n.py` | ✅ 五语言对称 |
| `smoke_plugin.sh` | ✅ **37/37**（新增加密落库断言） |
| `smoke_policy_config.sh` | ✅ 14/14 |
| 反向验证 ×2（裸 string） | ✅ 各自 FAIL |

### 遗留

- `principalUserId` 仍是单一身份（同第 23 节）。

---

## 25. 最后三个预留钩子接通（onBackup / onRestore / onCLI）

4 个预留钩子的最后 3 个。至此 `Hooks` 表里**没有任何 `Wired:false`**，
管理端不再显示"未接入"。

### onBackup —— 新增备份入口

原来后端**没有**备份路由。新增 `POST /api/admin/backup`：
生成 `backup-<ts>-<rand>.zip`，内含

- `nebula.db`（仅 SQLite；MySQL/PG 拿不到一致快照，硬做出来的是坏备份，
  响应里明说不带库文件）；
- `manifest.json`（库信息 + 文件清单，上限 2 万条并标记 `truncated`）。

生成**之后**触发 `onBackup`（异步通知）：插件可据此把包复制到 S3/异地或落审计。
异步意味着**拿不到返回值** —— 文档里写明了"别指望用它否决备份"，
避免插件作者以为自己能拦住。

### onRestore —— 暂存制

`POST /api/admin/backup/restore`（挂 `RequireConfirm`，与卸载插件同门槛）。

关键设计：**恢复不当场替换数据库**。热替换正在被使用的 SQLite 库
（活动连接 + WAL）会直接损坏数据，这不是保守，是唯一安全的做法。
所以接口只做三件事：

1. 解 zip、找 `nebula.db` 条目；
2. **校验 SQLite 文件头** —— 不校验就写进数据目录，等于允许把任意内容
   伪装成数据库，下次启动直接打不开；
3. 写进 `data/restore/` 并留下 `PENDING` 标记。

真正的替换发生在**下次启动**，由 `ApplyPendingRestore()` 在 `db.Init`
**之前**执行（那时本进程还没打开库，换文件是安全的）。原库会被改名保留
（`.pre-restore-<ts>`），出问题能手工回滚；`-wal`/`-shm` 一起挪走 ——
只挪主文件会留下不一致的 WAL。挪不动现库时**直接放弃**，不硬覆盖。

`onRestore` 在提交时触发。同样写明"插件在此拦不住这次恢复"，
需要否决请在权限层解决 —— 而不是让人对着一个异步钩子写拦截逻辑然后困惑为什么没用。

### onCLI —— 第一个真正的子命令

引入 `nebula plugin <cmd> [args...]`。放在 `flag.Parse` **之后**，
这样 `-data` 这类全局参数在子命令里照常可用；放在之前会让子命令
自己解析参数，两套解析迟早打架。

`onCLI` 是 **`ModeSync` + `Blockable`**（与另两个不同）：命令行必须拿到输出。
插件往 ctx 写 `cliOutput`（打印）与 `cliExitCode`。退出码约定
`0` 成功 / `1` 用法错误或被插件拒绝 / `2` 未安装，让脚本能安全地
`nebula plugin x && ...`。

子命令**不启动 HTTP 服务**：初始化依赖 → 派发 → 退出（冒烟里有断言）。

### 过程中的两个自我纠正

**① 钩子注册表被插入了两份 `onCLI`。** 我在改 `hooks.go` 时，
第一次编辑带了一份新条目、第二次替换又写了一遍，结果 `Hooks` 里出现
两个 `onCLI`。是**既有测试** `TestPluginHooksReturnsRealCounts` 抓出来的
（`钩子数 = 11, want 10`）。

顺带把那个测试的断言方式改掉了：它原先写死 `== 10`，于是每接通一个
预留钩子就要改一次 —— 这类"随进度过期"的断言一旦没人维护，
就会退化成"反正它红了，忽略掉"。现在改为与 `plugin.Hooks` 对齐，
并额外断言**没有重名**（正是这次踩到的形状）。

**② 测试辅助函数放错了包。** `positiveInt` 在 `package main`，
我一度把它放进 `controllers` 包的测试里，编译直接失败。

### 验证

| 检查 | 结果 |
|---|---|
| `go build` / `go vet` / `go test ./...` | ✅ 15 个包全绿 |
| 备份/恢复测试（9 个用例） | ✅ 归档内容可用、钩子 ctx 完整、**路径穿越全挡**、恢复 fail closed、暂存不替换现库、启动时替换并保留原库、悬空标记清理 |
| `onCLI` 派发测试（2 个） | ✅ 命令/参数到达插件、回写能回传、blocked 判定 |
| 退出码类型兼容 | ✅ 兼容 JSON 往返后的 float64 |
| `smoke_plugin.sh` | ✅ **41/41**（新增第 9 节：子命令识别、退出码约定、不启动 HTTP） |
| `smoke_policy_config.sh` | ✅ 14/14 |
| 前端 `npm run build` + `check_i18n.py` | ✅ 通过且对称 |

### 一处如实说明

**下载备份的路径穿越防护写了 8 种攻击形状的测试**（`../`、URL 编码、
绝对路径、子目录、目录遍历混合、错误扩展名、点开头、尾斜杠），
全部 400。但 `RestoreBackup` 的**上传**侧没有做 zip bomb 限额
（`install` 包解压插件时有，这里没有）—— 当前只限制了上传体积 4GB。
这是已知缺口，不是遗漏：备份包由本系统自己生成，正常不会超限，
但**如果将来开放接收外部备份包**，必须补上条目数与解压总量限制。

### 目标完成情况

| # | 功能 | 状态 |
|---|---|---|
| ① | onAntiLeech 覆盖公开分享下载 | ✅ FIXLOG §20 |
| ② | smtp-email 真实发信 | ✅ §21 |
| ③ | onApiAuth 接通 | ✅ §22 |
| ④ | hmac-auth 实现校验 | ✅ §23 |
| ⑤ | 插件配置加密 + 配置 UI | ✅ §24 |
| ⑥ | onBackup / onRestore / onCLI | ✅ §25 |

`Hooks` 表里已无 `Wired:false`；`TestHookPlanMetadata` 仍守住
"标了 Planned 就必须有 Plan"的不变量。

---

## 26. PBClus：插件包的独立容器格式 + 打包工具

需求是"最终可识别的插件格式是 `.PBClus`，**不是 zip 改个后缀**"。

先说明一件事实：`PBClus` 在本仓库此前**零命中** —— 它不是项目已有的格式，
所以这一节做的不是"接入"，而是**定义一个新格式并实现它**。

### 为什么"改后缀"不算数

改后缀只换名字，格式仍然是 zip：解析规则、攻击面、允许的结构全都没变。
而且更糟 —— 文件名叫 `.pbclus` 内容是 zip，会让"看到 .pbclus 就知道怎么处理"
这件事变得不可靠。所以这里定义的是真正不同的容器：

| 维度 | zip | PBClus v1 |
|---|---|---|
| 路径规则 | 解压方各自防 | **格式层规定**：相对、规范化、纯正斜杠，`..`/绝对路径/`a//b` 打包即拒 |
| 条目类型 | 文件/目录/符号链接/设备 | **只有普通文件与目录**（消灭 symlink 一类攻击） |
| 重复路径 | 允许，结果取决于实现顺序 | **拒绝** |
| 条目排列 | 任意，可有空洞 | 必须紧密连续，有空洞/重叠即判损坏 |
| 校验 | 每条目 CRC32 | 头部 CRC + 清单/载荷 SHA-256 + 尾部整文件 SHA-256 + 逐条目 SHA-256 |
| 识别方式 | 扩展名 | **魔术字**（`PBClus` + `0x1A 0x0A`） |

布局：`[Header 128][Manifest JSON][Payload][Footer 48]`，小端。
魔术字末尾用 `0x1A`（DOS EOF）与 `0x0A` 是学 PNG 的老办法：
文件被文本模式工具糟蹋过时立刻暴露，而不是解出一堆乱码。

### 校验的边界（写进文档，避免制造错觉）

CRC 与 SHA-256 只保证**完整性**（没损坏/没截断），**不提供防伪** ——
任何人都能重新打包并算出正确摘要。v1 没有签名，flags 保留位留给将来扩展。
装插件时它仍是不可信输入。这一点在 HTML 文档里用单独一张 danger 卡写明。

### 打包工具

新增 `backend/cmd/pbclus-pack`（`go build -o ../tools/pbclus-pack.exe ./cmd/pbclus-pack`）：

```
pack <目录> [-o 输出] [--store] [--level N] [--include-all] [--force] [--quiet]
inspect <包> [--json]     verify <包>     unpack <包> -o <目录>     info
```

退出码 `0` 成功 / `1` 失败 / `2` 用法错误 —— 让打包能写进脚本。

`pack` 会先用**与宿主同一套**校验检查 `plugin.json`，清单问题在打包阶段暴露，
而不是等管理员点安装。默认排除版本库/依赖目录/编辑器临时文件/其它压缩包。

### 安装侧

`InstallFromPackage` 按**魔术字**识别（不看扩展名），路由到 PBClus 或 zip；
两条路在"解包到临时目录"之后汇聚到同一个 `InstallFromDir`，
所以清单校验、插件名检查、落库语义只有一套。
管理端 `source: local` 现在同时接受目录与包文件。

**zip 改名成 `.pbclus` 会被明确拒绝**，并直说"改后缀不会改变格式，
请用 pbclus-pack 重新打包" —— 用户改后缀的意图很明确，错误信息要正面回应它。

### 实现过程中发现的三个问题

**① `ExecPath(".")` 误报"entry 越界"（既有 bug）。**
`ExecPath` 用 `filepath.Clean` 算前缀，`dir="."` 时 base 是 `"."` 而 p 是 `"entry"`，
前缀判断失败。生产路径下 `PluginsDir/name` 是多段路径所以看不出来，
但 `pbclus-pack pack .` 这种人最自然的写法直接撞上。已改为两侧都 `filepath.Abs`
（与 `install.mustBeUnder` 的判定方式一致），并补回归测试 `TestExecPathWithDotDir`。

**② 打包工具自己又实现了一遍入口检查。**
写 `pack` 时顺手写了 `os.Stat(dir/entry)`，漏了 Windows 的 `.exe` 补全，
于是"`referer-guard.exe` 存在、清单写 `referer-guard`"被误报成"入口不存在"。
改成调用宿主自己的 `man.ExecPath()` —— 跨平台后缀解析只有一处实现，就必须只有一处。
**这是同一种错误第二次出现**（§17 的裸 string、§25 的重复 onCLI），
共同点都是"另写一遍已有逻辑"。

**③ `flag` 包不接受"位置参数后面跟选项"。**
`pack . -o out.pbclus` 报用法错误，而我的用法提示写的正是这种顺序。
Go 的 `flag` 在遇到第一个非 flag 参数后停止解析。没有要求用户改习惯，
而是加了 `reorderArgs`（按每个子命令的布尔选项表，把 flag 搬到位置参数之前）。

### 测试与反向验证

`pkg/plugin/pbclus` 的测试重点不是"正常路径能跑通"，而是**恶意容器能不能挡住**：
用 `rawContainer` 拼出"结构完全合法、摘要全部正确、但清单是构造出来的"容器，
再验证解包侧防线。这一手法本身抓到了一个测试脚手架 bug ——
早期 `rawContainer` 无条件覆盖 `EntryCount`，导致"清单声明 7 个条目但只列了 1 个"
那个用例被悄悄修好，安全检查从没被触发过。

恶意清单用例 16 个：父目录穿越、深层穿越、绝对路径、反斜杠、连续斜杠、
`.` 段、结尾斜杠、Windows 保留名（含 `dir/COM1.txt`）、重复路径、
文件与目录前缀冲突、声明大小超上限、偏移越出载荷、未知压缩方式、
摘要非十六进制、条目数与声明不符。

**反向验证（两层都要证明是承重的）**：
- 只停用格式层 `..` 检查 → 用例仍通过，因为解包时的前缀校验独立挡住了它（纵深防御生效）；
- **两层同时停用** → 目录穿越直接成功，`TestExtractDoesNotEscapeTargetDir` 与
  "父目录穿越"用例双双报红。

也就是说：两层都在承重，不是装饰。

### 验证结果

| 检查 | 结果 |
|---|---|
| `go build` / `go vet` / `go test ./...` | ✅ **17 个包全绿**（新增 `pkg/plugin/pbclus`、`cmd/pbclus-pack`） |
| pbclus 单测（往返 / 篡改 / 截断 / 追加 / 16 个恶意清单） | ✅ |
| 打包工具单测（12 个用例） | ✅ 含参数重排、退出码约定、默认排除、覆盖保护、非空目录保护 |
| 安装器集成（.pbclus 安装 / 损坏拒绝 / **zip 改名拒绝** / 内容优先于扩展名） | ✅ |
| `ExecPath(".")` 回归 | ✅ |
| **真机端到端**：打包 → 校验 → 管理端安装 → 进程拉起 | ✅ `smoke_plugin.sh` **53/53** |
| 魔术字断言（前 6 字节 = `PBClus`，第 7–8 = `1a0a`，前 2 字节 ≠ `PK`） | ✅ |
| `smoke_policy_config.sh` | ✅ 14/14 |
| 前端 `npm run build` + `check_i18n.py` | ✅ |
| 文档 | ✅ HTML §10/§11（+304 行，标签配平、16 个锚点全在）；新建 `PBClus-格式规范.md` |

### 遗留（如实记录）

- **v1 无签名**：校验只保证完整性，不防伪。多平台分发靠各平台各打一个包
  （工具在"包里只有 `.exe`、没有无后缀同名文件"时会警告 Linux 上会找不到入口）。
  → **本轮已解决，见第 27 节**。
- **`MaxPackageBytes` 与安装器统一为 64MB**：格式层原本写 512MB，
  与 `install.MaxZipBytes` 不一致 —— 两个数字会让人反复确认"到底哪个说了算"，
  已统一。插件二进制通常只有几 MB，64MB 很宽裕。
- **既有产品行为未改**：首次安装后不重启服务，插件接口全部 503。

---

## 27. PBClus v2：跨平台包 + 签名

第 26 节的两条遗留（"一个包只能给一个平台"、"没有签名"）本轮一起解决。
两件事都是格式变更，合并成一次版本升级（v1 → v2），而不是加两个可选字段。

### 为什么必须升版本，而不是"加个可选字段就行"

v1 的解析方**不认识平台字段**。如果只加字段不升版本，老宿主会把所有平台的
二进制都解到同一个路径上，最后写进去的那个胜出 —— 于是
**静默装上一个架构不对的二进制**，表现为"插件启用后立刻崩"或
"入口不存在"，完全没有线索指向真实原因。

升版本让老解析方**直接拒绝**（`版本 %d 不受支持`），而不是做错事。
这正是版本字段存在的意义：兼容性判断要在"能不能安全地读"这一层做，
而不是指望每个解析方都恰好忽略新字段。

读取侧支持 `1~2`（`MinReadVersion=1`）：v1 包的条目天然都是通用文件，
v2 的读法能完整覆盖它的语义，所以不需要维护两套解析逻辑。

### 跨平台

条目新增可选的 `platform` 字段（`<goos>/<goarch>`，空 = 通用），
清单新增 `platforms` 声明。**同一路径可以有多份不同平台的条目** ——
唯一性判定从"路径"改成"(路径, 平台)"，这是多平台包的核心机制。

安装器在**解包之前**就检查 `Supports(当前平台)`，不支持时给出：

```
这个插件包不支持当前平台：包内含 linux/amd64、windows/amd64，当前是 darwin/arm64。
用 pbclus-pack pack --targets darwin/arm64,... 重新打包即可做成跨平台包
```

顺序是刻意的：先解包再报"入口文件不存在"会把排查方向完全带偏 ——
那个错误看起来像"打包漏了文件"，真实原因是平台不匹配。

打包工具侧的原则是**不猜**：

| 情况 | 处理 |
|---|---|
| 入口不是原生可执行文件（脚本/字节码） | 通用包 |
| 入口是原生可执行文件，且与当前主机同类型 | 标注当前平台 |
| 入口是原生可执行文件，但**类型与主机不一致** | **报错**，要求显式 `--platform` |

最后一条是关键：在 Linux 上打一个 Windows 的 `.exe`，自动标成 `linux/amd64`
会让包带着错误信息流通出去。报错只让你多打一行参数，猜错的代价是装上一个
跑不起来的二进制 —— 这个交换明显划算。

`--targets a,b,c --build` 会**交叉编译到临时目录**再打包（不污染作者的源码目录），
一次产出一个真正跨平台的包。实测 referer-guard 三平台包：
9.9MB → 5.7MB（58%）。

平台后缀解析只有一处实现（宿主的 `ExecPath`）。打包工具**自己又写了一遍**
`os.Stat(dir/entry)`，漏了 Windows 的 `.exe` 补全，于是
"`referer-guard.exe` 存在、清单写 `referer-guard`"被误报成"入口不存在"。
**这是同一种错误第三次出现**（§17 裸 string 绕过加密、§25 重复的 onCLI），
共同点都是"另写一遍已有逻辑"。

### 签名

定长 108 字节的签名块，位于载荷之后、尾部之前，有无由头部 flags 决定：

```
偏移  长度  字段
   0     1  算法（1 = Ed25519）
   1     3  保留
   4     8  keyId（公钥 SHA-256 前 8 字节）
  12    64  签名
  76    32  公钥
```

定长是刻意的：Ed25519 的签名恒 64 字节、公钥恒 32 字节，所以"有没有签名"
一个 flags 位就够，不必为签名去改头部布局。

签名消息 = `"PBClus/v2 signed-package\n"` ‖ fileSHA。
签名块自己不在被签内容里（否则自指），所以"先算摘要再签名"没有循环依赖。
尾部 `fileSHA` 的覆盖右边界也随之明确为 `contentEnd`（不含签名块）。

### 这一段是全文最重要的设计说明：签名 ≠ 可信

公钥**内嵌在包里**。因此**任何人都能签一个"验证通过"的包** ——
用自己的密钥签，把公钥一起放进去，`Valid` 就是 true。

所以状态被拆成**三个独立字段**，不给"顺手用错"留空间：

| 状态 | 含义 | 能否用于安全决策 |
|---|---|---|
| 完整性 | 没损坏、没截断 | ❌ 攻击者能同步所有公开摘要 |
| `Valid` | 内容与签名自洽 | ❌ 任何人都能签出 Valid 的包 |
| `Trusted` | keyId 在宿主信任库里 | ✅ **唯一**能用于决策的 |

安装器只用 `Trusted` 判定。宿主**没有信任库时不会信任任何签名**
（不会退化成"信任所有签名"）。

最有力的证据在测试里：`TestSignatureCatchesWhatIntegrityCannot` 构造了
"攻击者改完内容后把所有**公开可算**的校验数据全部同步一遍"的包 ——
条目摘要、清单摘要、载荷摘要、头部 CRC、尾部整文件摘要全部重算 ——
此时 `Verify()` **通过**，而**只有签名会失败**。如果签名只是
"和摘要比一比"，这条用例就会漏掉。

签名策略 `off`/`warn`(默认)/`require`，配置项 `plugin.signatureMode`。
默认 warn 的理由：require 会让所有存量无签名插件在升级后立刻装不上，
而建立信任库需要管理员先操作一步。策略值写错时按 `require` 处理（fail closed）——
配置笔误不该悄悄降低安全门槛。

### 工具侧补齐信任管理的两端

签名只是密码学，**信任**才是安全语义所在。所以工具提供了两端：

```
pbclus-pack keygen -o key.json --name 发布者      # 发布者生成密钥
pbclus-pack pack ./myplugin --sign key.json       # 签名打包
pbclus-pack trust add --key key.json --name 发布者 # 管理员建立信任
pbclus-pack verify pkg.pbclus --trusted-keys trust.json
```

`verify` 在**没有**信任库时明确说"Ed25519 有效 —— 未提供信任库，
**无法判断是否可信**"，而不是含糊的"通过"。把"签名有效"讲成"可信"
是这类工具最危险的表述。

信任库文件填了 `publicKey` 就必须与 `keyId` 自洽 —— 否则会出现
"keyId 写的是可信发布者、公钥却是别人的"，等于信任了一个冒充者。

### 反向验证（两条新防线都证明是承重的）

| 停用哪一层 | 结果 |
|---|---|
| 平台检查 + 解包不按平台过滤 | ✅ 三个平台的用例**全部装错**，不支持的平台也被接受 |
| 把"未受信任"当成通过 | ✅ `require + 陌生密钥`、`require + 无信任库`、`warn` 的告警断言全部报红 |

第二条专门模拟了本类设计最常见的误用（用 `Valid` 代替 `Trusted` 做决策），
并证明测试能抓住它。

### 验证结果

| 检查 | 结果 |
|---|---|
| `go build` / `go vet` / `go test ./...` | ✅ **17 个包全绿** |
| pbclus 新增测试（多平台往返、平台判定、恶意平台清单、签名、篡改同步、信任库） | ✅ |
| 安装器新增测试（逐平台装对二进制、平台拒绝、9 种策略组合、无信任库、信任库文件） | ✅ |
| **真机跨平台打包**：三个平台真实交叉编译 → 5.7MB 单包 | ✅ |
| **真机端到端** `smoke_plugin.sh` | ✅ **65/65**（新增平台标注 + 签名全链路 + 篡改检测 + 信任库） |
| `smoke_policy_config.sh` | ✅ 14/14 |
| 前端构建 + i18n | ✅ |
| 反向验证 ×2 | ✅ 各自 FAIL |
| 文档 | ✅ HTML 已更新为 v2；规范 MD 同步中 |

### 遗留（如实记录）

- **签名不解决"第一次怎么建立信任"**：`keygen` 之后的公钥分发靠带外渠道
  （网站、仓库 README）。工具不提供密钥服务器 —— 那需要一整套 PKI，
  超出插件打包的范畴。keyId 是 8 字节，第一次比对时**必须**用完整公钥核验，
  只比 keyId 无法防止"同名不同钥"。
  → **本轮给了入口，见第 28 节**（管理端可加公钥；并新增只含公钥的分发文件）。
- **没有撤销机制**：信任库只能手工 `remove`。私钥泄漏后的补救是移除信任并
  重装插件。
  → **本轮解决，见第 28 节**（管理端可移除，接口里说明对已装插件的影响）。
- **多平台包只覆盖"入口 + dist/ 下的文件"**：其它文件一律视为通用。
  若某插件有平台相关的**非入口**文件（如 Linux 专用的 `.so`），
  放进 `dist/<平台>/` 即可 —— 该目录下的一切都归那个平台。
- **`zip` 路径不受签名策略约束**：zip 没有签名与平台概念，给它硬套策略
  会让存量插件突然装不上。要签名就走 PBClus。这一点在安装器测试里
  有明确断言（`require` 策略下 zip 仍可安装）。

---

## 28. 信任管理入口 + 一个"插件重启后消失"的既有 bug

上一节留的两条遗留（"第一次怎么建立信任"、"没有撤销机制"）其实是同一个
缺口：**签名在格式层做完了，但没有人能操作信任**。同时补掉的还有一个
在写这组测试时**撞出来的既有 bug**（比新功能本身更重要）。

### 28.1 顺手发现并修掉的既有 bug：安装侧与加载侧用了两个不同的数据目录

写信任库路径的测试时发现 `trustStorePath()` 与 `conf.DataDir()` 对不上，
顺着查下去发现根因不在信任库，而在**整个插件子系统**：

| | 用的路径 |
|---|---|
| **加载侧** `pluginset.InitPlugins` | `conf.DataDir()/plugins` |
| **安装侧** `controllers.pluginsRootDir()` | `dataDirOrCwd()/plugins` |

而 `dataDirOrCwd()` 只认环境变量 `NEBULA_DATA_DIR`，取不到就退回**进程工作
目录** —— 而 `NEBULA_DATA_DIR` **在整个仓库里从未被设置过**（我全仓 grep 过），
所以它实际上永远返回 cwd，把 `-data` 指定的数据目录完全忽略掉。

**后果不是"插件装不上"，而是更隐蔽的一种**：

- 安装 + 启用**当场完全正常**（控制器两处都用自己那套路径，自洽）；
- **重启服务后启动加载在另一个目录里找不到插件**，且没有任何错误提示；
- 管理员看到的是"插件昨天还好好的，今天启动就没了"。

**冒烟测试抓不到它**：脚本只在安装向导之后重启一次（那时还没有任何插件），
装完插件后又从不重启。所以这个 bug 能一直活着。

修法是把 `conf.DataDir()` 作为唯一事实来源；`NEBULA_DATA_DIR` 保留为
**显式覆盖**（容器部署里指向挂载卷是常见做法，那种场景下 cwd 与数据目录
本来就不同）。`conf.DataDir()` 是备份、密钥、数据库文件用的那个路径，
插件没有理由另搞一套。

**两条独立验证**：
1. 单元测试断言"安装侧算出的插件目录 == 加载侧算出的目录"
   （`TestPluginPathsAgreeWithLoader`）——修复前 FAIL，报
   `加载侧: <temp>/plugins / 安装侧: E:\nebula-drive\backend\controllers\plugins`；
2. 冒烟新增第 9.4 节：**启用一个插件 → 重启服务 → 断言它自动恢复运行**
   （`70/70`）。这条同时覆盖了"PBClus 安装 → 启用 → 重启后恢复"整条链路。

顺带说一句：我第一版 9.4 的断言**前提是错的** —— §7 结束时插件已被禁用、
§9 收尾也禁用了包安装的那个，所以重启前根本没有启用的插件，
断言测的是空气。冒烟当场报了 2 个 FAIL，我改成"先启用 → 再重启"才是有效断言。
**这类"前提不成立"的测试比没有测试更危险**，因为它会给出虚假的安心。

### 28.2 信任管理：从"手工放文件"变成可操作的动作

之前建立信任的唯一办法是把 `plugin-trust.json` 手工放进数据目录。能用，
但两个实际问题：管理员**看不到现状**；改策略要手改配置文件，
而写错一个值（`Require` 大写）会按 fail closed 处理，表现为"插件突然都装不上"，
原因很难找。

新增四个管理端接口（全部落审计）：

| 方法 | 路径 | 作用 |
|---|---|---|
| GET | `/api/admin/plugins/trust` | 策略 + 信任库内容 + 已安装插件的签名统计 |
| POST | `/api/admin/plugins/trust` | 加公钥（或 keyId） |
| DELETE | `/api/admin/plugins/trust/:keyId` | 移除（= 撤销） |
| PUT | `/api/admin/plugins/trust/mode` | 改策略 |

几条刻意的设计：

- **接口只接受公钥，不接受私钥**。发布者的密钥文件里同时含私钥与公钥，
  而"把你的密钥文件发给我"是极自然的说法 —— 一旦照做就是私钥泄漏。
  所以接口挡掉 64 字节的私钥输入，并明确说"这看起来是私钥，请用
  `*.pub.json` 里的 publicKey 字段"。同时 `keygen` **额外写一个只含公钥的
  分发文件**（`<名字>.pub.json`，0644 权限），让"该发哪个文件"没有歧义。
- **信任库坏了不许静默重建**（409 而不是覆盖）。静默重建会把管理员已有的
  信任关系一次抹掉；而且列表接口要如实把错误报出来，不能显示成"空库" ——
  后者会让管理员以为自己配的信任丢了。
- **切到 require 要二次确认**（409 + `needConfirm`），因为存量无签名插件
  会让后续所有安装被拒。这里有个自己踩的坑：第一版在提示里写了
  "带 confirm=true"却**没读这个字段**，导致该分支下 require 永远设不上 ——
  **提示与实现必须一致**，测试把它抓出来了。
- **只登记 keyId 是允许的，但要给出诚实提示**：keyId 是公钥的摘要，
  无法反向校验，挡不住"同一个 keyId 配了别人的公钥"。

### 28.3 签名状态必须落库（包文件安装后不保留）

插件包安装完就只剩解出来的文件，**签名状态事后无法从磁盘重新推导**。
所以新增两列（`Plugin.Signature` JSON 快照 + `Plugin.Trusted` 布尔）在安装
那一刻记下来，列表接口把它解析成 `signatureInfo` 供界面展示。

为什么不每次从 JSON 里现解析布尔值：**当时是否受信任**这个判定结果要被
冻结下来 —— 事后往信任库里加一个 keyId，不应改变历史记录。
这也是"审计"与"现状"的区别。

### 28.4 一处顺带的安全提示修正

`SetPluginSignatureMode` 采用"整份复制再改、只有 Save 成功才换内存配置"的
写法。如果就地修改 `Current()` 返回的对象再 Save，会出现
"写盘失败但内存里已经变了"的半状态 —— 表现为"界面显示已改、实际没生效、
重启后又变回去"。`conf.Save` 会把传入指针直接挂成 `current`，所以必须传副本。

### 验证结果

| 检查 | 结果 |
|---|---|
| `go build` / `go vet` / `go test ./...` | ✅ **17 个包全绿** |
| 信任 API 测试（8 个：加公钥/拒私钥/仅 keyId 警告/重复需 force/移除/坏库不覆盖/改策略/快照落库） | ✅ |
| 路径一致性回归（`TestPluginPathsAgreeWithLoader`、`TestTrustStorePathFollowsDataDir`） | ✅ 修复前双双 FAIL |
| **真机端到端** `smoke_plugin.sh` | ✅ **83/83**（新增 9.4 重启恢复 5 项、9.5 信任 API 13 项） |
| `smoke_policy_config.sh` | ✅ 14/14 |
| 前端签名与信任界面 | 见 §28.5 |

### 28.5 顺带修掉的第二个既有 bug：前端 `request()` 丢掉了错误响应体

给信任界面接 409 的二次确认时，前端同学发现 `client.ts` 的 `request()` 是这样的：

```ts
if (!res.ok) {
    const msg = (data && data.message) || res.statusText
    throw new Error(...)          // ← 只有 message，结构化字段全丢了
}
```

而 `Plugins.tsx` 里有 **5 处**在读 `e?.data?.needAgreement`：

```
Plugins.tsx:121/146/166/187/211   if (e?.data?.needAgreement) return handleNeedAgreement()
```

`data` 从来没被挂到错误对象上，所以这 5 处**全是死代码**。后果是真实的：
后端在插件协议未接受时返回 403 + `needAgreement`（`plugins.go:213/535`），
而前端把响应体一丢，只能显示一条普通错误 —— **协议弹窗永远不会弹出来**。
管理员看到的是"启用失败"，而不是"请先阅读并同意协议"。

**后端是对的，冒烟也是对的**（它直接断言 403），所以这个缺陷在服务端视角下
完全不可见 —— 又是一次"端到端测到了后端，没人测前端"。

修法是把 `status` / `data` / `code` 挂到 Error 上（纯增量：只读 `.message`
的调用方行为不变）。我核对了影响范围：全前端读 `e?.data?.` 的只有 Plugins.tsx
这 7 处（另 3 处在 `Share.tsx`，用的是 axios 风格的 `e?.response?.data?.message`，
本来就有 `e?.message` 兜底，不依赖这次修复）。

顺带说明：新加的信任界面也因此才能工作 —— 切到 require 时的 409
（`needConfirm` / `unsigned`）走的是同一条路径。

### 验证结果

| 检查 | 结果 |
|---|---|
| `go build` / `go vet` / `go test ./...` | ✅ **17 个包全绿** |
| 信任 API 测试（8 个：加公钥/拒私钥/仅 keyId 警告/重复需 force/移除/坏库不覆盖/改策略/快照落库） | ✅ |
| 路径一致性回归（`TestPluginPathsAgreeWithLoader`、`TestTrustStorePathFollowsDataDir`） | ✅ 修复前双双 FAIL |
| **真机端到端** `smoke_plugin.sh` | ✅ **83/83**（新增 9.4 重启恢复、9.5 信任 API 13 项） |
| `smoke_policy_config.sh` | ✅ 14/14 |
| 前端 `npm run build` + `tsc --noEmit` + `check_i18n.py` | ✅ |
| 前端失败路径的**运行时**验证 | ⚠ 见下 |

### 遗留（如实记录）

- **前端只做了静态验证**（构建 + 类型 + i18n key 对称 + 产物里能查到文案），
  **没有在浏览器里真实点过** 409 确认回路、私钥 400 的渲染、徽标配色。
  它的后端契约已经由冒烟 9.5 在真实服务器上验证过，但"点击 → 交互"这一段
  仍是未验证的。
- **`NEBULA_DATA_DIR` 仍然是个"只被读、从未被写"的环境变量**。修完之后
  路径一致性不再依赖它（默认走 `conf.DataDir()`），但它作为显式覆盖
  依然有效 —— 我用"全仓 grep 无写入点"证明了它在正常部署里不起作用，
  这一点值得将来要么补上文档、要么去掉。
- **移除信任不会自动处理已安装的插件**：新安装会被拦，但已经装上的照旧运行。
  这是刻意的（决定权留给管理员），接口响应里也写明了这一点。
- **界面用 `^[0-9a-fA-F]{16}$` 启发式区分 publicKey 与 keyId**。
  依据是 keyId 恒为 16 个十六进制字符；若将来 keyId 长度变化，
  这个判别会把 keyId 当公钥发出去（后端会回"公钥不是合法 base64"）。
- **粘贴整个 `*.pub.json` 文件（而不是其中的 publicKey 字段）会失败**，
  界面没有对应引导文案。这是**刻意不做**自动抽取的：自动抽取会顺带让
  粘贴私钥文件也成功，正好毁掉后端想教的那一课。
- **信封加密/密钥轮换**不在范围内：信任库是明文 JSON（公钥本来就要公开），
  私钥的保管责任在发布者。

---

## 29. 四个能用的插件（覆盖 6 个此前没有示例的钩子）

之前仓库里的示例插件只有三个（referer-guard / smtp-email / hmac-auth），
覆盖 `onAntiLeech`、`onEmail`、`onApiAuth`。其余钩子**一个可运行示例都没有** ——
对插件作者来说等于"这些钩子怎么用，自己看源码猜"。

本轮补了四个，都是能直接用的（不是骨架），并各自覆盖一个新钩子：

| 插件 | 钩子 | 干什么 |
|---|---|---|
| `notify-webhook` | `onEmail` + `onCLI` | 把站内通知转发到 Slack / 飞书 / 钉钉 / Discord / 通用 webhook |
| `backup-mirror` | `onBackup` + `onRestore` + `onCLI` | 备份包的异地镜像 + 轮转 + 恢复审计 |
| `download-limiter` | `onAntiLeech` + `onCLI` | 按 IP 的滑动窗口下载限流，防批量抓取 |
| `collab-redirect` | `onCollabOpen` + `onCLI` | 把协作编辑 302 跳转到 OnlyOffice / Collabora |

四个都实现了 `onCLI`，所以都能用 `nebula plugin <name>` 做自检 ——
这既是运维手段，也顺便把 `onCLI` 从"只有测试夹具用过"变成"有真实示例"。

### 统一的设计约束

- **只用标准库**，`go.mod` 里没有 require。插件是独立 module，
  引入依赖就等于让用户为了一行功能去下载一堆包。
- **stdout 只放 RPC**，日志一律 stderr（混用会破坏分帧，插件直接失联）。
- **ctx 里的数字一律按 float64 读**（JSON 往返后的形态），断言成 int 会静默失败。
- 失败要么诚实失败（`notify-webhook` 未配置时退出码 1 并说明怎么配），
  要么 fail-open 但记日志（`backup-mirror` 复制失败不阻塞宿主）——
  不允许"静默成功"。

### 几个值得单独记下来的实现取舍

**`backup-mirror`**
- 轮转**只删匹配 `^backup-\d+-[0-9a-f]{8}\.zip$` 的文件**。target 目录是用户
  自己选的，很可能还放着别的东西 —— 一个"清理旧备份"的工具顺手删掉用户的
  文件是不可接受的。轮转时还用 `ReadDir` + `Type()&os.ModeSymlink` 跳过符号链接，
  防止有人在 target 里放个指向 `/etc` 的链接让插件去删系统文件。
- 写入走 `.part` → `fsync` → 重算摘要 → `rename`，校验不过就删文件且**不计入轮转**。
- `keep` 缺省是 0（不轮转）而不是 7：Go 零值无法区分"没写"与"显式 0"，
  而默认删旧备份的风险明显高于默认不删。

**`download-limiter`**
- 用**滑动窗口**而不是计数器清零。固定窗口在边界上会放过 2 倍流量
  （窗口末尾打满 + 下个窗口开头打满），这是限流实现最常见的错。
- 有 `maxTrackedIPs` 上限并淘汰最久未活动的 IP。**这不是优化而是安全要求**：
  没有上限时，攻击者用海量伪造 IP 就能把限流器的内存打爆 —— 限流器自己成了 DoS 面。
- 只在拦截时记日志。下载是高频操作，每次都记会淹掉管理端的 500 条日志缓冲。

**`collab-redirect`**
- 占位符替换的值**必须 URL 转义**：文件名里有 `&`、空格、中文、`#` 都是常态，
  不转义会破坏查询串甚至造成参数注入。测试里用 `报告 & 附表.docx` 验证。
- **不匹配就必须什么都不写**。宿主只要看到 `ctx["url"]` 非空就 302，
  乱写会把本站自己的 md/txt 协作也劫持掉。
- 渲染结果必须校验是 http/https 且有 host，否则不跳转 ——
  配置写错时 302 到 `javascript:` 之类的地址是真实风险。

**`notify-webhook`**
- 拒绝 HTTP 重定向（3xx 视为失败）：webhook 入口通常不带重定向，
  跟着跳转只会把载荷发到意料之外的地方。
- 日志里 URL 脱敏成 `scheme://host/…`。webhook URL 往往**自带凭据**
  （Slack 的 token 就在路径里），而插件日志会展示在管理端。

### 验证：我自己重跑，不看子代理的自检报告

四个插件是并行委派写的，所以每一项都独立复核过：

| 检查 | 结果 |
|---|---|
| 逐个 `go vet` / `go test -count=1` | ✅ 全过（12/18/12/11 个测试函数，共 100+ 用例） |
| `go.mod` 无第三方依赖 | ✅ 四个都是"仅标准库" |
| 后端 `TestExampleManifests` 自动校验清单 | ✅ 校验了 **7** 个插件清单 |
| **真机安装 → 启用 → 运行** | ✅ 四个都 running + 真实 pid |
| **钩子真的注册**（查 `outProcess`，不是只看钩子列表） | ✅ onEmail/onBackup/onRestore/onCollabOpen/onAntiLeech/onCLI 各有进程外 handler |
| **重启后自动恢复** | ✅ 四个都回来了 |
| **`onCLI` 真实往返**（宿主→子进程→ctxPatch→打印） | ✅ 三个带输出且内容对得上；notify-webhook 未配置时**诚实失败**（退出码 1 + 说明） |
| 打成三平台 `.pbclus` 并校验 | ✅ 四个都成功（各 ~5-14MB，含 windows/linux/darwin） |
| 前端/宿主全量回归 | ✅ 17 包全绿；`smoke_plugin.sh` **127/127**；policy smoke 14/14 |

CI 的"示例插件检查"顺手从**写死列表**改成动态枚举 `examples/plugins/*/`：
写死的话新加的插件不会被检查，而"加了插件但 CI 没覆盖"这件事在绿色流水线里
完全看不出来（这个仓库已经发生过一次脚本腐坏没人发现，见 §18）。

### 我自己踩的两个坑（记下来因为都是同类错误）

1. **第一版冒烟断言假设了"插件已配置"**。`notify-webhook` 未配 webhook 时
   退出码是 1、`collab-redirect` 未配 `editorURL` 时不会渲染示例 ——
   这两条都是**正确的**行为，错的是我的断言。改成"给真插件写真实配置后
   再断言功能"，并单独保留一条"未配置时诚实失败"的断言。
2. **配置 JSON 里我把 `&` 转义成 `\&`**，整份配置不是合法 JSON，接口 400。
   JSON 里 `&` 不需要转义。断言当场抓到了它 —— 如果只断言"状态码不是 500"
   这种松口径，就会漏过去。

### 遗留（如实记录）

- **`notify-webhook` 未接真实 IM 平台联调**：只验证了各格式的载荷、
  重试与脱敏（httptest）。飞书/钉钉的**加签**未实现，需要改用关键词或 IP 白名单。
- **`backup-mirror` 的 `onBackup` 在 fire 处理内同步复制**：宿主 `FireTimeout` 是 2s，
  大备份包会先让宿主记一条超时警告（复制不会被中断，之后仍完成）。
  要"立即返回、后台复制"需要任务队列，这版优先保证行为与测试的确定性。
- **`download-limiter` 的计数在内存里**：重启清零；多副本部署时各副本独立计数，
  实际总限额是 N 倍。要跨副本精确限流得用共享存储，超出插件能力范围。
- **`collab-redirect` 只做跳转，不做鉴权**：外部编辑器必须自己能校验访问权限，
  否则等于把文件公开。这一点在 README 里写在显眼位置。
- **`openInNewWindow` 无法真正生效**：宿主固定执行 302，浏览器必然在当前标签页打开。
  该字段只解析并展示 —— 与其假装支持，不如写在边界里。














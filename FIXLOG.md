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

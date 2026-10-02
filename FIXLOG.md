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
| — | 测试驱动与生产不一致（且需 CGO，测试跑不起来） | 严重 | ✅ 已修 | `pkg/testutil/db.go` |
| — | 测试连接不关闭，Windows 上清理失败致误判 FAIL | 中 | ✅ 已修 | `pkg/testutil/db.go`、`pkg/db/db_test.go`、`controllers/install_test.go` |
| — | `db.Init` 覆盖全局实例时泄漏旧连接池 | 中 | ✅ 已修 | `pkg/db/db.go` |

> 验证状态：`go build ./...` ✅ ｜ `go vet ./...` ✅ ｜ `go test ./...` ✅ 全部通过

**待办（下一批）**：P2-7（`TwoFactor` / `Token` / `ClientSecret` / `Policy.Config` 敏感字段加密，需兼容旧明文迁移的 GORM hook）。

> P1-3、P1-4 均已在本轮修完（见下方说明）。

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

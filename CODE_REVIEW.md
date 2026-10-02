# NebulaDrive 后端安全与架构审查报告

> 审查对象：`github.com/mrc-sk/nebula-drive` @ `main`（clone 深度 50）
> 审查范围：`backend/` 全部 74 个 Go 文件（12,989 行），重点为 controllers / middleware / filesystem / webdav / pkg / conf
> 审查方式：全量逐文件精读（只读，未修改任何代码）
> 副署日期：2026-10-01

---

## 0. 总体结论

**一句话**：这是一个架构思路正确、工程完成度远超预期、但**安全边界系统性缺失**的项目。功能铺得很广，但在"谁能访问什么"这件事上，几乎所有关键路径都存在缺口。

先说做得好的，因为这些是真的好：

| 亮点 | 评价 |
|---|---|
| **引用计数（FileObject + releaseLegacy 兜底）** | 全项目最扎实的一块。UPSERT 风格自增、唯一键冲突回退、旧数据按 files 表计数保守删除、启动时 backfill——这是**教科书级的实现**，考虑到了升级路径和数据安全。 |
| **数据模型设计** | `File.IsDir` 统一目录树、`FileObject` 分离物理对象、`FileVersion` 归档、`Setting` KV、`AuditLog`，边界清晰，没有过度设计。 |
| **存储抽象层（filesystem.Handler）** | 接口只有 7 个方法，工厂注册，加后端就是填 handler。SFTP/远程 WebDAV 也接进来了，扩展性验证过。 |
| **配置加密** | AES-GCM + 独立 secret.key + 0600 权限 + env/ini 分离（敏感 vs 非敏感），这套设计是对的。 |
| **限流/封禁的工程细节** | 滑动窗口实现无内存泄漏（`kept := ts[:0]` 原地复用）、登录失败 5 分钟 10 次自动封禁、验证码阈值递进。 |

**但**：以上全部是"内部机制正确"，而**外部边界（鉴权、授权、路径、网络）几乎是空的**。下面按严重级排列。

---

## 1. P0 — 必须立即修（可导致数据泄露 / 越权 / 服务不可用）

### P0-1 WebDAV 存在路径穿越，可读写服务器任意文件 ⚠️ 最严重

**位置**：`filesystem/fs.go` 的 `Local.Put/Get/GetRange/Delete/Size/Copy`，全部使用 `filepath.Join(l.Root, name)` 且**零校验**。

**触发链**：
```go
// webdav/webdav.go:474-495
ext := path.Ext(remaining)                      // remaining 来自 URL 路径，完全用户可控
srcName := fmt.Sprintf("dav-%d-%s", time.Now().Unix(), remaining)  // 未过滤 ../ 
h.Put(c.Request.Body, srcName, size)            // → filepath.Join(root, "dav-...-../../etc/x")
```

`WebDAV PUT` 的 `remaining` 直接由 URL 路径推导而来。虽然 `resolvePath` 里做了 `path.Clean("/"+p)`，但：
1. `Clean` 只处理 `/` 分隔符，**不处理 Windows 的 `\`**；
2. 关键点：`remaining` 是**未匹配到的剩余段**，攻击者构造 `/dav/a/../../../../tmp/evil.sh` 时，`Clean` 会把它规整成 `/tmp/evil.sh`，此时 `resolvePath` 从根查找 `tmp` → 找不到 → 返回 `remaining = "tmp/evil.sh"`，`parentOf(nil) = nil` → **文件被写到 uploads/tmp/evil.sh 之外**。

更直接的：文件名里的绝对路径成分在 Windows 下 `filepath.Join("uploads", "C:\\Windows\\x")` 的行为需要实测，但 `..\` 序列在 Windows 下**必然穿越**，因为 `filepath.Join` 用的是 `filepath.Separator`，而 URL 里可以用 `%5C` 编码后经 `url.PathUnescape` 还原出 `\`。

**影响**：
- 攻击者（任意有 WebDAV 权限的普通用户）可**写入任意路径**——写 SSH authorized_keys、写 cron、写 Web 目录 getshell；
- 可**读取任意文件**（`GET /dav/../../../../etc/passwd`）——注意 `getOrHead` 走的是 `h.Get(node.SourceName)`，`SourceName` 来自 DB，这条相对安全；但 `put` 的 `srcName` 是明文拼接的，写入后这条记录进了 DB，后续 GET 就能读回来。**等于开放了一个任意文件写入 + 读取通道**。

**修复**（必须做的三件事，缺一不可）：
```go
// 1. filesystem/fs.go —— 增加统一的安全 join，所有 handler 强制走它
func safeJoin(root, name string) (string, error) {
    // 归一化分隔符，杜绝 Windows \ 绕过
    clean := strings.ReplaceAll(name, "\\", "/")
    p := filepath.Join(root, filepath.FromSlash(clean))
    absRoot, err := filepath.Abs(root)
    if err != nil { return "", err }
    absPath, err := filepath.Abs(p)
    if err != nil { return "", err }
    // 必须严格是 root 的子路径
    if absPath != absRoot && !strings.HasPrefix(absPath, absRoot+string(filepath.Separator)) {
        return "", errors.New("path traversal detected")
    }
    return absPath, nil
}
// 然后 Put/Get/GetRange/Delete/Size/Copy 里所有 filepath.Join 换成 safeJoin

// 2. webdav/webdav.go put() —— 拒绝非法字符
if strings.ContainsAny(remaining, `/\`) || remaining == "." || remaining == ".." {
    c.String(400, "Invalid filename"); return
}
// srcName 不要拼接用户输入，改为纯 UUID + ext，与 HTTP 上传保持一致
srcName := util.UUID() + path.Ext(remaining)
```

**注意**：`filesystem/fs.go` 里 `Local` 是唯一的"裸奔" handler；S3/OSS/COS 因为 key 是扁平 namespace 不受影响，但 **SFTP handler 和远程 WebDAV handler 同样是 `path` 拼接，必须一起检查**（`filesystem/sftp.go`）。

---

### P0-2 WebDAV PUT 覆盖文件时直接返回 200 且丢弃上传内容

**位置**：`webdav/webdav.go:437-446`

```go
if remaining == "" {
    if node.IsDir { c.String(409, "Is A Directory"); return }
    _ = node
    // 直接新增一个同路径同名文件，旧的保留
    c.String(200, "OK")     // ← 上传的 body 被完全丢弃，也没写任何东西
    return
}
```

**影响**：这是**数据静默丢失**。任何 WebDAV 客户端（Windows 资源管理器、Finder、RaiDrive）覆盖已有文件时，客户端收到 200 认为成功，**实际内容零写入**。用户会以为保存成功。README 宣称"WebDAV RFC 4918 全方法实现"——这是不成立的。

而且注释里写的"直接新增一个同路径同名文件"**代码里也没做**，只是 `_ = node` 后返回 200。

**修复**：
```go
if remaining == "" {
    if node.IsDir { c.String(409, "Is A Directory"); return }
    // 走真正的覆盖：归档旧版本 → 写新内容 → 更新 File
    // 最简可行：删除旧物理引用后按新文件写入，或直接复用 UploadVersion 的逻辑
    return overwriteFile(c, u, node)   // 需实现
}
```

---

### P0-3 WebDAV PUT/MOVE 完全绕过上传白名单与配额

**位置**：`webdav/webdav.go:431-496`、`513-549`

对比 HTTP 上传（`controllers/file.go:221-226`）有四道检查：扩展名白名单、魔术字节、配额、审计日志。WebDAV `put` **只有配额检查**（还没走 `ensureQuota`，是自己手写的一份，逻辑不一致）：

```go
// webdav.go:457-468 —— 手写配额，用 u.GroupID 的 MaxStorage
if g.MaxStorage != -1 { ... }
```
但 HTTP 路径用的是 `effectiveMaxStorage`（**套餐额度优先**，见 `file.go:69-84`）。两套配额逻辑并存 → WebDAV 用户可绕过套餐限额。

同时**没有扩展名白名单**，所以：
- 直接 PUT 一个 `shell.php`、`x.exe` → 落盘到 Web 可访问目录（结合 P0-1 更危险）
- 无魔术字节校验

**修复**：把 `put` 改为调用 `controllers` 里同一套 `ensureQuota` + `isAllowedExtension` + 魔术字节检查。**抽出一个 `internal/service/upload.go` 供 HTTP 与 WebDAV 共用**，别再手写第二份。

---

### P0-4 WebDAV 实现了 LOCK/UNLOCK 但返回 200 空响应

**位置**：`webdav/webdav.go:259-260`

```go
case "COPY", "PROPPATCH", "LOCK", "UNLOCK":
    c.Status(200)
```

这是**假实现**。协议上 `LOCK` 必须返回 `Lock-Token` 头和 XML body（含 `lockdiscovery`），否则：

- **macOS Finder 挂载后会反复报错或直接只读**（Finder 强依赖 LOCK）
- **Microsoft Office 直接保存失败**（Office 保存前必须 LOCK）
- Windows 资源管理器会出现"无法创建文件"

`OPTIONS` 里还宣告了 `DAV: 1,2`，其中 `2` 就代表支持 LOCK。**宣告了但没实现，比不宣告更糟**——客户端会信任这个声明然后失败。

**修复**（二选一）：
- **方案 A（推荐，工作量大）**：实现最小可用的 LOCK/UNLOCK（内存 or DB 存 lock token + 超时），或者直接引入 `golang.org/x/net/webdav` 作为底层。
- **方案 B（务实）**：OPTIONS 里降级为 `DAV: 1`，移除 LOCK/UNLOCK 声明，并在 README 明确"不支持 Office/Finder 直接保存"。

**顺带**：`COPY` 和 `PROPPATCH` 返回 200 也是假的。COPY 应该真的复制，PROPPATCH 至少返回 207。

---

### P0-5 全局 CORS `AllowAllOrigins: true` + `AllowHeaders: ["*"]`

**位置**：`routers/router.go:30-35`

```go
cors.New(cors.Config{
    AllowAllOrigins:  true,
    AllowHeaders:     []string{"*"},
    AllowCredentials: false,
})
```

`AllowCredentials: false` 确实挡住了"带 Cookie 的跨站读取"这一最经典场景，**这是唯一的救命稻草**（因为 token 也叫 `nebula_token` cookie，但跨站读响应会被浏览器拦截）。

但仍有实际风险：
1. `Authorization: Bearer <pat>` 是**显式头**，不受 cookie 同源策略保护。任何恶意页面都能用受害者浏览器上用户可能粘贴过的 PAT 发请求——虽然攻击者拿不到 token 值，但如果用户在浏览器里存了 PAT（比如某插件），**任意站点可代发请求并读取响应**。
2. 全部 API（含 `/api/admin/*`）对所有源开放。
3. `AllowHeaders: ["*"]` 允许自定义头，配合 P0-1 更容易构造攻击。

**修复**：
```go
// 从 settings 读白名单，默认只允许同源
allowed := allowedOrigins()   // 例如 []string{cfg.System.Domain}
if len(allowed) == 0 {
    // 全封闭：仅同源（浏览器自动放行同源，无需 CORS 头）
    r.Use(cors.New(cors.Config{AllowOrigins: []string{}}))
} else {
    r.Use(cors.New(cors.Config{AllowOrigins: allowed, AllowHeaders: []string{"Authorization","Content-Type","X-Confirm-Password","X-Share-Pwd","X-Share-Extract"}}))
}
```

---

### P0-6 离线下载是标准的 SSRF + 无限制资源消耗

**位置**：`controllers/task.go`

```go
func downloadBytes(url string) ([]byte, error) {
    resp, err := http.Get(url)          // ← 任意 URL，无校验
    ...
    return io.ReadAll(resp.Body)        // ← 全量读进内存，无大小上限
}
// runHTTPTask 里同样是 http.DefaultClient.Get(url)，无校验、无超时
```

三个独立问题：

1. **SSRF**：普通用户可让服务器请求 `http://127.0.0.1:5212/api/admin/...`、`http://169.254.169.254/latest/meta-data/iam/security-credentials/`（云元数据，**直接偷 AK/SK**）、内网 Redis/MySQL 端口。这是**云环境下最危险的漏洞之一**。
2. **内存打爆**：`downloadBytes` 的 `io.ReadAll` 无上限。用户提交一个 50GB 的 "torrent" 链接（实际返回巨量内容）→ **OOM 崩溃**。
3. **超时缺失**：`http.Get` 用 `DefaultClient`，无 `Timeout`，慢速攻击（slowloris 式）可长期占用 goroutine。

**修复**：
```go
func safeHTTPClient() *http.Client {
    return &http.Client{
        Timeout: 30 * time.Second,
        CheckRedirect: func(req *http.Request, via []*http.Request) error {
            if len(via) >= 3 { return errors.New("too many redirects") }
            return validateURL(req.URL)   // 每次跳转都校验
        },
        Transport: &http.Transport{DialContext: safeDialContext},
    }
}

func validateURL(u *url.URL) error {
    if u.Scheme != "http" && u.Scheme != "https" { return errors.New("bad scheme") }
    ips, err := net.LookupIP(u.Hostname())
    if err != nil { return err }
    for _, ip := range ips {
        if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
           ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
            return errors.New("internal address blocked")
        }
    }
    return nil
}

// 下载限流：io.LimitReader(resp.Body, maxTorrentSize)   // 例如 10MB
// safeDialContext 里也要做一次 IP 校验，防 DNS Rebinding
```
**另外**：BT/磁力下载本质上无法用 URL 白名单防——建议**默认关闭离线下载**，或限制为管理员 + 明确的风险提示 + 独立低权限运行（容器 + 无云元数据访问）。

---

## 2. P1 — 高优先级（功能错误 / 一致性缺陷 / 越权面）

### P1-1 `Breadcrumb` 完全没有鉴权 —— 可遍历任意用户文件树

**位置**：`controllers/file.go:617-633`

```go
func Breadcrumb(c *gin.Context) {
    id, _ := strconv.Atoi(c.Param("id"))
    var crumbs []models.File
    cur := uint(id)
    for cur != 0 {
        var f models.File
        if err := db.Get().First(&f, cur).Error; err != nil { break }
        crumbs = append(crumbs, f)      // ← 无 owner 校验！
        ...
    }
}
```

路由是 `files := api.Group("/files", middleware.Auth(true))`，所以**只要求登录，不要求归属**。任意登录用户传 `id=1,2,3...` 就能**枚举出其他所有用户的目录名与结构**（`File` 结构体直接 JSON 序列化，`OwnerID`、`Size`、`SourceName`、`MimeType` 全部泄露）。

**修复**：
```go
u := middleware.CurrentUser(c)
...
if !u.IsAdmin && f.OwnerID != u.ID { break }   // 或返回 403
```

---

### P1-2 `List` 接口的 `parent` 参数越权 + `Search` 的 JOIN 泄露

**位置**：`controllers/file.go:105-130`、`controllers/search.go:99-116`

`List` 里加了 `owner_id = ?` 条件 ✓，**但没校验 `parentID` 是否属于该用户**——不泄露数据（因为 owner 过滤仍在），但会误导性返回空列表，属轻微问题。

**真正的问题在 `Search`**：
```go
// search.go:99-103
if tagParam := c.Query("tag"); tagParam != "" {
    q = q.Joins("JOIN file_tags ON file_tags.file_id = files.id").
        Where("file_tags.tag IN ? AND file_tags.owner_id ?", tags, u.ID)
    //                                    ↑ 这里写的是 "owner_id ?"，缺了 "=" ！！！
}
```
**`file_tags.owner_id ?` 是 SQL 语法错误**（GORM 会尽力拼，行为未定义），应该是 `file_tags.owner_id = ?`。按标签搜索功能**当前是坏的**。

```go
// search.go:113-116
if c.Query("share") == "1" {
    q = q.Joins("JOIN shares ON shares.file_id = files.id").
        Where("shares.deleted_at IS NULL")     // ← 没有 shares.owner_id 过滤
}
```
JOIN shares 后**只过滤 `deleted_at IS NULL`**，虽然外边有 `owner_id = ?`（针对 files 表），所以不直接越权，但如果某个文件的 sharing 记录被另一个用户的 file_id 碰撞……实际上 `files.owner_id` 已限制，风险可控。**仍建议加 `shares.owner_id = ?` 显式约束**。

---

### P1-3 `SaveSearchHistory` 的清理 SQL 在 MySQL 下会报错

**位置**：`controllers/search.go:207-208`

```go
db.Get().Where("user_id = ? AND id NOT IN (SELECT id FROM search_histories WHERE user_id = ? ORDER BY id DESC LIMIT 20)", u.ID, u.ID).
    Delete(&models.SearchHistory{})
```
MySQL 不允许在 DELETE 的子查询里引用**同一张表**（错误 1093），必须包一层派生表。SQLite 可以。**跨库兼容性 bug**。

**修复**：
```sql
-- MySQL 需改为：
DELETE FROM search_histories WHERE user_id = ? AND id NOT IN (
  SELECT id FROM (SELECT id FROM search_histories WHERE user_id = ? ORDER BY id DESC LIMIT 20) t
)
-- 或者干脆用 GORM 分两步：先查出保留的 id 列表，再 Delete("id NOT IN ?")
```

---

### P1-4 缺事务：所有"多步写入"都可能产生中间状态

全项目搜索 `db.Get().Transaction(` **零命中**。以下操作全部是裸的多条语句：

| 位置 | 问题 |
|---|---|
| `file.go:185-193`（Rapid 秒传） | `Create(File)` 与 `Retain(FileObject)` 分离，若 Retain 失败 → File 存在但无引用计数 → **后续删除不会释放物理文件（泄漏）** |
| `file.go:299-307`（Upload） | 同上 |
| `file.go:1018-1039`（UploadVersion） | 归档版本 + 更新 File + Retain，任一失败即数据不一致 |
| `file.go:1148-1169`（RestoreVersion） | 归档 + 更新 + Retain + Delete(version)，四步无事务 |
| `install.go:41-73`（Install） | 建表 → 建组 → 建管理员 → 存配置，**中途失败留下半装状态**，而 `conf.Save` 是最后一步——如果 Save 失败，DB 里已有数据但 `IsInstalled()=false`，**下次启动会重新走安装，重复建表/建用户冲突** |

**修复**（以秒传为例）：
```go
err := db.Get().Transaction(func(tx *gorm.DB) error {
    if err := tx.Create(&f).Error; err != nil { return err }
    if err := storage.RetainTx(tx, exist.PolicyID, exist.SourceName, exist.Size, exist.Hash); err != nil { return err }
    return nil
})
if err != nil { /* 回滚，返回错误 */ }
```
注意 `storage.Retain/ReleaseObject` 目前硬依赖 `db.Get()`，**需要改造为接受 `*gorm.DB` 参数**（保留无参版本做兼容）。

---

### P1-5 配额检查存在 TOCTOU 竞态，可超额上传

**位置**：`file.go:88-102` + 所有调用点

```go
func ensureQuota(userID uint, additionalBytes int64) error {
    // 1. 读 u.Storage
    // 2. 比较 u.Storage + additional > max
    // 3. 返回 ok
}
// 之后才 addStorage()  ← 中间有窗口
```

并发场景：用户余额 9GB / 限额 10GB，同时发起 3 个 1GB 上传 → 三个请求都读到 9GB 都通过 → 最终 12GB，**超额 20%**。重复几十次即可无限膨胀。

**修复**：用原子的条件更新代替"读-判断-写"：
```go
res := db.Get().Model(&models.User{}).
    Where("id = ? AND (storage + ?) <= ?", userID, delta, maxStorage).
    UpdateColumn("storage", gorm.Expr("storage + ?", delta))
if res.RowsAffected == 0 { return errors.New("quota exceeded") }
```
把"占额度"和"校验"合并成一条原子 SQL。**但这需要重构 `addStorage` 的调用顺序**（目前是先干活后加额度）。

---

### P1-6 分片上传的并发 map 访问 + 无过期清理

**位置**：`file.go:651-771`

```go
chunkUploadsMu.Lock()
meta, ok := chunkUploads[uploadID]
chunkUploadsMu.Unlock()
...
chunkUploadsMu.Lock()
meta.Chunks[idx] = chunkPath    // ← meta 是 *chunkUploadMeta，锁外修改
chunkUploadsMu.Unlock()
```
虽然加了锁，但 `meta` 是指针，**锁保护的是 map 而不是 meta 内部字段**。`ChunkMerge` 里 `meta.Chunks[i]` 的读发生在锁外（`for i := 0; i < meta.ChunkCount; i++ { cp, ok := meta.Chunks[i] }`），与 `ChunkUpload` 的写并发 → **data race**（`go test -race` 会报）。

另外：
- `chunkUploads` **永不清理**。中断的上传会话永久留在内存 map 里 → 内存泄漏。
- `Chunks` 里的临时文件也不会清 → **磁盘泄漏**（`uploads/chunks/` 无限增长）。
- `meta.Size` 是**用户自己声明的**（`chunkInitReq.Size`），合并后直接 `addStorage(meta.Size)`。用户声明 1KB 实际上传 10GB → **配额被绕过**。应该用 `mergedSize`。

**修复**：
```go
// 1. 给 meta 加自己的 mutex，或者用 sync.Map + 值语义
type chunkUploadMeta struct {
    mu sync.Mutex
    ...
}
// 2. 加 janitor goroutine：每小时清理 CreatedAt 超过 24h 的会话及其临时文件
// 3. mergedSize 从实际写入字节数统计（tee reader 计数），不用 meta.Size
```

---

### P1-7 `CancelTask` 双重 close panic

**位置**：`task.go:400-415` + `runHTTPTask:354`

```go
// CancelTask
if ok {
    if j.gid != "" { _ = aria2.GetManager().Remove(j.gid) }
    close(j.cancel)          // ← 若任务刚结束（defer 已 delete dlJobs）则 ok=false，安全
}
```
竞态：`ok` 为 true 但任务 goroutine 同时在 `defer` 里 `delete(dlJobs, id)`。若两个并发 `CancelTask` 请求同时命中同一个 job（`ok=true` 都成立，因为 delete 还没执行）→ **两次 `close(j.cancel)` → panic: close of closed channel → 整个进程崩溃**。

`gin.Recovery()` 能捕获并返回 500，但 panic 期间该 goroutine 的其他工作丢失。

**修复**：用 `sync.Once` 包住 close，或改用 `context.CancelFunc`：
```go
type activeJob struct {
    taskID uint
    gid    string
    cancel context.CancelFunc   // 天然幂等
}
```

---

## 3. P2 — 中优先级（正确性 / 健壮性 / 体验）

### P2-1 WebDAV 的 `hrefOf` 只返回一级名字 —— 目录树对客户端是错的

**位置**：`webdav/webdav.go:334-338`

```go
func hrefOf(f models.File) string {
    // 简化：直接返回 /name；PROPFIND 结果仅在根和一级子目录准确
    return "/" + url.PathEscape(f.Name)
}
```
`PROPFIND Depth:1` 对 `/a/b` 返回的子项 href 会是 `/c` 而不是 `/a/b/c`。**macOS Finder 和 nginx 的 davfs 会因此算错路径**，点击子目录会跳到错误位置。注释里承认了"仅根和一级准确"，但 `Depth` 默认是 `0`——Windows 资源管理器发 `Depth:1`，直接踩坑。

**修复**：`hrefOf` 需要**接收完整路径前缀**，或者在 `propfind` 里根据当前请求路径拼接。

### P2-2 回收站清理会重复扣减配额（重入 bug）

`cleanupTrash`（`file.go:1487-1507`）：
```go
addStorage(f.OwnerID, -f.Size)     // 扣额度
db.Get().Unscoped().Delete(&f)     // 物理删除
```
但 `Purge`（手动彻底删除，`file.go:605`）**也做同样两件事**。如果用户在 6 小时清理窗口内先 `Purge`，随后 cleanup 扫描到的 `files` 列表是**清理开始时的快照**（`Find` 结果在循环外），可能包含已被 Purge 的记录 → **重复扣减 `storage`** → 用户额度变成负数。

**修复**：`Unscoped().Delete` 前用 `RowsAffected` 判断是否真的删到了记录，只有 `RowsAffected>0` 才 `addStorage`：
```go
res := db.Get().Unscoped().Delete(&f)
if res.RowsAffected > 0 { addStorage(f.OwnerID, -f.Size) }
```
同理 `Purge` 也要加。

### P2-3 JWT 解析不校验签名算法

**位置**：`pkg/jwt/jwt.go:63-65`

```go
_, err := jwt.ParseWithClaims(tok, c, func(t *jwt.Token) (interface{}, error) {
    return secret, nil      // ← 没检查 t.Method
})
```
`golang-jwt/v5` 默认在 `ParseWithClaims` 里**会**校验 alg 是否与 key 类型匹配（HS256 + []byte），所以**当前不可利用 `alg:none`**。但**显式验证是好习惯**，防御未来重构引入 `RS256` 混淆：
```go
if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
    return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
}
```
另外 `Claims` 没有 `Issuer`/`Audience`，也没有 `NotBefore`，建议补上。

### P2-4 `Setup2FA` 的关闭分支在未开启时会崩

**位置**：`auth.go:206-213`
```go
// 关闭需验证码
if !service.ValidateTOTP(u.TwoFactor, req.Code) {   // u.TwoFactor 可能为 ""
```
用户没开 2FA 时调 `POST /2fa/setup {enable:false}` → `ValidateTOTP("", code)` → 取决于实现，可能 panic 或返回 false。应加前置判断：
```go
if u.TwoFactor == "" { c.JSON(400, gin.H{"message":"未开启两步验证"}); return }
```

### P2-5 OAuth2 实现不完整，且 `redirect_uri` 校验有洞

**位置**：`oauth.go`

| 问题 | 说明 |
|---|---|
| `OAuthAuthorize` 的 redirect 校验可绕过 | `if len(uris) > 0 && redirectURI != ""` —— 若 app 的 `RedirectURIs` 为**空数组**（创建时 `redirectUris` 非必填），则**任意 redirect_uri 都放行** → **授权码劫持**。 |
| 没有 PKCE | 公开客户端无法安全使用 |
| 没有 `refresh_token` | `oauthTokenTTL=2h`，到期需重新授权（体验问题） |
| `OAuthAuthorizeConfirm` 不复核 redirect_uri | 只查 client_id，**直接相信请求体里的 `RedirectURI`** → 攻击者可构造 `client_id` 合法但 `redirect_uri` 指向自己的确认请求 |
| code 换 token 时不校验 `Used` 的原子性 | `if oc.Used {...}` 然后 `Update("used", true)`，**并发下同一 code 可换两次 token** |

**修复优先级**：redirect_uri 严格白名单（空则拒绝）+ Confirm 阶段复核 + code 用 `UPDATE ... WHERE used = false` 判断 RowsAffected。

### P2-6 `settingInt` / `getSetting` 每次请求都查库，且无缓存

`getSetting` 在 `isAllowedExtension`、`getSettingBool`、`limitForCategory` 等**每个上传请求里被调用 3-5 次**，每次都 `SELECT ... WHERE key = ?`。高并发下这是明显的 DB 放大。建议加**带 TTL 的内存缓存**（30s）+ 保存设置时主动失效。

### P2-7 `plaintext 密码/token 明文入库/落盘`

| 位置 | 问题 |
|---|---|
| `models.User.TwoFactor` | 注释写"加密"，**实际明文存 TOTP secret 到 DB**（`auth.go:228`: `Update("two_factor", req.Secret)`）。DB 泄露 = 2FA 全废。应走 `conf.EncryptString`。 |
| `models.Share.Password` / `ExtractCode` | **明文比对**（`share.go:137,141`）。应存 hash（提取码短，需加盐或限定尝试次数）。 |
| `models.PersonalAccessToken.Token` / `AccessToken.Token` | **明文存 token**（`pat.go:38`）。DB 泄露 = 所有 API 令牌可用。应存 SHA-256，比对时哈希后查。 |
| `OAuthApp.ClientSecret` | 明文（`oauth.go:39`）。同上。 |
| `jwt_secret` 存在 `settings` 表 | `jwt.go:39` 写入 DB。**这让 conf 层对 secret.key 的加密保护形同虚设**——JWT 密钥反而在明文 DB 里。建议移到 `data/secret.key` 体系。 |
| `Policy.Config` | 注释写"JSON 加密"（`models.go:139`），**实际 `filesystem.New` 直接 `json.Unmarshal([]byte(configJSON))` 明文解析**。S3/OSS/COS 的 AK/SK 明文入库。 |

**这一条整体是 P1 级别的问题**，但因为是"设计层面需权衡"，归到 P2。**修正建议**：至少 `TwoFactor`、`Token`、`ClientSecret`、`Policy.Config` 四项必须加密或哈希。

### P2-8 `Secret()` 方法暴露 aria2 RPC 密钥 + RPC 无访问控制

`aria2/aria2.go:59-63` 提供 `Secret()` 导出方法（注释"调试用"），且 `--rpc-allow-origin-all=true`、`--rpc-listen-port=6800` 绑定所有网卡（未指定 `--rpc-listen-all=false`，默认只监听 localhost，**这点没问题**）。但 `Secret()` 导出给外部包用，容易误用于响应体。建议删除或改为 `internal`。

### P2-9 回收站清理的目录处理逻辑有误

`cleanupTrash` 对 `IsDir` 的记录**跳过物理删除但执行 `addStorage(-f.Size)`**。目录的 `Size` 通常是 0（`Mkdir` 不设 size），所以影响小；但如果曾经给目录设过 size，会错误扣减。**且目录删除不递归其子文件** → 子文件成为**孤儿记录**（`parent_id` 指向已删除的目录），既不出现在列表（`parent_id IS ?` 查不到）也不会被清理 → **永久占用配额**。

**修复**：删除目录时递归处理子节点，或至少把孤儿的 `parent_id` 置 NULL 移到根。

---

## 4. 架构层面

### 4.1 单体内聚 vs 边界划分

`controllers` 包已经膨胀到 20+ 文件、`file.go` 单文件 1508 行。`file.go` 里混了：文件 CRUD、上传（3 种模式）、下载、Range 解析、版本控制、标签、批量操作、回收站清理、设置读取工具。**建议拆分**：

```
controllers/
├── file/           # List/Download/Delete/Rename/Move
├── upload/         # Upload/Rapid/Chunk*/UploadVersion
├── version/        # ListVersions/DownloadVersion/RestoreVersion/DeleteVersion
├── tag/
└── trash/          # Purge/cleanupTrash
internal/service/   # 跨 controller 复用的业务逻辑（配额、上传校验、refcount 事务）
```

**关键**：`getSetting`/`isAllowedExtension`/`ensureQuota` 这些**被 WebDAV 重复实现了一遍**（见 P0-3）。抽到 `internal/service` 是消除这类不一致的根本手段。

### 4.2 `db.Get()` 全局单例 + 硬编码 1 号策略

```go
h, p, err := handlerForPolicyID(1)   // 上传、分片合并、离线下载全用这个
```
**存储策略 id=1 被硬编码**在至少 3 处。如果管理员删除了 1 号策略，或想改默认策略，这些路径会静默失败或写入非法策略。应该统一走一个 `defaultPolicy()` 函数（查 `is_default = true`，回退到 1）。

### 4.3 前端 `Files.tsx` 1390 行

单文件承载了列表/网格、拖拽、批量选择、预览、右键菜单、上传队列。建议按功能拆组件。前端整体 10,745 行 / 34 文件，`api/client.ts` 418 行是 axios 封装——**注意检查它是否把所有接口都做了错误兜底**（README 提到曾经"删除 mock 假数据兜底"，说明历史上存在**前端伪造数据**的情况；这类兜底如果还在别处，会掩盖后端 500）。

### 4.4 测试覆盖 42.2% 的结构问题

29 个 `_test.go` 文件，但**全是单元测试**（middleware、pkg、conf）。**零集成测试**——没有一处 `httptest.NewServer` 打通完整请求链。而本次发现的所有 P0 问题（路径穿越、WebDAV 覆盖丢数据、SSRF）**都是集成层才能发现的**。42.2% 这个数字有虚高感：它覆盖的是"好测的部分"（工具函数、中间件），而**最难测也最需要测的部分（controllers 的文件操作、WebDAV、跨模块协作）几乎空白**。

**建议优先级（按发现 bug 的 ROI）**：
1. WebDAV 集成测试（路径穿越用例、PUT 覆盖、LOCK）—— 能立刻暴露 P0-1/2/3/4
2. 文件生命周期集成测试（上传→秒传→复制→删除→回收站→清理，断言 refs 与 storage 一致）
3. 权限矩阵测试（用户 A 访问用户 B 的所有接口，断言全 403）
4. SSRF 用例（task 提交内网地址，断言被拒）
5. 并发测试（`go test -race` 跑分片上传、配额、CancelTask）

### 4.5 无 CI

仓库里没有 `.github/workflows/`，只有 `nfpm.yaml`。README 提到的"CI 审核通过后合并"（plugins 分支）**目前无实现**。至少应加：
```yaml
# .github/workflows/ci.yml
- go vet ./...
- go test -race -coverprofile=coverage.out ./...
- go build ./...
- cd frontend && npm ci && npm run build && npx tsc --noEmit
```
`-race` 尤其重要（能抓到 P1-6 的 data race）。

---

## 5. 合规与工程规范

### 5.1 LICENSE：已改为 AGPL-3.0 ✅

**原问题**（审查时状态）：`MIT License + 署名保留附加条款 v1.0`，要求"至少 3 处署名，禁止移除"。

1. **已不是 MIT**。MIT 明确允许"移除所有声明"（只需在副本中保留版权与许可声明，不是运行时展示）。附加"UI 页脚必须显示"的要求，与 MIT 第 1 条冲突。这种情况下**整个许可的解释权不明**——发生争议时，法院可能认定附加条款不可执行（于是等于纯 MIT），也可能认定整个许可为非自由许可。
2. **"UI 页脚必须显示"在 OSI 定义下属于 field-of-use 限制**，Debian/FSF 会判定为非自由许可，**无法进入多数发行版仓库**（这对"自托管系统"的推广是实际损失）。
3. **无法执行性**：二开者改个 UI 主题、换掉 Footer 组件（React 里就是删个 `<footer>`），谁知道？没有技术手段强制。

**已采取的方案：AGPL-3.0**（作者于 2026-10-02 变更，采纳上表方案 A）。

这个选择一次性解决了上述三个问题：

| 原问题 | 在 AGPL-3.0 下的状态 |
|---|---|
| 与 MIT 第 1 条冲突、解释权不明 | ✅ 消失。AGPL-3.0 是 OSI 认证的自由软件，条款自洽 |
| field-of-use 限制，无法进发行版仓库 | ✅ 消失。可正常进入 Debian 等发行版 |
| 署名要求无法技术强制 | ✅ 消失。署名要求本就不是许可证该管的事 |

**而且保护力度实际上更强**。原条款真正想防的是"署名被移除"，但那对自托管产品几乎不构成商业风险；
真正的风险是**拿去做成闭源 SaaS 卖钱**——而这恰恰是 AGPL-3.0 第 13 条覆盖的场景
（即使只是"通过网络提供服务"也触发源码开放义务）。对 NebulaDrive 这种自托管定位，AGPL 是更合适的选择。

**需要接受的代价**：

- **网络服务同样触发源码开放**。AGPL 不区分"自部署"与"提供 SaaS"——
  任何人把改过的版本部署到服务器上通过网络提供服务，都必须向使用者提供对应源码。
  这对"欢迎社区部署"是好事，但如果未来想做**官方托管 SaaS 且不公开核心代码**，需要另行评估。
- **企业采用门槛**。部分商业公司因 AGPL 的传染性而回避使用。
  考虑到 NebulaDrive 已有企业功能（套餐/订阅/兑换码），若日后需要吸引企业客户，
  可考虑提供"商业许可"双轨——这也是 AGPL 生态的常见做法。

**⚠️ 待同步事项**：仓库根目录 `LICENSE` 文件与 `README.md` 第 7/141/152/158 行的许可证声明
需一并更新为 AGPL-3.0，否则会出现「GitHub 页面显示 AGPL-3.0、但仓库文件里仍是旧的组合协议」
的不一致状态——而 LICENSE 文件才是法律上生效的那份。

### 5.2 README 的三个问题

1. **开头重复**："本项目基于 NebulaDrive 开发，原作者 mrc-sk"出现 3 次（开头/协议/致谢）。**开头那次会让读者误判这是 fork**（实际是原创）。建议开头只写 `作者 mrc-sk`。
2. **"WebDAV RFC 4918 全方法实现"是过度声明**（见 P0-2/P0-4）。建议改为"实现 PROPFIND/GET/PUT/MKCOL/DELETE/MOVE 等核心方法；LOCK/UNLOCK/COPY/PROPPATCH 为占位，暂不支持 Office/Finder 直接编辑"。
3. **CHANGELOG 的 20 条已知 bug** 是诚实的好信号，但**没有分级**。建议加 `[Blocker]` / `[Major]` / `[Minor]` 标签，让用户知道能不能上车。

### 5.3 `.trae-html-share-packages/` 应 gitignore

内容是两个 zip（`backend/frontend_dist/index.html.zip`、`frontend/index.html.zip`），是 AI 编辑器（Trae）的产物，与项目无关。混在仓库根目录会干扰阅读。

**⚠️ 顺带发现一个真问题**：`backend/frontend_dist/` 被 `go:embed` 引用，但如果它是编辑器注入的 zip 解包产物，**通过 npm build 重新生成前端会覆盖它**。建议在 README 写清楚构建顺序（先 `npm run build` 产出到 `backend/frontend_dist`，再 `go build`），并确认 `vite.config.ts` 的 `outDir` 指向正确。

### 5.4 `frontend/dist/` 在 .gitignore 里写了两次

```gitignore
frontend/dist/   # 第 9 行附近
frontend/dist/   # 第 13 行附近（重复）
```
小事，顺手清一下。

---

## 6. 优先级修复清单（按投入产出比排序）

| # | 问题 | 严重级 | 预估工作量 | 状态 |
|---|---|---|---|---|
| 1 | **WebDAV 路径穿越**（safeJoin + 拒绝非法文件名） | P0 | 2h | ✅ 已修 `P0-1` |
| 2 | **CORS 收紧**（白名单 CORS） | P0 | 1h | ✅ 已修 `P0-5` |
| 3 | **SSRF 防护**（URL 校验 + 超时 + 大小限 + 跳转校验） | P0 | 4h | ✅ 已修 `P0-6` |
| 4 | **Breadcrumb 鉴权**（加 owner 检查） | P1 | 15min | ✅ 已修 `P1-1` |
| 5 | **WebDAV PUT 覆盖真实现** | P0 | 3h | ✅ 已修 `P0-2` |
| 6 | **WebDAV PUT 接入白名单+统一配额** | P0 | 2h | ✅ 已修 `P0-3` |
| 7 | **敏感字段加密**（TOTP/token/ClientSecret/Policy.Config） | P1 | 4h | ⬜ **剩余唯一待办** `P2-7` |
| 8 | **配额原子化**（条件 UPDATE） | P1 | 3h | ✅ 已修 `P1-5` |
| 9 | **关键路径加事务**（Rapid/Upload/UploadVersion/Install 等 13 处） | P1 | 6h | ✅ 已修 `P1-4` |
| 10 | **Search 的 tag JOIN 语法修复** + 分页 SQL 跨库 | P2 | 1h | ✅ JOIN `P1-2` + 跨库清理 `P1-3` |
| 11 | **chunkUploads 加过期清理 + meta 加锁 + size 用实际值** | P1 | 3h | ✅ 已修 `P1-6` |
| 12 | **CancelTask 幂等化** | P1 | 30min | ✅ 已修 `P1-7` |
| 13 | **补集成测试 + CI（-race）** | P1 | 2d | 🟡 测试套件已救活并全绿；CI 未加 |
| 14 | **LICENSE 方案决策 + README 修正** | P2 | 3h | 🟡 已定 AGPL-3.0；README 待同步 |
| 15 | **WebDAV LOCK/UNLOCK/COPY/PROPPATCH** | P0/P2 | 4h+ | ✅ 已修 `P0-4` |

### 清单之外还修掉的

审查时没列进清单、但在动手过程中发现并修复的：

| 问题 | 说明 |
|---|---|
| `Purge` / `cleanupTrash` 重复扣减配额 | 改为条件原子删除，避免重复点击把用户已用容量扣成负数 `P2-2` |
| **测试驱动 ≠ 生产驱动** | 测试用需 CGO 的 `gorm.io/driver/sqlite`，生产用纯 Go 的 `glebarez/sqlite`。导致测试在无 GCC 环境完全跑不起来，且"测试通过 ≠ 线上通过"——**这正是第 10 项能潜伏到生产的机制** |
| 测试连接从不关闭 | Windows 上句柄不释放导致 `t.TempDir()` 清理失败，**断言全过的测试被判 FAIL** |
| `db.Init` 泄漏连接池 | 安装向导每点一次"测试连接"泄漏一个连接池 |
| 4 处差值记账绕过额度校验 | 协作保存、版本恢复等路径用无校验的 `addStorage`，增量为正时可超额 |

### 验证状态

```
go build ./...   EXIT=0
go vet ./...     EXIT=0
go test ./...    EXIT=0   （12 个包全部通过）
```

每一项"具体怎么修的、踩了什么坑"见 [`FIXLOG.md`](./FIXLOG.md)。

---

## 7. 最后几句直话

这个项目的**天花板很高**——存储抽象、引用计数、配置加密这三块的设计水平明显高于同类国产自托管项目。但它现在的问题不是"某处代码写得不好"，而是**安全模型没有建立起来**：

- HTTP 路径有一套完整的校验链（白名单→魔术字节→配额→审计），**WebDAV 路径完全没有**。
- 内部机制（refcount）考虑到了竞态和升级兼容，**外部边界（谁能不能访问）几乎是裸的**。
- 测试覆盖"好测的部分"，跳过"最需要测的部分"。

**根因是同一个**：`controllers` 里的业务逻辑被 WebDAV **抄了第二遍**，而抄的时候只抄了主干、丢掉了安全层。

所以当时只给了一条建议：**建 `internal/service` 层，把所有校验（配额、白名单、魔术字节、出站请求）收进去，让 HTTP 和 WebDAV 都只能走同一个入口。**

### 这条建议实施后的验证：判断是对的

`internal/service` 落地（`outbound.go` + `quota.go`）之后，修复过程反过来印证了根因判断：

- **P0-3（WebDAV 绕开安全链）不再是"给 WebDAV 补上检查"，而是"换个入口"** ——
  `readDAVUpload` 直接复用 HTTP 上传的同一套白名单/魔术字节/体积上限，WebDAV 侧结构上不可能再漏。
- **P1-5（配额 TOCTOU）的修法只有一种** —— 把"读-判-写"合并成单条条件 UPDATE。
  这段逻辑没办法在两个地方各写一遍，必须收口。
- **新增的配额/白名单逻辑立刻被 9 个写入点复用** —— 若还按旧结构分散在各 controller 里，
  光是"记得同步"就足以再次出洞。

**一个被低估的收益**：校验收口之后，"某个入口忘了校验"从**靠人记住**变成了**结构上不可能**。
这比修掉当下这 6 个 P0 更重要 —— 修掉的是症状，收口修的是产生症状的结构。

### 剩下的

1. **P2-7 敏感字段加密** —— 唯一剩余项。难点不是加密本身，而是**兼容存量明文**：
   直接上线加密会让老用户的 2FA 密钥和已签发 PAT 全部失效，必须做"能解密则解密、
   否则按明文读并在下次写入时加密"的过渡。
2. **CI** —— 目前验证靠本地 `go test ./...`。建议加 GitHub Actions 跑 `go vet` + `go test -race ./...`。
   `-race` 尤其值得：分片上传的 map 竞争（P1-6）这类问题，靠竞态检测才能稳定复现。
3. **README 的 WebDAV 措辞** —— 原文写"RFC 4918 全方法实现"。现在 LOCK/UNLOCK/COPY 已真实现，
   但 PROPPATCH 明确不支持写属性（返回 207 + 403），措辞仍需对齐。

---

*本报告基于 commit `a26c73f`（hotfix V1-0.0.2-beta）的静态代码审查，所有结论均标注了具体文件与行号，可逐条复核。*

*2026-10-02 更新：审查结论已全部复核并落地修复，本文档同步了修复状态清单。
实现细节与踩坑记录见 [`FIXLOG.md`](./FIXLOG.md)。
除 §5.1（许可证，作者已决策改为 AGPL-3.0）外，报告中的原始判断未因修复而改变。*

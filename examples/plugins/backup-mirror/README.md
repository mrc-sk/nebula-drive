# backup-mirror —— 备份包异地镜像、轮转与恢复审计

> 状态：**已实现**。宿主在 `controllers/backup.go` 里触发 `onBackup` / `onRestore`，
> 命令行通过 `onCLI` 提供状态查询。

## 用途

NebulaDrive 的备份包默认落在宿主的 `<dataDir>/backups/` 下。如果备份和数据库
在同一块盘上，一次磁盘故障会把两者一起带走。本插件做三件事：

1. **异地镜像**：`onBackup` 触发时，把刚生成的备份包复制到配置的每一个
   `targets` 目录。
2. **轮转**：每个 target 只保留最近 `keep` 份**命名匹配**的备份包，更旧的删除。
3. **恢复审计**：`onRestore` 触发时，往 `<dataDir>/restore-audit.jsonl`
   追加一行 JSON。恢复是暂存制（重启才真正替换数据库），这一行是
   “有人提交过恢复”的唯一持久痕迹。

## 配置

安装后在插件页填写：

```json
{
  "targets": ["D:/nd-backup", "/mnt/backup/nebula"],
  "keep": 7,
  "verify": true
}
```

| 字段 | 说明 | 缺省 |
| --- | --- | --- |
| `targets` | 镜像目标目录列表。不存在会自动创建（`MkdirAll`），创建失败只影响该 target，其余照常。空项会被忽略，重复项会去掉。 | 空（不镜像） |
| `keep` | 每个 target 保留多少份**匹配命名规则**的备份包。`<= 0` 表示不轮转，全部保留。 | `0`（不轮转） |
| `verify` | 复制完成后是否重算 SHA-256 与宿主给的 `sha256` 比对。 | `true` |

> `keep` 缺省是 **0（保留全部）**，不是示例里的 7。默认值绝不能是“删旧文件”：
> 没配 keep 时悄悄删掉历史备份是不可接受的。要轮转请显式配正数。

## 轮转命名规则（重要）

**只有**匹配下面正则的文件会被轮转删除、也只统计这些文件：

```
^backup-\d+-[0-9a-f]{8}\.zip$
```

即宿主生成的 `backup-<unix 时间戳>-<8 位小写十六进制>.zip`，例如
`backup-1700000000-3f8a1c2d.zip`。排序依据是文件名里的 unix 时间戳
（同一秒内再用文件名倒序兜底），保留最新的 `keep` 份。

**target 目录里的其它文件永远不会被本插件删除**，例如：

- `notes.txt`、`README.md`
- `archive.zip`（其它工具的备份）
- `backup-1700000000-ABCDEF12.zip`（大写 hex，不匹配）
- `backup-1700000000-deadbeef.tar`（扩展名不匹配）
- 任何子目录、符号链接

这些文件也**不参与 keep 计数**。如果你的备份命名不是这个形状，轮转不会生效
（但镜像仍然会复制，镜像不依赖命名规则）。

## 原子写入与校验

复制顺序：**写 `<target>/<filename>.part` → `fsync` → （可选）重算 SHA-256 → `rename`**。

- `rename` 在同一文件系统内是原子的：读者要么看到旧的完整文件，要么看到新的
  完整文件，不会看到半截备份。
- 中途失败（磁盘满、网络盘断开）只会留下 `.part`；正式文件名不会出现。
  轮转的正则永远不匹配 `.part`，所以半截文件不会被当成备份，也不会被计数。
- `verify=true` 时会**重新流式读取刚写下的 `.part`** 计算 SHA-256，与宿主 ctx 的
  `sha256` 比对；不一致直接删除 `.part`、记 stderr 错误，并跳过该 target 的本次
  轮转 —— 不给“校验失败的包”留下正式文件名。
- 大文件全程 `io.Copy` 流式处理，不会把整个备份包读进内存。

## 命令：`nebula plugin backup-mirror`

打印每个 target 的统计：备份包数量、总字节数、最新/最旧文件名。只统计匹配上述
命名规则的文件。

```console
$ nebula plugin backup-mirror
backup-mirror v1.0.0（targets=2，keep=7，verify=true）
轮转：只处理匹配 ^backup-\d+-[0-9a-f]{8}\.zip$ 的文件，每个 target 保留最新 7 份
- D:/nd-backup：12 个备份包，共 734003200 字节，最新 backup-1700000000-3f8a1c2d.zip，最旧 backup-1690000000-0a1b2c3d.zip
- /mnt/backup/nebula：目录不存在（下一次 onBackup 会创建）
```

命令名不是 `backup-mirror` 时插件不输出任何内容，避免污染其它插件的 CLI 输出。

## 恢复审计

每次 `onRestore` 追加一行到 `<init 下发的 dataDir>/restore-audit.jsonl`：

```json
{"time":"2026-10-18T17:00:00.123456789+08:00","filename":"backup-1700000000-3f8a1c2d.zip","size":12345,"operator":"alice","stagedPath":"/data/nebula/restore/nebula.db"}
```

- 采用 `O_APPEND` 打开，写完 `Sync` 再关闭；审计是“不能丢”的记录。
- 用 JSONL 而不是 JSON：追加不需要读回整个文件，单行损坏也不影响其余行。
- `dataDir` 为空时明确返回失败并记 stderr，不会假装写成功。

## 已知边界

- **备份包本身不加密。** 插件只做复制与轮转，不改变包内容；target 的访问控制
  （目录权限、磁盘加密、远端挂载的鉴权）由管理员负责。
- **插件无网络能力。** 目标必须是本机可写的目录（SMB/NFS/云盘挂载点可以，
  由操作系统负责挂载与凭据）。
- **`onBackup` 是异步通知。** 宿主不读返回值，也不会因插件失败而回滚备份；
  所有错误只出现在插件日志（stderr）里。宿主侧单次 fire 的等待超时默认 2 秒，
  **复制大包可能超过它**：宿主会记一条超时警告，但插件会继续把这次复制做完
  （超时不会中断复制）。看到该警告时请以插件日志与 target 里的文件为准。
- **只镜像、不校验源包是否存在第二个副本之外的冗余。** `verify` 只证明
  “target 里的字节与宿主给的摘要一致”，不证明 zip 结构可解析。
- **轮转按文件名里的 unix 时间戳排序**，不依赖文件 mtime（复制过程可能改变 mtime）。
- **进程被强杀时**可能残留 `<filename>.part`。它不会被轮转，也不会被误认为备份；
  同名文件的下一次镜像会以 `O_TRUNC` 覆盖它。
- **同目录 target 会被跳过**：target 与源文件所在目录相同（含 Windows 大小写
  不敏感比较）时记警告不复制，避免“自己复制给自己”和轮转误删唯一备份。
- **符号链接不跟随。** 轮转用 `os.ReadDir` + `entry.Type()&os.ModeSymlink` 跳过
  链接，链接本身也不删除。镜像目标目录若本身是符号链接，由操作系统正常解析
  （这是管理员的选择）。

## 构建与测试

```sh
go build -o backup-mirror .
go vet ./...
go test ./... -count=1
```

只使用 Go 标准库，无第三方依赖。

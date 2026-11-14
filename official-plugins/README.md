# 官方插件（10 个）

V26-11.1 上架的 10 个官方插件，全部为 **Python 纯钩子插件**，遵循 v2 三文件契约
（`plugin.json` + `register.go` + `co.BEGG`），默认关闭，由站长按需启用。

| 目录 | 作用 |
|---|---|
| `plugins/nd-invite-code/` | 邀请码注册 |
| `plugins/nd-upload-guard/` | 上传守卫 |
| `plugins/nd-filename-clean/` | 文件名规范化 |
| `plugins/nd-quota-watch/` | 配额预警 |
| `plugins/nd-login-brake/` | 登录爆破抑制 |
| `plugins/nd-share-policy/` | 分享策略 |
| `plugins/nd-audit-trail/` | 审计留痕 |
| `plugins/nd-webdav-guard/` | WebDAV 写保护 |
| `plugins/nd-sensitive-tag/` | 敏感标记 |
| `plugins/nd-maintenance/` | 维护模式 |

## 打包与校验

```bash
python3 build_plugins.py                    # 生成源码、校验契约、签名、产出 store/
go run ./backend/cmd/ndtools -store-url http://127.0.0.1:8084/ verify
go run ./backend/cmd/ndtools -store-url http://127.0.0.1:8084/ pack
```

`store/com.json` 为商店清单，`store/plugin-trust.json` 为信任库，`store/*.pbclus` 为签名包。

> **安全**：签名私钥 `official-signing-key.json` **不入库**，
> 只在本目录保留 `official-signing-key.example.json`（含公钥与占位私钥）。
> 私钥泄露意味着任何人都能签发"官方插件"，请仅保存在部署机。

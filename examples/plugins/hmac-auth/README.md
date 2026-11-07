# hmac-auth —— 开放 API 的 HMAC 签名鉴权

> 状态：**已实现**。宿主侧 `onApiAuth`（`middleware/api_auth.go`）在内置鉴权
> PAT / OAuth2 失败后触发；本插件校验签名，通过则声明调用者身份。

## 用途

`/api/v1/*` 默认只认 PAT 与 OAuth2 access token。本插件让第三方系统
用**共享密钥 + HMAC 签名**调用开放 API —— 不必为每个调用方签发令牌。

## 待签名规范串（与客户端必须完全一致）

```
METHOD\nPATH\nQUERY\nTIMESTAMP\nBODY_SHA256_HEX
```

- 五行，**缺一不可**。少任何一行，攻击者就能在不改签名的前提下替换那部分内容。
- `METHOD` 统一大写；`PATH` 不含 query；`QUERY` 是原始查询串（未规范化）。
- `BODY_SHA256_HEX` 是请求体 sha256 的**小写十六进制**；空 body 也要算
  （即 `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`）。
- 签名 = `hex(HMAC-SHA256(sharedSecret, 规范串))`，放在 `signatureHeader` 里。

客户端参考实现（shell）：

```sh
TS=$(date +%s)
BODYHASH=$(printf '' | sha256sum | cut -d' ' -f1)
CANON=$(printf 'GET\n/api/v1/files\n\n%s\n%s' "$TS" "$BODYHASH")
SIG=$(printf '%s' "$CANON" | openssl dgst -sha256 -hmac "$SECRET" -hex | sed 's/.*= *//')

curl -H "X-Nebula-Signature: $SIG" -H "X-Nebula-Timestamp: $TS" \
  https://your-host/api/v1/files
```

## 三条安全红线

1. **未配置 `sharedSecret` / `principalUserId` 时一律拒绝。**
   没有密钥就不存在鉴权，放行等于敞开；没有 `principalUserId` 就无法认定身份
   —— 这比"随便挑一个用户"安全得多。所以配错了的表现是**全部 403**，
   而不是"静默放开"。
2. **常量时间比较**（`hmac.Equal`）。用 `!=` 会在第一个不同字节处返回，
   攻击者可以逐字节猜出正确签名。
3. **缺签名头也拒绝**，不是"没意见"地退回 401 —— 否则任何人都能靠不带
   签名头绕过这层保护。需要放行的端点请显式写进 `exemptPaths`。

## 时间窗是防重放的唯一手段

签名本身是静态的：没有时间窗，截获的签名可以永远重放。
`toleranceSeconds` 默认 300；设 0 表示不校验（不建议）。

## 配置

```json
{
  "sharedSecret": "********",
  "principalUserId": 1,
  "signatureHeader": "X-Nebula-Signature",
  "timestampHeader": "X-Nebula-Timestamp",
  "algorithm": "sha256",
  "toleranceSeconds": 300,
  "exemptPaths": ["/api/v1/health"]
}
```

- `principalUserId`：签名通过后代表哪个用户。HMAC 只能证明"持有密钥"，
  证明不了"是谁"，所以必须显式指定。
- `exemptPaths`：免签路径。按**路径段边界**匹配 —— 配置 `/api/v1/health`
  能匹配 `/api/v1/health` 与 `/api/v1/health/live`，但**不**匹配
  `/api/v1/healthcheck`。（裸 `strings.HasPrefix` 会意外放行后者，
  表面只是"多放行一个端点"，实际放行了一个没人打算公开的端点。）
- `sharedSecret` 在日志里被脱敏为 `<set:N chars>`。
  注意：**插件配置在数据库里仍是明文**（宿主侧加密存储还排在计划里）。

## 请求体摘要的边界

宿主为了让插件能校验请求体完整性，会计算 `bodyHash` 并放在 ctx 里，
但**有 64KB 上限**（鉴权失败路径是攻击者可控的，不限量读 body 就是内存放大面）。
超过上限时 `bodyTruncated=true`、`bodyHash` 为空 —— 本插件此时**拒绝**，
因为放行等于允许篡改请求体而签名依然通过。

## 测试

`main_test.go` 覆盖：

| 用例 | 断言 |
|---|---|
| 篡改表（7 项） | 改方法 / 路径 / 查询串 / **请求体摘要** / 时间戳 / 签名 / 换密钥，逐项都必须被拒 |
| 时间窗 | 过去与未来超出容差都拒绝 |
| fail closed | 缺密钥 / 缺 principalUserId / 算法不支持都必须拒绝 |
| 缺头 | 缺签名头、缺时间戳头、时间戳非整数都拒绝 |
| bodyTruncated | 必须拒绝 |
| 规范串格式 | 顺序与分隔符是**对外约定**，改动即破坏所有客户端 |
| exemptPaths | 路径段边界匹配（`health` 不放行 `healthcheck`） |
| header 类型 | `map[string]string`（进程内）与 `map[string]any`（JSON 往返）都要能读 |
| 日志脱敏 | 摘要里不得出现密钥内容 |

```bash
cd examples/plugins/hmac-auth && go test ./...
```

端到端另有两层：

- `backend/pkg/plugin/host/hmac_auth_e2e_test.go`：真实子进程 + 真实签名，
  覆盖"有效放行 / 篡改拦截 / 免签路径 / 缺签名拦截"。测试里**刻意重写**
  一份客户端签名实现 —— 插件改了规范串而客户端不改，测试立刻红。
- `backend/smoke_plugin.sh` 第 8 节：真实 HTTP 请求，
  覆盖"未配密钥 403 / 有效签名 200 / 篡改 403 / 缺签名 403 / 过期时间戳 403"。

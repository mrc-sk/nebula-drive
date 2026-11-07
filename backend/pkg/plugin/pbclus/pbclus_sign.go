package pbclus

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// 签名与信任。
//
// # 必须说清楚的一件事：签名 ≠ 可信
//
// 公钥内嵌在包里，所以**任何人都能签一个合法的包**：
// 用自己的密钥签，再把公钥一起放进去，`SigValid` 就是 true。
// "验证通过"只证明**内容与签名自洽**，不证明签名者是谁、也不证明你要信任它。
//
// 安全语义完全来自宿主侧的**信任库**（keyId 白名单）：
// 只有 SigTrusted 才有意义。宿主在做安装决策时只能用 SigTrusted，
// 用 SigValid 做决策等于没做校验 —— 这是这类设计最常见也最危险的误用，
// 所以这里把两个状态分成两个字段，不给"顺手用错"留空间。
//
// # 签名覆盖什么
//
// 签名消息 = 域分隔前缀 ‖ fileSHA
//
// 其中 fileSHA 是 头部‖清单‖载荷 的 SHA-256（也就是尾部里那个值）。
// 签名块自己不在被签内容里（否则自指）。域分隔前缀保证这个签名
// 不会被挪用到别的协议里去。
const (
	// SigAlgEd25519 是当前定义的唯一签名算法。
	SigAlgEd25519 uint8 = 1
	// sigDomain 是签名消息的域分隔前缀。
	sigDomain = "PBClus/v2 signed-package\n"
	// KeyIDLen 是 keyId 的十六进制长度（8 字节）。
	KeyIDLen = 16
)

// KeyID 返回公钥的短标识（SHA-256 前 8 字节的十六进制）。
//
// 用 8 字节而不是完整 32 字节：keyId 是给人看、给人抄的
// （管理员第一次信任某个发布者时要能一眼比对），64 个字符太长。
// 8 字节的碰撞概率在"插件发布者数量"这个量级上可忽略。
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// SigningKey 是一个 Ed25519 私钥及其元信息。
type SigningKey struct {
	// Name 是给人看的名字（如发布者名字），只作展示。
	Name string `json:"name,omitempty"`
	// PrivateKey 是 base64 编码的 64 字节私钥（seed+public）。
	PrivateKey string `json:"privateKey"`
	// PublicKey 是 base64 编码的 32 字节公钥。
	PublicKey string `json:"publicKey"`
	// KeyID 是公钥短标识。
	KeyID string `json:"keyId"`
}

// GenerateSigningKey 生成一对新的 Ed25519 密钥。
func GenerateSigningKey(name string) (*SigningKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &SigningKey{
		Name:       name,
		PrivateKey: base64.StdEncoding.EncodeToString(priv),
		PublicKey:  base64.StdEncoding.EncodeToString(pub),
		KeyID:      KeyID(pub),
	}, nil
}

// ParsePrivateKey 解析出可用的私钥，并校验它必须与自述公钥一致。
//
// 这个一致性检查不是多余的：密钥文件是可以被人手改的，
// 而"私钥与公钥不匹配"会表现为"签出来的包验证不过"，
// 排查起来会绕一大圈（怀疑格式、怀疑传输，就是不会怀疑密钥文件）。
func (k *SigningKey) ParsePrivateKey() (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(k.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("私钥不是合法 base64: %w", err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("私钥长度 %d 异常，应为 %d", len(raw), ed25519.PrivateKeySize)
	}
	priv := ed25519.PrivateKey(raw)
	pub := priv.Public().(ed25519.PublicKey)
	if k.PublicKey != "" {
		want, err := base64.StdEncoding.DecodeString(strings.TrimSpace(k.PublicKey))
		if err != nil {
			return nil, fmt.Errorf("公钥不是合法 base64: %w", err)
		}
		if string(want) != string(pub) {
			return nil, errors.New("密钥文件里的私钥与公钥不匹配（文件被改过？）")
		}
	}
	if k.KeyID != "" && k.KeyID != KeyID(pub) {
		return nil, fmt.Errorf("密钥文件的 keyId(%s) 与公钥(%s) 不匹配", k.KeyID, KeyID(pub))
	}
	return priv, nil
}

// PublicKeyBytes 解析自述公钥。
func (k *SigningKey) PublicKeyBytes() (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(k.PublicKey))
	if err != nil {
		return nil, fmt.Errorf("公钥不是合法 base64: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("公钥长度 %d 异常，应为 %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

// LoadSigningKey 从 JSON 文件读取密钥。
func LoadSigningKey(path string) (*SigningKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var k SigningKey
	if err := json.Unmarshal(b, &k); err != nil {
		return nil, fmt.Errorf("密钥文件不是合法 JSON: %w", err)
	}
	if k.PrivateKey == "" {
		return nil, errors.New("密钥文件缺少 privateKey 字段")
	}
	return &k, nil
}

// Save 把密钥写入文件，权限 0600。
//
// 0600 而不是更宽松：这是能签出"被信任的包"的私钥，
// 泄漏等于别人可以冒充这个发布者。Windows 上 POSIX 权限位不生效，
// 所以文档里也提醒作者别把私钥提交进版本库。
func (k *SigningKey) Save(path string) error {
	b, err := json.MarshalIndent(k, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return err
	}
	// Windows 不认 POSIX 位，显式再 Chmod 一次（在 Unix 上这是幂等的）
	return os.Chmod(path, 0o600)
}

// signedMessage 构造签名消息。
func signedMessage(fileSHA []byte) []byte {
	msg := make([]byte, 0, len(sigDomain)+len(fileSHA))
	msg = append(msg, sigDomain...)
	msg = append(msg, fileSHA...)
	return msg
}

// Signature 是"这个包的签名状态"。
//
// SigPresent 与 SigValid 分开，是为了让调用方能区分三种情况：
// 没签名 / 签了但内容对不上（可能被改过）/ 签名有效。
// 把它们合成一个布尔值，错误信息就没法说清楚到底哪儿不对。
type Signature struct {
	// Present 包里是否有签名块。
	Present bool `json:"present"`
	// Valid 签名与内容是否自洽（**不代表可信**，见文件头说明）。
	Valid bool `json:"valid"`
	// Trusted keyId 是否在宿主的信任库里。
	Trusted bool `json:"trusted"`
	// KeyID 签名者标识。
	KeyID string `json:"keyId,omitempty"`
	// KeyName 信任库里该密钥的名字（未信任时为空）。
	KeyName string `json:"keyName,omitempty"`
	// Err 签名验证失败的原因（Present 且 !Valid 时）。
	Err string `json:"error,omitempty"`
}

// Status 返回签名状态。trusted 为 nil 表示宿主没有信任库
// （此时 Trusted 恒为 false，不会"默认信任"）。
//
// 注意：调用它之前应当先 Verify()。签名校验依赖尾部里的 fileSHA，
// 而 fileSHA 与内容的对应关系由 Verify 确认 —— 少了这一步，
// 攻击者可以改内容同时也改 fileSHA，签名照样"通过"。
// 这里不隐式调用 Verify 是因为那要读一遍载荷，
// 是否值得由调用方决定（安装前必须调）。
func (rd *Reader) Status(trusted TrustStore) Signature {
	st := Signature{}
	if rd.sig == nil {
		return st
	}
	st.Present = true
	st.KeyID = rd.sig.KeyID
	if trusted != nil {
		if name, ok := trusted.Lookup(rd.sig.KeyID); ok {
			st.Trusted = true
			st.KeyName = name
		}
	}
	foot := make([]byte, FooterSize)
	if _, err := rd.ra.ReadAt(foot, rd.size-FooterSize); err != nil {
		st.Err = "读取尾部失败: " + err.Error()
		return st
	}
	if !ed25519.Verify(rd.sig.PublicKey[:], signedMessage(foot[0:32]), rd.sig.Signature[:]) {
		st.Err = "签名与内容不匹配（包已损坏，或被改过）"
		return st
	}
	st.Valid = true
	return st
}

// TrustStore 是 keyId → 名称的信任库。
//
// 只做"这个 keyId 我认不认"的判断，不碰密码学 ——
// 密码学在 Status 里，信任决策在这里。两者分开才能把
// "签名有效"与"签名可信"这两件事讲清楚。
type TrustStore interface {
	// Lookup 返回该 keyId 是否被信任，以及它的名字。
	Lookup(keyID string) (name string, ok bool)
}

// TrustedKey 是信任库里的一条记录。
type TrustedKey struct {
	// KeyID 公钥短标识（16 个十六进制字符）。
	KeyID string `json:"keyId"`
	// Name 发布者名字，用于展示与审计。
	Name string `json:"name"`
	// PublicKey 可选：base64 公钥。填了就必须与 keyId 自洽 ——
	// 只写 keyId 时无法防止"keyId 相同但公钥不同"的替换，
	// 所以推荐填。
	PublicKey string `json:"publicKey,omitempty"`
	// AddedAt 记录信任建立时间。
	AddedAt string `json:"addedAt,omitempty"`
}

// KeyRing 是 TrustStore 的默认实现。
type KeyRing struct {
	Keys []TrustedKey `json:"keys"`
}

// Lookup 实现 TrustStore。
func (r *KeyRing) Lookup(keyID string) (string, bool) {
	if r == nil {
		return "", false
	}
	for _, k := range r.Keys {
		if strings.EqualFold(k.KeyID, keyID) {
			return k.Name, true
		}
	}
	return "", false
}

// Add 添加一条信任记录（已存在则更新名字）。
func (r *KeyRing) Add(k TrustedKey) {
	for i := range r.Keys {
		if strings.EqualFold(r.Keys[i].KeyID, k.KeyID) {
			r.Keys[i] = k
			return
		}
	}
	r.Keys = append(r.Keys, k)
}

// Remove 删除一条信任记录，返回是否删除。
func (r *KeyRing) Remove(keyID string) bool {
	for i := range r.Keys {
		if strings.EqualFold(r.Keys[i].KeyID, keyID) {
			r.Keys = append(r.Keys[:i], r.Keys[i+1:]...)
			return true
		}
	}
	return false
}

// Validate 检查信任库自身的自洽性。
//
// 填了 PublicKey 就必须与 KeyID 对得上：否则可能出现
// "信任库里写着可信的 keyId，公钥却是别人的"，那等于信任了一个假的发布者。
func (r *KeyRing) Validate() error {
	seen := map[string]bool{}
	for i, k := range r.Keys {
		if len(k.KeyID) != KeyIDLen {
			return fmt.Errorf("第 %d 条信任记录的 keyId 长度应为 %d：%q", i, KeyIDLen, k.KeyID)
		}
		if _, err := hex.DecodeString(k.KeyID); err != nil {
			return fmt.Errorf("第 %d 条信任记录的 keyId 不是十六进制：%q", i, k.KeyID)
		}
		lower := strings.ToLower(k.KeyID)
		if seen[lower] {
			return fmt.Errorf("信任库里 keyId %s 重复", k.KeyID)
		}
		seen[lower] = true
		if k.PublicKey != "" {
			raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(k.PublicKey))
			if err != nil {
				return fmt.Errorf("第 %d 条信任记录的公钥不是合法 base64: %w", i, err)
			}
			if len(raw) != ed25519.PublicKeySize {
				return fmt.Errorf("第 %d 条信任记录的公钥长度异常", i)
			}
			if got := KeyID(ed25519.PublicKey(raw)); !strings.EqualFold(got, k.KeyID) {
				return fmt.Errorf("第 %d 条信任记录的公钥与 keyId 不匹配（keyId 应为 %s）", i, got)
			}
		}
	}
	return nil
}

// NewEmptyKeyRing 返回一个空信任库。
func NewEmptyKeyRing() *KeyRing { return &KeyRing{Keys: []TrustedKey{}} }

// PublicKeyFileSuffix 是公钥分发文件的后缀。
//
// keygen 会额外写一个 `<名字><后缀>` 的公钥文件，专门用来交给管理员。
// 为什么要单独一个文件：私钥文件里**同时包含私钥与公钥**，
// 而"把你的密钥文件发给我"是极其自然的说法 —— 一旦照做就是私钥泄漏。
// 给一个只有公钥、名字里就写着 pub 的文件，能把这条歧义堵住。
const PublicKeyFileSuffix = ".pub.json"

// PublicKeyBundle 是公钥分发文件的内容。
type PublicKeyBundle struct {
	KeyID     string `json:"keyId"`
	Name      string `json:"name,omitempty"`
	Algorithm string `json:"algorithm"`
	PublicKey string `json:"publicKey"`
	// Notice 写给拿到这个文件的人看：这不是密钥，可以公开。
	Notice string `json:"notice"`
}

// NewPublicKeyBundle 构造公钥分发文件内容。
func NewPublicKeyBundle(name string, pub ed25519.PublicKey) *PublicKeyBundle {
	return &PublicKeyBundle{
		KeyID:     KeyID(pub),
		Name:      name,
		Algorithm: "ed25519",
		PublicKey: base64.StdEncoding.EncodeToString(pub),
		Notice: "这是**公钥**，可以安全地公开或交给管理员加进信任库。" +
			"私钥在另一个文件里，绝不要发送或提交它。",
	}
}

// TrustRecordFromPublicKey 由 base64 公钥构造一条信任记录。
//
// 这是"建立信任"的唯一推荐入口：keyId 由公钥推导，两者必然自洽，
// 从根上排除"keyId 写的是可信发布者、公钥却是别人的"这种替换。
func TrustRecordFromPublicKey(pubBase64, name string) (TrustedKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pubBase64))
	if err != nil {
		return TrustedKey{}, fmt.Errorf("公钥不是合法 base64: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return TrustedKey{}, fmt.Errorf("公钥长度 %d 异常（Ed25519 公钥应为 %d 字节）",
			len(raw), ed25519.PublicKeySize)
	}
	pub := ed25519.PublicKey(raw)
	return TrustedKey{
		KeyID:     KeyID(pub),
		Name:      strings.TrimSpace(name),
		PublicKey: base64.StdEncoding.EncodeToString(pub),
	}, nil
}

// TrustRecordFromKeyID 由裸 keyId 构造一条信任记录。
//
// 允许这样做（有时发布者只公布了 keyId），但**不如给公钥安全**：
// keyId 是公钥的摘要，无法反向校验，所以"同一个 keyId 配了别人的公钥"
// 这种替换挡住不。调用方应当把这一点提示给管理员。
func TrustRecordFromKeyID(keyID, name string) (TrustedKey, error) {
	id := strings.ToLower(strings.TrimSpace(keyID))
	rec := TrustedKey{KeyID: id, Name: strings.TrimSpace(name)}
	probe := NewEmptyKeyRing()
	probe.Add(rec)
	if err := probe.Validate(); err != nil {
		return TrustedKey{}, fmt.Errorf("keyId %q 不合法: %w", keyID, err)
	}
	return rec, nil
}

// ParsePublicKeyBundle 解析公钥分发文件。
func ParsePublicKeyBundle(raw []byte) (*PublicKeyBundle, error) {
	var b PublicKeyBundle
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, fmt.Errorf("公钥文件不是合法 JSON: %w", err)
	}
	if b.PublicKey == "" {
		return nil, errors.New("公钥文件缺少 publicKey 字段")
	}
	rec, err := TrustRecordFromPublicKey(b.PublicKey, b.Name)
	if err != nil {
		return nil, err
	}
	if b.KeyID != "" && !strings.EqualFold(b.KeyID, rec.KeyID) {
		return nil, fmt.Errorf("公钥文件自述 keyId(%s) 与公钥(%s) 不匹配", b.KeyID, rec.KeyID)
	}
	return &b, nil
}

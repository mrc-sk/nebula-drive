package main

import (
	"crypto/ed25519"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nebula-drive/nebula/pkg/plugin/install"
	"github.com/nebula-drive/nebula/pkg/plugin/pbclus"
)

// keygen 与 trust：把"签名"这件事的两端补齐。
//
// 签名本身只是密码学；**信任**才是安全语义所在。发布者用 keygen 生成密钥
// 并公布 keyId/公钥，管理员用 trust 把那个 keyId 加进宿主的信任库 ——
// 只有这两步都做完，"已由受信任的发布者签名"才成立。
//
// 没有信任库时宿主不会信任任何签名（不会退化成"信任所有签名"）。

// ---------- keygen ----------

func cmdKeygen(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "", "私钥输出文件（默认 pbclus-signing-key.json）")
	name := fs.String("name", "", "发布者名字（显示用）")
	force := fs.Bool("force", false, "覆盖已存在的文件")
	if err := fs.Parse(reorderArgs(args, map[string]bool{"force": true})); err != nil {
		return exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(stderr, "用法: pbclus-pack keygen [-o 密钥文件] [--name 发布者名字] [--force]")
		return exitUsage
	}
	outPath := *out
	if outPath == "" {
		outPath = "pbclus-signing-key.json"
	}
	if abs, err := filepath.Abs(outPath); err == nil {
		outPath = abs
	}
	if !*force {
		if _, err := os.Stat(outPath); err == nil {
			fmt.Fprintf(stderr, "%s 已存在（用 --force 覆盖）\n"+
				"注意: 覆盖会让已发布的包再也无法用新密钥验证 —— 除非你确实要换密钥\n", outPath)
			return exitFail
		}
	}

	key, err := pbclus.GenerateSigningKey(*name)
	if err != nil {
		fmt.Fprintf(stderr, "生成密钥失败: %v\n", err)
		return exitFail
	}
	if err := key.Save(outPath); err != nil {
		fmt.Fprintf(stderr, "写入密钥失败: %v\n", err)
		return exitFail
	}

	// 额外写一个**只含公钥**的分发文件。
	//
	// 动机是堵一个很自然的误操作："把你的密钥文件发给我" ——
	// 私钥文件里同时含私钥与公钥，照做就是私钥泄漏。
	// 给一个名字里带 pub、内容只有公钥的文件，并让它成为
	// "交给管理员"的那个文件，歧义就没了。
	pub, err := key.PublicKeyBytes()
	if err != nil {
		fmt.Fprintf(stderr, "公钥解析失败: %v\n", err)
		return exitFail
	}
	pubPath := strings.TrimSuffix(outPath, filepath.Ext(outPath)) + pbclus.PublicKeyFileSuffix
	if err := writePublicKeyFile(pubPath, key.Name, pub, *force); err != nil {
		fmt.Fprintf(stderr, "写入公钥文件失败: %v\n", err)
		return exitFail
	}

	fmt.Fprintf(stdout, "已生成 Ed25519 签名密钥\n\n")
	fmt.Fprintf(stdout, "  私钥      %s   ⚠ 只留在本机，不要发送或提交\n", outPath)
	fmt.Fprintf(stdout, "  公钥      %s   ← 把这个文件交给管理员\n", pubPath)
	fmt.Fprintf(stdout, "\n  keyId     %s\n", key.KeyID)
	fmt.Fprintf(stdout, "  公钥      %s\n", key.PublicKey)
	fmt.Fprintf(stdout, "\n接下来:\n")
	fmt.Fprintf(stdout, "  1. 打包时签名:\n")
	fmt.Fprintf(stdout, "       pbclus-pack pack ./myplugin --sign %s -o myplugin.pbclus\n",
		filepath.Base(outPath))
	fmt.Fprintf(stdout, "  2. 把 **%s** 发给管理员，让他在「插件 → 签名与信任」里加进来\n",
		filepath.Base(pubPath))
	fmt.Fprintf(stdout, "     （或命令行: pbclus-pack trust add --key %s --name %q）\n",
		filepath.Base(pubPath), firstName(*name))
	fmt.Fprintf(stdout, "\n⚠ 私钥泄漏 = 别人可以冒充你发布插件:\n")
	fmt.Fprintf(stdout, "    · 不要提交进版本库（把 %s 加进 .gitignore）\n", filepath.Base(outPath))
	fmt.Fprintf(stdout, "    · 不要贴进聊天/工单；要给别人就用上面那个公钥文件\n")
	if key.Name != "" {
		fmt.Fprintf(stdout, "\n发布者名字: %s\n", key.Name)
	}
	return exitOK
}

// writePublicKeyFile 写公钥分发文件。
func writePublicKeyFile(path, name string, pub ed25519.PublicKey, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s 已存在（用 --force 覆盖）", path)
		}
	}
	b, err := json.MarshalIndent(pbclus.NewPublicKeyBundle(name, pub), "", "  ")
	if err != nil {
		return err
	}
	// 0644：公钥本来就是要公开的，权限放开不影响安全，
	// 反而方便备份/拷贝。私钥才是 0600。
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func firstName(n string) string {
	if n == "" {
		return "发布者名字"
	}
	return n
}

// ---------- trust ----------

func cmdTrust(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		trustUsage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "list":
		return trustList(args[1:], stdout, stderr)
	case "add":
		return trustAdd(args[1:], stdout, stderr)
	case "remove", "rm":
		return trustRemove(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "未知子命令 %q\n\n", args[0])
		trustUsage(stderr)
		return exitUsage
	}
}

func trustUsage(w io.Writer) {
	fmt.Fprint(w, `信任库管理（宿主的信任库文件是数据目录下的 `+install.TrustStoreFileName+`）

用法:
  pbclus-pack trust list   [-f 信任库文件]
  pbclus-pack trust add    --key <密钥文件|公钥base64|keyId> --name <名字> [-f 信任库文件] [--force]
  pbclus-pack trust remove <keyId> [-f 信任库文件]

说明:
  · 信任库决定"哪些签名者的包可以被信任"。没有信任库时宿主
    **不会**信任任何签名 —— 不会退化成"信任所有签名"。
  · 只写 keyId 也能加，但强烈建议用发布者给的密钥文件/公钥：
    只凭 keyId 无法防止"同一个 keyId 配了别人的公钥"。
`)
}

// trustStorePathOf 解析信任库路径（默认当前目录下的文件名）。
func trustStorePathOf(flagValue string) string {
	p := flagValue
	if p == "" {
		p = install.TrustStoreFileName
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func trustList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("trust list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f := fs.String("f", "", "信任库文件路径")
	if err := fs.Parse(reorderArgs(args, map[string]bool{})); err != nil {
		return exitUsage
	}
	path := trustStorePathOf(*f)
	ring, err := install.LoadTrustStore(path)
	if err != nil {
		fmt.Fprintf(stderr, "读取信任库失败: %v\n", err)
		return exitFail
	}
	if len(ring.Keys) == 0 {
		fmt.Fprintf(stdout, "信任库 %s 为空：当前**没有**信任任何签名者。\n", path)
		fmt.Fprintln(stdout, "（这是安全默认值：空信任库 ≠ 信任所有签名）")
		return exitOK
	}
	fmt.Fprintf(stdout, "信任库 %s（%d 个发布者）\n\n", path, len(ring.Keys))
	sorted := append([]pbclus.TrustedKey(nil), ring.Keys...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	fmt.Fprintf(stdout, "  %-18s %-24s %s\n", "keyId", "名字", "公钥")
	for _, k := range sorted {
		pk := "(未记录)"
		if k.PublicKey != "" {
			if len(k.PublicKey) > 20 {
				pk = k.PublicKey[:20] + "…"
			} else {
				pk = k.PublicKey
			}
		}
		fmt.Fprintf(stdout, "  %-18s %-24s %s\n", k.KeyID, k.Name, pk)
	}
	return exitOK
}

func trustAdd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("trust add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	keyArg := fs.String("key", "", "密钥文件 / 公钥 base64 / keyId")
	name := fs.String("name", "", "发布者名字")
	f := fs.String("f", "", "信任库文件路径")
	force := fs.Bool("force", false, "覆盖同 keyId 的已有记录")
	if err := fs.Parse(reorderArgs(args, map[string]bool{"force": true})); err != nil {
		return exitUsage
	}
	if *keyArg == "" || *name == "" {
		fmt.Fprintln(stderr, "用法: pbclus-pack trust add --key <密钥文件|公钥|keyId> --name <名字>")
		return exitUsage
	}

	rec, err := resolveTrustRecord(*keyArg, *name)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return exitFail
	}

	path := trustStorePathOf(*f)
	ring, err := install.LoadTrustStore(path)
	if err != nil {
		// 文件存在但坏了：不静默重建，否则会把管理员已有的信任关系清掉
		fmt.Fprintf(stderr, "读取信任库失败（不自动重建，避免抹掉已有信任关系）: %v\n", err)
		return exitFail
	}
	if _, exists := ring.Lookup(rec.KeyID); exists && !*force {
		fmt.Fprintf(stderr, "keyId %s 已在信任库里（用 --force 更新名字）\n", rec.KeyID)
		return exitFail
	}
	rec.AddedAt = time.Now().UTC().Format(time.RFC3339)
	ring.Add(rec)
	if err := install.SaveTrustStore(path, ring); err != nil {
		fmt.Fprintf(stderr, "写入信任库失败: %v\n", err)
		return exitFail
	}
	fmt.Fprintf(stdout, "已信任 %s（%s）\n", rec.Name, rec.KeyID)
	fmt.Fprintf(stdout, "信任库: %s（共 %d 个发布者）\n", path, len(ring.Keys))
	fmt.Fprintln(stdout, "\n把该文件放到宿主的**数据目录**下（文件名 "+install.TrustStoreFileName+"）即可生效。")
	return exitOK
}

// resolveTrustRecord 把 --key 的四种写法都解析成一条信任记录：
// 公钥分发文件（.pub.json）、私钥/密钥文件（取其公钥）、公钥 base64、裸 keyId。
//
// 解析逻辑本身在 pbclus 包里（`TrustRecordFromPublicKey` /
// `TrustRecordFromKeyID`），这样管理端 API 与命令行走的是**同一套**校验 ——
// 各写一遍的下场是"某个入口能加进一个自相矛盾的记录"。
func resolveTrustRecord(arg, name string) (pbclus.TrustedKey, error) {
	// 1) 文件
	if st, err := os.Stat(arg); err == nil && !st.IsDir() {
		b, err := os.ReadFile(arg)
		if err != nil {
			return pbclus.TrustedKey{}, fmt.Errorf("读取 %s 失败: %w", arg, err)
		}
		// 1a) 公钥分发文件（推荐路径）
		if bundle, err := pbclus.ParsePublicKeyBundle(b); err == nil {
			if name == "" {
				name = bundle.Name
			}
			return pbclus.TrustRecordFromPublicKey(bundle.PublicKey, name)
		}
		// 1b) 私钥文件：从中取公钥。本地使用没问题，
		// 但仍以"只取公钥"的方式入库，绝不把私钥写进信任库。
		var sk pbclus.SigningKey
		if err := json.Unmarshal(b, &sk); err == nil && sk.PublicKey != "" {
			if name == "" {
				name = sk.Name
			}
			return pbclus.TrustRecordFromPublicKey(sk.PublicKey, name)
		}
		// 1c) 退化成"文件里就一行公钥/base64"的写法
		txt := strings.TrimSpace(string(b))
		if txt == "" {
			return pbclus.TrustedKey{}, fmt.Errorf("%s 是空文件", arg)
		}
		arg = txt
	}

	// 2) 公钥 base64
	if rec, err := pbclus.TrustRecordFromPublicKey(arg, name); err == nil {
		return rec, nil
	}

	// 3) 裸 keyId
	if len(strings.TrimSpace(arg)) == pbclus.KeyIDLen {
		return pbclus.TrustRecordFromKeyID(arg, name)
	}

	return pbclus.TrustedKey{}, fmt.Errorf("无法把 %q 识别为公钥文件、密钥文件、公钥或 keyId。"+
		"推荐使用 keygen 生成的公钥文件（*%s）", arg, pbclus.PublicKeyFileSuffix)
}

func trustRemove(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("trust remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f := fs.String("f", "", "信任库文件路径")
	if err := fs.Parse(reorderArgs(args, map[string]bool{})); err != nil {
		return exitUsage
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "用法: pbclus-pack trust remove <keyId> [-f 信任库文件]")
		return exitUsage
	}
	path := trustStorePathOf(*f)
	ring, err := install.LoadTrustStore(path)
	if err != nil {
		fmt.Fprintf(stderr, "读取信任库失败: %v\n", err)
		return exitFail
	}
	if !ring.Remove(rest[0]) {
		fmt.Fprintf(stderr, "信任库里没有 keyId %s\n", rest[0])
		return exitFail
	}
	if err := install.SaveTrustStore(path, ring); err != nil {
		fmt.Fprintf(stderr, "写入信任库失败: %v\n", err)
		return exitFail
	}
	fmt.Fprintf(stdout, "已移除 %s（剩 %d 个发布者）\n", rest[0], len(ring.Keys))
	fmt.Fprintln(stdout, "注意: 之后用该密钥签的包会被判为「未受信任」。已装的插件不受影响。")
	return exitOK
}

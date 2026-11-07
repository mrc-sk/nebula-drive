package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 这组测试走的是**真实文件系统**，不是 mock：
// 插件的行为边界（原子写、轮转白名单、符号链接、校验失败清理）
// 只有真的落盘才验证得出来。
//
// 重点覆盖"会不会删错东西"与"失败时留下什么"，
// 而不是"函数有没有被调用"。

// ---- 测试小工具 ----

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// backupName 生成与宿主一致的备份名：backup-<unix>-<8hex>.zip。
func backupName(ts int64) string {
	return fmt.Sprintf("backup-%d-%08x.zip", ts, ts)
}

// writeBackup 在 dir 里造一个备份包，内容即 content。
func writeBackup(t *testing.T, dir string, ts int64, content []byte) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	p := filepath.Join(dir, backupName(ts))
	if err := os.WriteFile(p, content, 0o600); err != nil {
		t.Fatalf("写测试备份失败: %v", err)
	}
	return p
}

// backupCtx 模拟宿主下发的 onBackup ctx。
//
// size 刻意用 float64：宿主 ctx 经 JSON 往返后数字就是 float64，
// 测试必须复现这一点（用 int64 会掩盖"assert 成 int64 静默拿到 0"的 bug）。
func backupCtx(path, filename string, content []byte) map[string]any {
	return map[string]any{
		"kind":       "full",
		"filename":   filename,
		"path":       path,
		"size":       float64(len(content)),
		"sha256":     sha256Hex(content),
		"dbPath":     "/data/nebula.db",
		"dbIncluded": true,
		"fileCount":  float64(42),
		"createdAt":  time.Now().Format(time.RFC3339),
		"operator":   "admin",
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", path, err)
	}
	return b
}

// matchingBackups 返回目录里匹配轮转规则的文件名（排序后）。
func matchingBackups(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取目录 %s 失败: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		if backupFilePattern.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}

func assertNoPart(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取目录失败: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".part") {
			t.Fatalf("目录里残留了半截文件 %s", e.Name())
		}
	}
}

// ---- 正常镜像 ----

// TestMirrorBackupCopiesFile 正常路径：源包被流式复制到每个 target，
// 内容逐字节一致，且不留 .part。
//
// target 特意包含一个不存在的多级路径，验证 MkdirAll。
func TestMirrorBackupCopiesFile(t *testing.T) {
	name := backupName(1700000000)
	content := []byte("PK\x03\x04 fake zip payload for mirror test")
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, name)
	if err := os.WriteFile(src, content, 0o600); err != nil {
		t.Fatalf("写源文件失败: %v", err)
	}

	base := t.TempDir()
	target1 := filepath.Join(base, "mirror-a")
	target2 := filepath.Join(base, "mirror-b", "nested")

	st := &pluginState{cfg: Config{
		Targets: []string{target1, target2},
		Keep:    3,
		Verify:  true,
	}}

	patch := st.mirrorBackup(backupCtx(src, name, content))
	if patch["backupMirror"] != "ok" {
		t.Fatalf("镜像结果 = %v，期望 ok", patch)
	}
	if patch["mirrored"] != 2 {
		t.Errorf("mirrored = %v，期望 2", patch["mirrored"])
	}

	for _, target := range []string{target1, target2} {
		got := mustRead(t, filepath.Join(target, name))
		if string(got) != string(content) {
			t.Errorf("%s 内容与源文件不一致:\n got=%q\nwant=%q", target, got, content)
		}
		assertNoPart(t, target)
	}
}

// TestMirrorBackupCreatesTarget 明确覆盖"target 不存在 → MkdirAll 创建"。
func TestMirrorBackupCreatesTarget(t *testing.T) {
	content := []byte("payload")
	name := backupName(1700000100)
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, name)
	if err := os.WriteFile(src, content, 0o600); err != nil {
		t.Fatalf("写源文件失败: %v", err)
	}

	target := filepath.Join(t.TempDir(), "deep", "not", "exist")
	st := &pluginState{cfg: Config{Targets: []string{target}, Keep: 1, Verify: true}}
	st.mirrorBackup(backupCtx(src, name, content))

	if _, err := os.Stat(filepath.Join(target, name)); err != nil {
		t.Fatalf("target 未被创建或文件未镜像: %v", err)
	}
}

// ---- 轮转 ----

// TestRotationKeepsNewestThree keep=3 时造 5 个旧包再镜像 1 个新包：
// 轮转后必须只剩最新 3 个（按文件名里的 unix 排序），内容与名字对应。
func TestRotationKeepsNewestThree(t *testing.T) {
	target := t.TempDir()
	contents := map[string]string{}
	for ts := int64(1000); ts <= 5000; ts += 1000 {
		c := []byte(fmt.Sprintf("payload-%d", ts))
		writeBackup(t, target, ts, c)
		contents[backupName(ts)] = string(c)
	}

	// 新备份（ts=6000）通过 onBackup 路径进入 target。
	srcDir := t.TempDir()
	newTS := int64(6000)
	newContent := []byte("payload-6000-new")
	src := filepath.Join(srcDir, backupName(newTS))
	if err := os.WriteFile(src, newContent, 0o600); err != nil {
		t.Fatalf("写新备份失败: %v", err)
	}
	contents[backupName(newTS)] = string(newContent)

	st := &pluginState{cfg: Config{Targets: []string{target}, Keep: 3, Verify: true}}
	st.mirrorBackup(backupCtx(src, backupName(newTS), newContent))

	remains := matchingBackups(t, target)
	want := []string{backupName(6000), backupName(5000), backupName(4000)}
	if len(remains) != len(want) {
		t.Fatalf("轮转后剩余 %d 个（%v），期望 3 个", len(remains), remains)
	}
	for _, name := range want {
		if got := string(mustRead(t, filepath.Join(target, name))); got != contents[name] {
			t.Errorf("%s 内容 = %q，期望 %q", name, got, contents[name])
		}
	}
	// 旧的三个必须真的被删掉
	for _, ts := range []int64{1000, 2000, 3000} {
		if _, err := os.Stat(filepath.Join(target, backupName(ts))); !os.IsNotExist(err) {
			t.Errorf("旧备份 %s 应被删除，stat err = %v", backupName(ts), err)
		}
	}
}

// TestRotationLeavesUnrelatedFilesAlone 这条是安全边界：
// 轮转绝不能碰目录里不匹配命名规则的东西（用户可能把别的东西也放在这儿）。
func TestRotationLeavesUnrelatedFilesAlone(t *testing.T) {
	target := t.TempDir()
	for ts := int64(1000); ts <= 4000; ts += 1000 {
		writeBackup(t, target, ts, []byte(fmt.Sprintf("payload-%d", ts)))
	}

	unrelated := map[string][]byte{
		"notes.txt":                []byte("管理员自己的说明，不能删"),
		"archive.zip":              []byte("别的工具的备份，不能删"),
		"backup-5000-ABCDEF12.zip": []byte("大写 hex 不匹配规则，不能删"),
		"backup-5000-short.zip":    []byte("命名不匹配，不能删"),
		"backup-5000-deadbeef.tar": []byte("扩展名不匹配，不能删"),
	}
	for name, content := range unrelated {
		if err := os.WriteFile(filepath.Join(target, name), content, 0o600); err != nil {
			t.Fatalf("写无关文件 %s 失败: %v", name, err)
		}
	}

	// 直接调用轮转：keep=2，只应保留 ts=4000,3000 这两个匹配的文件。
	removed := rotateTarget(target, 2)
	if len(removed) != 2 {
		t.Errorf("删除了 %d 个文件（%v），期望 2 个", len(removed), removed)
	}

	for name, content := range unrelated {
		got, err := os.ReadFile(filepath.Join(target, name))
		if err != nil {
			t.Errorf("无关文件 %s 被误删: %v", name, err)
			continue
		}
		if string(got) != string(content) {
			t.Errorf("无关文件 %s 内容被改动: %q", name, got)
		}
	}

	remains := matchingBackups(t, target)
	if len(remains) != 2 {
		t.Fatalf("匹配规则的备份剩余 %v，期望 2 个", remains)
	}
	for _, name := range remains {
		if name != backupName(3000) && name != backupName(4000) {
			t.Errorf("保留了不该保留的备份 %s", name)
		}
	}
}

// TestRotationSkipsSymlinks 符号链接绝不跟随、也不删除。
//
// 造一个名字匹配规则、指向别处文件的链接。若插件跟随它删除，
// 被指向的文件会消失；若把它计入 keep，真实文件会被多删一个。
// Windows 上普通用户可能没有创建符号链接的权限 → t.Skip。
func TestRotationSkipsSymlinks(t *testing.T) {
	target := t.TempDir()
	for ts := int64(1000); ts <= 4000; ts += 1000 {
		writeBackup(t, target, ts, []byte(fmt.Sprintf("payload-%d", ts)))
	}

	victimDir := t.TempDir()
	victim := filepath.Join(victimDir, "victim.txt")
	if err := os.WriteFile(victim, []byte("must survive"), 0o600); err != nil {
		t.Fatalf("写受害文件失败: %v", err)
	}

	link := filepath.Join(target, "backup-9999999999-deadbeef.zip")
	if err := os.Symlink(victim, link); err != nil {
		t.Skipf("当前环境无法创建符号链接，跳过该用例: %v", err)
	}

	rotateTarget(target, 2)

	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("符号链接指向的文件被删/不可访问: %v", err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("符号链接本身被删了: %v", err)
	}
	remains := matchingBackups(t, target)
	// 4 个真实文件 keep=2 → 3000/4000；链接不算、也不删。
	want := map[string]bool{backupName(3000): true, backupName(4000): true, "backup-9999999999-deadbeef.zip": true}
	if len(remains) != len(want) {
		t.Fatalf("剩余条目 %v，期望 %d 个", remains, len(want))
	}
	for _, name := range remains {
		if !want[name] {
			t.Errorf("不该保留的条目: %s", name)
		}
	}
}

// TestKeepZeroDoesNotRotate keep=0 表示保留全部：镜像后旧备份一个都不能少。
func TestKeepZeroDoesNotRotate(t *testing.T) {
	target := t.TempDir()
	oldNames := make([]string, 0, 5)
	for ts := int64(1000); ts <= 5000; ts += 1000 {
		writeBackup(t, target, ts, []byte(fmt.Sprintf("payload-%d", ts)))
		oldNames = append(oldNames, backupName(ts))
	}

	content := []byte("new payload")
	name := backupName(6000)
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, name)
	if err := os.WriteFile(src, content, 0o600); err != nil {
		t.Fatalf("写源文件失败: %v", err)
	}

	st := &pluginState{cfg: Config{Targets: []string{target}, Keep: 0, Verify: true}}
	st.mirrorBackup(backupCtx(src, name, content))

	for _, old := range oldNames {
		if _, err := os.Stat(filepath.Join(target, old)); err != nil {
			t.Errorf("keep=0 时旧备份 %s 不应被删除: %v", old, err)
		}
	}
	if got := len(matchingBackups(t, target)); got != 6 {
		t.Errorf("keep=0 后应有 6 个备份，实际 %d", got)
	}
}

// ---- 校验失败 ----

// TestVerifyMismatchDeletesPartAndTarget 校验不一致时必须删掉临时文件，
// 且正式文件名绝不出现。
//
// 通过 testHookAfterCopy 在校验前真的篡改 .part —— 不是用一个假摘要糊弄，
// 而是复现"复制过程中数据损坏/被改"的场景。
func TestVerifyMismatchDeletesPartAndTarget(t *testing.T) {
	content := []byte("the original backup payload")
	name := backupName(1700000200)
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, name)
	if err := os.WriteFile(src, content, 0o600); err != nil {
		t.Fatalf("写源文件失败: %v", err)
	}

	target := t.TempDir()
	origHook := testHookAfterCopy
	testHookAfterCopy = func(partPath string) {
		// 模拟复制后数据被损坏：内容变了，但文件名仍是 .part。
		if err := os.WriteFile(partPath, []byte("tampered!"), 0o600); err != nil {
			t.Fatalf("篡改 .part 失败: %v", err)
		}
	}
	t.Cleanup(func() { testHookAfterCopy = origHook })

	st := &pluginState{cfg: Config{Targets: []string{target}, Keep: 3, Verify: true}}
	patch := st.mirrorBackup(backupCtx(src, name, content))

	if patch["backupMirror"] != "failed" {
		t.Errorf("校验失败时结果 = %v，期望 failed", patch)
	}
	if _, err := os.Stat(filepath.Join(target, name)); !os.IsNotExist(err) {
		t.Fatalf("校验失败后正式文件不应存在（stat err=%v）", err)
	}
	if _, err := os.Stat(filepath.Join(target, name+".part")); !os.IsNotExist(err) {
		t.Fatalf("校验失败后 .part 必须被删掉（stat err=%v）", err)
	}
	assertNoPart(t, target)
}

// TestVerifyMismatchWithFakeSHA 另一个角度：ctx 的 sha256 本身是假的
// （例如宿主摘要算错/被篡改），同样不能落下正式文件。
func TestVerifyMismatchWithFakeSHA(t *testing.T) {
	content := []byte("payload")
	name := backupName(1700000300)
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, name)
	if err := os.WriteFile(src, content, 0o600); err != nil {
		t.Fatalf("写源文件失败: %v", err)
	}

	target := t.TempDir()
	ctx := backupCtx(src, name, content)
	ctx["sha256"] = strings.Repeat("0", 64) // 与真实摘要必然不符

	st := &pluginState{cfg: Config{Targets: []string{target}, Keep: 3, Verify: true}}
	st.mirrorBackup(ctx)

	if _, err := os.Stat(filepath.Join(target, name)); !os.IsNotExist(err) {
		t.Fatalf("摘要不符时正式文件不应存在（stat err=%v）", err)
	}
	assertNoPart(t, target)
}

// TestVerifyDisabledSkipsChecksum verify=false 时不做摘要比对，
// 即使 ctx 的 sha256 是假的也应完成镜像。
func TestVerifyDisabledSkipsChecksum(t *testing.T) {
	content := []byte("payload")
	name := backupName(1700000400)
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, name)
	if err := os.WriteFile(src, content, 0o600); err != nil {
		t.Fatalf("写源文件失败: %v", err)
	}

	target := t.TempDir()
	ctx := backupCtx(src, name, content)
	ctx["sha256"] = strings.Repeat("f", 64)

	st := &pluginState{cfg: Config{Targets: []string{target}, Keep: 3, Verify: false}}
	patch := st.mirrorBackup(ctx)
	if patch["backupMirror"] != "ok" {
		t.Fatalf("verify=false 时应成功，实际 %v", patch)
	}
	if got := mustRead(t, filepath.Join(target, name)); string(got) != string(content) {
		t.Errorf("内容不一致: %q", got)
	}
}

// ---- target 校验 ----

// TestSameDirTargetRejected target 与源文件同目录必须跳过：
// 那不是镜像，还会让轮转把唯一一份备份算进 keep。
func TestSameDirTargetRejected(t *testing.T) {
	content := []byte("payload")
	name := backupName(1700000500)
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, name)
	if err := os.WriteFile(src, content, 0o600); err != nil {
		t.Fatalf("写源文件失败: %v", err)
	}

	st := &pluginState{cfg: Config{Targets: []string{srcDir}, Keep: 3, Verify: true}}
	patch := st.mirrorBackup(backupCtx(src, name, content))

	if patch["backupMirror"] != "skipped" {
		t.Errorf("同目录 target 应被跳过，实际 %v", patch)
	}
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		t.Fatalf("读取源目录失败: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != name {
		t.Fatalf("同目录不应产生任何新文件，实际 %v", entries)
	}
	assertNoPart(t, srcDir)
}

// TestFailOneTargetStillMirrorsOthers 单个 target 建不出来不影响其它 target。
func TestFailOneTargetStillMirrorsOthers(t *testing.T) {
	content := []byte("payload")
	name := backupName(1700000600)
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, name)
	if err := os.WriteFile(src, content, 0o600); err != nil {
		t.Fatalf("写源文件失败: %v", err)
	}

	// 用一个普通文件充当 target 的父目录 → MkdirAll 必然失败。
	blockerDir := t.TempDir()
	blocker := filepath.Join(blockerDir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("写占位文件失败: %v", err)
	}
	badTarget := filepath.Join(blocker, "sub")
	goodTarget := filepath.Join(t.TempDir(), "good")

	st := &pluginState{cfg: Config{Targets: []string{badTarget, goodTarget}, Keep: 3, Verify: true}}
	patch := st.mirrorBackup(backupCtx(src, name, content))

	if patch["backupMirror"] != "partial" {
		t.Errorf("一个 target 失败时结果应为 partial，实际 %v", patch)
	}
	if _, err := os.Stat(filepath.Join(goodTarget, name)); err != nil {
		t.Errorf("可用 target 应完成镜像: %v", err)
	}
}

// ---- onRestore 审计 ----

// TestAuditRestoreAppendsJSONL onRestore 必须在 dataDir 下追加一行 JSONL，
// 字段与 ctx 对齐（size 是 JSON 数字，且在 ctx 里是 float64）。
func TestAuditRestoreAppendsJSONL(t *testing.T) {
	dataDir := t.TempDir()
	st := &pluginState{cfg: defaultConfig(), dataDir: dataDir}

	first := map[string]any{
		"filename":   "backup-1700000000-deadbeef.zip",
		"size":       float64(12345),
		"stagedPath": filepath.Join(dataDir, "restore", "nebula.db"),
		"currentDb":  "/data/nebula.db",
		"operator":   "alice",
	}
	if patch := st.auditRestore(first); patch["restoreAudit"] != "logged" {
		t.Fatalf("第一次审计结果 = %v", patch)
	}
	second := map[string]any{
		"filename":   "backup-1700000001-cafebabe.zip",
		"size":       float64(678),
		"stagedPath": "/tmp/staged.db",
		"operator":   "bob",
	}
	if patch := st.auditRestore(second); patch["restoreAudit"] != "logged" {
		t.Fatalf("第二次审计结果 = %v", patch)
	}

	raw := mustRead(t, filepath.Join(dataDir, auditFileName))
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("审计应有 2 行，实际 %d 行:\n%s", len(lines), raw)
	}

	var got restoreAuditRecord
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("第一行不是合法 JSON: %v（%q）", err, lines[0])
	}
	if got.Filename != first["filename"] {
		t.Errorf("filename = %q，期望 %q", got.Filename, first["filename"])
	}
	if got.Size != 12345 {
		t.Errorf("size = %d，期望 12345", got.Size)
	}
	if got.Operator != "alice" {
		t.Errorf("operator = %q，期望 alice", got.Operator)
	}
	if got.StagedPath != first["stagedPath"] {
		t.Errorf("stagedPath = %q，期望 %q", got.StagedPath, first["stagedPath"])
	}
	if _, err := time.Parse(time.RFC3339Nano, got.Time); err != nil {
		t.Errorf("time 不是 RFC3339 时间: %q (%v)", got.Time, err)
	}

	var got2 restoreAuditRecord
	if err := json.Unmarshal([]byte(lines[1]), &got2); err != nil {
		t.Fatalf("第二行不是合法 JSON: %v", err)
	}
	if got2.Operator != "bob" || got2.Size != 678 {
		t.Errorf("第二行字段不符: %+v", got2)
	}
}

// TestAuditRestoreWithoutDataDirMustNotClaimSuccess dataDir 为空时不能假装写成功。
func TestAuditRestoreWithoutDataDirMustNotClaimSuccess(t *testing.T) {
	st := &pluginState{cfg: defaultConfig(), dataDir: ""}
	patch := st.auditRestore(map[string]any{"filename": "x.zip", "size": float64(1)})
	if patch["restoreAudit"] != "failed" {
		t.Fatalf("没有 dataDir 时应返回 failed，实际 %v", patch)
	}
}

// ---- onCLI ----

// TestCLICommandMatching 命令名匹配时输出每个 target 的统计；
// 不匹配时什么都不做（不能污染别的插件的 CLI 输出）。
func TestCLICommandMatching(t *testing.T) {
	target := t.TempDir()
	writeBackup(t, target, 1000, []byte("old"))
	writeBackup(t, target, 2000, []byte("new"))
	if err := os.WriteFile(filepath.Join(target, "notes.txt"), []byte("not a backup"), 0o600); err != nil {
		t.Fatalf("写 notes.txt 失败: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "not-created")

	st := &pluginState{cfg: Config{Targets: []string{target, missing}, Keep: 2, Verify: true}}

	// 不匹配：返回 nil / 空 patch，且不能有 cliOutput。
	for _, cmd := range []string{"", "smtp-email", "backup", "BACKUP-MIRROR"} {
		patch := st.cliStatus(map[string]any{"command": cmd})
		if patch != nil {
			t.Errorf("command=%q 时不应输出任何东西，实际 %v", cmd, patch)
		}
	}

	patch := st.cliStatus(map[string]any{"command": pluginName})
	if patch == nil {
		t.Fatal("命令匹配时应返回 ctxPatch")
	}
	out, ok := patch["cliOutput"].(string)
	if !ok || out == "" {
		t.Fatalf("cliOutput 不是非空字符串: %#v", patch["cliOutput"])
	}
	for _, want := range []string{
		pluginName,
		target,
		"2 个备份包",
		backupName(2000), // 最新
		backupName(1000), // 最旧
		missing,
		"目录不存在",
		"keep=2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("CLI 输出缺少 %q:\n%s", want, out)
		}
	}
	// notes.txt 不是备份包，不应被统计进总数（总数必须恰好 2）。
	if strings.Contains(out, "3 个备份包") {
		t.Errorf("无关文件被算进了备份统计:\n%s", out)
	}
}

// ---- init ----

// TestInitParsesConfigAndDedupesTargets init 要清洗 targets、保留 keep，
// 并且在配置没写 verify 时默认开启校验。
func TestInitParsesConfigAndDedupesTargets(t *testing.T) {
	st := newPluginState()
	res := st.init(initParams{
		HostVersion:  "26.10.0",
		HostProtocol: 1,
		DataDir:      t.TempDir(),
		Config: json.RawMessage(`{
			"targets": [" D:/nd-backup ", "", "D:/nd-backup", "/mnt/backup/nebula"],
			"keep": 7,
			"verify": false
		}`),
	})

	if !res.OK {
		t.Fatalf("init 应成功: %+v", res)
	}
	if res.Name != pluginName || res.Version != version {
		t.Errorf("init 返回 name/version = %q/%q", res.Name, res.Version)
	}
	wantHooks := []string{"onBackup", "onRestore", "onCLI"}
	if len(res.Hooks) != len(wantHooks) {
		t.Fatalf("hooks = %v，期望 %v", res.Hooks, wantHooks)
	}
	for i, h := range wantHooks {
		if res.Hooks[i] != h {
			t.Errorf("hooks[%d] = %q，期望 %q", i, res.Hooks[i], h)
		}
	}

	if len(st.cfg.Targets) != 2 {
		t.Errorf("targets 去空白/去重后应为 2 个，实际 %v", st.cfg.Targets)
	}
	if st.cfg.Targets[0] != "D:/nd-backup" {
		t.Errorf("target 未 trim: %q", st.cfg.Targets[0])
	}
	if st.cfg.Keep != 7 {
		t.Errorf("keep = %d，期望 7", st.cfg.Keep)
	}
	if st.cfg.Verify {
		t.Error(`配置写了 "verify": false，应保持 false`)
	}
}

// TestInitDefaultsWhenConfigMissing 配置为空时：verify 默认 true、keep 默认 0（不轮转）。
func TestInitDefaultsWhenConfigMissing(t *testing.T) {
	st := newPluginState()
	res := st.init(initParams{HostProtocol: 1, DataDir: t.TempDir()})
	if !res.OK {
		t.Fatalf("init 应成功: %+v", res)
	}
	if !st.cfg.Verify {
		t.Error("未配置 verify 时应默认 true")
	}
	if st.cfg.Keep != 0 {
		t.Errorf("未配置 keep 时应为 0（不轮转），实际 %d", st.cfg.Keep)
	}
	if len(st.cfg.Targets) != 0 {
		t.Errorf("未配置 targets 时应为空，实际 %v", st.cfg.Targets)
	}
}

// TestInitRejectsWrongProtocol 协议版本不匹配必须明确失败。
func TestInitRejectsWrongProtocol(t *testing.T) {
	st := newPluginState()
	res := st.init(initParams{HostProtocol: 2, DataDir: t.TempDir()})
	if res.OK {
		t.Fatal("hostProtocol != 1 时必须返回 ok=false")
	}
	if !strings.Contains(res.Error, "协议版本") {
		t.Errorf("错误信息应说明协议版本问题，实际 %q", res.Error)
	}
}

// TestSanitizeFilenameRejectsTraversal 文件名不能带路径成分。
func TestSanitizeFilenameRejectsTraversal(t *testing.T) {
	cases := map[string]string{
		"backup-1-deadbeef.zip":           "backup-1-deadbeef.zip",
		"../../etc/passwd":                "passwd",
		`..\..\windows\system32\evil.dll`: "evil.dll",
		"":                                "",
		".":                               "",
		"..":                              "",
	}
	for in, want := range cases {
		if got := sanitizeFilename(in); got != want {
			t.Errorf("sanitizeFilename(%q) = %q，期望 %q", in, got, want)
		}
	}
}

package host_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"mime"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nebula-drive/nebula/pkg/plugin"
	"github.com/nebula-drive/nebula/pkg/plugin/host"
	"github.com/nebula-drive/nebula/pkg/plugin/install"
)

// 这组测试跑通邮件外发的**完整链路**（除了"创建通知"那一步，它由
// controllers 的 notification_email_test.go 覆盖）：
//
//	host.Manager 下发配置 → 拉起 smtp-email 子进程 → 派发 onEmail
//	→ 插件读 ctx["to"/"subject"/"body"] → net/smtp → 假 SMTP 服务器
//
// 为什么必须是端到端、不能只测单侧：
//   - 字段名是宿主与插件两个 module 之间的约定，改名没有编译错误；
//   - 中文主题编码、报文组装这些只有真跑一次 SMTP 才能确认；
//   - 「插件收不到事件」和「收到了但字段没读对」表现完全一样（都发不出信），
//     只有断言到服务端收到的报文才能把两者区分开。

type smtpMsg struct {
	from string
	to   string
	data string
}

// fakeSMTP 起一个最小可用的 SMTP 服务器，把收到的第一封邮件送进通道。
func fakeSMTP(t *testing.T) (string, <-chan smtpMsg) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	ch := make(chan smtpMsg, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(20 * time.Second))

		br := bufio.NewReader(conn)
		say := func(s string) { _, _ = conn.Write([]byte(s)) }
		say("220 fake ESMTP\r\n")

		var msg smtpMsg
		inData := false
		var body strings.Builder
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if strings.TrimRight(line, "\r\n") == "." {
					inData = false
					msg.data = body.String()
					select {
					case ch <- msg:
					default:
					}
					say("250 2.0.0 queued\r\n")
					continue
				}
				body.WriteString(line)
				continue
			}
			switch up := strings.ToUpper(strings.TrimSpace(line)); {
			case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
				// 不声明任何扩展：测试用的是 tls=none，客户端不该要求 STARTTLS/AUTH
				say("250 fake\r\n")
			case strings.HasPrefix(up, "MAIL FROM"):
				msg.from = angleAddr(line)
				say("250 ok\r\n")
			case strings.HasPrefix(up, "RCPT TO"):
				msg.to = angleAddr(line)
				say("250 ok\r\n")
			case up == "DATA":
				inData = true
				say("354 end with <CRLF>.<CRLF>\r\n")
			case up == "QUIT":
				say("221 bye\r\n")
				return
			default:
				say("250 ok\r\n")
			}
		}
	}()
	return ln.Addr().String(), ch
}

func angleAddr(line string) string {
	i := strings.Index(line, "<")
	j := strings.LastIndex(line, ">")
	if i >= 0 && j > i {
		return line[i+1 : j]
	}
	return strings.TrimSpace(line)
}

// TestSMTPEmailPlugin_EndToEnd 完整链路：宿主的配置与事件真的变成了一封邮件。
func TestSMTPEmailPlugin_EndToEnd(t *testing.T) {
	const src = "../../../../examples/plugins/smtp-email"
	if _, err := os.Stat(filepath.Join(src, plugin.ManifestFileName)); err != nil {
		t.Skipf("示例插件源码不存在: %v", err)
	}

	addr, received := fakeSMTP(t)
	smtpHost, smtpPort, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("解析假服务器地址失败: %v", err)
	}

	// 编译 + 走真实安装器
	staged := t.TempDir()
	copyDirFiles(t, src, staged)
	entry := "smtp-email"
	if runtime.GOOS == "windows" {
		entry += ".exe"
	}
	buildInto(t, staged, entry)

	ins := install.New(t.TempDir())
	res, err := ins.InstallFromDir(staged, "test")
	if err != nil {
		t.Fatalf("安装 smtp-email 失败: %v", err)
	}
	man, err := install.ReadManifest(res.Dir)
	if err != nil {
		t.Fatalf("读取清单失败: %v", err)
	}

	// 宿主下发的配置：指向假 SMTP 服务器
	config := fmt.Sprintf(
		`{"host":%q,"port":%s,"tls":"none","from":"noreply@example.com","fromName":"星云网盘","timeoutSeconds":5}`,
		smtpHost, smtpPort)

	m := host.New(host.Config{
		PluginsDir:   ins.PluginsDir,
		DataDir:      t.TempDir(),
		HostVersion:  "test",
		StartTimeout: 30 * time.Second,
		FireTimeout:  10 * time.Second,
		Log:          t.Logf,
		ConfigFor:    func(string) json.RawMessage { return json.RawMessage(config) },
	})
	defer m.StopAll()

	if err := m.Load(host.LoadSpec{Name: man.Name, Dir: res.Dir, Manifest: man}); err != nil {
		t.Fatalf("加载 smtp-email 失败: %v", err)
	}

	// 与 controllers/ws.go 的 CreateNotification 完全一致的 ctx 字段名
	ctx := map[string]any{
		"to":      "user@example.com",
		"subject": "上传完成通知",
		"body":    "你的文件已就绪。",
	}
	for _, e := range m.DispatchSync(plugin.HookEmail, ctx) {
		if plugin.IsBlockedMessage(e.Error()) {
			t.Fatalf("邮件钩子不该拦截任何请求: %v", e)
		}
	}

	// 先看插件自报的结果，再等服务器收件。
	//
	// 这一步不只是"顺手断言"：如果两个 module 之间的字段名漂移了，
	// 插件会把收件人读成空字符串、直接回 skipped-no-recipient 且
	// **根本不连 SMTP** —— 那样只会等到超时，报错是"没收到邮件"，
	// 排查方向会跑偏到网络/配置上去。这里先判状态，能把
	// "字段名断了" 与 "网络发不出去" 立刻分开。
	switch patch := fmt.Sprint(ctx["smtpEmail"]); patch {
	case "skipped-no-recipient":
		t.Fatalf("插件判定收件人为空 —— 宿主发的是 ctx[\"to\"]，"+
			"插件读的键名对不上（两个 module 的字段名约定断了）。日志：%+v",
			m.Logs("smtp-email", 50))
	case "failed":
		t.Fatalf("插件投递失败，见日志：%+v", m.Logs("smtp-email", 50))
	case "sent":
		// 正常路径，继续等服务器收件
	default:
		t.Fatalf("插件未回报投递结果（smtpEmail=%q，期望 sent/failed/skipped-no-recipient）；日志：%+v",
			patch, m.Logs("smtp-email", 50))
	}

	select {
	case msg := <-received:
		if msg.from != "noreply@example.com" {
			t.Errorf("MAIL FROM = %q, want noreply@example.com", msg.from)
		}
		if msg.to != "user@example.com" {
			t.Fatalf("RCPT TO = %q —— 插件没从 ctx[\"to\"] 读到收件人（字段名约定断了）", msg.to)
		}
		rawSubject, ok := headerOf(msg.data, "Subject")
		if !ok {
			t.Fatalf("报文没有 Subject 头:\n%s", msg.data)
		}
		if !strings.Contains(rawSubject, "=?") {
			t.Errorf("中文主题必须 RFC 2047 编码，实际 = %q", rawSubject)
		}
		decoded, err := new(mime.WordDecoder).DecodeHeader(rawSubject)
		if err != nil {
			t.Fatalf("主题解码失败: %v (raw=%q)", err, rawSubject)
		}
		if decoded != "上传完成通知" {
			t.Errorf("主题 = %q, want 上传完成通知", decoded)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("插件声称已投递但服务器 10s 内没收到邮件。日志：%+v", m.Logs("smtp-email", 50))
	}
}

func headerOf(data, key string) (string, bool) {
	for _, line := range strings.Split(data, "\r\n") {
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), key) {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

package main

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"mime"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// 这组测试用一个**最小可用的 SMTP 服务器**做真实投递验证 ——
// 不是 mock：客户端走完整的 net/smtp 协议往返，服务端逐条应答，
// 最后断言收到的报文内容。SMTP 的问题（分帧、应答码、DATA 终止符、
// 编码）只有真跑协议才暴露得出来。
//
// 覆盖的重点是"收件人实际看到什么"：
//   - 中文主题是否按 RFC 2047 编码且**解码回来与原文一致**（含长主题折行）
//   - 中文正文是否 base64 编码且内容无损
//   - 纯 ASCII 正文是否走 7bit（不给保守中继添麻烦）
//   - 头注入是否被挡住（CRLF 注入伪造 Bcc）
//   - 三种 TLS 模式的连接建立与"缺 STARTTLS"的明确报错
//   - 配置不全是否 fail closed

// ---- 假 SMTP 服务器 ----

type captured struct {
	mu       sync.Mutex
	mailFrom string
	rcptTo   []string
	data     string
	helo     string
}

// startFakeSMTP 起一个最小 SMTP 服务器，返回地址与捕获结果。
//
// 只实现 net/smtp 客户端会实际发出的命令。advertise 控制 EHLO 里声明哪些
// 扩展 —— 用来构造"服务器没声明 STARTTLS / AUTH"这类负例。
func startFakeSMTP(t *testing.T, useTLS bool, advertise ...string) (string, *captured) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	if useTLS {
		cert, err := selfSignedCert(t)
		if err != nil {
			t.Fatalf("生成自签证书失败: %v", err)
		}
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}})
	}
	t.Cleanup(func() { _ = ln.Close() })

	cap := &captured{}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

		br := bufio.NewReader(conn)
		w := func(s string) { _, _ = conn.Write([]byte(s)) }
		w("220 fake ESMTP\r\n")

		var inData bool
		var body strings.Builder
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				// DATA 阶段：读到单独一个 "." 行结束
				if strings.TrimRight(line, "\r\n") == "." {
					inData = false
					cap.mu.Lock()
					cap.data = body.String()
					cap.mu.Unlock()
					w("250 2.0.0 queued\r\n")
					continue
				}
				body.WriteString(line)
				continue
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				cap.mu.Lock()
				cap.helo = strings.TrimSpace(line)
				cap.mu.Unlock()
				if len(advertise) == 0 {
					w("250 fake\r\n")
				} else {
					// 多行应答：除最后一行外都要带 "-"
					for i, ext := range advertise {
						if i == len(advertise)-1 {
							w("250 " + ext + "\r\n")
						} else {
							w("250-" + ext + "\r\n")
						}
					}
				}
			case strings.HasPrefix(cmd, "MAIL FROM"):
				cap.mu.Lock()
				cap.mailFrom = extractAddr(line)
				cap.mu.Unlock()
				w("250 2.1.0 ok\r\n")
			case strings.HasPrefix(cmd, "RCPT TO"):
				cap.mu.Lock()
				cap.rcptTo = append(cap.rcptTo, extractAddr(line))
				cap.mu.Unlock()
				w("250 2.1.5 ok\r\n")
			case cmd == "DATA":
				inData = true
				w("354 end with <CRLF>.<CRLF>\r\n")
			case cmd == "QUIT":
				w("221 2.0.0 bye\r\n")
				return
			default:
				w("502 5.5.2 not implemented\r\n")
			}
		}
	}()
	return ln.Addr().String(), cap
}

func extractAddr(line string) string {
	line = strings.TrimSpace(line)
	i := strings.Index(line, "<")
	j := strings.LastIndex(line, ">")
	if i >= 0 && j > i {
		return line[i+1 : j]
	}
	return line
}

func selfSignedCert(t *testing.T) (tls.Certificate, error) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

// splitAddr 把 "127.0.0.1:port" 拆成 host 与 port 数字。
func splitAddr(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("端口解析失败: %v", err)
	}
	return host, port
}

// headerValue 从捕获到的报文里取某个头的值（不区分大小写）。
func headerValue(data, key string) (string, bool) {
	for _, line := range strings.Split(data, "\r\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), key) {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

// ---- 用例 ----

// TestSendChineseSubjectAndBody 核心用例：中文主题与正文经真实 SMTP 往返后
// 必须**解码回来与原文一致**。
//
// 主题特意取得足够长以触发 mime.QEncoding 的折行 ——
// writeHeader 会把折行里的 CRLF 换成空格，这条断言就是在验证
// 那一步没有破坏编码语义（RFC 2047 规定相邻 encoded-word 间的空白被忽略）。
func TestSendChineseSubjectAndBody(t *testing.T) {
	addr, cap := startFakeSMTP(t, false)
	host, port := splitAddr(t, addr)

	m := newMailer(Config{
		Host: host, Port: port, TLS: "none",
		From: "noreply@example.com", FromName: "星云网盘",
		TimeoutSeconds: 5,
	})

	subject := strings.Repeat("这是一封来自网盘的通知，", 8) // 足够长，必然折行
	body := "你好：\n你的文件已上传完成。\n——NebulaDrive"
	if err := m.Send(context.Background(), mail{
		To: "user@example.com", Subject: subject, Body: body,
	}); err != nil {
		t.Fatalf("投递失败: %v", err)
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if cap.mailFrom != "noreply@example.com" {
		t.Errorf("MAIL FROM = %q", cap.mailFrom)
	}
	if len(cap.rcptTo) != 1 || cap.rcptTo[0] != "user@example.com" {
		t.Errorf("RCPT TO = %v", cap.rcptTo)
	}

	rawSubject, ok := headerValue(cap.data, "Subject")
	if !ok {
		t.Fatalf("报文里没有 Subject 头:\n%s", cap.data)
	}
	if !strings.Contains(rawSubject, "=?") {
		t.Errorf("中文主题必须按 RFC 2047 编码，实际 = %q", rawSubject)
	}
	dec := new(mime.WordDecoder)
	gotSubject, err := dec.DecodeHeader(rawSubject)
	if err != nil {
		t.Fatalf("主题解码失败: %v (raw=%q)", err, rawSubject)
	}
	if gotSubject != subject {
		t.Errorf("主题解码后与原文不一致:\n got=%q\nwant=%q", gotSubject, subject)
	}

	// 中文正文必须 base64 编码，且解出来无损
	enc, _ := headerValue(cap.data, "Content-Transfer-Encoding")
	if enc != "base64" {
		t.Errorf("中文正文应 base64 编码，实际 %q", enc)
	}
	_, rawBody, _ := strings.Cut(cap.data, "\r\n\r\n")
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(rawBody, "\r\n", ""))
	if err != nil {
		t.Fatalf("正文 base64 解码失败: %v (raw=%q)", err, rawBody)
	}
	if string(decoded) != toCRLF(body) {
		t.Errorf("正文解码后不一致:\n got=%q\nwant=%q", string(decoded), toCRLF(body))
	}

	// 发件人显示名是中文，同样要编码
	from, _ := headerValue(cap.data, "From")
	if !strings.Contains(from, "=?") {
		t.Errorf("中文发件人名应编码，实际 From = %q", from)
	}
}

// TestSendASCIIBodyUses7bit 纯 ASCII 正文不该被 base64 化 ——
// 保守的中继更愿意转发 7bit，而且日志/抓包里可读。
func TestSendASCIIBodyUses7bit(t *testing.T) {
	addr, cap := startFakeSMTP(t, false)
	host, port := splitAddr(t, addr)

	m := newMailer(Config{
		Host: host, Port: port, TLS: "none",
		From: "noreply@example.com", TimeoutSeconds: 5,
	})
	if err := m.Send(context.Background(), mail{
		To: "u@example.com", Subject: "plain", Body: "line1\nline2",
	}); err != nil {
		t.Fatalf("投递失败: %v", err)
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if enc, _ := headerValue(cap.data, "Content-Transfer-Encoding"); enc != "7bit" {
		t.Errorf("ASCII 正文应为 7bit，实际 %q", enc)
	}
	// 换行必须已归一化为 CRLF
	if !strings.Contains(cap.data, "line1\r\nline2") {
		t.Errorf("正文换行未归一化为 CRLF:\n%q", cap.data)
	}
}

// TestSendStripsHeaderInjection 头注入防护。
//
// Subject 来自站内通知内容。若原样带着 CRLF 写进报文，攻击者可以
// 伪造 Bcc 把通知静默抄送给第三方。断言方式必须落在**行**上：
// 注入的文本可以出现在 Subject 值里（那是被无害化的普通文本），
// 但绝不能变成一行独立的头。
func TestSendStripsHeaderInjection(t *testing.T) {
	addr, cap := startFakeSMTP(t, false)
	host, port := splitAddr(t, addr)

	m := newMailer(Config{
		Host: host, Port: port, TLS: "none",
		From: "noreply@example.com", TimeoutSeconds: 5,
	})
	injected := "hello\r\nBcc: evil@example.net\r\nX-Evil: 1"
	if err := m.Send(context.Background(), mail{
		To: "u@example.com", Subject: injected, Body: "b",
	}); err != nil {
		t.Fatalf("投递失败: %v", err)
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	for _, line := range strings.Split(cap.data, "\r\n") {
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "bcc:") || strings.HasPrefix(lower, "x-evil:") {
			t.Fatalf("头注入成功，出现了伪造的头: %q\n%s", line, cap.data)
		}
	}
	// 无害化后的文本仍应在 Subject 里（换行被替换为空格）
	subj, _ := headerValue(cap.data, "Subject")
	if !strings.Contains(subj, "Bcc: evil@example.net") {
		t.Errorf("注入文本应被无害化保留在 Subject 值中，实际 = %q", subj)
	}
}

// TestSendFailsClosedWithoutConfig init 阶段对配置不全是"只警告"，
// 但投递必须 fail closed —— 绝不发一封注定被拒的信。
func TestSendFailsClosedWithoutConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"缺 host", Config{From: "a@b.c", Port: 25, TLS: "none", TimeoutSeconds: 5}},
		{"缺 from", Config{Host: "127.0.0.1", Port: 25, TLS: "none", TimeoutSeconds: 5}},
		{"tls 取值非法", Config{Host: "127.0.0.1", From: "a@b.c", Port: 25, TLS: "bogus", TimeoutSeconds: 5}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := newMailer(c.cfg)
			if err := m.Send(context.Background(), mail{To: "u@example.com"}); err == nil {
				t.Fatal("配置不完整时必须失败，实际返回 nil")
			}
		})
	}
}

// TestSendRejectsMissingStartTLS 服务器没声明 STARTTLS 时要给出**可操作**的
// 报错，而不是让用户对着超时干等。
func TestSendRejectsMissingStartTLS(t *testing.T) {
	addr, _ := startFakeSMTP(t, false) // 不声明任何扩展
	host, port := splitAddr(t, addr)

	m := newMailer(Config{
		Host: host, Port: port, TLS: "starttls",
		From: "noreply@example.com", TimeoutSeconds: 5,
	})
	err := m.Send(context.Background(), mail{To: "u@example.com", Subject: "s", Body: "b"})
	if err == nil {
		t.Fatal("服务器未声明 STARTTLS 时应失败")
	}
	if !strings.Contains(err.Error(), "STARTTLS") {
		t.Errorf("报错应指明 STARTTLS 问题，实际: %v", err)
	}
}

// TestSendRejectsAuthWhenNotAdvertised 配了 username 但服务器不声明 AUTH 时，
// 明确报错而不是静默以匿名身份发送（后者会让人以为认证生效了）。
func TestSendRejectsAuthWhenNotAdvertised(t *testing.T) {
	addr, _ := startFakeSMTP(t, false)
	host, port := splitAddr(t, addr)

	m := newMailer(Config{
		Host: host, Port: port, TLS: "none",
		Username: "u", Password: "p",
		From: "noreply@example.com", TimeoutSeconds: 5,
	})
	err := m.Send(context.Background(), mail{To: "u@example.com", Subject: "s", Body: "b"})
	if err == nil {
		t.Fatal("服务器未声明 AUTH 且配置了 username 时应失败")
	}
	if !strings.Contains(err.Error(), "AUTH") {
		t.Errorf("报错应指明 AUTH 问题，实际: %v", err)
	}
}

// TestSendImplicitTLS 465 路径：一上来就 TLS。
//
// 自签证书必然过不了证书校验，所以这里通过 tlsConfigForHost 这个
// **测试接缝**注入跳过校验的配置。生产默认值没有任何跳过开关 ——
// 本测试只是证明代码路径本身是通的。
func TestSendImplicitTLS(t *testing.T) {
	addr, cap := startFakeSMTP(t, true)
	host, port := splitAddr(t, addr)

	origTLS := tlsConfigForHost
	tlsConfigForHost = func(h string) *tls.Config {
		return &tls.Config{ServerName: h, MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}
	}
	defer func() { tlsConfigForHost = origTLS }()

	m := newMailer(Config{
		Host: host, Port: port, TLS: "implicit",
		From: "noreply@example.com", TimeoutSeconds: 5,
	})
	if err := m.Send(context.Background(), mail{
		To: "u@example.com", Subject: "tls", Body: "over tls",
	}); err != nil {
		t.Fatalf("implicit TLS 投递失败: %v", err)
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if !strings.Contains(cap.data, "over tls") {
		t.Errorf("未收到邮件内容:\n%s", cap.data)
	}
}

// TestSendNeverBlockedError 守住一条硬约束：Send 返回的 error 绝不含 "blocked"。
//
// onEmail 是异步、不可拦截的钩子。若错误文本里出现 "blocked"，
// 宿主会把它当成拦截语义 —— 邮件发不出去反而把站内通知也拦了。
func TestSendNeverBlockedError(t *testing.T) {
	addr, _ := startFakeSMTP(t, false)
	host, port := splitAddr(t, addr)

	cfgs := []Config{
		{Host: host, Port: port, TLS: "none", From: "a@b.c", Username: "u", Password: "p", TimeoutSeconds: 5},
		{Host: "127.0.0.1", Port: 1, TLS: "none", From: "a@b.c", TimeoutSeconds: 1}, // 连接必失败
		{Host: host, Port: port, TLS: "starttls", From: "a@b.c", TimeoutSeconds: 5},
	}
	for i, cfg := range cfgs {
		err := newMailer(cfg).Send(context.Background(), mail{To: "u@example.com", Subject: "s", Body: "b"})
		if err == nil {
			continue
		}
		if strings.Contains(err.Error(), "blocked") {
			t.Errorf("用例 %d：错误文本含 blocked，会被宿主误判为拦截: %v", i, err)
		}
	}
}

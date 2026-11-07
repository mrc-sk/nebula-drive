// Package pluginhttp 管理「注册 HTTP 页面」的插件。
//
// 宿主是 Go 单二进制，内部没有 PHP / Python / Node 解释器。
// 因此插件页面由**插件自己起 HTTP 服务**（例如 php -S 127.0.0.1:PORT -t ASS），
// 宿主只做三件事：
//
//  1. 动态分配一个本机端口，通过 NB_PLUGIN_PORT 等环境变量下发给插件；
//  2. 拉起 co.BEGG.command 指定的进程并探活；
//  3. 把 /ext/<plugin>/* 反向代理到 127.0.0.1:<port>。
//
// 关于 0.0.0.0：插件声明 listen=0.0.0.0 时，宿主**实际只绑 127.0.0.1**，
// 0.0.0.0 仅作为「生成对外 URL 时要替换」的标记（见 plugin.ResolveExternalBase）。
// 这样既满足「绑定域名就显示域名」的需求，又不把插件端口暴露到公网。
//
// 鉴权：按站长的选择，宿主**完全透传**，插件自己负责登录与权限。
// 宿主唯一的门槛是插件必须已被启用（否则返回 404）。
package pluginhttp

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/pkg/plugin"
)

// pagePathCtxKey 用于在请求 context 里把「相对文档根的路径」传给 Director。
//
// 不能像早期实现那样在每个请求里改写共享 ReverseProxy.Director：
// ReverseProxy 会被并发调用，改 Director 本身是数据竞争，而且每次请求都把
// 上一次的 Director 包一层闭包，闭包链会无限增长。Director 只在 Start 时
// 设置一次，之后只读 context —— 完全无共享可变状态。
type pagePathCtxKey struct{}

// route 一个已启动的插件页面服务。
type route struct {
	plugin   string
	port     int
	target   *url.URL
	reverse  *httputil.ReverseProxy
	cmd      *exec.Cmd
	home     string
	baseURL  string
	external string // 外部 URL（不反代）
	pages    []plugin.PageDecl
}

// Server 插件页面服务管理器。
type Server struct {
	mu      sync.RWMutex
	routes  map[string]*route
	enabled bool // 全局插件开关；默认 false
}

var global = &Server{routes: map[string]*route{}}

// Global 返回全局管理器。
func Global() *Server { return global }

// SetGlobalEnabled 设置全局插件开关（默认 false）。
func (s *Server) SetGlobalEnabled(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = v
}

// GlobalEnabled 返回全局开关状态。
func (s *Server) GlobalEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled
}

// Start 拉起一个插件的 HTTP 页面服务并登记路由。
//
// v1 插件或没有 pages 的插件调用本函数是 no-op。
// 解释器缺失的错误由 LoadContract(..., true) 在此之前拦下，这里不再重复探测。
func (s *Server) Start(c *plugin.Contract, siteURL, domain, boundIP, listenAddr string) error {
	if c == nil || c.Begg == nil || len(c.Begg.Pages) == 0 {
		return nil
	}
	b := c.Begg
	name := c.Manifest.Name

	s.Stop(name) // 重复启动先清理

	base, external := plugin.ResolveExternalBase(b, name, siteURL, domain, boundIP, listenAddr)

	// co.BEGG 所在目录是页面服务的工作目录，也是 home/static 的解析基准。
	workDir := c.BeggDir
	if workDir == "" {
		workDir = c.Dir
	}
	// 没有显式 home 时退回到第一个页面的 home，避免默认落到 / 后 404。
	home := b.Home
	if home == "" && len(b.Pages) > 0 {
		home = b.Pages[0].Home
	}
	rt := &route{
		plugin: name, home: b.PagePath(home),
		baseURL: base, pages: b.Pages,
	}

	if external {
		rt.external = base
	} else {
		port := b.Port
		if port == 0 {
			var err error
			port, err = plugin.AllocatePort()
			if err != nil {
				return fmt.Errorf("为插件 %s 分配端口失败: %w", name, err)
			}
		}
		rt.port = port
		target, _ := url.Parse("http://127.0.0.1:" + fmtInt(port))
		rt.target = target
		rt.reverse = httputil.NewSingleHostReverseProxy(target)
		originalDirector := rt.reverse.Director
		rt.reverse.Director = func(req *http.Request) {
			originalDirector(req)
			if p, ok := req.Context().Value(pagePathCtxKey{}).(string); ok && p != "" {
				req.URL.Path = p
				req.URL.RawPath = ""
			}
			req.Host = "127.0.0.1:" + fmtInt(port)
		}

		prog, args, err := b.RenderCommand(workDir, port, "127.0.0.1")
		if err != nil {
			return fmt.Errorf("插件 %s 的 co.BEGG command 无效: %w", name, err)
		}
		cmd := exec.Command(prog, args...)
		cmd.Dir = workDir
		env := plugin.PluginEnv(name, c.Dir, home, siteURL, port)
		for k, v := range b.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("启动插件 %s 页面服务失败（命令 %q）: %w", name, prog, err)
		}
		rt.cmd = cmd
		// 异步回收，避免子进程退出后变僵尸
		go func() { _ = cmd.Wait() }()

		if err := waitHealthy(port, 6*time.Second); err != nil {
			kill(cmd)
			return fmt.Errorf("插件 %s 页面服务未就绪（端口 %d）: %w", name, port, err)
		}
	}

	s.mu.Lock()
	s.routes[name] = rt
	s.mu.Unlock()

	// 登记 UI 扩展点（侧边栏/主题/设置面板）。
	// 侧边栏 URL 必须用「相对文档根」的路径，否则 php -S -t ASS 会把
	// ASS/ 前缀重复拼一次导致 404。这里只改传给注册表的副本，不动
	// c.Begg.Pages（诊断接口还要按原始路径展示）。
	pages := make([]plugin.PageDecl, len(b.Pages))
	copy(pages, b.Pages)
	for i := range pages {
		if pages[i].URL == "" && pages[i].Home != "" {
			pages[i].Home = b.PagePath(pages[i].Home)
		}
	}
	plugin.GlobalRegistry().Add(name, b.Providers, pages, b.UI, base)
	log.Printf("[plugin] 页面服务就绪: %s -> %s（%s）", name, base, b.Runtime)
	return nil
}

// Stop 停止某插件的页面服务并注销路由与 UI 扩展点。
func (s *Server) Stop(name string) {
	s.mu.Lock()
	rt, ok := s.routes[name]
	if ok {
		delete(s.routes, name)
	}
	s.mu.Unlock()
	if ok && rt.cmd != nil && rt.cmd.Process != nil {
		kill(rt.cmd)
	}
	plugin.GlobalRegistry().Remove(name)
}

// StopAll 停止全部页面服务（宿主退出时调用）。
func (s *Server) StopAll() {
	s.mu.Lock()
	names := make([]string, 0, len(s.routes))
	for n := range s.routes {
		names = append(names, n)
	}
	s.mu.Unlock()
	for _, n := range names {
		s.Stop(n)
	}
}

// RegisterRoutes 把 /ext/:plugin/*path 挂到 Gin。
//
// 不做任何身份校验（按站长选择：插件自负责、宿主透传）。
// 但全局开关关闭、或插件未启用时一律 404，保证「默认关闭」真的生效。
func (s *Server) RegisterRoutes(r gin.IRouter) {
	r.Any("/ext/:plugin/*path", s.Serve)
}

// Serve 反向代理入口，同时触发 onUIPageOpen。
func (s *Server) Serve(c *gin.Context) {
	if !s.GlobalEnabled() {
		c.Status(http.StatusNotFound)
		return
	}
	name := c.Param("plugin")
	s.mu.RLock()
	rt, ok := s.routes[name]
	s.mu.RUnlock()
	if !ok {
		c.Status(http.StatusNotFound)
		return
	}
	if rt.external != "" {
		c.Redirect(http.StatusFound, rt.external)
		return
	}
	if rt.reverse == nil {
		c.Status(http.StatusBadGateway)
		return
	}

	path := c.Param("path")
	if path == "" || path == "/" {
		path = rt.home
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	// 页面打开事件（异步通知，不阻塞代理）
	plugin.FireIf(plugin.HookUIPageOpen, map[string]any{
		"plugin": name, "page": path, "ip": c.ClientIP(),
		"ua": c.GetHeader("User-Agent"),
	})

	// 把路径放进 request context，由 Start 时固定的 Director 读取。
	// 用浅拷贝的 Request，绝不碰共享 proxy 的状态。
	req := c.Request.WithContext(context.WithValue(c.Request.Context(), pagePathCtxKey{}, path))
	rt.reverse.ServeHTTP(c.Writer, req)
}

// waitHealthy 轮询插件 HTTP 服务直到返回任意响应。
func waitHealthy(port int, timeout time.Duration) error {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(timeout)
	urlStr := "http://127.0.0.1:" + fmtInt(port) + "/"
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, urlStr, nil)
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("等待 %s 超时", urlStr)
}

func kill(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}

func fmtInt(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

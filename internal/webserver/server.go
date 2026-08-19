// server.go Web 服务主体：Server 装配、Handler 挂载入口与监听启动，
// 行为对齐 apps/cli/src/web.rs 的 AppState / start_web_server / print_webui_banner。
// The web server core: Server assembly, the Handler entry point mounting all
// routes, and the listener startup, aligned with the AppState /
// start_web_server / print_webui_banner of apps/cli/src/web.rs.
package webserver

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/FelixJI/iKuai-Toolbox/internal/config"
	"github.com/FelixJI/iKuai-Toolbox/internal/runtime"
)

// Server Web 服务状态（web.rs AppState 的 Go 对应物）：cfgHolder 原子持有
// 当前配置（save-raw 后刷新，BasicAuth 与 /api/config 每请求重读），
// rt 为共享的运行时服务。
// Server is the web state (the Go counterpart of AppState in web.rs):
// cfgHolder atomically holds the current config (refreshed after save-raw and
// re-read per request by BasicAuth and /api/config), while rt is the shared
// runtime service.
type Server struct {
	cfgHolder atomic.Pointer[config.Config]
	rt        *runtime.RuntimeService
	cfgPath   string

	// CLILogin CLI 登录参数（-l url,user,pass），空串走配置/网关回退；
	// 诊断报告与清理流程共用（对齐 Rust AppState.cli_login）。
	// CLILogin is the CLI login triple (-l url,user,pass); blank falls back
	// to config/gateway resolution. Shared by diagnostics and clean flows
	// (mirroring Rust's AppState.cli_login).
	CLILogin string
}

// NewServer 构造 Web 服务。
// NewServer builds the web server.
func NewServer(rt *runtime.RuntimeService, cfg *config.Config, cfgPath string) *Server {
	s := &Server{rt: rt, cfgPath: cfgPath}
	s.cfgHolder.Store(cfg)
	return s
}

// Handler 挂全部 API 路由 + 静态 SPA fallback，整体包一层动态 BasicAuth
// 中间件（对齐 web.rs 的 Router + fallback + middleware 分层）。
// Handler mounts every API route plus the static SPA fallback, all wrapped
// in the dynamic BasicAuth middleware (mirroring the Router + fallback +
// middleware layering of web.rs).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	return s.withBasicAuth(mux)
}

// StartWebServer 绑 0.0.0.0:{port}、后台启动 HTTP 服务并打印启动横幅
// （web.rs L121-136：bind 失败返回错误；serve 交给后台 goroutine，横幅在
// 绑定成功后打印）。
// StartWebServer binds 0.0.0.0:{port}, serves in the background, then prints
// the startup banner (web.rs L121-136: a bind failure returns the error,
// serving runs in a background goroutine, and the banner prints only after a
// successful bind).
func StartWebServer(s *Server, port string) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%s", port))
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: s.Handler()}
	go func() { _ = srv.Serve(ln) }()
	printWebuiBanner(port, displayConfPath(s.cfgPath), s.cfgHolder.Load().WebUI.User)
	return nil
}

// executablePath 返回当前可执行文件路径（web.rs L139-142 的
// current_exe；失败回退空串由调用方处理）。
// executablePath returns the current executable path (the current_exe of
// web.rs L139-142; callers handle failures by falling back to "").
func executablePath() (string, error) {
	return os.Executable()
}

// displayConfPath 相对路径拼接进程 cwd 后展示（web.rs L18-24）。
// displayConfPath joins relative paths with the process cwd for display
// (web.rs L18-24).
func displayConfPath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return filepath.Join(cwd, p)
}

// printWebuiBanner 启动横幅，逐行对齐 web.rs L26-56（中文文案为既有
// CLI 输出契约，原样保留）。
// printWebuiBanner prints the startup banner line-by-line as in web.rs
// L26-56 (the Chinese wording is the established CLI output contract and is
// preserved verbatim).
func printWebuiBanner(port, confPath, authUser string) {
	listenAddr := fmt.Sprintf("0.0.0.0:%s", port)
	openURL := fmt.Sprintf("http://127.0.0.1:%s", port)
	authUser = strings.TrimSpace(authUser)
	authEnabled := authUser != ""

	fmt.Println()
	fmt.Println("===========================================================")
	fmt.Println("[WEB:服务启动] iKuai Bypass WebUI")
	fmt.Println("-----------------------------------------------------------")
	fmt.Printf("访问地址: %s\n", openURL)
	fmt.Printf("监听地址: %s\n", listenAddr)
	fmt.Printf("配置路径: %s\n", confPath)
	if authEnabled {
		fmt.Printf("认证模式: BasicAuth 已开启 (user: %s)\n", authUser)
	} else {
		fmt.Println("认证模式: BasicAuth 未开启 (webui.user 为空)")
	}
	// 在线保存为强制开启（不再支持 enable_update 之类的开关）。
	// Online save is forced on (no enable_update switch).
	fmt.Println("在线保存: 已开启 (固定)")
	fmt.Println("-----------------------------------------------------------")
	if !authEnabled {
		fmt.Println("警告: 当前未启用 BasicAuth，WebUI 将对局域网完全开放")
		fmt.Println("提示: 建议在配置文件中设置 webui.user/webui.pass 启用 BasicAuth")
	}
	fmt.Println("提示: 停止定时任务后，计划任务将不会再按 Cron 自动执行")
	fmt.Println("退出方式: Ctrl+C")
	fmt.Println("===========================================================")
	fmt.Println()
}

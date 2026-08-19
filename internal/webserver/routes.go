// routes.go 15 个 API 端点注册与处理器，行为对齐 apps/cli/src/web.rs
// L91-434 的路由表；使用 Go 1.22 方法路由（"POST /api/save-raw"）。
// Registration and handlers for the 15 API endpoints, aligned with the
// routing table of apps/cli/src/web.rs L91-434, using Go 1.22 method
// patterns ("POST /api/save-raw").
package webserver

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/FelixJI/iKuai-Toolbox/internal/app"
	"github.com/FelixJI/iKuai-Toolbox/internal/config"
)

// 生产网络调用的函数缝：webserver 契约测试从这里注入确定性桩，
// 生产路径保持直连 internal/app。
// Function seams for the production network calls: contract tests inject
// deterministic stubs here while production paths go straight to internal/app.
var (
	fetchRemoteConfigFn   = app.FetchRemoteConfig
	fetchGithubReleasesFn = app.FetchGithubReleases
	testIkuaiLoginFn      = app.TestIkuaiLogin
	testGithubProxyFn     = app.TestGithubProxy
	runCleanFn            = app.RunClean
)

// registerRoutes 注册全部 API 路由与静态 "/" 兜底（对齐 web.rs L91-110
// 的 Router 与 embedded fallback）。
// registerRoutes mounts every API route plus the static "/" catch-all
// (mirroring the Router and embedded fallback of web.rs L91-110).
func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("GET /api/config/default", s.handleDefaultConfig)
	mux.HandleFunc("GET /api/diagnostics/report", s.handleDiagnosticsReport)
	mux.HandleFunc("POST /api/save-raw", s.handleSaveRaw)
	mux.HandleFunc("POST /api/remote/fetch", s.handleRemoteFetch)
	mux.HandleFunc("POST /api/test/ikuai-login", s.handleTestIkuaiLogin)
	mux.HandleFunc("POST /api/test/github-proxy", s.handleTestGithubProxy)
	mux.HandleFunc("GET /api/github/releases", s.handleGithubReleases)
	mux.HandleFunc("POST /api/github/releases", s.handleGithubReleasesWithProxy)
	mux.HandleFunc("GET /api/runtime/status", s.handleRuntimeStatus)
	mux.HandleFunc("POST /api/runtime/run-once", s.handleRunOnce)
	mux.HandleFunc("POST /api/runtime/cron/start", s.handleCronStart)
	mux.HandleFunc("POST /api/runtime/cron/stop", s.handleCronStop)
	mux.HandleFunc("POST /api/runtime/stop", s.handleRuntimeStop)
	mux.HandleFunc("POST /api/runtime/clean", s.handleClean)
	mux.HandleFunc("GET /api/runtime/logs", s.handleLogs)
	mux.HandleFunc("GET /api/runtime/logs/stream", s.handleLogsStream)
	mux.HandleFunc("/", s.handleStatic)
}

// writeText 以 text/plain; charset=utf-8 写文本响应（对齐 axum (StatusCode, String)）。
// writeText writes a text response as text/plain; charset=utf-8 (matching
// axum's (StatusCode, String) into_response).
func writeText(w http.ResponseWriter, code int, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	_, _ = io.WriteString(w, text)
}

// writeJSON 以 application/json 写响应体。
// writeJSON writes the response body as application/json.
func writeJSON(w http.ResponseWriter, code int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(data)
}

// decodeJSONBody 解析 JSON 请求体，失败时写 400 并返回 false。
// decodeJSONBody decodes the JSON request body, answering 400 and returning
// false on failure.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeText(w, http.StatusBadRequest, "Failed to read request body: "+err.Error())
		return false
	}
	if err := json.Unmarshal(body, v); err != nil {
		writeText(w, http.StatusBadRequest, "Failed to parse the request body as JSON: "+err.Error())
		return false
	}
	return true
}

// handleConfig GET /api/config：ConfigMeta 展平 + exe_path，no-store
// （响应含密码等敏感信息，web.rs L138-159）。
// handleConfig GET /api/config: flattened ConfigMeta plus exe_path with
// no-store (the response carries secrets; web.rs L138-159).
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	exePath := ""
	if p, err := executablePath(); err == nil {
		exePath = p
	}
	meta, err := app.BuildConfigMeta(s.cfgHolder.Load(), s.cfgPath)
	if err != nil {
		writeText(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		app.ConfigMeta
		ExePath string `json:"exe_path"`
	}{ConfigMeta: meta, ExePath: exePath})
}

// handleDefaultConfig GET /api/config/default：内嵌默认配置原文（web.rs L161-171）。
// handleDefaultConfig GET /api/config/default: the embedded default YAML
// verbatim (web.rs L161-171).
func (s *Server) handleDefaultConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeText(w, http.StatusOK, config.EmbeddedDefaultYAML())
}

// handleDiagnosticsReport GET /api/diagnostics/report：诊断报告 JSON，no-store
// （web.rs L173-191）。
// handleDiagnosticsReport GET /api/diagnostics/report: the diagnostics report
// JSON with no-store (web.rs L173-191).
func (s *Server) handleDiagnosticsReport(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgHolder.Load()
	st := s.rt.Status()
	report := app.BuildDiagnosticsReport(cfg, s.cfgPath, &st, s.CLILogin)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, report)
}

// handleSaveRaw POST /api/save-raw（web.rs L193-235）：校验并原文落盘；
// 仅当 YAML 显式含 mode 才更新运行时 module，防止 apply_defaults 的默认值
// "ispdomain" 悄悄覆盖 CLI 启动参数指定的模块；随后同步 cron 默认值并刷新
// web/runtime 两侧配置快照。
// handleSaveRaw POST /api/save-raw (web.rs L193-235): validate then write the
// raw YAML verbatim; the runtime module only follows the save when the YAML
// explicitly contains `mode`, so the "ispdomain" default from apply_defaults
// never silently overrides the module chosen via CLI args; afterwards the
// cron default is synced and both the web/runtime config snapshots refreshed.
func (s *Server) handleSaveRaw(w http.ResponseWriter, r *http.Request) {
	var req struct {
		YAMLText *string `json:"yaml_text"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.YAMLText == nil {
		writeText(w, http.StatusBadRequest, "Failed to parse the request body as JSON: missing field yaml_text")
		return
	}
	raw := *req.YAMLText
	cfg, err := config.ValidateAndSaveRawYAML(raw, s.cfgPath)
	if err != nil {
		writeText(w, http.StatusBadRequest, "Failed to save config: "+err.Error())
		return
	}

	newModule := ""
	if config.YAMLHasExplicitMode(raw) {
		newModule = cfg.Module
	}
	s.cfgHolder.Store(cfg)
	s.rt.UpdateConfig(cfg)
	s.rt.SetDefaults(newModule, cfg.Cron)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `{"status":"success","message":"Raw YAML saved successfully"}`)
}

// remoteFetchRequest 双命名字段直解：github_proxy 与 githubProxy 指向同一
// 值，二者同时出现视为重复（对齐 web.rs L253-259 的 serde alias）。
// remoteFetchRequest decodes both spellings directly: github_proxy and
// githubProxy target the same value while both present counts as a duplicate
// (mirroring the serde alias of web.rs L253-259).
type remoteFetchRequest struct {
	URL              *string             `json:"url"`
	Proxy            *config.ProxyConfig `json:"proxy"`
	GithubProxySnake *string             `json:"github_proxy"`
	GithubProxyCamel *string             `json:"githubProxy"`
}

// resolveGithubProxy 归并双命名，非法组合返回错误文案。
// resolveGithubProxy folds the two spellings, returning an error text for
// invalid combinations.
func (req *remoteFetchRequest) resolveGithubProxy() (string, bool) {
	if req.GithubProxySnake != nil && req.GithubProxyCamel != nil {
		return "duplicate field github_proxy", false
	}
	if req.GithubProxySnake != nil {
		return *req.GithubProxySnake, true
	}
	if req.GithubProxyCamel != nil {
		return *req.GithubProxyCamel, true
	}
	return "missing field github_proxy", false
}

// handleRemoteFetch POST /api/remote/fetch：成功 200 text/plain，失败 502
// text/plain（web.rs L261-279）。
// handleRemoteFetch POST /api/remote/fetch: success answers 200 text/plain
// and failure 502 text/plain (web.rs L261-279).
func (s *Server) handleRemoteFetch(w http.ResponseWriter, r *http.Request) {
	var req remoteFetchRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.URL == nil {
		writeText(w, http.StatusBadRequest, "Failed to parse the request body as JSON: missing field url")
		return
	}
	if req.Proxy == nil {
		writeText(w, http.StatusBadRequest, "Failed to parse the request body as JSON: missing field proxy")
		return
	}
	githubProxy, ok := req.resolveGithubProxy()
	if !ok {
		writeText(w, http.StatusBadRequest, "Failed to parse the request body as JSON: "+githubProxy)
		return
	}
	text, err := fetchRemoteConfigFn(*req.URL, req.Proxy, githubProxy)
	if err != nil {
		writeText(w, http.StatusBadGateway, err.Error())
		return
	}
	writeText(w, http.StatusOK, text)
}

// handleTestIkuaiLogin POST /api/test/ikuai-login：恒 200 JSON TestResult
// （web.rs L237-243）。
// handleTestIkuaiLogin POST /api/test/ikuai-login: always 200 JSON TestResult
// (web.rs L237-243).
func (s *Server) handleTestIkuaiLogin(w http.ResponseWriter, r *http.Request) {
	var req app.TestIkuaiLoginRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	writeJSON(w, http.StatusOK, testIkuaiLoginFn(req))
}

// handleTestGithubProxy POST /api/test/github-proxy：恒 200 JSON TestResult
// （web.rs L245-251）。
// handleTestGithubProxy POST /api/test/github-proxy: always 200 JSON
// TestResult (web.rs L245-251).
func (s *Server) handleTestGithubProxy(w http.ResponseWriter, r *http.Request) {
	var req app.TestGithubProxyRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	writeJSON(w, http.StatusOK, testGithubProxyFn(req))
}

// handleGithubReleases GET /api/github/releases：用当前配置的代理
// （web.rs L281-288）。
// handleGithubReleases GET /api/github/releases: uses the live config proxy
// (web.rs L281-288).
func (s *Server) handleGithubReleases(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgHolder.Load()
	proxy := cfg.Proxy
	releases, err := fetchGithubReleasesFn(&proxy)
	if err != nil {
		writeText(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, releases)
}

// handleGithubReleasesWithProxy POST /api/github/releases：用请求体代理
// （web.rs L289-302）。
// handleGithubReleasesWithProxy POST /api/github/releases: uses the request
// body proxy (web.rs L289-302).
func (s *Server) handleGithubReleasesWithProxy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Proxy *config.ProxyConfig `json:"proxy"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.Proxy == nil {
		writeText(w, http.StatusBadRequest, "Failed to parse the request body as JSON: missing field proxy")
		return
	}
	releases, err := fetchGithubReleasesFn(req.Proxy)
	if err != nil {
		writeText(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, releases)
}

// handleRuntimeStatus GET /api/runtime/status（web.rs L304-306）。
// handleRuntimeStatus GET /api/runtime/status (web.rs L304-306).
func (s *Server) handleRuntimeStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.rt.Status())
}

// handleRunOnce POST /api/runtime/run-once（web.rs L308-329）。
// handleRunOnce POST /api/runtime/run-once (web.rs L308-329).
func (s *Server) handleRunOnce(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Module *string `json:"module"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.Module == nil {
		writeText(w, http.StatusBadRequest, "Failed to parse the request body as JSON: missing field module")
		return
	}
	started, err := s.rt.StartRunOnce(*req.Module)
	if err != nil {
		writeText(w, http.StatusInternalServerError, "Failed to start run: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"started": started})
}

// handleCronStart POST /api/runtime/cron/start（web.rs L331-356）。
// handleCronStart POST /api/runtime/cron/start (web.rs L331-356).
func (s *Server) handleCronStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Expr   *string `json:"expr"`
		Module *string `json:"module"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.Expr == nil || req.Module == nil {
		writeText(w, http.StatusBadRequest, "Failed to parse the request body as JSON: missing field expr/module")
		return
	}
	if err := s.rt.StartCron(*req.Expr, *req.Module); err != nil {
		writeText(w, http.StatusBadRequest, "Failed to start cron: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "success"})
}

// handleCronStop POST /api/runtime/cron/stop（web.rs L358-371）。
// handleCronStop POST /api/runtime/cron/stop (web.rs L358-371).
func (s *Server) handleCronStop(w http.ResponseWriter, r *http.Request) {
	if err := s.rt.StopCron(); err != nil {
		writeText(w, http.StatusInternalServerError, "Failed to stop cron: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "success"})
}

// handleRuntimeStop POST /api/runtime/stop（web.rs L373-386）。
// handleRuntimeStop POST /api/runtime/stop (web.rs L373-386).
func (s *Server) handleRuntimeStop(w http.ResponseWriter, r *http.Request) {
	if err := s.rt.StopAll(); err != nil {
		writeText(w, http.StatusInternalServerError, "Failed to stop runtime: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "success"})
}

// handleClean POST /api/runtime/clean：错误文案直接用 CleanError.Error()
// （对齐 web.rs L388-409 的纯文本错误）。
// handleClean POST /api/runtime/clean: the error text is CleanError.Error()
// verbatim (the plain-text errors of web.rs L388-409).
func (s *Server) handleClean(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CleanTag *string `json:"clean_tag"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.CleanTag == nil {
		writeText(w, http.StatusBadRequest, "Failed to parse the request body as JSON: missing field clean_tag")
		return
	}
	if err := runCleanFn(s.cfgHolder.Load(), s.CLILogin, *req.CleanTag); err != nil {
		writeText(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "success"})
}

// handleLogs GET /api/runtime/logs?tail=N（web.rs L411-434）：tail 缺省或
// 非法（含 0）回退 200。
// handleLogs GET /api/runtime/logs?tail=N (web.rs L411-434): a missing or
// invalid tail (including 0) falls back to 200.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	tail := parseTailQuery(r.URL.RawQuery)
	writeJSON(w, http.StatusOK, s.rt.TailLogs(tail))
}

// parseTailQuery 解析 tail 查询参数：仅接受正整数，其余（缺失/0/负数/非数字）
// 返回默认 200（web.rs L421-434）。
// parseTailQuery parses the tail query parameter: only positive integers
// count; anything else (missing/0/negative/non-numeric) yields the default
// of 200 (web.rs L421-434).
func parseTailQuery(query string) int {
	for _, part := range strings.Split(query, "&") {
		k, v, _ := strings.Cut(part, "=")
		if k != "tail" {
			continue
		}
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 200
}

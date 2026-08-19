// harness_test.go smoke 共用测试基座：一次性构建 CLI 二进制、启动模拟器与
// 列表源 fixture、渲染测试配置、以子进程运行 CLI 并捕获输出。
// 行为对齐 apps/integration-tests/tests/common/mod.rs 的 simulator 后端路径。
// Shared smoke-test harness: build the CLI binary once, start the simulator
// and the list-source fixture, render test configs, and run the CLI as a
// subprocess with captured output. Aligned with the simulator-backend path of
// apps/integration-tests/tests/common/mod.rs.
package smoke

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"net/http"
	"net/http/httptest"

	"github.com/FelixJI/iKuai-Toolbox/apps/integration-tests-go/simulator"
	"github.com/FelixJI/iKuai-Toolbox/internal/config"
	"github.com/FelixJI/iKuai-Toolbox/internal/ikuai"
)

const (
	harnessUser = "admin"
	harnessPass = "admin888"
)

// cliBin TestMain 中一次性构建出的被测二进制路径（全包复用，避免每个
// smoke 重复编译）。
// cliBin is the binary under test, built once in TestMain and shared by every
// smoke (avoiding a rebuild per test).
var cliBin string

// findRepoRoot 从进程 cwd 向上寻找 go.mod 定位仓库根（go test 的 cwd 是
// 包目录，向上两级即仓库根）。
// findRepoRoot walks up from the process cwd to the go.mod (go test runs with
// the package dir as cwd; two levels up is the repo root).
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, serr := os.Stat(filepath.Join(dir, "go.mod")); serr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}

// TestMain 构建被测 CLI（可用 IKB_GO_TEST_CLI_BIN 指定现成二进制跳过构建），
// 跑完全部测试后清理构建目录。
// TestMain builds the CLI under test (a prebuilt binary can be supplied via
// IKB_GO_TEST_CLI_BIN to skip the build) and cleans the build dir afterwards.
func TestMain(m *testing.M) {
	if preset := strings.TrimSpace(os.Getenv("IKB_GO_TEST_CLI_BIN")); preset != "" {
		if info, err := os.Stat(preset); err == nil && !info.IsDir() {
			cliBin = preset
			os.Exit(m.Run())
		}
	}

	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "smoke harness: %v\n", err)
		os.Exit(1)
	}
	binName := "ikuai-bypass"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	dir, err := os.MkdirTemp("", "ikuai-smoke-bin-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "smoke harness: create temp dir: %v\n", err)
		os.Exit(1)
	}
	bin := filepath.Join(dir, binName)
	build := exec.Command("go", "build", "-o", bin, "./cmd/ikuai-bypass")
	build.Dir = root
	if out, berr := build.CombinedOutput(); berr != nil {
		os.RemoveAll(dir)
		fmt.Fprintf(os.Stderr, "smoke harness: go build failed: %v\n%s\n", berr, out)
		os.Exit(1)
	}
	cliBin = bin
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// ---- 列表源 fixture（可编程状态码的静态 HTTP 服务） ----
// ---- List-source fixture (a programmable static HTTP server) ----

type fixtureResponse struct {
	status int
	body   string
}

type fixtureServer struct {
	srv *httptest.Server
	mu  sync.Mutex
	// routes 按 path 存放响应；未登记路径一律 404。
	// routes holds responses per path; unregistered paths answer 404.
	routes map[string]fixtureResponse
}

func startFixture(t *testing.T) *fixtureServer {
	t.Helper()
	f := &fixtureServer{routes: make(map[string]fixtureResponse)}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		resp, ok := f.routes[r.URL.Path]
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(resp.status)
		_, _ = w.Write([]byte(resp.body))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fixtureServer) setText(t *testing.T, path, body string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[path] = fixtureResponse{status: 200, body: body}
}

func (f *fixtureServer) setStatus(t *testing.T, path string, status int, body string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[path] = fixtureResponse{status: status, body: body}
}

func (f *fixtureServer) url(path string) string {
	return f.srv.URL + path
}

// ---- 每个 smoke 的测试环境 ----
// ---- Per-smoke environment ----

type harness struct {
	t       *testing.T
	sim     *simulator.Simulator
	fixture *fixtureServer
	dir     string
}

func startHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		t:       t,
		sim:     simulator.Start(harnessUser, harnessPass),
		fixture: startFixture(t),
		dir:     t.TempDir(),
	}
	t.Cleanup(h.sim.Close)
	return h
}

// renderTestConfig 生成公共配置头（对齐 Rust common::render_test_config），
// 节流时长压到 1ms 级避免单测卡顿。
// renderTestConfig emits the shared config header (mirroring the Rust
// common::render_test_config) with throttle durations pinned to 1ms so tests
// never stall on real sleeps.
func renderTestConfig(baseURL, username, password, extra string) string {
	return fmt.Sprintf(`ikuai-url: %q
username: %q
password: %q
cron: ""
AddErrRetryWait: 1ms
AddWait: 1ms
github-proxy: ""
proxy:
  mode: system
  url: ""
  user: ""
  pass: ""
%s`, baseURL, username, password, extra)
}

// writeConfig 先用生产加载器校验再落盘（对齐 Rust write_config 的
// Config::load_from_yaml_str 预检）。
// writeConfig validates the YAML through the production loader before writing
// (the Config::load_from_yaml_str precheck of the Rust harness).
func (h *harness) writeConfig(name, rawYAML string) string {
	h.t.Helper()
	if _, err := config.LoadFromYAMLString(rawYAML); err != nil {
		h.t.Fatalf("generated test config is invalid: %v", err)
	}
	path := filepath.Join(h.dir, name)
	if err := os.WriteFile(path, []byte(rawYAML), 0o644); err != nil {
		h.t.Fatalf("write test config %s: %v", path, err)
	}
	return path
}

type cliResult struct {
	exitCode int
	stdout   string
	stderr   string
}

func (r cliResult) stdoutContains(needle string) bool { return strings.Contains(r.stdout, needle) }
func (r cliResult) stderrContains(needle string) bool { return strings.Contains(r.stderr, needle) }

// runCLI 以子进程运行被测二进制并捕获三件套（退出码/stdout/stderr）。
// runCLI runs the binary under test as a subprocess, capturing the exit code
// plus stdout/stderr.
func (h *harness) runCLI(args ...string) cliResult {
	h.t.Helper()
	cmd := exec.Command(cliBin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			h.t.Fatalf("run CLI %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return cliResult{exitCode: code, stdout: stdout.String(), stderr: stderr.String()}
}

func (h *harness) runCLISuccess(label string, args ...string) cliResult {
	h.t.Helper()
	res := h.runCLI(args...)
	if res.exitCode != 0 {
		h.t.Fatalf("%s: CLI %v failed with exit %d\nstdout:\n%s\nstderr:\n%s",
			label, args, res.exitCode, res.stdout, res.stderr)
	}
	return res
}

func (h *harness) runCLIFailure(label string, args ...string) cliResult {
	h.t.Helper()
	res := h.runCLI(args...)
	if res.exitCode == 0 {
		h.t.Fatalf("%s: CLI %v unexpectedly succeeded\nstdout:\n%s\nstderr:\n%s",
			label, args, res.stdout, res.stderr)
	}
	return res
}

// loginAPI 以 smoke 凭据登录模拟器，返回生产客户端（与 Rust 版用 ikb_core
// 客户端断言状态一致）。
// loginAPI logs into the simulator with the smoke credentials and returns the
// production client (the Rust suite asserted state through ikb_core likewise).
func (h *harness) loginAPI() *ikuai.IKuaiClient {
	h.t.Helper()
	api, err := ikuai.NewIKuaiClient(h.sim.URL())
	if err != nil {
		h.t.Fatalf("create ikuai client: %v", err)
	}
	if err := api.Login(harnessUser, harnessPass); err != nil {
		h.t.Fatalf("login to simulator: %v", err)
	}
	return api
}

// ---- 断言辅助 ----
// ---- Assertion helpers ----

// csvItems 逗号分词、trim、剔除空/{}/[]、排序（对齐 Rust common::csv_items）。
// csvItems splits on commas, trims, drops blank/{}/[] entries, and sorts
// (mirroring the Rust common::csv_items).
func csvItems(value string) []string {
	var items []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" || item == "{}" || item == "[]" {
			continue
		}
		items = append(items, item)
	}
	sort.Strings(items)
	return items
}

// sortedByID 按 id 升序排列（对齐 Rust 各 smoke 的 sort_by_key(|item| item.id)）。
// sortedByID orders rows by ascending id (the sort_by_key(|item| item.id) of
// the Rust smokes).
func sortedByID[T any](rows []T, id func(T) int64) []T {
	sort.Slice(rows, func(i, j int) bool { return id(rows[i]) < id(rows[j]) })
	return rows
}

// idsOf 提取各行 id（对齐 Rust 的 rows.iter().map(|item| item.id)）。
// idsOf extracts the id of every row (the Rust rows.iter().map(|i| i.id)).
func idsOf[T any](rows []T, id func(T) int64) []int64 {
	out := make([]int64, 0, len(rows))
	for _, r := range rows {
		out = append(out, id(r))
	}
	return out
}

// mapRows 逐行映射（对齐 Rust 的 iter().map().collect::<Vec<_>>()）。
// mapRows maps every row (the Rust iter().map().collect::<Vec<_>>())).
func mapRows[T any, R any](rows []T, fn func(T) R) []R {
	out := make([]R, 0, len(rows))
	for _, r := range rows {
		out = append(out, fn(r))
	}
	return out
}

func showCustomIsp(t *testing.T, api *ikuai.IKuaiClient, tag string) []ikuai.CustomIspData {
	t.Helper()
	rows, err := ikuai.ShowCustomIspByTagName(api, tag)
	if err != nil {
		t.Fatalf("query custom ISP %s: %v", tag, err)
	}
	return sortedByID(rows, func(r ikuai.CustomIspData) int64 { return r.ID })
}

func showStreamDomain(t *testing.T, api *ikuai.IKuaiClient, tag string) []ikuai.StreamDomainData {
	t.Helper()
	rows, err := ikuai.ShowStreamDomainByTagName(api, tag)
	if err != nil {
		t.Fatalf("query stream-domain %s: %v", tag, err)
	}
	return sortedByID(rows, func(r ikuai.StreamDomainData) int64 { return r.ID })
}

func showIpGroup(t *testing.T, api *ikuai.IKuaiClient, tag string) []ikuai.IpGroupData {
	t.Helper()
	rows, err := ikuai.ShowIpGroupByTagName(api, tag)
	if err != nil {
		t.Fatalf("query ip-group %s: %v", tag, err)
	}
	return sortedByID(rows, func(r ikuai.IpGroupData) int64 { return r.ID })
}

func showIpv6Group(t *testing.T, api *ikuai.IKuaiClient, tag string) []ikuai.Ipv6GroupData {
	t.Helper()
	rows, err := ikuai.ShowIpv6GroupByTagName(api, tag)
	if err != nil {
		t.Fatalf("query ipv6-group %s: %v", tag, err)
	}
	return sortedByID(rows, func(r ikuai.Ipv6GroupData) int64 { return r.ID })
}

func showStreamIpPort(t *testing.T, api *ikuai.IKuaiClient, tag string) []ikuai.StreamIpPortData {
	t.Helper()
	rows, err := ikuai.ShowStreamIpPortByTagName(api, tag)
	if err != nil {
		t.Fatalf("query stream-ipport %s: %v", tag, err)
	}
	return sortedByID(rows, func(r ikuai.StreamIpPortData) int64 { return r.ID })
}

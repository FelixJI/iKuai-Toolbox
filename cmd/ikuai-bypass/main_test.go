// main_test.go 模式分发、参数兼容与配置创建提示的表驱动测试。
// Table-driven tests for run-mode dispatch, argument compatibility, and the
// config-creation prompt.
package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FelixJI/iKuai-Toolbox/internal/app"
	"github.com/FelixJI/iKuai-Toolbox/internal/config"
	"github.com/FelixJI/iKuai-Toolbox/internal/logger"
	"github.com/FelixJI/iKuai-Toolbox/internal/runtime"
	"github.com/FelixJI/iKuai-Toolbox/internal/update"
)

// ---- 测试桩 / test doubles ----

// fakeRuntime 记录方法调用的 runtimeService 桩。
// fakeRuntime is a runtimeService stub recording every call.
type fakeRuntime struct {
	mu             sync.Mutex
	runOnceModules []string
	startCronCalls [][2]string
	startCronTimes []time.Time
	stopAllCalls   int
	status         runtime.RuntimeStatus
	startRunOn     bool
	startRunErr    error
	startCronErr   error
}

func (f *fakeRuntime) StartRunOnce(module string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runOnceModules = append(f.runOnceModules, module)
	return f.startRunOn, f.startRunErr
}

func (f *fakeRuntime) StartCron(expr, module string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startCronCalls = append(f.startCronCalls, [2]string{expr, module})
	f.startCronTimes = append(f.startCronTimes, time.Now())
	return f.startCronErr
}

func (f *fakeRuntime) Status() runtime.RuntimeStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *fakeRuntime) StopAll() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopAllCalls++
	return nil
}

func (f *fakeRuntime) SubscribeLogs() (<-chan logger.LogRecord, func()) {
	ch := make(chan logger.LogRecord, 8)
	return ch, func() {}
}

func (f *fakeRuntime) setRunning(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status.Running = v
}

func (f *fakeRuntime) runOnceCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.runOnceModules)
}

func (f *fakeRuntime) cronCalls() [][2]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][2]string{}, f.startCronCalls...)
}

func (f *fakeRuntime) firstStartCronTime() (time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.startCronTimes) == 0 {
		return time.Time{}, false
	}
	return f.startCronTimes[0], true
}

func (f *fakeRuntime) stopAllCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopAllCalls
}

// runtimeInit 记录 newRuntime 收到的参数。
// runtimeInit records what newRuntime received.
type runtimeInit struct {
	login, cron, module string
}

type updateCall struct {
	module, login string
}

// testDeps 注入 fakeRuntime 的记录型依赖集合。
// testDeps is the recording dependency set injected around fakeRuntime.
type testDeps struct {
	mu sync.Mutex

	newRuntimeCalls []runtimeInit
	runUpdateCalls  []updateCall
	runUpdateRes    *update.UpdateError
	exportCalls     []string
	exportRes       *update.UpdateError
	cleanCalls      []string
	cleanRes        error
	webPorts        []string
	webRes          error
}

func (d *testDeps) deps(rt *fakeRuntime) dispatchDeps {
	return dispatchDeps{
		newRuntime: func(cfg *config.Config, cliLogin, defaultCron, defaultModule string, opts *update.UpdateOptions) runtimeService {
			d.mu.Lock()
			defer d.mu.Unlock()
			d.newRuntimeCalls = append(d.newRuntimeCalls, runtimeInit{login: cliLogin, cron: defaultCron, module: defaultModule})
			return rt
		},
		runUpdate: func(cfg *config.Config, cliLogin, module string, opts *update.UpdateOptions, sink logger.LogSink) *update.UpdateError {
			d.mu.Lock()
			defer d.mu.Unlock()
			d.runUpdateCalls = append(d.runUpdateCalls, updateCall{module: module, login: cliLogin})
			return d.runUpdateRes
		},
		exportTxt: func(cfg *config.Config, exportPath string, sink logger.LogSink) *update.UpdateError {
			d.mu.Lock()
			defer d.mu.Unlock()
			d.exportCalls = append(d.exportCalls, exportPath)
			return d.exportRes
		},
		runClean: func(cfg *config.Config, cliLogin, cleanTag string) error {
			d.mu.Lock()
			defer d.mu.Unlock()
			d.cleanCalls = append(d.cleanCalls, cleanTag)
			return d.cleanRes
		},
		startWeb: func(cfg *config.Config, rt runtimeService, cfgPath, cliLogin, port string) error {
			d.mu.Lock()
			defer d.mu.Unlock()
			d.webPorts = append(d.webPorts, port)
			return d.webRes
		},
	}
}

func (d *testDeps) webPortCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.webPorts)
}

// dispatchFixture 单次分发的完整装配。
// dispatchFixture assembles everything one dispatch call needs.
type dispatchFixture struct {
	mode   string
	cfg    *config.Config
	fake   *fakeRuntime
	deps   *testDeps
	args   cliArgs
	out    *bytes.Buffer
	errOut *bytes.Buffer
}

func baseCfg() *config.Config {
	return &config.Config{
		IkuaiURL: "http://127.0.0.1:1",
		Username: "admin",
		Password: "pass",
		Cron:     "*/5 * * * *",
		WebUI:    config.WebUiConfig{Port: "19001"},
	}
}

func newFixture(mode string) *dispatchFixture {
	fx := &dispatchFixture{
		mode:   mode,
		cfg:    baseCfg(),
		fake:   &fakeRuntime{startRunOn: true},
		args:   cliArgs{exportPath: "/tmp", isIpGroupRand: "1"},
		out:    bytes.NewBuffer(nil),
		errOut: bytes.NewBuffer(nil),
	}
	fx.deps = &testDeps{}
	return fx
}

func (fx *dispatchFixture) env() dispatchEnv {
	return dispatchEnv{
		deps:    fx.deps.deps(fx.fake),
		args:    fx.args,
		cfg:     fx.cfg,
		cfgPath: "test-config.yml",
		runMode: fx.mode,
		module:  "ispdomain",
		stdout:  fx.out,
		stderr:  fx.errOut,
	}
}

func assertContains(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("output missing %q:\n%s", sub, s)
	}
}

func assertNotContains(t *testing.T, s, sub string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Fatalf("output unexpectedly contains %q:\n%s", sub, s)
	}
}

// TestRunModeDispatch 表驱动验证模式 => 服务调用与退出码的映射。
// TestRunModeDispatch is the table-driven mapping of mode => service calls and
// exit codes.
func TestRunModeDispatch(t *testing.T) {
	cases := []struct {
		name      string
		mode      string
		args      func(*cliArgs)
		cfg       func(*config.Config)
		setupRt   func(*fakeRuntime)
		setupDeps func(*testDeps)
		wantCode  int
		check     func(t *testing.T, fx *dispatchFixture)
	}{
		{
			name:     "once runs update and prints done banner",
			mode:     "once",
			wantCode: 0,
			check: func(t *testing.T, fx *dispatchFixture) {
				fx.deps.mu.Lock()
				calls := append([]updateCall{}, fx.deps.runUpdateCalls...)
				fx.deps.mu.Unlock()
				if len(calls) != 1 || calls[0].module != "ispdomain" {
					t.Fatalf("runUpdate calls = %+v, want one ispdomain", calls)
				}
				assertContains(t, fx.out.String(), "[END:运行完毕] 任务完成")
				assertContains(t, fx.out.String(), "模式: once")
			},
		},
		{
			name:     "nocron banner normalizes to once",
			mode:     "nocron",
			wantCode: 0,
			check: func(t *testing.T, fx *dispatchFixture) {
				assertContains(t, fx.out.String(), "模式: once")
			},
		},
		{
			name:     "mode 1 banner normalizes to once",
			mode:     "1",
			wantCode: 0,
			check: func(t *testing.T, fx *dispatchFixture) {
				assertContains(t, fx.out.String(), "模式: once")
			},
		},
		{
			name: "once update failure exits 1",
			mode: "once",
			setupDeps: func(d *testDeps) {
				d.runUpdateRes = &update.UpdateError{Kind: update.ErrKindIkuai, Msg: "boom"}
			},
			wantCode: 1,
			check: func(t *testing.T, fx *dispatchFixture) {
				assertContains(t, fx.errOut.String(), "[UPDATE:更新失败] ikuai error: boom")
				assertNotContains(t, fx.out.String(), "[END:运行完毕]")
			},
		},
		{
			name:     "export ok exports trimmed path",
			mode:     "exportDomainStreamToTxt",
			args:     func(a *cliArgs) { a.exportPath = "  /tmp/out  " },
			wantCode: 0,
			check: func(t *testing.T, fx *dispatchFixture) {
				fx.deps.mu.Lock()
				exports := append([]string{}, fx.deps.exportCalls...)
				fx.deps.mu.Unlock()
				if len(exports) != 1 || exports[0] != "/tmp/out" {
					t.Fatalf("export calls = %+v, want [/tmp/out]", exports)
				}
				assertContains(t, fx.out.String(), "[END:导出完毕] 导出完成")
				assertContains(t, fx.out.String(), "导出目录: /tmp/out")
			},
		},
		{
			name:     "export accepts legacy exportDomainSteamToTxt spelling",
			mode:     "exportDomainSteamToTxt",
			wantCode: 0,
			check: func(t *testing.T, fx *dispatchFixture) {
				fx.deps.mu.Lock()
				n := len(fx.deps.exportCalls)
				fx.deps.mu.Unlock()
				if n != 1 {
					t.Fatalf("export calls = %d, want 1", n)
				}
			},
		},
		{
			name:     "export blank path exits 2",
			mode:     "exportDomainStreamToTxt",
			args:     func(a *cliArgs) { a.exportPath = "   " },
			wantCode: 2,
			check: func(t *testing.T, fx *dispatchFixture) {
				assertContains(t, fx.errOut.String(), "[ERR:参数错误] exportDomainSteamToTxt requires -exportPath")
				fx.deps.mu.Lock()
				n := len(fx.deps.exportCalls)
				fx.deps.mu.Unlock()
				if n != 0 {
					t.Fatalf("export calls = %d, want 0", n)
				}
			},
		},
		{
			name: "export failure exits 1",
			mode: "exportDomainStreamToTxt",
			setupDeps: func(d *testDeps) {
				d.exportRes = &update.UpdateError{Kind: update.ErrKindDownload, Msg: "502"}
			},
			wantCode: 1,
			check: func(t *testing.T, fx *dispatchFixture) {
				assertContains(t, fx.errOut.String(), "[EXPORT:导出失败] download error: 502")
			},
		},
		{
			name:     "clean without tag exits 2",
			mode:     "clean",
			wantCode: 2,
			check: func(t *testing.T, fx *dispatchFixture) {
				assertContains(t, fx.errOut.String(), "[ERR:参数错误] Clean mode requires -tag (or cleanAll)")
				fx.deps.mu.Lock()
				n := len(fx.deps.cleanCalls)
				fx.deps.mu.Unlock()
				if n != 0 {
					t.Fatalf("clean calls = %d, want 0", n)
				}
			},
		},
		{
			name:     "clean by tag exits 0",
			mode:     "clean",
			args:     func(a *cliArgs) { a.cleanTag = "mytag" },
			wantCode: 0,
			check: func(t *testing.T, fx *dispatchFixture) {
				fx.deps.mu.Lock()
				calls := append([]string{}, fx.deps.cleanCalls...)
				fx.deps.mu.Unlock()
				if len(calls) != 1 || calls[0] != "mytag" {
					t.Fatalf("clean calls = %+v, want [mytag]", calls)
				}
				assertContains(t, fx.out.String(), "清理目标: mytag")
				assertContains(t, fx.out.String(), "[END:清理完毕] 清理完成")
			},
		},
		{
			name:     "cleanAll banner says all rules",
			mode:     "clean",
			args:     func(a *cliArgs) { a.cleanTag = "cleanAll" },
			wantCode: 0,
			check: func(t *testing.T, fx *dispatchFixture) {
				assertContains(t, fx.out.String(), "清理目标: 全部 IKB 规则")
			},
		},
		{
			name: "clean login cli-format error exits 2 with friendly hint",
			mode: "clean",
			args: func(a *cliArgs) { a.cleanTag = "t1"; a.login = "bad,only" },
			setupDeps: func(d *testDeps) {
				d.cleanRes = &app.CleanError{Source: &update.UpdateError{
					Kind: update.ErrKindLoginParams, Msg: "command line parameter format error"}}
			},
			wantCode: 2,
			check: func(t *testing.T, fx *dispatchFixture) {
				assertContains(t, fx.errOut.String(),
					"[AUTH:登录认证] Command line parameter format error, please use -login http://ip,username,password")
			},
		},
		{
			name: "clean login gateway error exits 2 with raw message",
			mode: "clean",
			args: func(a *cliArgs) { a.cleanTag = "t1"; a.login = "" },
			setupDeps: func(d *testDeps) {
				d.cleanRes = &app.CleanError{Source: &update.UpdateError{
					Kind: update.ErrKindLoginParams, Msg: "default gateway not found"}}
			},
			wantCode: 2,
			check: func(t *testing.T, fx *dispatchFixture) {
				assertContains(t, fx.errOut.String(), "[AUTH:登录认证] default gateway not found")
			},
		},
		{
			name: "clean step login failure exits 1",
			mode: "clean",
			args: func(a *cliArgs) { a.cleanTag = "t1" },
			setupDeps: func(d *testDeps) {
				d.cleanRes = &app.CleanError{Step: "login", Source: errors.New("bad credentials")}
			},
			wantCode: 1,
			check: func(t *testing.T, fx *dispatchFixture) {
				assertContains(t, fx.errOut.String(), "[LOGIN:登录失败] Failed to login to iKuai: bad credentials")
			},
		},
		{
			name: "clean step custom_isp failure exits 1 with tag",
			mode: "clean",
			args: func(a *cliArgs) { a.cleanTag = "t2" },
			setupDeps: func(d *testDeps) {
				d.cleanRes = &app.CleanError{Step: "custom_isp", Source: errors.New("nope")}
			},
			wantCode: 1,
			check: func(t *testing.T, fx *dispatchFixture) {
				assertContains(t, fx.errOut.String(), "Failed to remove old custom ISP for tag t2: nope")
			},
		},
		{
			name:     "web mode removed hint exits 2",
			mode:     "web",
			wantCode: 2,
			check: func(t *testing.T, fx *dispatchFixture) {
				assertContains(t, fx.errOut.String(), "-r web 已移除：请使用 -r cron / cronAft，并在配置中启用 webui.enable=true")
			},
		},
		{
			name:     "unknown mode exits 2",
			mode:     "frobnicate",
			wantCode: 2,
			check: func(t *testing.T, fx *dispatchFixture) {
				assertContains(t, fx.errOut.String(), "[ERR:参数错误] Invalid -r parameter: frobnicate")
			},
		},
		{
			name:     "cronAft starts cron only and stops on signal",
			mode:     "cronAft",
			wantCode: 0,
			check: func(t *testing.T, fx *dispatchFixture) {
				if fx.fake.runOnceCount() != 0 {
					t.Fatalf("run-once calls = %d, want 0", fx.fake.runOnceCount())
				}
				crons := fx.fake.cronCalls()
				if len(crons) != 1 || crons[0][0] != "*/5 * * * *" || crons[0][1] != "ispdomain" {
					t.Fatalf("start-cron calls = %+v", crons)
				}
				if fx.fake.stopAllCount() != 1 {
					t.Fatalf("stop-all calls = %d, want 1", fx.fake.stopAllCount())
				}
				assertContains(t, fx.out.String(), "模式: cronAft")
				assertContains(t, fx.out.String(), "解析: 0 */5 * * * *")
			},
		},
		{
			name:     "cron runs once then starts cron",
			mode:     "cron",
			wantCode: 0,
			check: func(t *testing.T, fx *dispatchFixture) {
				if fx.fake.runOnceCount() != 1 {
					t.Fatalf("run-once calls = %d, want 1", fx.fake.runOnceCount())
				}
				if len(fx.fake.cronCalls()) != 1 {
					t.Fatalf("start-cron calls = %d, want 1", len(fx.fake.cronCalls()))
				}
				assertContains(t, fx.out.String(), "模式: cron")
			},
		},
		{
			name:     "cron skips wait when task already running",
			mode:     "cron",
			setupRt:  func(f *fakeRuntime) { f.startRunOn = false },
			wantCode: 0,
			check: func(t *testing.T, fx *dispatchFixture) {
				assertContains(t, fx.out.String(), "[TASK:任务状态] Task is already running, ignore start request")
				if len(fx.fake.cronCalls()) != 1 {
					t.Fatalf("start-cron calls = %d, want 1", len(fx.fake.cronCalls()))
				}
			},
		},
		{
			name:     "cron empty and no webui exits 0 without blocking",
			mode:     "cron",
			cfg:      func(c *config.Config) { c.Cron = " " },
			wantCode: 0,
			check: func(t *testing.T, fx *dispatchFixture) {
				assertContains(t, fx.out.String(), "Cron 配置为空：不会自动定时；可在 WebUI 中手动启动 cron")
				if len(fx.fake.cronCalls()) != 0 {
					t.Fatalf("start-cron calls = %d, want 0", len(fx.fake.cronCalls()))
				}
				if fx.fake.stopAllCount() != 0 {
					t.Fatalf("stop-all calls = %d, want 0", fx.fake.stopAllCount())
				}
			},
		},
		{
			name:     "cron webui enabled starts web server",
			mode:     "cronAft",
			cfg:      func(c *config.Config) { c.WebUI.Enable = true },
			wantCode: 0,
			check: func(t *testing.T, fx *dispatchFixture) {
				if fx.deps.webPortCount() != 1 {
					t.Fatalf("web starts = %d, want 1", fx.deps.webPortCount())
				}
				assertContains(t, fx.out.String(), "WebUI: http://127.0.0.1:19001")
			},
		},
		{
			name:     "cron webui enabled but port blank exits 2",
			mode:     "cronAft",
			cfg:      func(c *config.Config) { c.WebUI.Enable = true; c.WebUI.Port = "" },
			wantCode: 2,
			check: func(t *testing.T, fx *dispatchFixture) {
				assertContains(t, fx.errOut.String(), "[CONF:配置错误] webui.port 为空，无法启动 WebUI")
				if fx.deps.webPortCount() != 0 {
					t.Fatalf("web starts = %d, want 0", fx.deps.webPortCount())
				}
			},
		},
		{
			name: "cron webui start failure exits 1",
			mode: "cron",
			cfg:  func(c *config.Config) { c.WebUI.Enable = true },
			setupDeps: func(d *testDeps) {
				d.webRes = errors.New("bind: address already in use")
			},
			wantCode: 1,
			check: func(t *testing.T, fx *dispatchFixture) {
				assertContains(t, fx.errOut.String(),
					"[ERR:启动失败] WebUI Server failed to start, port might be occupied: bind: address already in use")
			},
		},
		{
			name:     "cron start-cron failure exits 1",
			mode:     "cron",
			setupRt:  func(f *fakeRuntime) { f.startCronErr = errors.New("Invalid cron expression") },
			wantCode: 1,
			check: func(t *testing.T, fx *dispatchFixture) {
				assertContains(t, fx.errOut.String(), "[CRON:定时任务] Failed to start scheduled task: Invalid cron expression")
			},
		},
		{
			name:     "cron bad cli login format exits 2",
			mode:     "cron",
			args:     func(a *cliArgs) { a.login = "nope" },
			wantCode: 2,
			check: func(t *testing.T, fx *dispatchFixture) {
				assertContains(t, fx.errOut.String(),
					"[AUTH:登录认证] Command line parameter format error, please use -login http://ip,username,password")
			},
		},
		{
			name:     "cron cli login prints cli-source banner",
			mode:     "cronAft",
			args:     func(a *cliArgs) { a.login = "http://127.0.0.1:1,admin,pass" },
			wantCode: 0,
			check: func(t *testing.T, fx *dispatchFixture) {
				assertContains(t, fx.out.String(), "[AUTH:登录认证] Logging in using command line parameters")
				assertNotContains(t, fx.out.String(), "[SYS:网关检测]")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixture(tc.mode)
			if tc.args != nil {
				tc.args(&fx.args)
			}
			if tc.cfg != nil {
				tc.cfg(fx.cfg)
			}
			if tc.setupRt != nil {
				tc.setupRt(fx.fake)
			}
			if tc.setupDeps != nil {
				tc.setupDeps(fx.deps)
			}
			// cron/cronAft 的等待语义用预取消 ctx 模拟"信号已到"。
			// A pre-cancelled ctx simulates "signal already received" for cron waits.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.mode == "cron" || tc.mode == "cronAft" {
				cancel()
			}
			got := runModeDispatch(ctx, fx.env())
			if got != tc.wantCode {
				t.Fatalf("exit = %d, want %d\nstdout:\n%s\nstderr:\n%s", got, tc.wantCode, fx.out.String(), fx.errOut.String())
			}
			if tc.check != nil {
				tc.check(t, fx)
			}
		})
	}
}

// TestCronWaitsForRunOnceCompletion 验证 cron 模式等首次运行完成后才启动定时。
// TestCronWaitsForRunOnceCompletion verifies cron starts scheduling only after
// the initial run finishes.
func TestCronWaitsForRunOnceCompletion(t *testing.T) {
	fx := newFixture("cron")
	fx.fake.status = runtime.RuntimeStatus{Running: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	flipped := make(chan time.Time, 1)
	time.AfterFunc(50*time.Millisecond, func() {
		flipped <- time.Now()
		fx.fake.setRunning(false)
		cancel()
	})

	got := runModeDispatch(ctx, fx.env())
	if got != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got, fx.out.String(), fx.errOut.String())
	}
	flipTime := <-flipped
	cronTime, ok := fx.fake.firstStartCronTime()
	if !ok {
		t.Fatal("start-cron never called")
	}
	if !cronTime.After(flipTime) {
		t.Fatalf("start-cron at %v preceded run completion at %v", cronTime, flipTime)
	}
	if fx.fake.runOnceCount() != 1 {
		t.Fatalf("run-once calls = %d, want 1", fx.fake.runOnceCount())
	}
}

// TestCronAftBlocksUntilSignal 验证 cronAft 在信号到来前保持阻塞。
// TestCronAftBlocksUntilSignal verifies cronAft keeps blocking until signaled.
func TestCronAftBlocksUntilSignal(t *testing.T) {
	fx := newFixture("cronAft")
	fx.cfg.WebUI.Enable = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan int, 1)
	go func() { done <- runModeDispatch(ctx, fx.env()) }()

	deadline := time.Now().Add(2 * time.Second)
	for fx.deps.webPortCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if fx.deps.webPortCount() == 0 {
		t.Fatal("web server never started")
	}
	select {
	case <-done:
		t.Fatal("dispatch returned before signal")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch did not exit after signal")
	}
	if fx.fake.stopAllCount() != 1 {
		t.Fatalf("stop-all calls = %d, want 1", fx.fake.stopAllCount())
	}
}

func TestNormalizeGoStyleArgs(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "rewrites single-dash longs",
			in:   []string{"ikuai-bypass", "-tag", "v1", "-exportPath=/x", "-login", "a,b,c", "-isIpGroupNameAddRandomSuff=0"},
			want: []string{"ikuai-bypass", "--tag", "v1", "--exportPath=/x", "--login", "a,b,c", "--isIpGroupNameAddRandomSuff=0"},
		},
		{
			name: "keeps short flags and double dash forms",
			in:   []string{"ikuai-bypass", "-c", "cfg.yml", "--c=cfg.yml", "-m", "ipgroup", "--tag=v2"},
			want: []string{"ikuai-bypass", "-c", "cfg.yml", "--c=cfg.yml", "-m", "ipgroup", "--tag=v2"},
		},
		{
			name: "keeps unknown and prefix-collision args",
			in:   []string{"ikuai-bypass", "-tags", "-loginx", "-"},
			want: []string{"ikuai-bypass", "-tags", "-loginx", "-"},
		},
		{
			name: "empty argv",
			in:   []string{},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeGoStyleArgs(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %+v, want %+v", got, tc.want)
				}
			}
		})
	}
}

func TestParseBoolFlag(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"1", true},
		{"yes", true},
		{"true", true},
		{"2", true},
		{"0", false},
		{"false", false},
		{"OFF", false},
		{" No ", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := parseBoolFlag(tc.raw); got != tc.want {
			t.Fatalf("parseBoolFlag(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestParseArgsSetTracking(t *testing.T) {
	args, err := parseArgs([]string{"-r", "", "-m", "ipgroup", "-tag=x", "-isIpGroupNameAddRandomSuff", "0"}, io.Discard)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if !args.runModeSet || args.runMode != "" {
		t.Fatalf("runMode set=%v value=%q", args.runModeSet, args.runMode)
	}
	if !args.moduleSet || args.module != "ipgroup" {
		t.Fatalf("module set=%v value=%q", args.moduleSet, args.module)
	}
	if args.cleanTag != "x" {
		t.Fatalf("cleanTag = %q", args.cleanTag)
	}
	if args.isIpGroupRand != "0" {
		t.Fatalf("isIpGroupRand = %q", args.isIpGroupRand)
	}
	if args.configPathSet {
		t.Fatal("configPath should be unset")
	}

	args, err = parseArgs([]string{"--c", "a.yml"}, io.Discard)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if !args.configPathSet || args.configPath != "a.yml" {
		t.Fatalf("configPath set=%v value=%q", args.configPathSet, args.configPath)
	}

	if _, err = parseArgs([]string{"-nope"}, io.Discard); err == nil {
		t.Fatal("unknown flag should fail")
	}
}

func TestEnsureConfigExistsOrPromptCreate(t *testing.T) {
	t.Run("existing file skips prompt", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "cfg.yml")
		if err := os.WriteFile(path, []byte("ikuai-url: http://x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		out := bytes.NewBuffer(nil)
		if code := ensureConfigExistsOrPromptCreate(path, strings.NewReader(""), false, out, io.Discard); code != 0 {
			t.Fatalf("code = %d, want 0", code)
		}
		if out.Len() != 0 {
			t.Fatalf("unexpected prompt:\n%s", out.String())
		}
	})
	t.Run("non tty refuses", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing.yml")
		errOut := bytes.NewBuffer(nil)
		code := ensureConfigExistsOrPromptCreate(path, strings.NewReader(""), false, io.Discard, errOut)
		if code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
		assertContains(t, errOut.String(), "[CONF:配置读取] 非交互终端，已禁止启动")
		if _, serr := os.Stat(path); !os.IsNotExist(serr) {
			t.Fatal("config file should not exist")
		}
	})
	t.Run("tty accept writes default", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "new.yml")
		errOut := bytes.NewBuffer(nil)
		out := bytes.NewBuffer(nil)
		code := ensureConfigExistsOrPromptCreate(path, strings.NewReader("y\n"), true, out, errOut)
		if code != 0 {
			t.Fatalf("code = %d, want 0, stderr:\n%s", code, errOut.String())
		}
		assertContains(t, out.String(), "[CONF:配置文件创建] 已创建默认配置文件")
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			t.Fatal(rerr)
		}
		if !strings.Contains(string(data), "ikuai-url") {
			t.Fatal("written default config missing ikuai-url")
		}
	})
	t.Run("tty decline refuses", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "new2.yml")
		errOut := bytes.NewBuffer(nil)
		code := ensureConfigExistsOrPromptCreate(path, strings.NewReader("n\n"), true, io.Discard, errOut)
		if code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
		assertContains(t, errOut.String(), "[CONF:配置读取] 已取消创建配置文件，程序未启动")
		if _, serr := os.Stat(path); !os.IsNotExist(serr) {
			t.Fatal("config file should not exist")
		}
	})
}

func writeTempConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test-config.yml")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCliMainValidation(t *testing.T) {
	cfgPath := writeTempConfig(t)

	t.Run("invalid module exits 2", func(t *testing.T) {
		errOut := bytes.NewBuffer(nil)
		code := cliMain([]string{"ikuai-bypass", "-c", cfgPath, "-m", "badmod", "-r", "once"},
			strings.NewReader(""), false, io.Discard, errOut)
		if code != 2 {
			t.Fatalf("code = %d, want 2", code)
		}
		assertContains(t, errOut.String(), "[ERR:参数错误] Invalid -m parameter: badmod")
	})

	t.Run("module export hint exits 2", func(t *testing.T) {
		errOut := bytes.NewBuffer(nil)
		code := cliMain([]string{"ikuai-bypass", "-c", cfgPath, "-m", "exportDomainSteamToTxt", "-r", "once"},
			strings.NewReader(""), false, io.Discard, errOut)
		if code != 2 {
			t.Fatalf("code = %d, want 2", code)
		}
		assertContains(t, errOut.String(), "请使用 -r exportDomainStreamToTxt 并可配合 -exportPath 指定导出目录")
	})

	t.Run("missing config non tty exits 1", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "none.yml")
		errOut := bytes.NewBuffer(nil)
		code := cliMain([]string{"ikuai-bypass", "-c", missing, "-r", "once"},
			strings.NewReader(""), false, io.Discard, errOut)
		if code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
		assertContains(t, errOut.String(), "[CONF:配置读取] 非交互终端，已禁止启动")
	})

	t.Run("bad flag exits 2", func(t *testing.T) {
		code := cliMain([]string{"ikuai-bypass", "-c", cfgPath, "-nope"},
			strings.NewReader(""), false, io.Discard, io.Discard)
		if code != 2 {
			t.Fatalf("code = %d, want 2", code)
		}
	})
}

// TestCliMainOnceLoginError 走真实 RunUpdateByModule 的登录参数错误路径
// （无网络依赖），验证 cliMain 到 once 分发的完整接线。
// TestCliMainOnceLoginError drives the real RunUpdateByModule login-params
// failure path (no network) to verify the full cliMain-to-once wiring.
func TestCliMainOnceLoginError(t *testing.T) {
	cfgPath := writeTempConfig(t)
	out := bytes.NewBuffer(nil)
	errOut := bytes.NewBuffer(nil)
	code := cliMain([]string{"ikuai-bypass", "-c", cfgPath, "-r", "once", "-login", "bad"},
		strings.NewReader(""), false, out, errOut)
	if code != 1 {
		t.Fatalf("code = %d, want 1\nstderr:\n%s", code, errOut.String())
	}
	assertContains(t, out.String(), "[START:启动程序] Run mode: once")
	assertContains(t, errOut.String(), "[UPDATE:更新失败] login params error: command line parameter format error")
}

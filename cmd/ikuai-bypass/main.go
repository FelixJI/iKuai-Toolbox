// main.go CLI 入口：Go 风格单横线长参兼容、运行模式分发、信号处理与退出码，
// 行为对齐 apps/cli/src/main.rs + lib.rs。
// CLI entry point: Go-style single-dash long-flag compatibility, run-mode
// dispatch, signal handling, and exit codes, aligned with apps/cli/src/main.rs + lib.rs.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/FelixJI/iKuai-Toolbox/internal/app"
	"github.com/FelixJI/iKuai-Toolbox/internal/config"
	"github.com/FelixJI/iKuai-Toolbox/internal/ikuai"
	"github.com/FelixJI/iKuai-Toolbox/internal/logger"
	"github.com/FelixJI/iKuai-Toolbox/internal/runtime"
	"github.com/FelixJI/iKuai-Toolbox/internal/update"
	"github.com/FelixJI/iKuai-Toolbox/internal/webserver"
)

// ---- argv 归一化（lib.rs normalize_go_style_args） ----

// singleDashLongs 需要兼容单横线写法的长参数（LuCI/libexec 的 Go 风格调用），lib.rs L31。
// singleDashLongs lists the long flags accepted with a single dash (the LuCI/libexec
// Go-style calling convention), mirroring lib.rs L31.
var singleDashLongs = [...]string{"exportPath", "tag", "login", "isIpGroupNameAddRandomSuff"}

// rewriteSingleDashLong 把 "-name" / "-name=value" 重写为双横线形式（lib.rs L26-45）。
// Go flag 本身两种写法都接受，重写保持与 Rust 归一化器逐参数等价。
// rewriteSingleDashLong rewrites "-name" / "-name=value" into double-dash form
// (lib.rs L26-45). Go's flag accepts both spellings natively; the rewrite keeps
// exact parity with the Rust normalizer.
func rewriteSingleDashLong(arg string) (string, bool) {
	if !strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "--") {
		return "", false
	}
	for _, name := range singleDashLongs {
		if arg == "-"+name {
			return "--" + name, true
		}
		if rest, ok := strings.CutPrefix(arg, "-"+name+"="); ok {
			return "--" + name + "=" + rest, true
		}
	}
	return "", false
}

// normalizeGoStyleArgs 保留 argv[0]，其余逐个重写单横线长参（lib.rs L3-24）。
// normalizeGoStyleArgs keeps argv[0] verbatim and rewrites the remaining
// single-dash long flags (lib.rs L3-24).
func normalizeGoStyleArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, 0, len(args))
	out = append(out, args[0])
	for _, a := range args[1:] {
		if rewritten, ok := rewriteSingleDashLong(a); ok {
			out = append(out, rewritten)
		} else {
			out = append(out, a)
		}
	}
	return out
}

// ---- 参数表与解析（main.rs Args） ----

// cliArgs 参数表对齐 main.rs L165-188：c/r/m 三个短参 + 四个长参；
// Set 字段区分"显式传了空串"与"未传"（clap Option 语义）。
// cliArgs mirrors the argument table of main.rs L165-188: three short flags
// c/r/m plus four long flags; the Set fields distinguish an explicitly passed
// empty value from an absent one (clap Option semantics).
type cliArgs struct {
	configPath    string
	configPathSet bool
	runMode       string
	runModeSet    bool
	module        string
	moduleSet     bool
	cleanTag      string
	exportPath    string
	login         string
	isIpGroupRand string
}

// parseArgs 解析归一化后的 argv（不含 argv[0]），解析失败由 flag 包打印错误。
// parseArgs parses the normalized argv (without argv[0]); parse failures are
// printed by the flag package itself.
func parseArgs(argv []string, errOut io.Writer) (cliArgs, error) {
	var a cliArgs
	fs := flag.NewFlagSet("ikuai-bypass", flag.ContinueOnError)
	fs.SetOutput(errOut)
	fs.StringVar(&a.configPath, "c", "", "config file path")
	fs.StringVar(&a.runMode, "r", "", "run mode")
	fs.StringVar(&a.module, "m", "", "update module")
	fs.StringVar(&a.cleanTag, "tag", "", "clean tag")
	fs.StringVar(&a.exportPath, "exportPath", "/tmp", "stream-domain export directory")
	fs.StringVar(&a.login, "login", "", "ikuai login info: http://ip,username,password")
	fs.StringVar(&a.isIpGroupRand, "isIpGroupNameAddRandomSuff", "1", "random suffix for ip group names")
	if err := fs.Parse(argv); err != nil {
		return a, err
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	a.configPathSet = set["c"]
	a.runModeSet = set["r"]
	a.moduleSet = set["m"]
	return a, nil
}

// parseBoolFlag 对齐 main.rs L708-711：非 0/false/off/no/空 即为 true。
// parseBoolFlag mirrors main.rs L708-711: anything but 0/false/off/no/blank is true.
func parseBoolFlag(raw string) bool {
	s := strings.ToLower(strings.TrimSpace(raw))
	return !(s == "" || s == "0" || s == "false" || s == "off" || s == "no")
}

// updateOptionsFromArgs 由参数构造更新选项（main.rs L278-281）。
// updateOptionsFromArgs builds the update options from the flags (main.rs L278-281).
func updateOptionsFromArgs(a cliArgs) *update.UpdateOptions {
	return &update.UpdateOptions{
		ExportPath:                 a.exportPath,
		IpGroupNameAddRandomSuffix: parseBoolFlag(a.isIpGroupRand),
	}
}

// ---- 通用小工具 ----

// displayConfPath 相对路径拼进程 cwd 后展示（main.rs L22-28）。
// displayConfPath joins relative paths with the process cwd (main.rs L22-28).
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

// isTerminal 以字符设备判定终端（Windows 控制台与 unix tty 均命中）。
// isTerminal detects a terminal via the char-device bit (covers both the
// Windows console and unix ttys).
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// renderSink 构造渲染到 stdout 的日志 sink（TTY 时彩色），main.rs L671-674。
// renderSink builds a stdout-rendering log sink (colored on a TTY), main.rs L671-674.
func renderSink(stdout io.Writer, useColor bool) logger.LogSink {
	renderer := logger.NewRenderer(useColor)
	return func(rec logger.LogRecord) {
		fmt.Fprintln(stdout, renderer.Render(rec))
	}
}

// ensureConfigExistsOrPromptCreate 配置缺失时交互确认创建（main.rs L74-116）：
// 非 TTY 拒启；输入 y 写内嵌默认配置；其余输入取消。返回 0 继续、1 终止。
// ensureConfigExistsOrPromptCreate interactively confirms creating a missing
// config (main.rs L74-116): non-TTY refuses to start, a "y" answer writes the
// embedded default, anything else cancels. Returns 0 to continue, 1 to abort.
func ensureConfigExistsOrPromptCreate(path string, stdin io.Reader, stdinIsTTY bool, stdout, stderr io.Writer) int {
	if info, serr := os.Stat(path); serr == nil && !info.IsDir() {
		return 0
	}
	display := displayConfPath(path)
	fmt.Fprintf(stdout, "[CONF:配置文件不存在] 指定的配置文件路径 %s 不存在，是否创建，输入y创建，输入其他字符禁止启动\n", display)
	if !stdinIsTTY {
		fmt.Fprintln(stderr, "[CONF:配置读取] 非交互终端，已禁止启动")
		return 1
	}
	fmt.Fprint(stdout, "> ")
	input, rerr := bufio.NewReader(stdin).ReadString('\n')
	if rerr != nil && !errors.Is(rerr, io.EOF) {
		fmt.Fprintf(stderr, "[CONF:配置读取] 读取输入失败: %s\n", rerr)
		return 1
	}
	if strings.TrimSpace(input) != "y" {
		fmt.Fprintln(stderr, "[CONF:配置读取] 已取消创建配置文件，程序未启动")
		return 1
	}
	if werr := config.WriteEmbeddedDefaultToPath(path); werr != nil {
		fmt.Fprintf(stderr, "[CONF:配置读取] 创建默认配置文件失败: %s\n", werr)
		return 1
	}
	fmt.Fprintf(stdout, "[CONF:配置文件创建] 已创建默认配置文件: %s\n", display)
	return 0
}

// ---- 完成横幅（main.rs L30-72 / L118-163） ----

func printOnceDoneBanner(out io.Writer, mode, module, confPath string, elapsed time.Duration) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "===========================================================")
	fmt.Fprintln(out, "[END:运行完毕] 任务完成")
	fmt.Fprintln(out, "-----------------------------------------------------------")
	fmt.Fprintf(out, "模式: %s\n", mode)
	fmt.Fprintf(out, "模块: %s\n", module)
	fmt.Fprintf(out, "配置: %s\n", confPath)
	fmt.Fprintf(out, "耗时: %.3fs\n", elapsed.Seconds())
	fmt.Fprintln(out, "提示: 如需定时运行，请使用 -r cron 或 -r cronAft")
	fmt.Fprintln(out, "===========================================================")
	fmt.Fprintln(out)
}

func printCleanDoneBanner(out io.Writer, tag string, isAll bool, confPath string, elapsed time.Duration) {
	target := tag
	if isAll {
		target = "全部 IKB 规则"
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "===========================================================")
	fmt.Fprintln(out, "[END:清理完毕] 清理完成")
	fmt.Fprintln(out, "-----------------------------------------------------------")
	fmt.Fprintln(out, "模式: clean")
	fmt.Fprintf(out, "清理目标: %s\n", target)
	fmt.Fprintf(out, "配置: %s\n", confPath)
	fmt.Fprintf(out, "耗时: %.3fs\n", elapsed.Seconds())
	fmt.Fprintln(out, "提示: 如需重新同步规则，请使用 -r once / cron / cronAft")
	fmt.Fprintln(out, "===========================================================")
	fmt.Fprintln(out)
}

func printExportDoneBanner(out io.Writer, exportPath, confPath string, elapsed time.Duration) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "===========================================================")
	fmt.Fprintln(out, "[END:导出完毕] 导出完成")
	fmt.Fprintln(out, "-----------------------------------------------------------")
	fmt.Fprintln(out, "模式: exportDomainSteamToTxt")
	fmt.Fprintf(out, "导出目录: %s\n", exportPath)
	fmt.Fprintf(out, "配置: %s\n", confPath)
	fmt.Fprintf(out, "耗时: %.3fs\n", elapsed.Seconds())
	fmt.Fprintln(out, "===========================================================")
	fmt.Fprintln(out)
}

// bannerCronParser 与 internal/runtime 的解析器同构（6 段含秒 + 描述符，既定
// 裁决），仅用于横幅的下次执行时间回退计算（main.rs L138-144 的 fallback）。
// bannerCronParser mirrors the parser of internal/runtime (6-field second-capable
// with descriptors, per the adjudicated ruling); it exists only for the banner's
// next-run fallback (the fallback of main.rs L138-144).
var bannerCronParser = cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// printCronStartedBanner 定时启动横幅（main.rs L118-163）：norm 与配置表达式
// 不同才打印解析行；下次执行为空时按归一化表达式计算回退值。
// printCronStartedBanner prints the cron-started banner (main.rs L118-163): the
// parsed line appears only when norm differs from the configured expression;
// an empty next-run falls back to the normalized schedule.
func printCronStartedBanner(out io.Writer, mode string, st runtime.RuntimeStatus, norm string, webuiPort string) {
	runningText := "待机"
	if st.Running {
		runningText = "执行中"
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "===========================================================")
	fmt.Fprintln(out, "[CRON:定时任务] 已启动")
	fmt.Fprintln(out, "-----------------------------------------------------------")
	fmt.Fprintf(out, "模式: %s\n", mode)
	fmt.Fprintf(out, "模块: %s\n", st.Module)
	fmt.Fprintf(out, "表达式: %s\n", st.CronExpr)
	if norm != "" && norm != st.CronExpr {
		fmt.Fprintf(out, "解析: %s\n", norm)
	}
	nextRun := strings.TrimSpace(st.NextRunAt)
	if nextRun == "" && norm != "" {
		if sched, perr := bannerCronParser.Parse(norm); perr == nil {
			nextRun = sched.Next(time.Now()).Format(time.RFC3339)
		}
	}
	if nextRun == "" {
		fmt.Fprintln(out, "下次执行: -")
	} else {
		fmt.Fprintf(out, "下次执行: %s\n", nextRun)
	}
	fmt.Fprintf(out, "运行状态: %s (cron_running=%v)\n", runningText, st.CronRunning)
	if webuiPort != "" {
		fmt.Fprintf(out, "WebUI: http://127.0.0.1:%s\n", webuiPort)
	}
	fmt.Fprintln(out, "提示: 可在 WebUI 中停止定时任务；或 Ctrl+C 退出")
	fmt.Fprintln(out, "===========================================================")
	fmt.Fprintln(out)
}

// ---- 分发依赖（可测缝） ----

// runtimeService 分发所需的最小运行时接口，*runtime.RuntimeService 天然满足；
// 抽接口仅为测试注入记录桩。
// runtimeService is the minimal runtime surface dispatch needs; the concrete
// *runtime.RuntimeService satisfies it. The interface exists so tests can
// inject a recording stub.
type runtimeService interface {
	StartRunOnce(module string) (bool, error)
	StartCron(expr, module string) error
	Status() runtime.RuntimeStatus
	StopAll() error
	SubscribeLogs() (<-chan logger.LogRecord, func())
}

// dispatchDeps 模式分发的协作件；生产装配真实服务，测试注入记录桩。
// RunUpdateByModule / ExportStreamDomainToTxt 返回具体类型 *UpdateError，
// 保持具体类型传递避免 typed-nil 误判（台账 Task 5 项）。
// dispatchDeps holds the dispatch collaborators; production wires the real
// services while tests inject recording stubs. RunUpdateByModule /
// ExportStreamDomainToTxt return the concrete *UpdateError, which is kept
// concrete through the seam to avoid the typed-nil trap (ledger Task 5 item).
type dispatchDeps struct {
	newRuntime func(cfg *config.Config, cliLogin, defaultCron, defaultModule string, opts *update.UpdateOptions) runtimeService
	runUpdate  func(cfg *config.Config, cliLogin, module string, opts *update.UpdateOptions, sink logger.LogSink) *update.UpdateError
	exportTxt  func(cfg *config.Config, exportPath string, sink logger.LogSink) *update.UpdateError
	runClean   func(cfg *config.Config, cliLogin, cleanTag string) error
	startWeb   func(cfg *config.Config, rt runtimeService, cfgPath, cliLogin, port string) error
}

// prodDeps 装配真实服务。
// prodDeps wires the real services.
func prodDeps() dispatchDeps {
	return dispatchDeps{
		newRuntime: func(cfg *config.Config, cliLogin, defaultCron, defaultModule string, opts *update.UpdateOptions) runtimeService {
			return runtime.NewRuntimeService(cfg, cliLogin, defaultCron, defaultModule, opts)
		},
		runUpdate: update.RunUpdateByModule,
		exportTxt: update.ExportStreamDomainToTxt,
		runClean:  app.RunClean,
		startWeb: func(cfg *config.Config, rt runtimeService, cfgPath, cliLogin, port string) error {
			concrete, ok := rt.(*runtime.RuntimeService)
			if !ok {
				return errors.New("webui requires the concrete runtime service")
			}
			srv := webserver.NewServer(concrete, cfg, cfgPath)
			srv.CLILogin = cliLogin
			return webserver.StartWebServer(srv, port)
		},
	}
}

// dispatchEnv 一次模式分发所需的全部输入。
// dispatchEnv carries everything one dispatch call needs.
type dispatchEnv struct {
	deps        dispatchDeps
	args        cliArgs
	cfg         *config.Config
	cfgPath     string
	runMode     string
	module      string
	stdout      io.Writer
	stderr      io.Writer
	stdoutIsTTY bool
}

// spawnLogForwarder 订阅运行时日志并渲染到 stdout（TTY 彩色，main.rs L648-663）。
// 消费循环必须 select ctx.Done 退出：broker 的 cancel 只注销订阅、不 close
// channel（台账 Task 6 遗留约束）。
// spawnLogForwarder subscribes to runtime logs and renders them to stdout
// (colored on a TTY, main.rs L648-663). The loop must exit on ctx.Done: the
// broker cancel only unsubscribes and never closes the channel (the Task 6
// ledger constraint).
func spawnLogForwarder(ctx context.Context, rt runtimeService, stdout io.Writer, useColor bool) {
	ch, cancel := rt.SubscribeLogs()
	renderer := logger.NewRenderer(useColor)
	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case rec := <-ch:
				fmt.Fprintln(stdout, renderer.Render(rec))
			}
		}
	}()
}

// ---- 运行模式分发（main.rs run()） ----

// runModeDispatch 按运行模式分发并返回进程退出码。
// runModeDispatch dispatches on the run mode and returns the process exit code.
func runModeDispatch(ctx context.Context, env dispatchEnv) int {
	opts := updateOptionsFromArgs(env.args)
	switch env.runMode {
	case "exportDomainStreamToTxt", "exportDomainSteamToTxt":
		return runExportMode(env)
	case "cron":
		return runCronMode(ctx, env, opts, "cron", true)
	case "cronAft":
		return runCronMode(ctx, env, opts, "cronAft", false)
	case "nocron", "once", "1":
		return runOnceMode(env, opts)
	case "clean":
		return runCleanMode(env)
	default:
		if env.runMode == "web" {
			fmt.Fprintln(env.stderr, "[ERR:参数错误] -r web 已移除：请使用 -r cron / cronAft，并在配置中启用 webui.enable=true")
		} else {
			fmt.Fprintf(env.stderr, "[ERR:参数错误] Invalid -r parameter: %s\n", env.runMode)
		}
		return 2
	}
}

// runOnceMode 单次更新（main.rs L504-525）：失败 exit 1，成功打完成横幅。
// runOnceMode runs one update pass (main.rs L504-525): failure exits 1 and
// success prints the done banner.
func runOnceMode(env dispatchEnv, opts *update.UpdateOptions) int {
	started := time.Now()
	if uerr := env.deps.runUpdate(env.cfg, env.args.login, env.module, opts, renderSink(env.stdout, env.stdoutIsTTY)); uerr != nil {
		fmt.Fprintf(env.stderr, "[UPDATE:更新失败] %s\n", uerr)
		return 1
	}
	label := env.runMode
	if env.runMode == "nocron" || env.runMode == "1" {
		label = "once"
	}
	printOnceDoneBanner(env.stdout, label, env.module, displayConfPath(env.cfgPath), time.Since(started))
	return 0
}

// runExportMode 域名规则导出（main.rs L284-304）：空目录 exit 2、失败 exit 1。
// runExportMode exports stream-domain lists (main.rs L284-304): a blank
// directory exits 2 and a failure exits 1.
func runExportMode(env dispatchEnv) int {
	started := time.Now()
	fmt.Fprintln(env.stdout, "[MODE:运行模式] exportDomainSteamToTxt - exporting stream-domain lists to TXT")
	exportPath := strings.TrimSpace(env.args.exportPath)
	if exportPath == "" {
		fmt.Fprintln(env.stderr, "[ERR:参数错误] exportDomainSteamToTxt requires -exportPath")
		return 2
	}
	if uerr := env.deps.exportTxt(env.cfg, exportPath, renderSink(env.stdout, env.stdoutIsTTY)); uerr != nil {
		fmt.Fprintf(env.stderr, "[EXPORT:导出失败] %s\n", uerr)
		return 1
	}
	printExportDoneBanner(env.stdout, exportPath, displayConfPath(env.cfgPath), time.Since(started))
	return 0
}

// runCleanMode 清理（main.rs L527-633）：空 tag exit 2；错误经 reportCleanError 映射。
// runCleanMode cleans managed rules (main.rs L527-633): a blank tag exits 2;
// errors map through reportCleanError.
func runCleanMode(env dispatchEnv) int {
	started := time.Now()
	cleanTag := env.args.cleanTag
	if strings.TrimSpace(cleanTag) == "" {
		fmt.Fprintln(env.stderr, "[ERR:参数错误] Clean mode requires -tag (or cleanAll)")
		return 2
	}
	fmt.Fprintln(env.stdout, "[MODE:运行模式] Clean mode")
	if strings.TrimSpace(cleanTag) == ikuai.CleanModeAll {
		fmt.Fprintln(env.stdout, "[CLEAN:清理范围] Clearing all rules with prefix IKB (includes legacy notes)")
	} else {
		fmt.Fprintf(env.stdout, "[CLEAN:清理范围] Clearing rules with TagName or Name: %s\n", cleanTag)
	}
	if err := env.deps.runClean(env.cfg, env.args.login, cleanTag); err != nil {
		return reportCleanError(env.stderr, err, cleanTag, env.args.login)
	}
	printCleanDoneBanner(env.stdout, cleanTag, strings.TrimSpace(cleanTag) == ikuai.CleanModeAll,
		displayConfPath(env.cfgPath), time.Since(started))
	return 0
}

// reportCleanError 清理错误映射（main.rs L554-623）：登录参数错误 exit 2
// （CLI 提供了 -login 时必为格式错误，走友好提示；否则透出底层文案），
// 步骤失败 exit 1。
// reportCleanError maps clean failures (main.rs L554-623): login-param errors
// exit 2 (a failure with -login provided is necessarily a format error and
// gets the friendly hint; otherwise the underlying text passes through);
// step failures exit 1.
func reportCleanError(errOut io.Writer, err error, cleanTag, cliLogin string) int {
	var ce *app.CleanError
	if !errors.As(err, &ce) {
		fmt.Fprintf(errOut, "[CLEAN:清理失败] %s\n", err)
		return 1
	}
	if ce.Step == "" {
		var ue *update.UpdateError
		if errors.As(ce.Source, &ue) && ue.Kind == update.ErrKindLoginParams {
			if strings.TrimSpace(cliLogin) != "" {
				fmt.Fprintln(errOut, "[AUTH:登录认证] Command line parameter format error, please use -login http://ip,username,password")
			} else {
				fmt.Fprintf(errOut, "[AUTH:登录认证] %s\n", ue.Msg)
			}
			return 2
		}
		fmt.Fprintln(errOut, "[ERR:参数错误] Clean mode requires -tag (or cleanAll)")
		return 2
	}
	source := ""
	if ce.Source != nil {
		source = ce.Source.Error()
	}
	switch ce.Step {
	case "init_client":
		fmt.Fprintf(errOut, "[LOGIN:登录失败] Failed to build iKuai client: %s\n", source)
	case "login":
		fmt.Fprintf(errOut, "[LOGIN:登录失败] Failed to login to iKuai: %s\n", source)
	case "custom_isp":
		fmt.Fprintf(errOut, "[CLEAN:清理失败] Failed to remove old custom ISP for tag %s: %s\n", cleanTag, source)
	case "stream_domain":
		fmt.Fprintf(errOut, "[CLEAN:清理失败] Failed to remove old domain streaming for tag %s: %s\n", cleanTag, source)
	case "ip_group":
		fmt.Fprintf(errOut, "[CLEAN:清理失败] Failed to remove old IP group for tag %s: %s\n", cleanTag, source)
	case "ipv6_group":
		fmt.Fprintf(errOut, "[CLEAN:清理失败] Failed to remove old IPv6 group for tag %s: %s\n", cleanTag, source)
	case "stream_ipport":
		fmt.Fprintf(errOut, "[CLEAN:清理失败] Failed to remove old port streaming for tag %s: %s\n", cleanTag, source)
	default:
		fmt.Fprintf(errOut, "[CLEAN:清理失败] clean step %s failed: %s\n", ce.Step, source)
	}
	return 1
}

// runCronMode cron/cronAft 共用流程（main.rs L306-502）：登录参数校验（含来源
// 横幅）→ WebUI 端口校验 → 运行时 + stdout forwarder → 可选 WebUI →（cron 先跑
// 一次并等待完成）→ 启动定时 → 空定时且无 WebUI 直接退出 → 阻塞等信号。
// runCronMode is the shared cron/cronAft flow (main.rs L306-502): login-param
// validation (with source banners) → WebUI port check → runtime plus the stdout
// forwarder → optional WebUI → (cron runs once first and waits for completion)
// → start scheduling → exit immediately when cron is empty without a WebUI →
// block until signaled.
func runCronMode(ctx context.Context, env dispatchEnv, opts *update.UpdateOptions, modeLabel string, runOnceFirst bool) int {
	if runOnceFirst {
		fmt.Fprintln(env.stdout, "[MODE:运行模式] Cron mode - executing once then entering scheduled mode")
	} else {
		fmt.Fprintln(env.stdout, "[MODE:运行模式] CronAft mode - scheduled execution only")
	}

	cliLogin := env.args.login
	baseURL, _, _, uerr := update.ParseLoginParams(cliLogin, env.cfg)
	if uerr != nil {
		fmt.Fprintln(env.stderr, "[AUTH:登录认证] Command line parameter format error, please use -login http://ip,username,password")
		return 2
	}
	if strings.TrimSpace(cliLogin) != "" {
		fmt.Fprintln(env.stdout, "[AUTH:登录认证] Logging in using command line parameters")
	} else if strings.TrimSpace(env.cfg.IkuaiURL) == "" {
		fmt.Fprintf(env.stdout, "[SYS:网关检测] Using default gateway address: %s\n", baseURL)
	}

	webuiEnable := env.cfg.WebUI.Enable
	webuiPort := strings.TrimSpace(env.cfg.WebUI.Port)
	if webuiEnable && webuiPort == "" {
		fmt.Fprintln(env.stderr, "[CONF:配置错误] webui.port 为空，无法启动 WebUI")
		return 2
	}

	cronExpr := env.cfg.Cron
	rt := env.deps.newRuntime(env.cfg, cliLogin, cronExpr, env.module, opts)
	spawnLogForwarder(ctx, rt, env.stdout, env.stdoutIsTTY)

	if webuiEnable {
		if err := env.deps.startWeb(env.cfg, rt, env.cfgPath, cliLogin, webuiPort); err != nil {
			fmt.Fprintf(env.stderr, "[ERR:启动失败] WebUI Server failed to start, port might be occupied: %s\n", err)
			return 1
		}
	}

	if runOnceFirst {
		started, serr := rt.StartRunOnce(env.module)
		if serr != nil {
			fmt.Fprintf(env.stderr, "[UPDATE:更新失败] %s\n", serr)
		} else if !started {
			fmt.Fprintln(env.stdout, "[TASK:任务状态] Task is already running, ignore start request")
		} else {
			// 先跑一次（等完成）再进入定时，对齐 main.rs L372-377 的轮询等待。
			// Run once to completion first, then schedule (the polling wait of
			// main.rs L372-377).
			for rt.Status().Running {
				time.Sleep(200 * time.Millisecond)
			}
		}
	}

	if strings.TrimSpace(cronExpr) == "" {
		fmt.Fprintln(env.stdout, "[CRON:定时任务] Cron 配置为空：不会自动定时；可在 WebUI 中手动启动 cron")
	} else {
		if err := rt.StartCron(cronExpr, env.module); err != nil {
			fmt.Fprintf(env.stderr, "[CRON:定时任务] Failed to start scheduled task: %s\n", err)
			return 1
		}
		norm := ""
		if n, nerr := runtime.NormalizeCronExpr(cronExpr); nerr == nil {
			norm = n
		}
		bannerPort := ""
		if webuiEnable {
			bannerPort = webuiPort
		}
		printCronStartedBanner(env.stdout, modeLabel, rt.Status(), norm, bannerPort)
	}

	// 未启用 WebUI 且定时为空时直接退出，避免无意义常驻（main.rs L405-409）。
	// Exit immediately when the WebUI is off and cron is empty, avoiding a
	// pointless daemon (main.rs L405-409).
	if !webuiEnable && strings.TrimSpace(cronExpr) == "" {
		return 0
	}

	<-ctx.Done()
	_ = rt.StopAll()
	return 0
}

// ---- 入口 ----

// cliMain 完整 CLI 主流程（main.rs main()）：解析 → 配置确认与加载 → 有效
// mode/module 推导 → 模块校验 → 信号 ctx → 分发。返回进程退出码。
// cliMain is the full CLI flow (main.rs main()): parse → confirm and load the
// config → derive the effective mode/module → validate the module → signal ctx
// → dispatch. Returns the process exit code.
func cliMain(argv []string, stdin io.Reader, stdinIsTTY bool, stdout, stderr io.Writer) int {
	normalized := normalizeGoStyleArgs(argv)
	if len(normalized) > 0 {
		normalized = normalized[1:]
	}
	args, perr := parseArgs(normalized, stderr)
	if perr != nil {
		return 2
	}

	configPath := args.configPath
	if !args.configPathSet {
		configPath = config.DefaultConfigPath()
	}

	if code := ensureConfigExistsOrPromptCreate(configPath, stdin, stdinIsTTY, stdout, stderr); code != 0 {
		return code
	}

	cfg, lerr := config.LoadFromPath(configPath)
	if lerr != nil {
		fmt.Fprintf(stderr, "[CONF:配置读取] Failed to read configuration file: %s\n", lerr)
		return 1
	}

	effectiveRunMode := "cronAft"
	if args.runModeSet {
		effectiveRunMode = args.runMode
	} else if m := strings.TrimSpace(cfg.RunMode); m != "" {
		effectiveRunMode = m
	}

	effectiveModule := "ispdomain"
	if args.moduleSet {
		effectiveModule = args.module
	} else if m := strings.TrimSpace(cfg.Module); m != "" {
		effectiveModule = m
	}

	fmt.Fprintf(stdout, "[START:启动程序] Run mode: %s, Config path: '%s'\n", effectiveRunMode, configPath)

	// 文案对齐 runner.rs 的大写 "Invalid -m parameter"（main.rs L234-243）。
	// The wording matches the capitalized "Invalid -m parameter" of runner.rs
	// (main.rs L234-243).
	if update.ValidateModule(effectiveModule) != nil {
		if effectiveModule == "exportDomainSteamToTxt" {
			fmt.Fprintln(stderr, "[ERR:参数错误] -m exportDomainSteamToTxt 不是模块：请使用 -r exportDomainStreamToTxt 并可配合 -exportPath 指定导出目录")
		} else {
			fmt.Fprintf(stderr, "[ERR:参数错误] Invalid -m parameter: %s\n", effectiveModule)
		}
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return runModeDispatch(ctx, dispatchEnv{
		deps:        prodDeps(),
		args:        args,
		cfg:         cfg,
		cfgPath:     configPath,
		runMode:     effectiveRunMode,
		module:      effectiveModule,
		stdout:      stdout,
		stderr:      stderr,
		stdoutIsTTY: isTerminal(os.Stdout),
	})
}

func main() {
	os.Exit(cliMain(os.Args, os.Stdin, isTerminal(os.Stdin), os.Stdout, os.Stderr))
}

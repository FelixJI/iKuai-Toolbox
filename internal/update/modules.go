// modules.go 更新入口与模块分发矩阵，行为对齐 crates/core/src/update.rs
// L57-124（run_update_by_module）与 crates/core/src/runner.rs（validate_module）。
// 严格顺序：先 Login，再按 module 串行执行（普通 for 调用链天然顺序，
// 禁止 goroutine 并发跑更新）；组合模式必须先落地 IP 分组再处理域名类规则。
// The update entrypoint and module dispatch matrix, aligned with update.rs
// L57-124 (run_update_by_module) and runner.rs (validate_module). Strict order:
// login first, then the module runs serially (a plain call chain is naturally
// sequential; goroutines are forbidden); combined modes must materialize IP
// groups before domain rules.
package update

import (
	"fmt"
	"strings"

	"github.com/FelixJI/iKuai-Toolbox/internal/config"
	"github.com/FelixJI/iKuai-Toolbox/internal/ikuai"
)

// 合法模块清单，对齐 runner.rs L9 的 match 分支。
// The legal module list mirroring the match arms of runner.rs L9.
var validModules = map[string]struct{}{
	"ispdomain": {}, "ipgroup": {}, "ipv6group": {}, "ii": {}, "ip": {}, "iip": {},
}

// ValidateModule 校验 -m 参数（runner.rs L7-12），未知模块 => invalid_module。
// ValidateModule checks the -m parameter (runner.rs L7-12); unknown => invalid_module.
func ValidateModule(module string) *UpdateError {
	if _, ok := validModules[module]; !ok {
		return &UpdateError{Kind: ErrKindInvalidModule, Msg: module}
	}
	return nil
}

// RunUpdateByModule 更新主入口（update.rs L57-124）：解析登录参数 -> Login ->
// 按模块矩阵串行执行。单条目失败已在模块函数内部记日志，不中断后续条目；
// 登录失败 / 未知模块以 UpdateError 返回。
// RunUpdateByModule is the main entry (update.rs L57-124): resolve login params,
// login, then run the module matrix serially. Per-entry failures are logged
// inside the module functions and never abort the pass; login failures and
// unknown modules return an UpdateError.
func RunUpdateByModule(cfg *config.Config, cliLogin, module string, opts *UpdateOptions, sink LogSink) *UpdateError {
	params, err := resolveLoginParamsForRun(cfg, cliLogin)
	if err != nil {
		return err
	}
	api, clientErr := ikuai.NewIKuaiClient(params.BaseURL)
	if clientErr != nil {
		return ikuaiErr(clientErr)
	}

	auth := newLogger("AUTH:登录认证", sink)
	auth.info("LOGIN:开始登录", fmt.Sprintf("Logging in to iKuai: %s", params.BaseURL))
	if loginErr := api.Login(params.Username, params.Password); loginErr != nil {
		return ikuaiErr(loginErr)
	}
	auth.success("LOGIN:登录成功", "Login succeeded")

	sys := newLogger("SYS:系统组件", sink)
	switch module {
	case "ispdomain":
		sys.info("TASK:任务启动", "Starting ISP and Domain streaming mode")
		updateIspdomain(cfg, api, opts, sink)
	case "ipgroup":
		sys.info("TASK:任务启动", "Starting IP group and Next-hop gateway mode")
		updateIpgroup(cfg, api, opts, sink)
	case "ipv6group":
		sys.info("TASK:任务启动", "Starting IPv6 group mode")
		updateIpv6group(cfg, api, opts, sink)
	case "ii":
		sys.info("TASK:任务启动", "Starting hybrid mode: ISP/Domain + IP group")
		// stream-domain / stream-ipport 可能依赖本轮刚同步出的 IP 分组，
		// 因此组合模式需要先落地 IP 分组，再处理域名类规则。
		// stream-domain / stream-ipport may depend on freshly synced IP groups,
		// so combined modes materialize IP groups before domain rules.
		updateIpgroup(cfg, api, opts, sink)
		updateIspdomain(cfg, api, opts, sink)
	case "ip":
		sys.info("TASK:任务启动", "Starting hybrid mode: IPv4 group + IPv6 group")
		updateIpgroup(cfg, api, opts, sink)
		updateIpv6group(cfg, api, opts, sink)
	case "iip":
		sys.info("TASK:任务启动", "Starting full hybrid mode: ISP/Domain + IPv4/v6 group")
		updateIpgroup(cfg, api, opts, sink)
		updateIspdomain(cfg, api, opts, sink)
		updateIpv6group(cfg, api, opts, sink)
	default:
		return &UpdateError{Kind: ErrKindInvalidModule, Msg: module}
	}
	return nil
}

// loginParams 登录参数三元组。
// loginParams is the login credential triple.
type loginParams struct {
	BaseURL  string
	Username string
	Password string
}

// resolveLoginParamsForRun 模块编排阶段的登录参数解析：当前仅支持 CLI
// 三段式（url,user,pass），畸形或缺失 => login_params；配置/网关回退由
// session.go 的 ParseLoginParams 提供（登录参数解析任务）。
// resolveLoginParamsForRun resolves login params for the orchestration stage:
// currently only the CLI triple (url,user,pass) is honored, malformed or
// missing values yield login_params; the config/gateway fallbacks come from
// ParseLoginParams in session.go (the login-params task).
func resolveLoginParamsForRun(cfg *config.Config, cliLogin string) (*loginParams, *UpdateError) {
	raw := strings.TrimSpace(cliLogin)
	if raw == "" {
		return nil, &UpdateError{Kind: ErrKindLoginParams, Msg: "ikuai-url is empty in config file"}
	}
	parts := strings.Split(raw, ",")
	if len(parts) != 3 {
		return nil, &UpdateError{Kind: ErrKindLoginParams, Msg: "command line parameter format error"}
	}
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			return nil, &UpdateError{Kind: ErrKindLoginParams, Msg: "command line parameter format error"}
		}
	}
	return &loginParams{
		BaseURL:  strings.TrimSpace(parts[0]),
		Username: strings.TrimSpace(parts[1]),
		Password: strings.TrimSpace(parts[2]),
	}, nil
}

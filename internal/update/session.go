// session.go 登录参数解析，行为对齐 rust_archive/crates/core/src/session.rs L30-78：
// CLI `url,user,pass` > 配置 ikuai-url/username/password > router.go 网关猜测。
// Login-parameter resolution aligned with session.rs L30-78: the CLI triple
// `url,user,pass` wins over the config values, which win over the gateway guess.
package update

import (
	"strings"

	"github.com/FelixJI/iKuai-Toolbox/internal/config"
)

// ParseLoginParams 三级优先解析登录参数（session.rs L30-78）：
//   - CLI 非空：恰好三段且各段 trim 后非空，否则 login_params 格式错误；
//   - 配置 ikuai-url trim 后非空：baseURL trim，用户名/密码原样；
//   - 网关猜测：仅 Linux 读取默认网关，成功则 http://<gw>，
//     失败（含非 Linux 平台）=> login_params "default gateway not found"。
//
// Rust 版在移动端（android/ios）直接拒绝网关猜测；Go 版无移动目标，
// 非 Linux 平台统一落入网关不可得分支。
// ParseLoginParams resolves login params with the three-tier priority
// (session.rs L30-78):
//   - a non-empty CLI login must be exactly three non-blank trimmed segments,
//     otherwise a login_params format error;
//   - a non-blank config ikuai-url trims the base URL and copies the credentials verbatim;
//   - gateway guess: Linux only; a hit yields http://<gw>, any failure
//     (including non-Linux platforms) yields the login_params gateway error.
//
// The Rust build rejects the gateway guess outright on mobile (android/ios);
// the Go port has no mobile target, so non-Linux platforms simply land in the
// gateway-unavailable branch.
func ParseLoginParams(cliLogin string, cfg *config.Config) (baseURL, username, password string, err *UpdateError) {
	raw := strings.TrimSpace(cliLogin)
	if raw != "" {
		parts := strings.Split(raw, ",")
		if len(parts) != 3 {
			return "", "", "", &UpdateError{Kind: ErrKindLoginParams,
				Msg: "command line parameter format error"}
		}
		base, user, pass := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])
		if base == "" || user == "" || pass == "" {
			return "", "", "", &UpdateError{Kind: ErrKindLoginParams,
				Msg: "command line parameter format error"}
		}
		return base, user, pass, nil
	}

	if strings.TrimSpace(cfg.IkuaiURL) != "" {
		return strings.TrimSpace(cfg.IkuaiURL), cfg.Username, cfg.Password, nil
	}

	gw, gwErr := GetGatewayV4()
	if gwErr != nil {
		return "", "", "", &UpdateError{Kind: ErrKindLoginParams,
			Msg: "default gateway not found"}
	}
	return "http://" + gw, cfg.Username, cfg.Password, nil
}

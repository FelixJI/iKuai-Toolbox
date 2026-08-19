// clean.go 清理流程编排，行为对齐 crates/core/src/app/clean.rs（71 行）：
// 顺序固定 custom_isp→stream_domain→ip_group→ipv6_group→stream_ipport，
// 任一步失败即停；错误文案保持英文（面向 API），对齐 CleanError 的 thiserror Display。
// Clean-flow orchestration aligned with app/clean.rs (71 lines): the fixed
// order is custom_isp→stream_domain→ip_group→ipv6_group→stream_ipport, first
// failure stops the pass; error strings stay English (API-facing), mirroring
// the thiserror Display of CleanError.
package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/FelixJI/iKuai-Toolbox/internal/config"
	"github.com/FelixJI/iKuai-Toolbox/internal/ikuai"
	"github.com/FelixJI/iKuai-Toolbox/internal/update"
)

// errCleanMissingTag 对齐 clean.rs L8 的 MissingTag Display。
// errCleanMissingTag mirrors the MissingTag Display of clean.rs L8.
var errCleanMissingTag = errors.New("Clean mode requires clean_tag")

// CleanError 清理错误：Step 为空表示缺失 tag / 登录参数错误（直接透出来源文案），
// 非空表示某一步骤失败，Display 为 "clean step {step} failed: {source}"
// （对齐 clean.rs L5-17 的枚举）。
// CleanError is a clean failure: an empty Step means a missing tag or a
// login-params error (the source text passes through verbatim), a non-empty
// Step renders as "clean step {step} failed: {source}" (the enum of clean.rs L5-17).
type CleanError struct {
	Step   string
	Source error
}

// Error 对齐 Rust CleanError 的 Display 前缀。
// Error mirrors the Display prefixes of the Rust CleanError.
func (e *CleanError) Error() string {
	if e.Step == "" {
		return e.Source.Error()
	}
	return fmt.Sprintf("clean step %s failed: %s", e.Step, e.Source)
}

// RunClean 登录爱快后按固定顺序删除受管规则（clean.rs L19-71）。
// 空 clean_tag 拒绝执行；登录参数沿用 CLI > 配置 > 网关猜测的三级优先。
// RunClean logs into iKuai and deletes managed rules in the fixed order
// (clean.rs L19-71). A blank clean_tag is rejected up front; login params
// follow the CLI > config > gateway-guess priority.
func RunClean(cfg *config.Config, cliLogin, cleanTag string) error {
	tag := strings.TrimSpace(cleanTag)
	if tag == "" {
		return &CleanError{Source: errCleanMissingTag}
	}

	baseURL, username, password, err := update.ParseLoginParams(cliLogin, cfg)
	if err != nil {
		// ParseLoginParams 返回具体类型 *UpdateError，此处仅在非 nil 时包装，
		// 避免 typed-nil 直接转入 error 接口。
		// ParseLoginParams returns the concrete *UpdateError; wrap only when
		// non-nil so a typed nil never leaks into the error interface.
		return &CleanError{Source: err}
	}

	api, clientErr := ikuai.NewIKuaiClient(baseURL)
	if clientErr != nil {
		return &CleanError{Step: "init_client", Source: clientErr}
	}
	if loginErr := api.Login(username, password); loginErr != nil {
		return &CleanError{Step: "login", Source: loginErr}
	}

	steps := []struct {
		name string
		run  func(*ikuai.IKuaiClient, string) error
	}{
		{"custom_isp", ikuai.DelCustomIspAll},
		{"stream_domain", ikuai.DelStreamDomainAll},
		{"ip_group", ikuai.DelIkuaiBypassIpGroup},
		{"ipv6_group", ikuai.DelIkuaiBypassIpv6Group},
		{"stream_ipport", ikuai.DelIkuaiBypassStreamIpPort},
	}
	for _, step := range steps {
		if stepErr := step.run(api, tag); stepErr != nil {
			return &CleanError{Step: step.name, Source: stepErr}
		}
	}
	return nil
}

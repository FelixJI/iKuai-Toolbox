// diagnostics.go 登录/代理连通性测试与诊断报告，行为对齐 crates/core/src/app/diagnostics.rs（491 行）。
// 密码只以 (set,len=N) 形式出现在报告中，绝不回传明文；cron 归一化后给出下次触发时刻；
// 规则 URL 以 Range bytes=0-2047 抽样探测。
// Login/proxy connectivity probes and the diagnostics report, aligned with
// crates/core/src/app/diagnostics.rs (491 lines). Passwords only ever appear as
// (set,len=N); the cron expression is normalized with its next fire time; rule
// URLs are sampled with a Range bytes=0-2047 probe.
package app

import (
	"encoding/json"
	"fmt"
)

// TestResult 连通性测试结果，JSON 键 ok/message 为前端契约（diagnostics.rs L12-16）。
// TestResult is a connectivity probe result; the ok/message JSON keys are a
// frontend contract (diagnostics.rs L12-16).
type TestResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// TestIkuaiLoginRequest 登录测试入参；JSON 同时接受 baseUrl 与 base_url
// （前端 Tauri 走 camelCase、HTTP 走 snake_case，diagnostics.rs L18-24 的 serde alias）。
// TestIkuaiLoginRequest is the login-probe input; JSON accepts both baseUrl and
// base_url (the Tauri frontend sends camelCase while HTTP sends snake_case,
// mirroring the serde alias of diagnostics.rs L18-24).
type TestIkuaiLoginRequest struct {
	BaseURL  string
	Username string
	Password string
}

// UnmarshalJSON 双命名直解：base_url 与 baseUrl 指向同一字段，二者同时出现视为
// 重复字段，任一必填字段缺失均报错（对齐 serde alias + 无默认值 String 的拒绝语义）。
// UnmarshalJSON decodes both spellings directly: base_url and baseUrl target the
// same field, both present counts as a duplicate, and any missing required
// field errors out (mirroring serde aliases plus the no-default String rejection).
func (r *TestIkuaiLoginRequest) UnmarshalJSON(data []byte) error {
	var wire struct {
		BaseURLSnake *string `json:"base_url"`
		BaseURLCamel *string `json:"baseUrl"`
		Username     *string `json:"username"`
		Password     *string `json:"password"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.BaseURLSnake != nil && wire.BaseURLCamel != nil {
		return fmt.Errorf("duplicate field base_url")
	}
	if wire.BaseURLSnake == nil && wire.BaseURLCamel == nil {
		return fmt.Errorf("missing field base_url")
	}
	if wire.Username == nil {
		return fmt.Errorf("missing field username")
	}
	if wire.Password == nil {
		return fmt.Errorf("missing field password")
	}
	if wire.BaseURLSnake != nil {
		r.BaseURL = *wire.BaseURLSnake
	} else {
		r.BaseURL = *wire.BaseURLCamel
	}
	r.Username = *wire.Username
	r.Password = *wire.Password
	return nil
}

// TestGithubProxyRequest ghproxy 连通性测试入参；接受 githubProxy 与 github_proxy 两种命名。
// TestGithubProxyRequest is the ghproxy probe input; both githubProxy and
// github_proxy spellings are accepted.
type TestGithubProxyRequest struct {
	GithubProxy string
}

// UnmarshalJSON 双命名直解，语义与 TestIkuaiLoginRequest 一致（diagnostics.rs L26-30）。
// UnmarshalJSON decodes both spellings directly with the same semantics as
// TestIkuaiLoginRequest (diagnostics.rs L26-30).
func (r *TestGithubProxyRequest) UnmarshalJSON(data []byte) error {
	var wire struct {
		Snake *string `json:"github_proxy"`
		Camel *string `json:"githubProxy"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.Snake != nil && wire.Camel != nil {
		return fmt.Errorf("duplicate field github_proxy")
	}
	if wire.Snake == nil && wire.Camel == nil {
		return fmt.Errorf("missing field github_proxy")
	}
	if wire.Snake != nil {
		r.GithubProxy = *wire.Snake
	} else {
		r.GithubProxy = *wire.Camel
	}
	return nil
}

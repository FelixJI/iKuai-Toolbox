// duration.go 提供 humantime 双格式兼容的 Duration 类型
// duration.go provides the Duration type with humantime dual-format compatibility.
//
// 行为规格对齐 rust_archive/crates/core/src/config.rs duration_compat（L12-74）：
// 反序列化同时接受纳秒整数（visit_u64/visit_i64）与 humantime 字符串（visit_str），
// 序列化统一输出 humantime 风格字符串。
// Behavioral spec mirrors rust_archive/crates/core/src/config.rs duration_compat (L12-74):
// deserialization accepts both nanosecond integers (visit_u64/visit_i64)
// and humantime strings (visit_str); serialization emits a humantime-style string.
package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration 是 time.Duration 的 YAML/JSON 兼容封装
// Duration wraps time.Duration for YAML/JSON dual-format compatibility.
type Duration time.Duration

// UnmarshalYAML 先尝试按纳秒整数解码，再回退到 time.ParseDuration 字符串
// 这与 serde 的 deserialize_any 派发行为一致：YAML 整数 → 纳秒，YAML 字符串 → 时长。
// UnmarshalYAML first tries nanosecond integers, then falls back to time.ParseDuration strings.
// This mirrors serde's deserialize_any dispatch: YAML integers map to nanoseconds,
// YAML strings map to durations.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	// 先试整数（纳秒） / try integer (nanoseconds) first.
	var nanos int64
	if err := value.Decode(&nanos); err == nil {
		if nanos < 0 {
			// 对齐 Rust visit_i64：负纳秒直接报错 / match Rust visit_i64: reject negatives.
			return fmt.Errorf("duration nanoseconds must be >= 0")
		}
		*d = Duration(time.Duration(nanos))
		return nil
	}

	// 再试 humantime 风格字符串 / then try a humantime-style string.
	var s string
	if err := value.Decode(&s); err != nil {
		return fmt.Errorf("duration as string or nanoseconds: %w", err)
	}
	parsed, err := parseDurationString(s)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

// MarshalYAML 输出 humantime 风格字符串（如 "10s"、"1m30s"）
// 对齐 Rust duration_compat::serialize 使用 humantime::format_duration。
// MarshalYAML emits a humantime-style string (e.g. "10s", "1m30s"),
// matching Rust duration_compat::serialize via humantime::format_duration.
func (d Duration) MarshalYAML() (interface{}, error) {
	return (time.Duration)(d).String(), nil
}

// UnmarshalJSON JSON 侧同样接受字符串或纳秒整数（前端两种形式都可能提交）
// UnmarshalJSON accepts a string or nanosecond integer on the JSON side too
// (the frontend may submit either form).
func (d *Duration) UnmarshalJSON(data []byte) error {
	var nanos int64
	if err := json.Unmarshal(data, &nanos); err == nil {
		if nanos < 0 {
			return fmt.Errorf("duration nanoseconds must be >= 0")
		}
		*d = Duration(time.Duration(nanos))
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("duration as string or nanoseconds: %w", err)
	}
	parsed, err := parseDurationString(s)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

// MarshalJSON 输出字符串形式 / MarshalJSON emits the string form.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal((time.Duration)(d).String())
}

// parseDurationString 解析时长字符串；负时长被拒绝（humantime 不支持负值）
// parseDurationString parses a duration string; negative durations are rejected
// because humantime has no negative support.
func parseDurationString(s string) (time.Duration, error) {
	parsed, err := time.ParseDuration(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		return 0, fmt.Errorf("failed to parse duration %q: %w", s, err)
	}
	if parsed < 0 {
		return 0, fmt.Errorf("duration %q must not be negative", s)
	}
	return parsed, nil
}

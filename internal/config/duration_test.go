// Duration 双格式兼容测试 / Duration dual-format compatibility tests
package config

import (
	"encoding/json"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// yamlUnmarshalStr 测试助手：把字符串当作 YAML 文档反序列化
// Test helper: unmarshal the given string as a YAML document.
func yamlUnmarshalStr(s string, v interface{}) error {
	return yaml.Unmarshal([]byte(s), v)
}

// TestDurationDualFormat 验证 Duration 同时接受 humantime 字符串与纳秒整数两种输入
// 对应 crates/core/src/config.rs duration_compat L12-74 的 deserialize 行为。
func TestDurationDualFormat(t *testing.T) {
	cases := []struct {
		in      string
		wantSec int
	}{
		{"30s", 30}, {"1m30s", 90}, {"5000000000", 5}, {"0", 0},
	}
	for _, c := range cases {
		var d Duration
		if err := yamlUnmarshalStr(c.in, &d); err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if got := int(time.Duration(d) / time.Second); got != c.wantSec {
			t.Errorf("%s => %ds, want %ds", c.in, got, c.wantSec)
		}
	}
}

// TestDurationMarshalRoundTrip 序列化应输出 humantime 风格字符串并可无损读回
// Serialization must emit a humantime-style string that round-trips losslessly.
func TestDurationMarshalRoundTrip(t *testing.T) {
	cases := []struct {
		d    Duration
		want string
	}{
		{Duration(10 * time.Second), "10s"},
		{Duration(90 * time.Second), "1m30s"},
		{Duration(0), "0s"},
	}
	for _, c := range cases {
		got, err := yaml.Marshal(c.d)
		if err != nil {
			t.Fatalf("marshal %v: %v", c.d, err)
		}
		// yaml.Marshal 输出带换行 / yaml.Marshal appends a trailing newline.
		if string(got) != c.want+"\n" {
			t.Errorf("marshal %v => %q, want %q", c.d, string(got), c.want+"\n")
		}
		var back Duration
		if err := yamlUnmarshalStr(string(got), &back); err != nil {
			t.Fatalf("unmarshal %q: %v", string(got), err)
		}
		if back != c.d {
			t.Errorf("round trip %v => %v", c.d, back)
		}
	}
}

// TestDurationRejectsNegativeNanos 负纳秒整数必须被拒绝（对齐 Rust visit_i64 行为）
// Negative nanosecond integers must be rejected (matches Rust visit_i64).
func TestDurationRejectsNegativeNanos(t *testing.T) {
	var d Duration
	if err := yamlUnmarshalStr("-5", &d); err == nil {
		t.Errorf("-5 should be rejected, got %v", d)
	}
}

// TestDurationJSONDualFormat JSON 侧同样支持字符串与纳秒整数双格式
// The JSON side also accepts both string and nanosecond integer forms.
func TestDurationJSONDualFormat(t *testing.T) {
	cases := []struct {
		in      string
		wantSec int
	}{
		{`"30s"`, 30}, {`5000000000`, 5}, {`0`, 0},
	}
	for _, c := range cases {
		var d Duration
		if err := json.Unmarshal([]byte(c.in), &d); err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if got := int(time.Duration(d) / time.Second); got != c.wantSec {
			t.Errorf("%s => %ds, want %ds", c.in, got, c.wantSec)
		}
	}

	// 序列化输出字符串形式 / marshal emits the string form.
	out, err := json.Marshal(Duration(90 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `"1m30s"` {
		t.Errorf("json marshal => %s, want %q", out, `"1m30s"`)
	}
}

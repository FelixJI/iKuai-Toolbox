// utils_test.go 工具函数测试，行为对齐 rust_archive/crates/core/src/ikuai/utils.rs。
// Utility function tests aligned with rust_archive/crates/core/src/ikuai/utils.rs.
package ikuai

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestMD5Hex 对齐 utils.rs L3-6：小写十六进制 md5（锚点为公开已知值，独立于实现）。
func TestMD5Hex(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "d41d8cd98f00b204e9800998ecf8427e"},
		{"abc", "900150983cd24fb0d6963f7d28e17f72"},
		{"pass", "1a1dc91c907325c69271ddf0c944bc72"},
	}
	for _, tc := range cases {
		if got := MD5Hex(tc.in); got != tc.want {
			t.Errorf("MD5Hex(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestToStringList 对齐 utils.rs L8-32：null/数组/单值三分派，
// 对象按 gp_name→name→ip→ipv6 顺序取首个字符串字段，否则回退紧凑 JSON 去引号。
func TestToStringList(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"null", `null`, []string{}},
		{"single string", `"abc"`, []string{"abc"}},
		{"string array", `["a","b"]`, []string{"a", "b"}},
		// utils.rs L23 键优先级：gp_name 先于 name/ip/ipv6。
		{"gp_name wins", `[{"gp_name":"g","name":"n","ip":"1.1.1.1"}]`, []string{"g"}},
		{"name fallback", `[{"name":"n","ip":"1.1.1.1"}]`, []string{"n"}},
		{"ip before ipv6", `[{"ip":"1.2.3.4","ipv6":"::1"}]`, []string{"1.2.3.4"}},
		{"ipv6 only", `[{"ipv6":"::1"}]`, []string{"::1"}},
		// utils.rs L24-29：非字符串 gp_name 被跳过，整个对象回退紧凑 JSON 并去引号。
		{"non-string gp_name skipped", `[{"gp_name":42}]`, []string{`{"gp_name":42}`}},
		{"empty object", `[{}]`, []string{`{}`}},
		{"scalar array", `[42,true,null]`, []string{"42", "true", "null"}},
		{"bare number", `42`, []string{"42"}},
	}
	for _, tc := range cases {
		got := ToStringList(json.RawMessage(tc.in))
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: ToStringList(%s) = %#v, want %#v", tc.name, tc.in, got, tc.want)
		}
	}
	if got := ToStringList(nil); len(got) != 0 {
		t.Errorf("ToStringList(nil) = %#v, want empty", got)
	}
}

// TestCategorizeAddrs 对齐 utils.rs L34-51：IKB 前缀或无点/冒/横线 => 对象名，其余 => 自定义地址；
// 空白跳过、条目 trim 后归档，保持输入顺序。
func TestCategorizeAddrs(t *testing.T) {
	in := []string{
		"1.2.3.4",
		"IKBfoo",
		"::1",
		"10.0.0.1-10.0.0.9",
		"plain",
		"  8.8.8.8  ",
		"",
		"   ",
	}
	custom, objects := CategorizeAddrs(in)
	if want := []string{"1.2.3.4", "::1", "10.0.0.1-10.0.0.9", "8.8.8.8"}; !reflect.DeepEqual(custom, want) {
		t.Errorf("custom = %#v, want %#v", custom, want)
	}
	if want := []string{"IKBfoo", "plain"}; !reflect.DeepEqual(objects, want) {
		t.Errorf("objects = %#v, want %#v", objects, want)
	}

	c2, o2 := CategorizeAddrs(nil)
	if len(c2) != 0 || len(o2) != 0 {
		t.Errorf("nil input => %#v / %#v, want both empty", c2, o2)
	}
}

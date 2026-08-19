// cron_norm_test.go cron 表达式归一化测试（TDD 先行）。
// cron expression normalization tests (TDD first).
package runtime

import "testing"

// TestNormalizeCron 字段数归一化（runtime.rs L425-450 的裁决版映射）：
// 5 段补秒前缀、6 段原样、7 段去年份；@ 前缀描述符原样放行。
// TestNormalizeCron covers the adjudicated field-count mapping (runtime.rs
// L425-450): 5 fields gain a second prefix, 6 stay as-is, 7 drop the year;
// "@" descriptors pass through unchanged.
func TestNormalizeCron(t *testing.T) {
	cases := []struct{ in, want string }{
		{"5 * * * *", "0 5 * * * *"},     // 5 段 → 补秒
		{"0 5 * * * *", "0 5 * * * *"},   // 6 段原样
		{"0 5 * * * * *", "0 5 * * * *"}, // 7 段去年份（需求用例 want 少一个 "*"，按裁决规则修正）
		{"*/15 * * * *", "0 */15 * * * *"},
		{"0 */2 * * * *", "0 */2 * * * *"},
		{"  5 * * * *  ", "0 5 * * * *"}, // 前后空白先 trim
		{"@daily", "@daily"},
		{"@every 1h", "@every 1h"},
	}
	for _, tc := range cases {
		got, err := NormalizeCronExpr(tc.in)
		if err != nil {
			t.Errorf("NormalizeCronExpr(%q) error = %v, want nil", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("NormalizeCronExpr(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestNormalizeCronErrors 空表达式、字段数不足与非法字段值都必须报错。
// TestNormalizeCronErrors: empty input, too few fields and invalid field
// values must all fail.
func TestNormalizeCronErrors(t *testing.T) {
	for _, in := range []string{
		"",
		"   ",
		"* * * *",
		"99 * * * *",  // 分钟越界
		"0 5 * * * x", // 星期非法
		"0 5 * *",     // 4 段
	} {
		if got, err := NormalizeCronExpr(in); err == nil {
			t.Errorf("NormalizeCronExpr(%q) = %q, want error", in, got)
		}
	}
}

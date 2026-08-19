// cron_norm.go cron 表达式归一化，行为对齐 crates/core/src/runtime.rs
// L425-450 的 normalize_cron_expr_for_parser（按既定裁决的字段数映射改写为
// robfig/cron 6 段含秒解析器的确定性规则）。
// Cron expression normalization aligned with normalize_cron_expr_for_parser of
// crates/core/src/runtime.rs L425-450, rewritten as deterministic field-count
// rules for the 6-field second-capable robfig/cron parser.
package runtime

import (
	"errors"
	"strings"

	"github.com/robfig/cron/v3"
)

// cronParser 6 段含秒 + 描述符的解析器（既定裁决），等价 Rust cron crate 的
// sec/min/hour/dom/mon/dow[,year] 语法去掉年份段。
// cronParser is the 6-field second-capable parser with descriptors (adjudicated),
// equivalent to the Rust cron crate's sec/min/hour/dom/mon/dow[,year] syntax
// minus the year field.
var cronParser = cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// NormalizeCronExpr 把常见 crontab 写法归一为 6 段含秒表达式：
//   - 5 段（分 时 日 月 周）→ 前缀补 "0 "；
//   - 6 段 → 原样；
//   - 7 段 → 去掉末尾年份段；
//   - 4 段及以下 → 错误；
//   - "@xxx" 描述符（@daily / @every 1h 等，Rust cron crate shorthand 同源）
//     原样放行，由解析器校验。
//
// 返回值保证能被 cronParser 解析，否则返回 Invalid cron expression 错误。
// NormalizeCronExpr normalizes common crontab spellings into a 6-field
// second-capable expression:
//   - 5 fields (min hour dom mon dow) gain a "0 " second prefix;
//   - 6 fields stay as-is;
//   - 7 fields drop the trailing year field;
//   - 4 or fewer fields fail;
//   - "@" descriptors (@daily / @every 1h, the same shorthands the Rust cron
//     crate accepts) pass through for the parser to validate.
//
// The result is guaranteed parseable by cronParser; anything else yields an
// "Invalid cron expression" error.
func NormalizeCronExpr(expr string) (string, error) {
	raw := strings.TrimSpace(expr)
	if raw == "" {
		return "", errors.New("cron expression is empty")
	}

	var normalized string
	if strings.HasPrefix(raw, "@") {
		normalized = raw
	} else {
		parts := strings.Fields(raw)
		switch len(parts) {
		case 5:
			normalized = "0 " + raw
		case 6:
			normalized = raw
		case 7:
			normalized = strings.Join(parts[:6], " ")
		default:
			return "", errors.New("Invalid cron expression")
		}
	}

	if _, err := cronParser.Parse(normalized); err != nil {
		return "", errors.New("Invalid cron expression")
	}
	return normalized, nil
}

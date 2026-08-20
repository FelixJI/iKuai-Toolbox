// logger_test.go 日志记录器与 ANSI 渲染测试。
// Logger and ANSI rendering tests.
package logger

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestLogLevelJSON 四个级别的 JSON 序列化值必须首字母大写，与 Rust 版一致
// （前端 level 判断依赖）。
// TestLogLevelJSON: the four levels must serialize capitalized exactly like the
// Rust version (frontend level checks depend on it).
func TestLogLevelJSON(t *testing.T) {
	cases := map[LogLevel]string{
		LevelInfo:    "Info",
		LevelSuccess: "Success",
		LevelWarn:    "Warn",
		LevelError:   "Error",
	}
	for level, want := range cases {
		b, err := json.Marshal(LogRecord{Level: level})
		if err != nil {
			t.Fatalf("marshal level %v: %v", level, err)
		}
		if !strings.Contains(string(b), `"level":"`+want+`"`) {
			t.Errorf("level %v json = %s, want %q", level, b, want)
		}
	}
}

// TestLogRecordJSONShape 五字段 JSON 键名逐一断言。
// TestLogRecordJSONShape asserts all five JSON keys verbatim.
func TestLogRecordJSONShape(t *testing.T) {
	rec := LogRecord{Ts: "2026/08/19 12:00:00", Module: "SYS:系统组件", Tag: "TASK:任务启动",
		Level: LevelSuccess, Detail: "OK"}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"ts":"2026/08/19 12:00:00","module":"SYS:系统组件","tag":"TASK:任务启动","level":"Success","detail":"OK"}`
	if string(b) != want {
		t.Errorf("json = %s, want %s", b, want)
	}
}

// TestLoggerEmits NewLogger 填充 ts/module 并透传 tag/level/detail。
// TestLoggerEmits: NewLogger fills ts/module and passes tag/level/detail through.
func TestLoggerEmits(t *testing.T) {
	var got []LogRecord
	l := NewLogger("AUTH:登录认证", func(rec LogRecord) { got = append(got, rec) })
	l.Info("LOGIN:开始登录", "Logging in to iKuai: http://x")
	l.Success("LOGIN:登录成功", "Login succeeded")
	l.Warn("PROXY:代理降级", "fallback")
	l.Error("LOGIN:登录失败", "denied")
	if len(got) != 4 {
		t.Fatalf("records = %d, want 4", len(got))
	}
	wantLevels := []LogLevel{LevelInfo, LevelSuccess, LevelWarn, LevelError}
	for i, rec := range got {
		if rec.Module != "AUTH:登录认证" || rec.Level != wantLevels[i] {
			t.Errorf("record %d = %+v, wrong module/level", i, rec)
		}
		if len(rec.Ts) != len("2026/08/19 12:00:00") || !strings.Contains(rec.Ts, "/") {
			t.Errorf("ts = %q, want local timestamp format", rec.Ts)
		}
	}
	if got[0].Tag != "LOGIN:开始登录" || got[0].Detail != "Logging in to iKuai: http://x" {
		t.Errorf("record 0 = %+v, wrong tag/detail", got[0])
	}
}

// TestRendererPlain 无色渲染输出 "ts [module] [tag] detail"。
// TestRendererPlain: colorless rendering emits "ts [module] [tag] detail".
func TestRendererPlain(t *testing.T) {
	r := NewRenderer(false)
	rec := LogRecord{Ts: "2026/08/19 12:00:00", Module: "IP:IP分组", Tag: "CLEAN:清理成功",
		Level: LevelSuccess, Detail: "tag: deleted 2 extra groups"}
	want := "2026/08/19 12:00:00 [IP:IP分组] [CLEAN:清理成功] tag: deleted 2 extra groups"
	if got := r.Render(rec); got != want {
		t.Errorf("plain render = %q, want %q", got, want)
	}
}

// TestRendererColor 彩色渲染逐字节对齐 logger.rs 的 ANSI 布局：
// 时间 90、模块 1;36、tag 按级别 34/32/33/1;31、detail 三段高亮。
// TestRendererColor pins the ANSI layout of logger.rs byte for byte:
// time 90, module 1;36, tag 34/32/33/1;31 by level, detail three-pass highlight.
func TestRendererColor(t *testing.T) {
	rec := LogRecord{Ts: "2026/08/19 12:00:00", Module: "IP:IP分组", Tag: "CLEAN:清理成功",
		Level: LevelSuccess, Detail: "tag: deleted 2 extra groups"}
	want := "\x1b[90m2026/08/19 12:00:00\x1b[0m " +
		"\x1b[1;36m[IP:IP分组]\x1b[0m " +
		"\x1b[32m[CLEAN:清理成功]\x1b[0m " +
		"tag: \x1b[1;93mdeleted\x1b[0m \x1b[95m2\x1b[0m extra groups"
	if got := NewRenderer(true).Render(rec); got != want {
		t.Errorf("color render =\n%q\nwant\n%q", got, want)
	}

	errRec := LogRecord{Ts: "t", Module: "m", Tag: "E", Level: LevelError, Detail: "boom"}
	if got := NewRenderer(true).Render(errRec); !strings.Contains(got, "\x1b[1;31m[E]\x1b[0m") {
		t.Errorf("error tag render = %q, want bold red tag", got)
	}
}

// TestHighlightPasses 高亮三段规则：单引号串 1;93、KV 值 1;93、裸数字 95。
// TestHighlightPasses: the three highlight rules — quoted strings 1;93,
// KV values 1;93, bare numbers 95.
func TestHighlightPasses(t *testing.T) {
	cases := []struct{ in, want string }{
		{"interface='wan1'", "interface=\x1b[1;93m'wan1'\x1b[0m"},
		{"status: ok", "status: \x1b[1;93mok\x1b[0m"},
		{"Error: gone", "Error: \x1b[1;93mgone\x1b[0m"},
		{"chunk 3 of 12", "chunk \x1b[95m3\x1b[0m of \x1b[95m12\x1b[0m"},
		{"plain text", "plain text"},
	}
	for _, tc := range cases {
		if got := highlight(tc.in); got != tc.want {
			t.Errorf("highlight(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

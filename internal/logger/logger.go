// logger.go 日志记录器与 ANSI 渲染，行为对齐 crates/core/src/logger.rs（151 行）：
// LogLevel 枚举值、LogRecord 五字段 JSON 形状、Logger（module + sink）、
// Renderer（无色纯文本 / 彩色 + 三段正则高亮）。
// 本文件是 LogLevel/LogRecord/LogSink 的规范定义处（Task 6 归一），
// internal/update 以类型别名引用同一形状。
// Logger and ANSI rendering aligned with crates/core/src/logger.rs (151 lines):
// the LogLevel enum values, the five-field LogRecord JSON shape, Logger
// (module + sink), and Renderer (plain text / colored with three-pass regex
// highlighting). This file is the canonical home of LogLevel/LogRecord/LogSink
// (normalized in Task 6); internal/update references the same shapes via
// type aliases.
package logger

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// LogLevel 日志级别，值与 JSON 序列化对齐 logger.rs L8-14（前端 level 判断依赖）。
// LogLevel mirrors logger.rs L8-14; the capitalized values are a frontend contract.
type LogLevel string

const (
	LevelInfo    LogLevel = "Info"
	LevelSuccess LogLevel = "Success"
	LevelWarn    LogLevel = "Warn"
	LevelError   LogLevel = "Error"
)

// LogRecord 五字段日志记录（ts/module/tag/level/detail），对齐 logger.rs L16-23。
// LogRecord is the five-field log record (ts/module/tag/level/detail), mirroring logger.rs L16-23.
type LogRecord struct {
	Ts     string   `json:"ts"`
	Module string   `json:"module"`
	Tag    string   `json:"tag"`
	Level  LogLevel `json:"level"`
	Detail string   `json:"detail"`
}

// LogSink 日志接收函数；必须非 nil（对齐 Rust Arc<dyn Fn(LogRecord)> 的必传约定）。
// LogSink receives every log record; it must be non-nil (matching the mandatory
// Arc<dyn Fn(LogRecord)> convention of the Rust version).
type LogSink func(rec LogRecord)

// Logger 模块级日志器（module + sink），对齐 logger.rs L27-67。
// Logger is the per-module logger (module + sink), mirroring logger.rs L27-67.
type Logger struct {
	module string
	sink   LogSink
}

// NewLogger 绑定模块名与 sink。
// NewLogger binds a module name to a sink.
func NewLogger(module string, sink LogSink) *Logger {
	return &Logger{module: module, sink: sink}
}

// Info 以 Info 级别投递一条记录。
// Info emits one Info record.
func (l *Logger) Info(tag, detail string) { l.emit(LevelInfo, tag, detail) }

// Success 以 Success 级别投递一条记录。
// Success emits one Success record.
func (l *Logger) Success(tag, detail string) { l.emit(LevelSuccess, tag, detail) }

// Warn 以 Warn 级别投递一条记录。
// Warn emits one Warn record.
func (l *Logger) Warn(tag, detail string) { l.emit(LevelWarn, tag, detail) }

// Error 以 Error 级别投递一条记录。
// Error emits one Error record.
func (l *Logger) Error(tag, detail string) { l.emit(LevelError, tag, detail) }

// emit 填充本地时间戳后投递，时间格式对齐 logger.rs L58（%Y/%m/%d %H:%M:%S）。
// emit stamps the local time and delivers the record; the format mirrors
// logger.rs L58 (%Y/%m/%d %H:%M:%S).
func (l *Logger) emit(level LogLevel, tag, detail string) {
	l.sink(LogRecord{
		Ts:     time.Now().Format("2006/01/02 15:04:05"),
		Module: l.module,
		Tag:    tag,
		Level:  level,
		Detail: detail,
	})
}

// Renderer 控制台渲染器，对齐 logger.rs L69-95。
// Renderer is the console renderer, mirroring logger.rs L69-95.
type Renderer struct {
	useColor bool
}

// NewRenderer 按是否启用颜色构造渲染器。
// NewRenderer builds a renderer with color on or off.
func NewRenderer(useColor bool) *Renderer {
	return &Renderer{useColor: useColor}
}

// Render 输出一行日志：无色 "ts [module] [tag] detail"；彩色为
// 时间(90) 模块(1;36) tag(级别色) detail(高亮)。
// Render emits one log line: plain "ts [module] [tag] detail", or colored
// time(90) module(1;36) tag(level color) detail(highlighted).
func (r *Renderer) Render(rec LogRecord) string {
	if !r.useColor {
		return fmt.Sprintf("%s [%s] [%s] %s", rec.Ts, rec.Module, rec.Tag, rec.Detail)
	}

	timeStr := style(90, false, rec.Ts)
	module := style(36, true, "["+rec.Module+"]")
	var tagColor int
	tagBold := false
	switch rec.Level {
	case LevelInfo:
		tagColor = 34
	case LevelSuccess:
		tagColor = 32
	case LevelWarn:
		tagColor = 33
	case LevelError:
		tagColor, tagBold = 31, true
	}
	tag := style(tagColor, tagBold, "["+rec.Tag+"]")
	detail := highlight(rec.Detail)
	return fmt.Sprintf("%s %s %s %s", timeStr, module, tag, detail)
}

// style 输出 ANSI 颜色包裹（bold 时 "1;color"），对齐 logger.rs L97-103。
// style wraps text in an ANSI sequence ("1;color" when bold), mirroring logger.rs L97-103.
func style(color int, bold bool, s string) string {
	if bold {
		return fmt.Sprintf("\x1b[1;%dm%s\x1b[0m", color, s)
	}
	return fmt.Sprintf("\x1b[%dm%s\x1b[0m", color, s)
}

// 三段高亮正则，对齐 logger.rs L105-109 的 Lazy 静态（regexp 并发安全，包级初始化一次）。
// The three highlight regexes mirror the Lazy statics of logger.rs L105-109
// (compiled once at package init; regexp is safe for concurrent use).
var (
	reQuoted  = regexp.MustCompile(`'([^']+)'`)
	reKV      = regexp.MustCompile(`(?i)(Prefix|Tag|IDs?|found|error|status|interface):\s*([^\s,)]+)`)
	reSafeNum = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]|\\b\\d+\\b")
)

// highlight 三段式高亮（logger.rs L111-151）：先单引号串，再 KV 值
// （已含转义序列的值保持原样），最后裸数字（转义序列整体放行）。
// highlight applies the three passes of logger.rs L111-151: quoted strings
// first, then KV values (already-escaped values stay untouched), and finally
// bare numbers (whole escape sequences pass through).
func highlight(s string) string {
	out := reQuoted.ReplaceAllStringFunc(s, func(m string) string {
		return style(93, true, m)
	})

	out = reKV.ReplaceAllStringFunc(out, func(m string) string {
		groups := reKV.FindStringSubmatch(m)
		if groups == nil {
			return m
		}
		key, val := groups[1], groups[2]
		if strings.Contains(val, "\x1b[") {
			return key + ": " + val
		}
		return key + ": " + style(93, true, val)
	})

	out = reSafeNum.ReplaceAllStringFunc(out, func(m string) string {
		if strings.HasPrefix(m, "\x1b") {
			return m
		}
		return style(95, false, m)
	})

	return out
}

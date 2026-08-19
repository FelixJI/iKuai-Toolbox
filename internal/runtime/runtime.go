// runtime.go RuntimeService（WebUI/CLI 共用），行为对齐 rust_archive/crates/core/src/runtime.rs
// L77-423：run-once 的 CAS 防重入、cron 的归一化启动与每秒轮询 next_run_at、
// 状态查询与停止语义。日志代理见 broker.go。
// RuntimeService shared by WebUI and CLI, aligned with rust_archive/crates/core/src/runtime.rs
// L77-423: the CAS reentrancy guard of run-once, cron startup with
// normalization plus the per-second next_run_at polling, status queries and
// stop semantics. The log broker lives in broker.go.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/FelixJI/iKuai-Toolbox/internal/config"
	"github.com/FelixJI/iKuai-Toolbox/internal/logger"
	"github.com/FelixJI/iKuai-Toolbox/internal/update"
)

// RuntimeStatus 运行时状态；JSON 字段名与 /api/runtime/status 完全一致
// （runtime.rs L15-23），前端契约不得改动。
// RuntimeStatus is the runtime snapshot; its JSON keys must stay verbatim
// compatible with /api/runtime/status (runtime.rs L15-23) — a frontend
// contract.
type RuntimeStatus struct {
	Running     bool   `json:"running"`
	CronRunning bool   `json:"cron_running"`
	CronExpr    string `json:"cron_expr"`
	Module      string `json:"module"`
	LastRunAt   string `json:"last_run_at"`
	NextRunAt   string `json:"next_run_at"`
}

// runHandle 标识一次 run-once 的取消句柄；runCur 的 CAS 用于区分
// "仍在跑的那次" 与 "已被 StopAll 作废的历史句柄"。
// runHandle identifies one run-once cancellation; runCur's compare-and-swap
// distinguishes "the still-current run" from "stale handles voided by StopAll".
type runHandle struct {
	cancel context.CancelFunc
}

// RuntimeService 见文件头注释；cfg 以原子指针共享（对齐 Rust 的
// Arc<Mutex<Arc<Config>>> 只读快照语义）。
// RuntimeService: see the file header; cfg is shared as an atomic pointer
// (matching the read-only snapshot semantics of Rust's Arc<Mutex<Arc<Config>>>).
type RuntimeService struct {
	cfg     atomic.Pointer[config.Config]
	running atomic.Bool
	runCur  atomic.Pointer[runHandle]

	mu         sync.Mutex
	module     string
	cronExpr   string
	lastRunAt  string
	nextRunAt  string
	cronCancel context.CancelFunc

	cliLogin   string
	updateOpts *update.UpdateOptions
	logs       *logBroker
}

// NewRuntimeService 构造运行时服务（runtime.rs L98-120）。
// NewRuntimeService builds the runtime service (runtime.rs L98-120).
func NewRuntimeService(cfg *config.Config, cliLogin, defaultCron, defaultModule string, opts *update.UpdateOptions) *RuntimeService {
	s := &RuntimeService{
		module:     defaultModule,
		cronExpr:   defaultCron,
		cliLogin:   cliLogin,
		updateOpts: opts,
		logs:       newLogBroker(brokerMaxLines),
	}
	s.cfg.Store(cfg)
	return s
}

// UpdateConfig 替换运行时持有的配置快照（WebUI save-raw 后刷新，对齐
// Rust 中 web 与 runtime 共享同一把配置锁、后续 run-once 即读新配置的语义）。
// UpdateConfig swaps the config snapshot held by the runtime (refreshed by
// the WebUI save-raw flow, mirroring the shared config lock of the Rust
// web/runtime pair so subsequent run-once passes read the new config).
func (s *RuntimeService) UpdateConfig(cfg *config.Config) {
	if cfg == nil {
		return
	}
	s.cfg.Store(cfg)
}

// SetDefaults 覆盖默认 module / cron 表达式；空白参数保持原值
// （runtime.rs L122-132 的 trim + 空过滤）。
// SetDefaults overrides the default module / cron expression; blank arguments
// keep the current values (the trim + empty filter of runtime.rs L122-132).
func (s *RuntimeService) SetDefaults(module, cronExpr string) {
	module = strings.TrimSpace(module)
	cronExpr = strings.TrimSpace(cronExpr)
	s.mu.Lock()
	defer s.mu.Unlock()
	if module != "" {
		s.module = module
	}
	if cronExpr != "" {
		s.cronExpr = cronExpr
	}
}

// TailLogs 返回最新 n 条日志。
// TailLogs returns the newest n log records.
func (s *RuntimeService) TailLogs(n int) []logger.LogRecord {
	return s.logs.tail(n)
}

// SubscribeLogs 订阅实时日志广播，返回 channel 与取消函数。
// SubscribeLogs subscribes to the live log broadcast, returning the channel
// and its cancel func.
func (s *RuntimeService) SubscribeLogs() (<-chan logger.LogRecord, func()) {
	return s.logs.subscribe()
}

// Status 返回状态快照（runtime.rs L142-162）。
// Status returns the status snapshot (runtime.rs L142-162).
func (s *RuntimeService) Status() RuntimeStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return RuntimeStatus{
		Running:     s.running.Load(),
		CronRunning: s.cronCancel != nil,
		CronExpr:    s.cronExpr,
		Module:      s.module,
		LastRunAt:   s.lastRunAt,
		NextRunAt:   s.nextRunAt,
	}
}

// StartRunOnce 后台启动一次更新；atomic.CompareAndSwap 防重入，已在跑时
// 返回 (false, nil)（runtime.rs L164-294）。空 module 回退默认值并写回。
// StartRunOnce launches one update pass in the background; an
// atomic.CompareAndSwap guards reentrancy so an in-flight call yields
// (false, nil) (runtime.rs L164-294). An empty module falls back to the
// default and is written back.
func (s *RuntimeService) StartRunOnce(module string) (bool, error) {
	module = strings.TrimSpace(module)
	if module == "" {
		s.mu.Lock()
		module = s.module
		s.mu.Unlock()
	}

	if !s.running.CompareAndSwap(false, true) {
		return false, nil
	}

	// 将本次成功启动的 module 写回默认值，便于状态展示与后续重复执行。
	// Persist the module as default only when we actually started.
	s.mu.Lock()
	s.module = module
	s.mu.Unlock()

	s.appendSys(logger.LevelInfo, "TASK:任务启动", fmt.Sprintf("module=%s", module))

	ctx, cancel := context.WithCancel(context.Background())
	// running 的 CAS 已保证同一时刻仅一个存活 run，此处 prev 必已清理；
	// 句柄同步捕获后传入 goroutine，收尾 CAS 只匹配自己那次 run。
	// The running CAS guarantees a single live run, so the previous handle
	// must already have been cleared here; the handle is captured
	// synchronously and passed in so the finalizing CAS matches this run only.
	h := &runHandle{cancel: cancel}
	s.runCur.Store(h)

	go s.runOnce(ctx, h, module)
	return true, nil
}

// runOnce 执行一次更新并收尾（runtime.rs L195-292）：拷贝配置快照、写计划
// 日志、串行跑 RunUpdateByModule；被 StopAll 取消后的收尾副作用（完成/
// 失败日志与 last_run_at）跳过，对齐 Rust abort 语义。
// runOnce executes one update and finalizes it (runtime.rs L195-292): copy the
// config snapshot, log the plan, run RunUpdateByModule serially; when
// cancelled by StopAll the finalization side effects (completion/failure logs
// and last_run_at) are skipped, matching the Rust abort semantics.
func (s *RuntimeService) runOnce(ctx context.Context, h *runHandle, module string) {
	defer h.cancel()

	cfg := s.cfg.Load()

	s.appendSys(logger.LevelInfo, "TASK:任务执行", fmt.Sprintf("module=%s", module))
	s.appendSys(logger.LevelInfo, "TASK:任务计划", modulePlan(cfg, module))

	// 广播走 broker 锁内的非阻塞发送，直接同步投递即可保序且永不阻塞。
	// Broadcasting is a non-blocking send under the broker lock, so a direct
	// synchronous call preserves order and never blocks.
	sink := func(rec logger.LogRecord) { s.logs.append(rec) }

	res := update.RunUpdateByModule(cfg, s.cliLogin, module, s.updateOpts, sink)

	if ctx.Err() == nil {
		if res == nil {
			s.appendSys(logger.LevelSuccess, "DONE:任务完成", fmt.Sprintf("module=%s", module))
		} else {
			s.appendSys(logger.LevelError, "TASK:任务失败", fmt.Sprintf("module=%s error=%s", module, res))
		}
		s.mu.Lock()
		s.lastRunAt = time.Now().Format(time.RFC3339)
		s.mu.Unlock()
	}

	// 仅当本次 run 仍是当前句柄时复位 running；StopAll 已接管则不动，
	// 避免作废的收尾清掉新一次 run 的运行标记。
	// Reset running only when this run is still the current handle; if StopAll
	// already took over, leave it be so a stale finish cannot clear the flag
	// of a newer run.
	if s.runCur.CompareAndSwap(h, nil) {
		s.running.Store(false)
	}
}

// modulePlan 按模块生成计划摘要（runtime.rs L207-241 的条目计数矩阵）。
// modulePlan renders the per-module plan summary (the entry-count matrix of
// runtime.rs L207-241).
func modulePlan(cfg *config.Config, module string) string {
	if cfg == nil {
		return fmt.Sprintf("module=%s", module)
	}
	switch module {
	case "ispdomain":
		return fmt.Sprintf("custom_isp=%d stream_domain=%d", len(cfg.CustomIsp), len(cfg.StreamDomain))
	case "ipgroup":
		return fmt.Sprintf("ip_group=%d stream_ipport=%d", len(cfg.IpGroup), len(cfg.StreamIpPort))
	case "ipv6group":
		return fmt.Sprintf("ipv6_group=%d", len(cfg.Ipv6Group))
	case "ii":
		return fmt.Sprintf("custom_isp=%d stream_domain=%d ip_group=%d stream_ipport=%d",
			len(cfg.CustomIsp), len(cfg.StreamDomain), len(cfg.IpGroup), len(cfg.StreamIpPort))
	case "ip":
		return fmt.Sprintf("ip_group=%d ipv6_group=%d", len(cfg.IpGroup), len(cfg.Ipv6Group))
	case "iip":
		return fmt.Sprintf("custom_isp=%d stream_domain=%d ip_group=%d ipv6_group=%d stream_ipport=%d",
			len(cfg.CustomIsp), len(cfg.StreamDomain), len(cfg.IpGroup), len(cfg.Ipv6Group), len(cfg.StreamIpPort))
	default:
		return fmt.Sprintf("module=%s", module)
	}
}

// StartCron 启动 cron 循环（runtime.rs L296-364）：表达式归一化 + 解析，
// 校验通过才写入状态；已在跑时返回 "cron is already running"。
// StartCron starts the cron loop (runtime.rs L296-364): the expression is
// normalized and parsed first, and only a valid one mutates state; an active
// cron yields "cron is already running".
func (s *RuntimeService) StartCron(expr, module string) error {
	expr = strings.TrimSpace(expr)
	module = strings.TrimSpace(module)
	if expr == "" {
		return errors.New("Cron expression is empty in config file")
	}
	schedExpr, err := NormalizeCronExpr(expr)
	if err != nil {
		return err
	}
	sched, perr := cronParser.Parse(schedExpr)
	if perr != nil {
		return perr
	}

	if module == "" {
		s.mu.Lock()
		module = s.module
		s.mu.Unlock()
	}

	s.mu.Lock()
	if s.cronCancel != nil {
		s.mu.Unlock()
		return errors.New("cron is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cronCancel = cancel
	s.cronExpr = expr
	s.module = module
	s.mu.Unlock()

	go s.cronLoop(ctx, sched, module)

	s.appendSys(logger.LevelInfo, "CRON:定时任务启动", fmt.Sprintf("cron=%s module=%s", expr, module))
	return nil
}

// cronLoop 每秒轮询的调度循环（runtime.rs L324-349）：预取下次触发点写
// next_run_at，等待期按秒分片并响应取消，到点触发 run-once。
// cronLoop is the per-second scheduling loop (runtime.rs L324-349): it
// prefetches the next fire time into next_run_at, waits in one-second slices
// honouring cancellation, then triggers run-once on time.
func (s *RuntimeService) cronLoop(ctx context.Context, sched cron.Schedule, module string) {
	for {
		next := sched.Next(time.Now())
		s.mu.Lock()
		s.nextRunAt = next.Format(time.RFC3339)
		s.mu.Unlock()

		for {
			now := time.Now()
			if !next.After(now) {
				break
			}
			wait := time.Until(next)
			if wait <= 0 {
				wait = 200 * time.Millisecond
			}
			if wait > time.Second {
				wait = time.Second
			}
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}

		select {
		case <-ctx.Done():
			return
		default:
		}

		_, _ = s.StartRunOnce(module)
	}
}

// StopCron 停止 cron 循环并清空 next_run_at（runtime.rs L366-387）。
// StopCron stops the cron loop and clears next_run_at (runtime.rs L366-387).
func (s *RuntimeService) StopCron() error {
	s.mu.Lock()
	cancel := s.cronCancel
	s.cronCancel = nil
	s.nextRunAt = ""
	expr, module := s.cronExpr, s.module
	s.mu.Unlock()

	if cancel != nil {
		cancel()
		s.appendSys(logger.LevelWarn, "CRON:定时任务停止", fmt.Sprintf("cron=%s module=%s", expr, module))
	}
	return nil
}

// StopAll 停止 run-once 与 cron（runtime.rs L389-411）：running 立即复位，
// 在跑的 run 通过取消句柄跳过收尾副作用。
// StopAll stops run-once and cron (runtime.rs L389-411): running resets
// immediately and the in-flight run skips its finalization side effects via
// the cancelled handle.
func (s *RuntimeService) StopAll() error {
	s.mu.Lock()
	cronCancel := s.cronCancel
	s.cronCancel = nil
	s.nextRunAt = ""
	s.mu.Unlock()

	if h := s.runCur.Swap(nil); h != nil {
		h.cancel()
	}
	if cronCancel != nil {
		cronCancel()
	}
	s.running.Store(false)

	s.appendSys(logger.LevelWarn, "TASK:任务停止", "runtime stop requested")
	return nil
}

// appendSys 追加一条 SYS 模块日志（runtime.rs L413-422），时间格式对齐
// logger.rs L58。
// appendSys appends one SYS-module record (runtime.rs L413-422); the
// timestamp format mirrors logger.rs L58.
func (s *RuntimeService) appendSys(level logger.LogLevel, tag, detail string) {
	s.logs.append(logger.LogRecord{
		Ts:     time.Now().Format("2006/01/02 15:04:05"),
		Module: "SYS:系统组件",
		Tag:    tag,
		Level:  level,
		Detail: detail,
	})
}

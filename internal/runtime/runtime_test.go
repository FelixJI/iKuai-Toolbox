// runtime_test.go RuntimeService 与日志代理测试（TDD 先行），
// 行为规格对齐 crates/core/src/runtime.rs（450 行）。
// RuntimeService and log-broker tests (TDD first), aligned with the
// behavioral spec of crates/core/src/runtime.rs (450 lines).
package runtime

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FelixJI/iKuai-Toolbox/internal/config"
	"github.com/FelixJI/iKuai-Toolbox/internal/logger"
)

// newTestCfg 登录走 127.0.0.1:1，避免 Linux 网关猜测拖慢测试。
// newTestCfg points logins at 127.0.0.1:1 so the Linux gateway guess
// never slows tests down.
func newTestCfg() *config.Config {
	return &config.Config{IkuaiURL: "http://127.0.0.1:1", Username: "admin", Password: "secret"}
}

// gatedLoginServer 登录请求阻塞到 release 关闭，随后返回 code!=0 的确定性失败。
// gatedLoginServer blocks login requests until release closes, then answers a
// deterministic code!=0 failure.
func gatedLoginServer(t *testing.T) (*httptest.Server, chan struct{}) {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"code":-1,"message":"blocked"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, release
}

// waitFor 轮询直到 cond 为真或超时。
// waitFor polls until cond turns true or the timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}

// hasLog 判断 TailLogs(500) 中存在指定 tag。
// hasLog reports a record with the tag within TailLogs(500).
func hasLog(svc *RuntimeService, tag string) bool {
	for _, rec := range svc.TailLogs(500) {
		if rec.Tag == tag {
			return true
		}
	}
	return false
}

// drainNonBlocking 非阻塞读空 channel 并计数。
// drainNonBlocking counts everything currently readable without blocking.
func drainNonBlocking(ch <-chan logger.LogRecord) int {
	n := 0
	for {
		select {
		case <-ch:
			n++
		default:
			return n
		}
	}
}

// TestRunOnceReentrancy 第一次启动 true；未完成时第二次 false 且无错误；
// 完成后 running 复位、last_run_at 写入、可再次启动（runtime.rs L164-294）。
// TestRunOnceReentrancy: the first start returns true; an in-flight second
// start returns false without error; on completion running resets,
// last_run_at is written, and a new start is accepted (runtime.rs L164-294).
func TestRunOnceReentrancy(t *testing.T) {
	srv, release := gatedLoginServer(t)
	svc := NewRuntimeService(newTestCfg(), srv.URL+",admin,secret", "0 */5 * * * *", "ispdomain", nil)

	ok1, err := svc.StartRunOnce("")
	if err != nil || !ok1 {
		t.Fatalf("first start = %v, %v; want true, nil", ok1, err)
	}
	ok2, err := svc.StartRunOnce("ispdomain")
	if err != nil || ok2 {
		t.Fatalf("second start = %v, %v; want false, nil (reentrancy guard)", ok2, err)
	}
	if st := svc.Status(); !st.Running || st.Module != "ispdomain" {
		t.Fatalf("status while in flight = %+v, want running with module=ispdomain", st)
	}

	close(release)
	waitFor(t, 5*time.Second, func() bool { return !svc.Status().Running })

	st := svc.Status()
	if st.LastRunAt == "" {
		t.Fatal("last_run_at empty after completed run")
	}
	if _, perr := time.Parse(time.RFC3339, st.LastRunAt); perr != nil {
		t.Errorf("last_run_at = %q not RFC3339: %v", st.LastRunAt, perr)
	}
	for _, tag := range []string{"TASK:任务启动", "TASK:任务执行", "TASK:任务计划", "TASK:任务失败"} {
		if !hasLog(svc, tag) {
			t.Errorf("missing log tag %q after failed run", tag)
		}
	}

	ok3, err := svc.StartRunOnce("ipgroup")
	if err != nil || !ok3 {
		t.Fatalf("restart after completion = %v, %v; want true, nil", ok3, err)
	}
	waitFor(t, 5*time.Second, func() bool { return !svc.Status().Running })
	if st := svc.Status(); st.Module != "ipgroup" {
		t.Errorf("module = %q, want ipgroup written back", st.Module)
	}
}

// TestStatusFields JSON 字段名逐一断言（running/cron_running/cron_expr/module/
// last_run_at/next_run_at），且 StartCron/StopCron 驱动 cron_running 与
// next_run_at（runtime.rs L15-23、L142-162）。
// TestStatusFields asserts every JSON key verbatim, and that StartCron /
// StopCron drive cron_running and next_run_at (runtime.rs L15-23, L142-162).
func TestStatusFields(t *testing.T) {
	svc := NewRuntimeService(newTestCfg(), "", "0 */2 * * * *", "ipgroup", nil)

	st := svc.Status()
	if st.Running || st.CronRunning {
		t.Fatalf("fresh status = %+v, want all flags false", st)
	}
	if st.CronExpr != "0 */2 * * * *" || st.Module != "ipgroup" {
		t.Fatalf("defaults = %q / %q", st.CronExpr, st.Module)
	}

	b, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if uerr := json.Unmarshal(b, &m); uerr != nil {
		t.Fatalf("unmarshal: %v", uerr)
	}
	wantKeys := []string{"running", "cron_running", "cron_expr", "module", "last_run_at", "next_run_at"}
	if len(m) != len(wantKeys) {
		t.Fatalf("json keys = %v (json %s), want exactly %v", m, b, wantKeys)
	}
	for _, k := range wantKeys {
		if _, ok := m[k]; !ok {
			t.Errorf("json missing key %q: %s", k, b)
		}
	}
	if m["module"] != "ipgroup" || m["cron_expr"] != "0 */2 * * * *" {
		t.Errorf("json values module/cron_expr = %v/%v", m["module"], m["cron_expr"])
	}

	if serr := svc.StartCron("*/2 * * * * *", ""); serr != nil {
		t.Fatalf("StartCron: %v", serr)
	}
	waitFor(t, 5*time.Second, func() bool { return svc.Status().NextRunAt != "" })
	if st := svc.Status(); !st.CronRunning {
		t.Error("cron_running = false while cron active")
	} else if _, perr := time.Parse(time.RFC3339, st.NextRunAt); perr != nil {
		t.Errorf("next_run_at = %q not RFC3339: %v", st.NextRunAt, perr)
	}

	if serr := svc.StopCron(); serr != nil {
		t.Fatalf("StopCron: %v", serr)
	}
	waitFor(t, 2*time.Second, func() bool {
		st := svc.Status()
		return !st.CronRunning && st.NextRunAt == ""
	})
	if !hasLog(svc, "CRON:定时任务停止") {
		t.Error("missing CRON:定时任务停止 log after StopCron")
	}
}

// TestBrokerRing 写入 6000 条后缓冲封顶 5000，TailLogs(10) 取最新 10 条；
// n<=0 收敛为 1（runtime.rs L26-75）。
// TestBrokerRing: after 6000 appends the buffer caps at 5000 and
// TailLogs(10) returns the newest 10; n<=0 clamps to 1 (runtime.rs L26-75).
func TestBrokerRing(t *testing.T) {
	svc := NewRuntimeService(newTestCfg(), "", "0 */5 * * * *", "ispdomain", nil)
	for i := 0; i < 6000; i++ {
		svc.appendSys(logger.LevelInfo, "RING", fmt.Sprintf("line-%d", i))
	}
	if svc.logs.count != 5000 {
		t.Errorf("buffer size = %d, want cap 5000", svc.logs.count)
	}
	tail := svc.TailLogs(10)
	if len(tail) != 10 {
		t.Fatalf("TailLogs(10) len = %d, want 10", len(tail))
	}
	for i, rec := range tail {
		want := fmt.Sprintf("line-%d", 5990+i)
		if rec.Detail != want {
			t.Errorf("tail[%d].Detail = %q, want %q", i, rec.Detail, want)
		}
	}
	if all := svc.TailLogs(100000); len(all) != 5000 {
		t.Errorf("TailLogs(100000) len = %d, want 5000", len(all))
	}
	if one := svc.TailLogs(0); len(one) != 1 {
		t.Errorf("TailLogs(0) len = %d, want 1 (clamped)", len(one))
	}
}

// TestSubscribeBroadcast 订阅后写入按序到达；cancel 后不再收；
// 慢订阅者在 512 缓冲满后丢新不阻塞，环形缓冲不受影响（runtime.rs L26-75）。
// TestSubscribeBroadcast: appended records reach subscribers in order;
// cancelled subscribers receive nothing further; a slow subscriber drops new
// records once its 512-slot buffer fills without blocking anyone, and the
// ring buffer is unaffected (runtime.rs L26-75).
func TestSubscribeBroadcast(t *testing.T) {
	svc := NewRuntimeService(newTestCfg(), "", "0 */5 * * * *", "ispdomain", nil)

	ch, cancel := svc.SubscribeLogs()
	svc.appendSys(logger.LevelWarn, "B1", "first")
	svc.appendSys(logger.LevelWarn, "B2", "second")

	var got []string
	deadline := time.Now().Add(2 * time.Second)
	for len(got) < 2 && time.Now().Before(deadline) {
		select {
		case rec := <-ch:
			got = append(got, rec.Detail)
		case <-time.After(50 * time.Millisecond):
		}
	}
	if len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("received %v, want [first second] in order", got)
	}

	cancel()
	svc.appendSys(logger.LevelWarn, "B3", "after-cancel")
	select {
	case rec, ok := <-ch:
		t.Fatalf("received after cancel: %+v ok=%v", rec, ok)
	case <-time.After(150 * time.Millisecond):
	}

	ch2, cancel2 := svc.SubscribeLogs()
	for i := 0; i < 600; i++ {
		svc.appendSys(logger.LevelInfo, "FLOOD", fmt.Sprintf("f-%d", i))
	}
	if n := drainNonBlocking(ch2); n != 512 {
		t.Errorf("slow subscriber received %d, want 512 (drop-when-full)", n)
	}
	cancel2()
	if tail := svc.TailLogs(1); len(tail) != 1 || tail[0].Detail != "f-599" {
		t.Errorf("ring tail = %+v, want newest flood record", tail)
	}
}

// TestStartCronValidation 空表达式 / 非法表达式报错；重复启动报
// "cron is already running"；停止后可重启（runtime.rs L296-364）。
// TestStartCronValidation: empty and invalid expressions fail; a second
// start reports "cron is already running"; restart works after stop
// (runtime.rs L296-364).
func TestStartCronValidation(t *testing.T) {
	svc := NewRuntimeService(newTestCfg(), "", "0 */5 * * * *", "ispdomain", nil)

	if err := svc.StartCron("", "m"); err == nil || err.Error() != "Cron expression is empty in config file" {
		t.Errorf("empty expr err = %v, want 'Cron expression is empty in config file'", err)
	}
	if err := svc.StartCron("* * * *", "m"); err == nil {
		t.Error("4-field expr accepted, want error")
	}

	if err := svc.StartCron("*/2 * * * * *", ""); err != nil {
		t.Fatalf("StartCron: %v", err)
	}
	if err := svc.StartCron("*/3 * * * * *", ""); err == nil || err.Error() != "cron is already running" {
		t.Errorf("double start err = %v, want 'cron is already running'", err)
	}
	if st := svc.Status(); st.CronExpr != "*/2 * * * * *" {
		t.Errorf("cron_expr = %q, want the started expression", st.CronExpr)
	}

	if err := svc.StopCron(); err != nil {
		t.Fatalf("StopCron: %v", err)
	}
	if err := svc.StartCron("*/3 * * * * *", ""); err != nil {
		t.Errorf("restart after stop: %v", err)
	}
	if err := svc.StopCron(); err != nil {
		t.Fatalf("final StopCron: %v", err)
	}
}

// TestCronFiresRun 短周期表达式（*/2 秒）触发 run-once 并写 last_run_at
// （runtime.rs L324-349 的每秒轮询循环）。
// TestCronFiresRun: a short schedule (every 2 seconds) fires run-once and
// writes last_run_at (the per-second polling loop of runtime.rs L324-349).
func TestCronFiresRun(t *testing.T) {
	svc := NewRuntimeService(newTestCfg(), "", "0 */5 * * * *", "ispdomain", nil)
	if err := svc.StartCron("*/2 * * * * *", "ipgroup"); err != nil {
		t.Fatalf("StartCron: %v", err)
	}
	defer svc.StopCron()

	waitFor(t, 8*time.Second, func() bool { return svc.Status().LastRunAt != "" })
	st := svc.Status()
	if _, perr := time.Parse(time.RFC3339, st.LastRunAt); perr != nil {
		t.Errorf("last_run_at = %q not RFC3339: %v", st.LastRunAt, perr)
	}
	if !hasLog(svc, "CRON:定时任务启动") {
		t.Error("missing CRON:定时任务启动 log")
	}
}

// TestStopAll 同时停掉 run-once 与 cron；被中止的 run 不再写 last_run_at
// 并记录 TASK:任务停止（runtime.rs L389-411）。
// TestStopAll stops run-once and cron together; the aborted run writes no
// last_run_at and TASK:任务停止 is logged (runtime.rs L389-411).
func TestStopAll(t *testing.T) {
	srv, release := gatedLoginServer(t)
	svc := NewRuntimeService(newTestCfg(), srv.URL+",admin,secret", "0 */5 * * * *", "ispdomain", nil)

	if ok, err := svc.StartRunOnce(""); err != nil || !ok {
		t.Fatalf("StartRunOnce = %v, %v", ok, err)
	}
	if err := svc.StartCron("*/5 * * * * *", ""); err != nil {
		t.Fatalf("StartCron: %v", err)
	}
	if err := svc.StopAll(); err != nil {
		t.Fatalf("StopAll: %v", err)
	}

	st := svc.Status()
	if st.Running || st.CronRunning {
		t.Fatalf("status after StopAll = %+v, want all stopped", st)
	}

	close(release)
	time.Sleep(300 * time.Millisecond)
	if svc.Status().LastRunAt != "" {
		t.Error("aborted run wrote last_run_at, want skipped")
	}
	if !hasLog(svc, "TASK:任务停止") {
		t.Error("missing TASK:任务停止 log")
	}
	for _, rec := range svc.TailLogs(500) {
		if rec.Tag == "DONE:任务完成" {
			t.Error("aborted run logged DONE:任务完成, want skipped")
		}
	}
}

// TestSetDefaults 空白参数不改默认值，非空覆盖（runtime.rs L122-132）。
// TestSetDefaults: blank arguments keep the defaults; non-blank ones override
// (runtime.rs L122-132).
func TestSetDefaults(t *testing.T) {
	svc := NewRuntimeService(newTestCfg(), "", "0 */5 * * * *", "ispdomain", nil)
	svc.SetDefaults("", "")
	if st := svc.Status(); st.Module != "ispdomain" || st.CronExpr != "0 */5 * * * *" {
		t.Fatalf("blank SetDefaults changed status: %+v", st)
	}
	svc.SetDefaults("  ", "  ")
	if st := svc.Status(); st.Module != "ispdomain" || st.CronExpr != "0 */5 * * * *" {
		t.Fatalf("whitespace SetDefaults changed status: %+v", st)
	}
	svc.SetDefaults("ipgroup", "*/9 * * * * *")
	if st := svc.Status(); st.Module != "ipgroup" || !strings.HasPrefix(st.CronExpr, "*/9") {
		t.Fatalf("SetDefaults not applied: %+v", st)
	}
}

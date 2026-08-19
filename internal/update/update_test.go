// update_test.go 更新主流程测试（内存版假爱快 + 列表源服务器），
// 行为对齐 crates/core/src/update.rs：Safe-Before / Edit 优先 / 分片清理 / 严格顺序。
// Update main-flow tests (in-memory fake iKuai + rule-source servers), aligned with
// update.rs: Safe-Before / Edit-first / shrink cleanup / strict ordering.
package update

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/FelixJI/iKuai-Toolbox/internal/config"
	"github.com/FelixJI/iKuai-Toolbox/internal/ikuai"
)

// fakeCall 记录一次 /Action/call 或 /Action/login 请求。
// fakeCall records one /Action/call or /Action/login request.
type fakeCall struct {
	FuncName string
	Action   string
	Param    map[string]any
}

// fakeIkuai 内存版假爱快：支持 /Action/login 与五个 func_name 的
// show/add/edit/del，记录调用序列；show 返回预置行（route_object 额外
// 模拟 FILTER1 type 服务端过滤），add/edit/del 只记录不落库。
// fakeIkuai is the in-memory fake iKuai: it serves /Action/login plus
// show/add/edit/del for the five func_names while recording the call order;
// show returns seeded rows (route_object additionally honors the FILTER1 type
// filter), while add/edit/del are recorded but never mutate the store.
type fakeIkuai struct {
	t     *testing.T
	mu    sync.Mutex
	calls []fakeCall
	store map[string][]map[string]any
	srv   *httptest.Server
}

// newFakeIkuai 启动假爱快并在测试结束自动关闭。
// newFakeIkuai starts the fake server and closes it on test cleanup.
func newFakeIkuai(t *testing.T, store map[string][]map[string]any) *fakeIkuai {
	t.Helper()
	f := &fakeIkuai{t: t, store: store}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			f.t.Errorf("fake ikuai: read body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.URL.Path == "/Action/login" {
			f.record(fakeCall{FuncName: "login", Action: "login"})
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"code":0,"message":"ok"}`))
			return
		}
		if r.URL.Path != "/Action/call" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req struct {
			FuncName string         `json:"func_name"`
			Action   string         `json:"action"`
			Param    map[string]any `json:"param"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			f.t.Errorf("fake ikuai: decode request %q: %v", body, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.record(fakeCall{FuncName: req.FuncName, Action: req.Action, Param: req.Param})
		w.Header().Set("Content-Type", "application/json")
		if req.Action == "show" {
			rows := f.showRows(req.FuncName, req.Param)
			out, mErr := json.Marshal(map[string]any{
				"code":    0,
				"message": "ok",
				"results": map[string]any{"total": len(rows), "data": rows},
			})
			if mErr != nil {
				f.t.Errorf("fake ikuai: marshal response: %v", mErr)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Write(out)
			return
		}
		w.Write([]byte(`{"code":0,"message":"ok"}`))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// record 追加调用记录（并发安全）。
// record appends one recorded call (concurrency-safe).
func (f *fakeIkuai) record(c fakeCall) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
}

// showRows 计算应答行；route_object 模拟爱快服务端 FILTER1 "type,=,N" 过滤。
// showRows computes the rows to answer with; route_object emulates the iKuai
// server-side FILTER1 "type,=,N" filter.
func (f *fakeIkuai) showRows(funcName string, param map[string]any) []map[string]any {
	rows := f.store[funcName]
	out := make([]map[string]any, 0, len(rows))
	filter, hasFilter := param["FILTER1"].(string)
	for _, row := range rows {
		if hasFilter && funcName == ikuai.FUNC_NAME_ROUTE_OBJECT {
			parts := strings.Split(filter, ",")
			if len(parts) == 3 && parts[0] == "type" && parts[1] == "=" &&
				fmt.Sprintf("%v", row["type"]) != parts[2] {
				continue
			}
		}
		out = append(out, row)
	}
	return out
}

// client 基于假服务地址构造被测客户端。
// client builds the client under test against the fake server.
func (f *fakeIkuai) client() *ikuai.IKuaiClient {
	f.t.Helper()
	c, err := ikuai.NewIKuaiClient(f.srv.URL)
	if err != nil {
		f.t.Fatalf("NewIKuaiClient: %v", err)
	}
	return c
}

// names 生成 "login" / "func.action" 形式的调用序列（顺序断言用）。
// names renders the call sequence as "login" / "func.action" entries for order assertions.
func (f *fakeIkuai) names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		if c.FuncName == "login" {
			out = append(out, "login")
			continue
		}
		out = append(out, c.FuncName+"."+c.Action)
	}
	return out
}

// callsFor 过滤指定 func/action 的请求。
// callsFor filters recorded requests by func/action.
func (f *fakeIkuai) callsFor(funcName, action string) []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeCall
	for _, c := range f.calls {
		if c.FuncName == funcName && c.Action == action {
			out = append(out, c)
		}
	}
	return out
}

// callCount 统计全部变更类（add/edit/del）调用数。
// callCount counts every mutating (add/edit/del) call.
func (f *fakeIkuai) mutatingCalls() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeCall
	for _, c := range f.calls {
		if c.Action == "add" || c.Action == "edit" || c.Action == "del" {
			out = append(out, c)
		}
	}
	return out
}

// recSink 收集日志记录的 LogSink。
// recSink collects log records as a LogSink.
type recSink struct {
	mu   sync.Mutex
	recs []LogRecord
}

func (s *recSink) sink(rec LogRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs = append(s.recs, rec)
}

// findByTag 返回匹配 tag 的记录。
// findByTag returns records whose tag matches.
func (s *recSink) findByTag(tag string) []LogRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []LogRecord
	for _, r := range s.recs {
		if r.Tag == tag {
			out = append(out, r)
		}
	}
	return out
}

// hasRecord 判断存在 level 级别且 detail 包含 sub 的记录。
// hasRecord reports a record of the level whose detail contains sub.
func (s *recSink) hasRecord(level LogLevel, sub string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.recs {
		if r.Level == level && strings.Contains(r.Detail, sub) {
			return true
		}
	}
	return false
}

// testCfg 构造零等待、分片上限显式给定的测试配置。
// testCfg builds a test config with zero waits and explicit chunk limits.
func testCfg() *config.Config {
	return &config.Config{
		MaxNumberOfOneRecords: config.MaxNumberOfOneRecordsConfig{
			Isp: 5000, Ipv4: 1000, Ipv6: 1000, Domain: 5000,
		},
	}
}

// newSourceServer 启动规则列表源服务器：单路径固定状态码与响应体。
// newSourceServer starts a rule-list source server: one path, fixed status and body.
func newSourceServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// customIspSeed 预置 chunk1..n 的 custom_isp 行（id 从 start 起连续分配）。
// customIspSeed seeds custom_isp rows for chunk1..n with consecutive ids from start.
func customIspSeed(tag string, startID, chunks int) []map[string]any {
	rows := make([]map[string]any, 0, chunks)
	for i := 0; i < chunks; i++ {
		comment := ikuai.BuildCustomIspChunkComment(int64(i))
		rows = append(rows, map[string]any{
			"id":      int64(startID + i),
			"name":    ikuai.BuildTagName(tag),
			"comment": comment,
			"ipgroup": fmt.Sprintf("9.%d.9.9", i+1),
			"time":    "",
		})
	}
	return rows
}

// routeObjectSeed 预置 chunk1..n 的 route_object v4/v6 分片行。
// routeObjectSeed seeds route_object v4/v6 shard rows for chunk1..n.
func routeObjectSeed(tag string, startID, chunks int, typ int64, key string) []map[string]any {
	rows := make([]map[string]any, 0, chunks)
	for i := 0; i < chunks; i++ {
		name := ikuai.BuildIndexedTagName(tag, int64(i))
		rows = append(rows, map[string]any{
			"id":         int64(startID + i),
			"group_name": name,
			"type":       typ,
			"comment":    "",
			"group_value": []any{map[string]any{
				key:       fmt.Sprintf("9.%d.9.9", i+1),
				"comment": "",
			}},
		})
	}
	return rows
}

// TestSafeBefore_DownloadFailNeverMutates 数据安全边界（最高优先级）：
// 列表源 502 时，即使爱快上已有该 tag 的 3 条旧规则，也绝不发生任何
// add/edit/del——失败必须发生在任何 API 变更调用之前（update.rs L560 Safe-Before）。
// TestSafeBefore_DownloadFailNeverMutates is the data-safety boundary (top
// priority): with the source returning 502 and three old rules seeded on the
// fake iKuai, not a single add/edit/del may happen — the failure must occur
// before any mutating API call (the Safe-Before rule of update.rs L560).
func TestSafeBefore_DownloadFailNeverMutates(t *testing.T) {
	src := newSourceServer(t, http.StatusBadGateway, "boom")
	f := newFakeIkuai(t, map[string][]map[string]any{
		ikuai.FUNC_NAME_CUSTOM_ISP: customIspSeed("demo", 7, 3),
	})
	cfg := testCfg()
	cfg.CustomIsp = []config.CustomIspItem{{Tag: "demo", URL: src.URL + "/list.txt"}}

	var sink recSink
	updateIspdomain(cfg, f.client(), &UpdateOptions{}, sink.sink)

	if got := f.mutatingCalls(); len(got) != 0 {
		t.Fatalf("mutating calls = %+v, want none (download failure must never mutate)", got)
	}
	// Safe-Before 更强断言：下载失败连 show 查询都不应发生。
	// Stronger Safe-Before assertion: a failed download must not even issue show queries.
	if shows := f.callsFor(ikuai.FUNC_NAME_CUSTOM_ISP, "show"); len(shows) != 0 {
		t.Fatalf("custom_isp show calls = %d, want 0 (fail before any API call)", len(shows))
	}
	if !sink.hasRecord(LevelError, "download error") || !sink.hasRecord(LevelError, "502") {
		t.Fatalf("expected an error log mentioning the download failure, got %+v", sink.recs)
	}
	if len(sink.findByTag("UPDATE:更新失败")) == 0 {
		t.Fatalf("expected a UPDATE:更新失败 (Chinese) log record, got %+v", sink.recs)
	}
}

// TestSafeBefore_PerItemContinue 单条目下载失败只记日志不中断：
// 第一个 tag 源 502、第二个正常 => 第二个 tag 照常完成增改。
// TestSafeBefore_PerItemContinue: a per-item download failure logs and moves on
// without aborting the whole pass — the second tag still completes its writes.
func TestSafeBefore_PerItemContinue(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/bad.txt", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	mux.HandleFunc("/good.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("1.1.1.1\n2.2.2.2\n"))
	})
	src := httptest.NewServer(mux)
	t.Cleanup(src.Close)

	f := newFakeIkuai(t, map[string][]map[string]any{})
	cfg := testCfg()
	cfg.CustomIsp = []config.CustomIspItem{
		{Tag: "bad", URL: src.URL + "/bad.txt"},
		{Tag: "good", URL: src.URL + "/good.txt"},
	}

	var sink recSink
	updateIspdomain(cfg, f.client(), &UpdateOptions{}, sink.sink)

	adds := f.callsFor(ikuai.FUNC_NAME_CUSTOM_ISP, "add")
	if len(adds) != 1 {
		t.Fatalf("custom_isp add calls = %d, want 1 for the healthy tag only", len(adds))
	}
	if adds[0].Param["name"] != ikuai.BuildTagName("good") {
		t.Errorf("add name = %v, want %s", adds[0].Param["name"], ikuai.BuildTagName("good"))
	}
	if adds[0].Param["ipgroup"] != "1.1.1.1,2.2.2.2" {
		t.Errorf("add ipgroup = %v, want joined chunk", adds[0].Param["ipgroup"])
	}
	if !sink.hasRecord(LevelError, "'bad'") || !sink.hasRecord(LevelSuccess, "'good'") {
		t.Fatalf("expected failure log for tag bad and success log for tag good, got %+v", sink.recs)
	}
}

// TestEditFirst_KeepsIdAndName Edit 优先（update.rs L781-822）：预置
// IKBdemo1(id=7)，新数据仍是 1 片 => 发生 edit(id=7) 且沿用既有分组名
// （名称不变）、无 add、无 del。
// TestEditFirst_KeepsIdAndName covers Edit-first (update.rs L781-822): with
// IKBdemo1(id=7) seeded and the new data still fitting one chunk, an edit with
// id=7 reuses the existing group name (name unchanged) and no add/del happens.
func TestEditFirst_KeepsIdAndName(t *testing.T) {
	src := newSourceServer(t, http.StatusOK, "1.1.1.1\n2.2.2.2\n3.3.3.3\n")
	f := newFakeIkuai(t, map[string][]map[string]any{
		ikuai.FUNC_NAME_ROUTE_OBJECT: {
			{"id": int64(7), "group_name": "IKBdemo1", "type": int64(0), "comment": "",
				"group_value": []any{map[string]any{"ip": "9.9.9.9", "comment": ""}}},
		},
	})
	cfg := testCfg()
	cfg.MaxNumberOfOneRecords.Ipv4 = 100
	cfg.IpGroup = []config.IpGroupItem{{Tag: "demo", URL: src.URL + "/list.txt"}}

	var sink recSink
	updateIpgroup(cfg, f.client(), &UpdateOptions{}, sink.sink)

	edits := f.callsFor(ikuai.FUNC_NAME_ROUTE_OBJECT, "edit")
	if len(edits) != 1 {
		t.Fatalf("route_object edit calls = %d, want 1", len(edits))
	}
	if edits[0].Param["id"] != float64(7) {
		t.Errorf("edit id = %v, want 7", edits[0].Param["id"])
	}
	if edits[0].Param["group_name"] != "IKBdemo1" {
		t.Errorf("edit group_name = %v, want existing name IKBdemo1 (kept as-is)", edits[0].Param["group_name"])
	}
	gv, _ := edits[0].Param["group_value"].([]any)
	if len(gv) != 3 || gv[0].(map[string]any)["ip"] != "1.1.1.1" || gv[2].(map[string]any)["ip"] != "3.3.3.3" {
		t.Errorf("edit group_value = %v, want the three fresh addresses", edits[0].Param["group_value"])
	}
	if adds := f.callsFor(ikuai.FUNC_NAME_ROUTE_OBJECT, "add"); len(adds) != 0 {
		t.Errorf("route_object add calls = %d, want 0 (edit-first)", len(adds))
	}
	if dels := f.callsFor(ikuai.FUNC_NAME_ROUTE_OBJECT, "del"); len(dels) != 0 {
		t.Errorf("route_object del calls = %d, want 0", len(dels))
	}
	if len(sink.findByTag("EDIT:正在修改")) != 1 {
		t.Errorf("expected one EDIT:正在修改 log, got %+v", sink.recs)
	}
}

// TestShrinkDeletesRedundantChunks 冗余分片清理（update.rs L609-633）：
// 预置 3 片、新数据只够 1 片 => 片1 edit，片2/3 一次批量 del（单次调用）。
// TestShrinkDeletesRedundantChunks covers shrink cleanup (update.rs L609-633):
// three chunks seeded, the new data fits one chunk => chunk 1 is edited and
// chunks 2/3 go away in a single batched del call.
func TestShrinkDeletesRedundantChunks(t *testing.T) {
	src := newSourceServer(t, http.StatusOK, "1.1.1.1\n2.2.2.2\n")
	f := newFakeIkuai(t, map[string][]map[string]any{
		ikuai.FUNC_NAME_CUSTOM_ISP: customIspSeed("demo", 7, 3),
	})
	cfg := testCfg()
	cfg.MaxNumberOfOneRecords.Isp = 2
	cfg.CustomIsp = []config.CustomIspItem{{Tag: "demo", URL: src.URL + "/list.txt"}}

	var sink recSink
	updateIspdomain(cfg, f.client(), &UpdateOptions{}, sink.sink)

	edits := f.callsFor(ikuai.FUNC_NAME_CUSTOM_ISP, "edit")
	if len(edits) != 1 || edits[0].Param["id"] != float64(7) {
		t.Fatalf("custom_isp edits = %+v, want single edit id=7", edits)
	}
	if edits[0].Param["ipgroup"] != "1.1.1.1,2.2.2.2" {
		t.Errorf("edit ipgroup = %v, want joined fresh chunk", edits[0].Param["ipgroup"])
	}
	dels := f.callsFor(ikuai.FUNC_NAME_CUSTOM_ISP, "del")
	if len(dels) != 1 {
		t.Fatalf("custom_isp del calls = %d, want exactly 1 batched del", len(dels))
	}
	if dels[0].Param["id"] != "8,9" {
		t.Errorf("del id csv = %v, want 8,9", dels[0].Param["id"])
	}
	if adds := f.callsFor(ikuai.FUNC_NAME_CUSTOM_ISP, "add"); len(adds) != 0 {
		t.Errorf("custom_isp add calls = %d, want 0", len(adds))
	}
	if n := len(sink.findByTag("CLEAN:冗余删除")); n != 2 {
		t.Errorf("CLEAN:冗余删除 logs = %d, want 2", n)
	}
	if len(sink.findByTag("CLEAN:清理成功")) != 1 {
		t.Errorf("expected one CLEAN:清理成功 log, got %+v", sink.recs)
	}
}

// TestShrinkIpGroupDeletesRedundant ip_group 分支的冗余清理（update.rs
// L824-847）：预置 3 片、新数据 1 片 => edit(7) + 一次批量 del "8,9"。
// TestShrinkIpGroupDeletesRedundant covers the ip_group shrink branch
// (update.rs L824-847): three chunks seeded, one chunk of new data =>
// edit(7) plus one batched del "8,9".
func TestShrinkIpGroupDeletesRedundant(t *testing.T) {
	src := newSourceServer(t, http.StatusOK, "1.1.1.1\n")
	f := newFakeIkuai(t, map[string][]map[string]any{
		ikuai.FUNC_NAME_ROUTE_OBJECT: routeObjectSeed("demo", 7, 3, 0, "ip"),
	})
	cfg := testCfg()
	cfg.MaxNumberOfOneRecords.Ipv4 = 1
	cfg.IpGroup = []config.IpGroupItem{{Tag: "demo", URL: src.URL + "/list.txt"}}

	var sink recSink
	updateIpgroup(cfg, f.client(), &UpdateOptions{}, sink.sink)

	edits := f.callsFor(ikuai.FUNC_NAME_ROUTE_OBJECT, "edit")
	if len(edits) != 1 || edits[0].Param["id"] != float64(7) {
		t.Fatalf("route_object edits = %+v, want single edit id=7", edits)
	}
	if edits[0].Param["group_name"] != "IKBdemo1" {
		t.Errorf("edit group_name = %v, want IKBdemo1 kept", edits[0].Param["group_name"])
	}
	dels := f.callsFor(ikuai.FUNC_NAME_ROUTE_OBJECT, "del")
	if len(dels) != 1 || dels[0].Param["id"] != "8,9" {
		t.Fatalf("route_object dels = %+v, want single batch id 8,9", dels)
	}
	if !sink.hasRecord(LevelInfo, "deleting IDs: 8,9") {
		t.Errorf("expected CLEAN log listing IDs 8,9, got %+v", sink.recs)
	}
}

// TestGrowAddsNewChunks 增长补片（update.rs L787-822）：预置 1 片、新数据
// 3 片 => edit + 2×add；AddChunk 名字走 BuildIndexedTagName，开关打开时走
// BuildIndexedIpGroupTagName 的确定性随机后缀。
// TestGrowAddsNewChunks covers growth (update.rs L787-822): one chunk seeded,
// three chunks of new data => edit plus two adds; new names use
// BuildIndexedTagName, or BuildIndexedIpGroupTagName's deterministic random
// suffix when the option is enabled.
func TestGrowAddsNewChunks(t *testing.T) {
	src := newSourceServer(t, http.StatusOK, "1.1.1.1\n2.2.2.2\n3.3.3.3\n")
	f := newFakeIkuai(t, map[string][]map[string]any{
		ikuai.FUNC_NAME_ROUTE_OBJECT: routeObjectSeed("demo", 7, 1, 0, "ip"),
	})
	cfg := testCfg()
	cfg.MaxNumberOfOneRecords.Ipv4 = 1
	cfg.IpGroup = []config.IpGroupItem{{Tag: "demo", URL: src.URL + "/list.txt"}}

	var sink recSink
	updateIpgroup(cfg, f.client(), &UpdateOptions{}, sink.sink)

	edits := f.callsFor(ikuai.FUNC_NAME_ROUTE_OBJECT, "edit")
	if len(edits) != 1 || edits[0].Param["id"] != float64(7) {
		t.Fatalf("route_object edits = %+v, want single edit id=7", edits)
	}
	adds := f.callsFor(ikuai.FUNC_NAME_ROUTE_OBJECT, "add")
	if len(adds) != 2 {
		t.Fatalf("route_object add calls = %d, want 2", len(adds))
	}
	if adds[0].Param["group_name"] != ikuai.BuildIndexedTagName("demo", 1) ||
		adds[1].Param["group_name"] != ikuai.BuildIndexedTagName("demo", 2) {
		t.Errorf("add names = %v/%v, want plain indexed names",
			adds[0].Param["group_name"], adds[1].Param["group_name"])
	}
	if dels := f.callsFor(ikuai.FUNC_NAME_ROUTE_OBJECT, "del"); len(dels) != 0 {
		t.Errorf("route_object del calls = %d, want 0", len(dels))
	}

	fr := newFakeIkuai(t, map[string][]map[string]any{
		ikuai.FUNC_NAME_ROUTE_OBJECT: routeObjectSeed("demo", 7, 1, 0, "ip"),
	})
	updateIpgroup(cfg, fr.client(), &UpdateOptions{IpGroupNameAddRandomSuffix: true}, sink.sink)
	radds := fr.callsFor(ikuai.FUNC_NAME_ROUTE_OBJECT, "add")
	if len(radds) != 2 {
		t.Fatalf("random-suffix add calls = %d, want 2", len(radds))
	}
	if radds[0].Param["group_name"] != ikuai.BuildIndexedIpGroupTagName("demo", 1) ||
		radds[1].Param["group_name"] != ikuai.BuildIndexedIpGroupTagName("demo", 2) {
		t.Errorf("random-suffix add names = %v/%v, want BuildIndexedIpGroupTagName outputs",
			radds[0].Param["group_name"], radds[1].Param["group_name"])
	}
}

// TestIpv6GroupUpdate ipv6 分支走 v6 清洗（含 ':' 保留）与 FILTER type=1
// 分片（update.rs L852-935）。
// TestIpv6GroupUpdate covers the ipv6 branch: v6 line filtering (keep lines
// containing ':') and the type=1 shards (update.rs L852-935).
func TestIpv6GroupUpdate(t *testing.T) {
	src := newSourceServer(t, http.StatusOK, "1.2.3.4\nfd00::1\nfd00::2\n# comment\n")
	f := newFakeIkuai(t, map[string][]map[string]any{
		ikuai.FUNC_NAME_ROUTE_OBJECT: routeObjectSeed("demo", 7, 1, 1, "ipv6"),
	})
	cfg := testCfg()
	cfg.MaxNumberOfOneRecords.Ipv6 = 1
	cfg.Ipv6Group = []config.Ipv6GroupItem{{Tag: "demo", URL: src.URL + "/list.txt"}}

	var sink recSink
	updateIpv6group(cfg, f.client(), &UpdateOptions{}, sink.sink)

	edits := f.callsFor(ikuai.FUNC_NAME_ROUTE_OBJECT, "edit")
	if len(edits) != 1 || edits[0].Param["id"] != float64(7) {
		t.Fatalf("route_object edits = %+v, want single edit id=7", edits)
	}
	if edits[0].Param["type"] != float64(1) {
		t.Errorf("edit type = %v, want 1 (ipv6 shard)", edits[0].Param["type"])
	}
	gv, _ := edits[0].Param["group_value"].([]any)
	if len(gv) != 1 || gv[0].(map[string]any)["ipv6"] != "fd00::1" {
		t.Errorf("edit group_value = %v, want the first v6 line only (v4 dropped)", edits[0].Param["group_value"])
	}
	adds := f.callsFor(ikuai.FUNC_NAME_ROUTE_OBJECT, "add")
	if len(adds) != 1 || adds[0].Param["group_name"] != ikuai.BuildIndexedTagName("demo", 1) {
		t.Fatalf("route_object adds = %+v, want one add named IKBdemo2", adds)
	}
	if len(sink.findByTag("UPDATE:更新成功")) == 0 {
		t.Errorf("expected success logs, got %+v", sink.recs)
	}
}

// emptyAddrBlock / emptyTimeBlock 构造 stream_domain/stream_ipport 种子行的
// 空 src_addr/domain 与固定周期块。
// emptyAddrBlock / emptyTimeBlock build the empty src_addr/domain blocks and
// the fixed weekly block for stream_domain/stream_ipport seed rows.
func emptyAddrBlock() map[string]any {
	return map[string]any{"custom": []any{}, "object": []any{}}
}

func emptyTimeBlock() map[string]any {
	return map[string]any{"custom": []any{map[string]any{"type": "weekly", "weekdays": "1234567",
		"start_time": "00:00", "end_time": "23:59", "comment": ""}}, "object": []any{}}
}

// ipportCfg 构造单条端口分流配置。
// ipportCfg builds a one-entry stream-ipport config.
func ipportCfg(ipGroup string) *config.Config {
	cfg := testCfg()
	cfg.StreamIpPort = []config.StreamIpPortItem{{
		OptTagName: "", Type: "1", Interface: "wan1", Nexthop: "10.0.0.1",
		SrcAddr: "", SrcAddrOptIpGroup: "", IPGroup: ipGroup,
		Prio: 5, Protocol: "",
	}}
	return cfg
}

// wanTag 端口分流规则名（interface+nexthop 经 BuildTagName）。
// wanTag is the stream-ipport rule name (interface+nexthop through BuildTagName).
func wanTag() string {
	return ikuai.BuildTagName("wan1" + "10.0.0.1")
}

// ipportSeed 预置同名单条端口分流规则（id=61）。
// ipportSeed seeds one same-name stream-ipport rule (id=61).
func ipportSeed() []map[string]any {
	return []map[string]any{{
		"id": int64(61), "enabled": "yes", "tagname": wanTag(), "interface": "wan1",
		"nexthop": "10.0.0.1", "comment": "IkuaiBypass", "protocol": "tcp+udp", "type": 1,
		"src_addr": emptyAddrBlock(), "dst_addr": emptyAddrBlock(), "time": emptyTimeBlock(),
	}}
}

// TestStreamIpPortSingleRule 每 tag 单条：命中 edit 否则 add；引用展开为空
// 跳过不报错（update.rs L937-1045）。
// TestStreamIpPortSingleRule covers the single-rule-per-tag flow: edit on hit,
// add otherwise, and a skip (not an error) when reference expansion is empty
// (update.rs L937-1045).
func TestStreamIpPortSingleRule(t *testing.T) {
	seed := map[string][]map[string]any{
		ikuai.FUNC_NAME_ROUTE_OBJECT: {
			{"id": int64(21), "group_name": "IKBdemo1", "type": int64(0), "comment": "",
				"group_value": []any{map[string]any{"ip": "10.0.0.0/8", "comment": ""}}},
		},
	}

	// 新增：map 为空 => add，ip-group 引用展开为 IKBdemo1 对象引用。
	// Fresh add: empty map => add, with the ip-group reference expanded into an IKBdemo1 object ref.
	f := newFakeIkuai(t, seed)
	var sink recSink
	updateIpgroup(ipportCfg("demo"), f.client(), &UpdateOptions{}, sink.sink)
	adds := f.callsFor(ikuai.FUNC_NAME_STREAM_IPPORT, "add")
	if len(adds) != 1 {
		t.Fatalf("stream_ipport add calls = %d, want 1", len(adds))
	}
	if adds[0].Param["tagname"] != wanTag() {
		t.Errorf("add tagname = %v, want %s", adds[0].Param["tagname"], wanTag())
	}
	dst, _ := adds[0].Param["dst_addr"].(map[string]any)
	objs, _ := dst["object"].([]any)
	if len(objs) != 1 || objs[0].(map[string]any)["gid"] != "IPGP21" ||
		objs[0].(map[string]any)["gp_name"] != "IKBdemo1" {
		t.Errorf("add dst objects = %v, want [{gid:IPGP21 gp_name:IKBdemo1}]", dst["object"])
	}
	if adds[0].Param["protocol"] != "tcp+udp" {
		t.Errorf("add protocol = %v, want tcp+udp default", adds[0].Param["protocol"])
	}

	// 编辑：预置同名规则 => edit 传既有 id，不再 add。
	// Edit: a seeded same-name rule => edit carries the existing id, no add.
	fe := newFakeIkuai(t, map[string][]map[string]any{
		ikuai.FUNC_NAME_ROUTE_OBJECT:  seed[ikuai.FUNC_NAME_ROUTE_OBJECT],
		ikuai.FUNC_NAME_STREAM_IPPORT: ipportSeed(),
	})
	updateIpgroup(ipportCfg("demo"), fe.client(), &UpdateOptions{}, sink.sink)
	edits := fe.callsFor(ikuai.FUNC_NAME_STREAM_IPPORT, "edit")
	if len(edits) != 1 || edits[0].Param["id"] != float64(61) {
		t.Fatalf("stream_ipport edits = %+v, want single edit id=61", edits)
	}
	if adds := fe.callsFor(ikuai.FUNC_NAME_STREAM_IPPORT, "add"); len(adds) != 0 {
		t.Errorf("stream_ipport add calls = %d, want 0 (edit-first)", len(adds))
	}

	// 跳过：ip-group 引用展开为空 => 记 SKIP 日志、无 add/edit，且不报错。
	// Skip: empty reference expansion => a SKIP log, no add/edit, no error.
	fs := newFakeIkuai(t, seed)
	updateIpgroup(ipportCfg("missing-group"), fs.client(), &UpdateOptions{}, sink.sink)
	if got := fs.mutatingCalls(); len(got) != 0 {
		t.Fatalf("mutating calls = %+v, want none for unresolved reference", got)
	}
	if len(sink.findByTag("SKIP:跳过操作")) == 0 {
		t.Errorf("expected SKIP:跳过操作 log, got %+v", sink.recs)
	}

	// 空 ip-group：记参数校验日志后继续，目的地址为空仍新增。
	// Empty ip-group: a validation log is emitted, then the rule is still added with an empty dst.
	fn := newFakeIkuai(t, map[string][]map[string]any{})
	updateIpgroup(ipportCfg(""), fn.client(), &UpdateOptions{}, sink.sink)
	emptyAdds := fn.callsFor(ikuai.FUNC_NAME_STREAM_IPPORT, "add")
	if len(emptyAdds) != 1 {
		t.Fatalf("stream_ipport add calls = %d, want 1 with empty dst", len(emptyAdds))
	}
	dst, _ = emptyAdds[0].Param["dst_addr"].(map[string]any)
	if custom, _ := dst["custom"].([]any); len(custom) != 0 {
		t.Errorf("empty ip-group dst custom = %v, want empty", dst["custom"])
	}
	if len(sink.findByTag("CHECK:参数校验")) == 0 {
		t.Errorf("expected CHECK:参数校验 log, got %+v", sink.recs)
	}
}

// TestStreamIpPortSrcGroupSkip src 引用（src-addr-opt-ipgroup）展开为空 =>
// 跳过整条规则（update.rs L971-994）。
// TestStreamIpPortSrcGroupSkip: an empty src reference expansion
// (src-addr-opt-ipgroup) skips the whole rule (update.rs L971-994).
func TestStreamIpPortSrcGroupSkip(t *testing.T) {
	f := newFakeIkuai(t, map[string][]map[string]any{})
	cfg := ipportCfg("")
	cfg.StreamIpPort[0].SrcAddrOptIpGroup = "nope-group"

	var sink recSink
	updateIpgroup(cfg, f.client(), &UpdateOptions{}, sink.sink)
	if got := f.mutatingCalls(); len(got) != 0 {
		t.Fatalf("mutating calls = %+v, want none", got)
	}
	found := false
	for _, r := range sink.findByTag("SKIP:跳过操作") {
		if strings.Contains(r.Detail, "srcAddrOptIpGroup: nope-group") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected srcAddrOptIpGroup skip log, got %+v", sink.recs)
	}
}

// TestGroup MaxNumberOfOneRecords 分片（update.rs L534-544）：n 至少 1、
// 尾片取余、空输入空输出。
// TestGroup covers MaxNumberOfOneRecords chunking (update.rs L534-544): n has
// a floor of 1, the tail keeps the remainder, empty input yields no chunks.
func TestGroup(t *testing.T) {
	got := Group([]int{1, 2, 3, 4, 5, 6, 7}, 3)
	want := [][]int{{1, 2, 3}, {4, 5, 6}, {7}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Group(7 items, 3) = %v, want %v", got, want)
	}
	if got := Group([]string{}, 5); len(got) != 0 {
		t.Errorf("Group(empty) = %v, want empty", got)
	}
	if got := Group([]string{"a", "b"}, 0); !reflect.DeepEqual(got, [][]string{{"a"}, {"b"}}) {
		t.Errorf("Group(2 items, 0) = %v, want singleton chunks (floor 1)", got)
	}
}

// TestSplitLines 对齐 Rust str::lines 语义：\r\n 剥 \r、末尾换行不产生空行。
// TestSplitLines mirrors Rust str::lines: \r\n strips the \r, a trailing
// newline yields no extra empty element.
func TestSplitLines(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"a\r\nb\nc", []string{"a", "b", "c"}},
		{"a\n", []string{"a"}},
		{"", nil},
		{"a\n\n", []string{"a", ""}},
		{"\n", []string{""}},
	}
	for _, tc := range cases {
		got := splitLines([]byte(tc.in))
		// 统一空切片便于比较 / normalize empty slices for comparison.
		if len(tc.want) == 0 && len(got) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("splitLines(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestLineFilters 行清洗规则（update.rs L442-496）：去 # 注释、去空行、
// 含 ':' 分 v4/v6、域名行含 '_' 过滤。
// TestLineFilters covers the line filters (update.rs L442-496): strip #
// comments, drop blanks, split v4/v6 by ':', drop domains containing '_'.
func TestLineFilters(t *testing.T) {
	v4 := removeIpv6AndEmpty([]string{"1.2.3.4", "# comment", "", "   ", "fd00::1 # x", "5.6.7.8#9", " 6.6.6.6 "})
	if want := []string{"1.2.3.4", "5.6.7.8", "6.6.6.6"}; !reflect.DeepEqual(v4, want) {
		t.Errorf("removeIpv6AndEmpty = %v, want %v", v4, want)
	}
	v6 := removeIpv4AndEmpty([]string{"1.2.3.4", "fd00::1", "::1", "# c", " fe80::/10 #x", ""})
	if want := []string{"fd00::1", "::1", "fe80::/10"}; !reflect.DeepEqual(v6, want) {
		t.Errorf("removeIpv4AndEmpty = %v, want %v", v6, want)
	}
	domains := filterDomains([]string{"a.com", "# c", "b.com#c", "   ", "under_score.com", " x.com ", "_y.com#z"})
	if want := []string{"a.com", "b.com", "x.com"}; !reflect.DeepEqual(domains, want) {
		t.Errorf("filterDomains = %v, want %v", domains, want)
	}
}

// TestHttpGet 非 2xx / 网络错误 / 成功路径与 ghproxy 改写（update.rs L389-435）。
// TestHttpGet covers the non-2xx / network-error / success paths plus the
// ghproxy rewrite (update.rs L389-435).
func TestHttpGet(t *testing.T) {
	var sink recSink
	cfg := testCfg()

	if _, err := httpGet(cfg, sink.sink, newSourceServer(t, http.StatusBadGateway, "x").URL); err == nil ||
		err.Kind != ErrKindDownload || !strings.Contains(err.Msg, "502") {
		t.Fatalf("502 => got %v, want download error with status text", err)
	}

	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closed.Close()
	if _, err := httpGet(cfg, sink.sink, closed.URL); err == nil || err.Kind != ErrKindDownload {
		t.Fatalf("network failure => got %v, want download error", err)
	}

	ok := newSourceServer(t, http.StatusOK, "payload")
	body, err := httpGet(cfg, sink.sink, ok.URL)
	if err != nil || string(body) != "payload" {
		t.Fatalf("success => body=%q err=%v, want payload/nil", body, err)
	}

	// smart+ghproxy 命中 github 源 => 请求改写发往 ghproxy 且日志带 (ghproxy)。
	// smart+ghproxy on a github source => the request is rewritten to the ghproxy
	// and the log carries the (ghproxy) marker.
	ghHit := false
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ghHit = true
		if !strings.HasPrefix(r.URL.Path, "/https://raw.githubusercontent.com/") {
			t.Errorf("ghproxy path = %q, want the joined github URL", r.URL.Path)
		}
		w.Write([]byte("via-gh"))
	}))
	t.Cleanup(gh.Close)
	cfgGh := testCfg()
	cfgGh.Proxy.Mode = config.ProxyModeSmart
	cfgGh.GithubProxy = gh.URL
	body, err = httpGet(cfgGh, sink.sink, "https://raw.githubusercontent.com/a/b/list.txt")
	if err != nil || string(body) != "via-gh" || !ghHit {
		t.Fatalf("ghproxy fetch => body=%q hit=%v err=%v", body, ghHit, err)
	}
	found := false
	for _, rec := range sink.recs {
		if strings.Contains(rec.Detail, "via=直连 (ghproxy)") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a 'via=直连 (ghproxy)' download log, got %+v", sink.recs)
	}
}

// TestExportStreamDomainFile 导出文件名清洗与内容（update.rs L498-532）。
// TestExportStreamDomainFile covers the sanitized filename and file content
// (update.rs L498-532).
func TestExportStreamDomainFile(t *testing.T) {
	dir := t.TempDir()
	path, err := exportStreamDomains(dir, "wan 1", "de mo", []string{"a.com", "b.com"})
	if err != nil {
		t.Fatalf("exportStreamDomains: %v", err)
	}
	if filepath.Base(path) != "stream-domain_wan1_demo.txt" {
		t.Errorf("export filename = %q, want sanitized stream-domain_wan1_demo.txt", path)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "a.com\nb.com\n" {
		t.Fatalf("exported = %q err=%v, want one domain per line", got, err)
	}

	if p, err := exportStreamDomains("   ", "wan1", "demo", nil); err != nil || p != "" {
		t.Errorf("blank dir => (%q, %v), want empty path without error", p, err)
	}
}

// TestExportStreamDomainToTxt 导出入口（update.rs L128-211）：空路径错误、
// 空列表警告放行、url 空与下载失败计入 failed 并报 download。
// TestExportStreamDomainToTxt covers the export entry (update.rs L128-211):
// empty path errors, an empty list warns and returns, blank urls and download
// failures count into failed and surface a download error.
func TestExportStreamDomainToTxt(t *testing.T) {
	var sink recSink
	cfg := testCfg()

	if err := ExportStreamDomainToTxt(cfg, "  ", sink.sink); err == nil || err.Kind != ErrKindDownload {
		t.Fatalf("empty exportPath => %v, want download error", err)
	}

	cfgEmpty := testCfg()
	if err := ExportStreamDomainToTxt(cfgEmpty, t.TempDir(), sink.sink); err != nil {
		t.Fatalf("empty stream-domain list => %v, want nil", err)
	}
	if len(sink.findByTag("EXPORT:无可导出项")) != 1 {
		t.Fatalf("expected one EXPORT:无可导出项 warn, got %+v", sink.recs)
	}

	bad := newSourceServer(t, http.StatusInternalServerError, "x")
	cfgBad := testCfg()
	cfgBad.StreamDomain = []config.StreamDomainItem{
		{Interface: "wan1", Tag: "a", URL: ""},
		{Interface: "wan1", Tag: "b", URL: bad.URL},
	}
	err := ExportStreamDomainToTxt(cfgBad, t.TempDir(), sink.sink)
	if err == nil || err.Kind != ErrKindDownload || !strings.Contains(err.Msg, "2 failures") {
		t.Fatalf("two broken items => %v, want download error mentioning 2 failures", err)
	}

	good := newSourceServer(t, http.StatusOK, "a.com\n_under.com\n")
	cfgGood := testCfg()
	dir := t.TempDir()
	cfgGood.StreamDomain = []config.StreamDomainItem{{Interface: "wan1", Tag: "demo", URL: good.URL}}
	if err := ExportStreamDomainToTxt(cfgGood, dir, sink.sink); err != nil {
		t.Fatalf("healthy export => %v, want nil", err)
	}
	if got, rErr := os.ReadFile(filepath.Join(dir, "stream-domain_wan1_demo.txt")); rErr != nil ||
		string(got) != "a.com\n" {
		t.Fatalf("exported file = %q err=%v, want a.com only", got, rErr)
	}
	if len(sink.findByTag("EXPORT:导出完成")) == 0 {
		t.Errorf("expected EXPORT:导出完成 logs, got %+v", sink.recs)
	}
}

// TestUpdateErrorMessages 错误 Display 文案对齐 update.rs L21-31 的 thiserror。
// TestUpdateErrorMessages keeps the Display strings aligned with the
// thiserror definitions of update.rs L21-31.
func TestUpdateErrorMessages(t *testing.T) {
	cases := []struct {
		e    *UpdateError
		want string
	}{
		{&UpdateError{Kind: ErrKindLoginParams, Msg: "command line parameter format error"},
			"login params error: command line parameter format error"},
		{&UpdateError{Kind: ErrKindIkuai, Msg: "api error: boom"},
			"ikuai error: api error: boom"},
		{&UpdateError{Kind: ErrKindDownload, Msg: "502 Bad Gateway"},
			"download error: 502 Bad Gateway"},
		{&UpdateError{Kind: ErrKindInvalidModule, Msg: "bogus"},
			"invalid -m parameter: bogus"},
	}
	for _, tc := range cases {
		if got := tc.e.Error(); got != tc.want {
			t.Errorf("Error() = %q, want %q", got, tc.want)
		}
	}
}

// TestLogRecordShape LogRecord 五字段与 JSON 标签形状（Task 6 logger 复用）。
// TestLogRecordShape pins the five LogRecord fields and their JSON tags (the
// shape Task 6's logger will reuse).
func TestLogRecordShape(t *testing.T) {
	if LevelInfo != "Info" || LevelSuccess != "Success" || LevelWarn != "Warn" || LevelError != "Error" {
		t.Fatalf("level constants = %q/%q/%q/%q, want Info/Success/Warn/Error",
			LevelInfo, LevelSuccess, LevelWarn, LevelError)
	}
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

// TestLoggerEmitsTsAndModule 内部 logger 填充 ts/module 并透传 tag/level/detail。
// TestLoggerEmitsTsAndModule: the internal logger fills ts/module and passes
// tag/level/detail through.
func TestLoggerEmitsTsAndModule(t *testing.T) {
	var sink recSink
	l := newLogger("AUTH:登录认证", sink.sink)
	l.info("LOGIN:开始登录", "Logging in to iKuai: http://x")
	recs := sink.findByTag("LOGIN:开始登录")
	if len(recs) != 1 {
		t.Fatalf("records = %+v, want one", sink.recs)
	}
	r := recs[0]
	if r.Module != "AUTH:登录认证" || r.Level != LevelInfo || r.Detail != "Logging in to iKuai: http://x" {
		t.Errorf("record = %+v, wrong fields", r)
	}
	if len(r.Ts) != len("2026/08/19 12:00:00") || !strings.Contains(r.Ts, "/") {
		t.Errorf("ts = %q, want local timestamp format", r.Ts)
	}
}

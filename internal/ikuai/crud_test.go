// crud_test.go 爱快规则 CRUD 五模块共用测试（httptest 假爱快），
// 行为对齐 rust_archive/crates/core/src/ikuai/ 下 custom_isp.rs / ip_group.rs / ipv6_group.rs /
// stream_domain.rs / stream_ipport.rs / clean.rs。
// Shared tests for the five iKuai rule CRUD modules against one httptest fake,
// aligned with the Rust sources under rust_archive/crates/core/src/ikuai/.
package ikuai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestParseChunkIndexLegacyMarkers 分片索引解析必须兼容三类历史备注标记
// （custom_isp.rs L130-158）：IkuaiBypass[-N]、joyanhui/ikuai-bypass-N、IKUAI_BYPASS_N。
// 存量用户的清理安全依赖该兼容解析，属本任务最高风险点。
// TestParseChunkIndexLegacyMarkers verifies chunk-index parsing across all three
// legacy comment markers (custom_isp.rs L130-158). Cleanup safety for existing
// users depends on this compatibility, so it is the top risk of this task.
func TestParseChunkIndexLegacyMarkers(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"IkuaiBypass", 1, true},
		{"IkuaiBypass-3", 3, true},
		{"joyanhui/ikuai-bypass-2", 2, true},
		{"IKUAI_BYPASS_4", 4, true},
		{"别家的备注", 0, false},
	}
	for _, tc := range cases {
		got, ok := ParseCustomIspChunkIndexFromComment(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("ParseCustomIspChunkIndexFromComment(%q) = (%d, %v), want (%d, %v)",
				tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// fakeCall 记录一次 /Action/call 请求（信封 + 参数 + 原始字节）。
// fakeCall records one /Action/call request (envelope, param and raw bytes).
type fakeCall struct {
	FuncName string
	Action   string
	Param    map[string]any
	Raw      string
}

// fakeIkuai 五模块共用的假爱快服务：show 按 func_name 返回种子行（route_object
// 额外模拟 FILTER1 type 过滤），del 按 id CSV 真删行（供 Del*All 循环收敛）。
// fakeIkuai is the shared fake iKuai server: show returns seeded rows per
// func_name (route_object additionally honors the FILTER1 type filter), and
// del really removes rows by id CSV so Del*All loops converge.
type fakeIkuai struct {
	t     *testing.T
	mu    sync.Mutex
	rows  map[string][]map[string]any
	calls []fakeCall
	srv   *httptest.Server
}

// newFakeIkuai 启动假服务并在测试结束自动关闭。
// newFakeIkuai starts the fake server and closes it on test cleanup.
func newFakeIkuai(t *testing.T, rows map[string][]map[string]any) *fakeIkuai {
	f := &fakeIkuai{t: t, rows: rows}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/Action/call" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			f.t.Errorf("fake ikuai: read body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
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
		f.mu.Lock()
		f.calls = append(f.calls, fakeCall{FuncName: req.FuncName, Action: req.Action, Param: req.Param, Raw: string(body)})
		if req.Action == "del" {
			f.applyDel(req.FuncName, req.Param["id"])
		}
		rows := f.showRows(req.FuncName, req.Param)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if req.Action == "show" {
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

// applyDel 按 "id" CSV 删除行，需在持锁状态调用。
// applyDel removes rows by the "id" CSV; caller must hold the lock.
func (f *fakeIkuai) applyDel(funcName string, idCSV any) {
	s, ok := idCSV.(string)
	if !ok {
		return
	}
	drop := make(map[string]struct{})
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			drop[part] = struct{}{}
		}
	}
	rows := f.rows[funcName]
	kept := rows[:0]
	for _, row := range rows {
		if _, hit := drop[fmt.Sprintf("%v", row["id"])]; !hit {
			kept = append(kept, row)
		}
	}
	f.rows[funcName] = kept
}

// showRows 计算应答行；route_object 模拟爱快服务端 FILTER1 "type,=,N" 过滤，需在持锁状态调用。
// showRows computes the rows to return; for route_object it emulates the iKuai
// FILTER1 "type,=,N" server-side filter. Caller must hold the lock.
func (f *fakeIkuai) showRows(funcName string, param map[string]any) []map[string]any {
	rows := f.rows[funcName]
	out := make([]map[string]any, 0, len(rows))
	filter, hasFilter := param["FILTER1"].(string)
	for _, row := range rows {
		if hasFilter && funcName == FUNC_NAME_ROUTE_OBJECT {
			parts := strings.Split(filter, ",")
			if len(parts) == 3 && parts[0] == "type" && parts[1] == "=" {
				// 种子行数字可能是 int 或经 JSON 往返的 float64，统一按文本比较。
				// Seeded numbers may be int or JSON round-tripped float64; compare as text.
				if fmt.Sprintf("%v", row["type"]) != parts[2] {
					continue
				}
			}
		}
		out = append(out, row)
	}
	return out
}

// client 基于假服务地址构造被测客户端。
// client builds the client under test against the fake server.
func (f *fakeIkuai) client() *IKuaiClient {
	c, err := NewIKuaiClient(f.srv.URL)
	if err != nil {
		f.t.Fatalf("NewIKuaiClient: %v", err)
	}
	return c
}

// lastCall 返回最近一次 call 请求。
// lastCall returns the most recent call request.
func (f *fakeIkuai) lastCall() fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		f.t.Fatalf("fake ikuai: no call recorded")
	}
	return f.calls[len(f.calls)-1]
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

// rowsOf 读取当前种子行快照。
// rowsOf snapshots the current seeded rows.
func (f *fakeIkuai) rowsOf(funcName string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any{}, f.rows[funcName]...)
}

// TestBuildCustomIspChunkComment 分片备注构造（custom_isp.rs L122-128）：1 档裸标记，N 档 "-N"。
// TestBuildCustomIspChunkComment covers chunk comment building (custom_isp.rs L122-128):
// bare marker for chunk 1, "-N" otherwise.
func TestBuildCustomIspChunkComment(t *testing.T) {
	cases := []struct {
		index int64
		want  string
	}{
		{0, "IkuaiBypass"},
		{-1, "IkuaiBypass"},
		{1, "IkuaiBypass-2"},
		{9, "IkuaiBypass-10"},
	}
	for _, tc := range cases {
		if got := BuildCustomIspChunkComment(tc.index); got != tc.want {
			t.Errorf("BuildCustomIspChunkComment(%d) = %q, want %q", tc.index, got, tc.want)
		}
	}
}

// TestCustomIspCrud add/edit/del 请求体逐字段对齐 custom_isp.rs L43-87：
// name=BuildTagName、ipgroup 仅整体 trim、comment=分片备注；edit 多 id；del 只带 id CSV。
// TestCustomIspCrud asserts add/edit/del request bodies field by field against
// custom_isp.rs L43-87: name=BuildTagName, ipgroup only end-trimmed, comment=chunk
// comment; edit adds id; del carries only the id CSV.
func TestCustomIspCrud(t *testing.T) {
	f := newFakeIkuai(t, map[string][]map[string]any{FUNC_NAME_CUSTOM_ISP: {}})
	api := f.client()

	if err := AddCustomIsp(api, "demo", "  1.2.3.4/24, 5.6.7.8  ", 1); err != nil {
		t.Fatalf("AddCustomIsp: %v", err)
	}
	call := f.lastCall()
	if call.FuncName != FUNC_NAME_CUSTOM_ISP || call.Action != "add" {
		t.Fatalf("call = %s/%s, want custom_isp/add", call.FuncName, call.Action)
	}
	if len(call.Param) != 3 {
		t.Errorf("add param keys = %v, want exactly name/ipgroup/comment", call.Param)
	}
	if call.Param["name"] != "IKBdemo" {
		t.Errorf("add name = %v, want IKBdemo", call.Param["name"])
	}
	if call.Param["ipgroup"] != "1.2.3.4/24, 5.6.7.8" {
		t.Errorf("add ipgroup = %q, want end-trimmed original", call.Param["ipgroup"])
	}
	if call.Param["comment"] != "IkuaiBypass-2" {
		t.Errorf("add comment = %v, want IkuaiBypass-2", call.Param["comment"])
	}
	// 信封与参数排序后的出站字节应与 Rust serde_json 产物一致（键名字典序）。
	// Outbound bytes must match the Rust serde_json output (lexicographic key order).
	wantRaw := `{"func_name":"custom_isp","action":"add","param":{` +
		`"comment":"IkuaiBypass-2","ipgroup":"1.2.3.4/24, 5.6.7.8","name":"IKBdemo"}}`
	if call.Raw != wantRaw {
		t.Errorf("add wire body =\n%s\nwant\n%s", call.Raw, wantRaw)
	}

	if err := EditCustomIsp(api, "demo", "9.9.9.9", 0, 42); err != nil {
		t.Fatalf("EditCustomIsp: %v", err)
	}
	call = f.lastCall()
	if call.Action != "edit" {
		t.Fatalf("action = %s, want edit", call.Action)
	}
	if len(call.Param) != 4 {
		t.Errorf("edit param keys = %v, want name/ipgroup/comment/id", call.Param)
	}
	if call.Param["comment"] != "IkuaiBypass" || call.Param["id"] != float64(42) {
		t.Errorf("edit comment/id = %v/%v, want IkuaiBypass/42", call.Param["comment"], call.Param["id"])
	}

	if err := DelCustomIsp(api, "5,6"); err != nil {
		t.Fatalf("DelCustomIsp: %v", err)
	}
	call = f.lastCall()
	if call.Action != "del" || len(call.Param) != 1 || call.Param["id"] != "5,6" {
		t.Errorf("del call = %+v, want param {id:5,6} only", call)
	}
}

// TestCustomIspShowAndMap show 参数照抄 Rust（TYPE/limit 两键），GetMap 兼容三类标记、
// 备注失败回退名称解析、重复分片保留先见 id（custom_isp.rs L22-41 / L89-105）。
// TestCustomIspShowAndMap: show params copied from Rust (TYPE/limit only); GetMap
// accepts all three markers, falls back to name parsing, keeps the first id per
// chunk (custom_isp.rs L22-41 / L89-105).
func TestCustomIspShowAndMap(t *testing.T) {
	f := newFakeIkuai(t, map[string][]map[string]any{
		FUNC_NAME_CUSTOM_ISP: {
			{"id": 11, "name": "IKBdemo", "comment": "IkuaiBypass", "ipgroup": "1.1.1.1", "time": ""},
			{"id": 12, "name": "IKBdemo", "comment": "IkuaiBypass-2", "ipgroup": "2.2.2.2", "time": ""},
			{"id": 13, "name": "IKBdemo", "comment": "joyanhui/ikuai-bypass-3", "ipgroup": "3.3.3.3", "time": ""},
			{"id": 14, "name": "IKBdemo", "comment": "IKUAI_BYPASS_4", "ipgroup": "4.4.4.4", "time": ""},
			{"id": 16, "name": "IKBdemo", "comment": "IkuaiBypass-2", "ipgroup": "6.6.6.6", "time": ""},
			{"id": 17, "name": "IKBdemo5", "comment": "别家的备注", "ipgroup": "7.7.7.7", "time": ""},
			{"id": 15, "name": "other", "comment": "", "ipgroup": "5.5.5.5", "time": ""},
		},
	})
	api := f.client()

	all, err := ShowCustomIspByTagName(api, "")
	if err != nil {
		t.Fatalf("ShowCustomIspByTagName: %v", err)
	}
	if len(all) != 7 {
		t.Fatalf("show all = %d rows, want 7", len(all))
	}
	call := f.lastCall()
	if len(call.Param) != 2 || call.Param["TYPE"] != "total,data" || call.Param["limit"] != "0,1000" {
		t.Errorf("show param = %v, want {TYPE:total,data, limit:0,1000} only", call.Param)
	}

	filtered, err := ShowCustomIspByTagName(api, "demo")
	if err != nil {
		t.Fatalf("ShowCustomIspByTagName(demo): %v", err)
	}
	if len(filtered) != 6 {
		t.Fatalf("show demo = %d rows, want 6 (exclude unmanaged other)", len(filtered))
	}

	got, err := GetCustomIspMap(api, "demo")
	if err != nil {
		t.Fatalf("GetCustomIspMap: %v", err)
	}
	want := map[int64]int64{1: 11, 2: 12, 3: 13, 4: 14, 5: 17}
	if len(got) != len(want) {
		t.Fatalf("GetCustomIspMap = %v, want %v", got, want)
	}
	for idx, id := range want {
		if got[idx] != id {
			t.Errorf("GetCustomIspMap[%d] = %d, want %d", idx, got[idx], id)
		}
	}
}

// TestCustomIspDelAll 循环删除仅命中受管规则：IKB 名字命中、legacy 备注命中，
// 非受管行必须原样保留（custom_isp.rs L107-120 + clean 语义）。
// TestCustomIspDelAll loops deletion over managed rules only: IKB-name hits and
// legacy-comment hits go away, unmanaged rows must survive
// (custom_isp.rs L107-120 + clean semantics).
func TestCustomIspDelAll(t *testing.T) {
	f := newFakeIkuai(t, map[string][]map[string]any{
		FUNC_NAME_CUSTOM_ISP: {
			{"id": 21, "name": "IKBdemo", "comment": "IkuaiBypass", "ipgroup": "1.1.1.1", "time": ""},
			{"id": 22, "name": "IKUAI_BYPASS_demo", "comment": "joyanhui/ikuai-bypass-demo", "ipgroup": "2.2.2.2", "time": ""},
			{"id": 23, "name": "user-rule", "comment": "keep me", "ipgroup": "3.3.3.3", "time": ""},
			{"id": 24, "name": "IKBdemo2", "comment": "IkuaiBypass-2", "ipgroup": "4.4.4.4", "time": ""},
		},
	})
	api := f.client()

	if err := DelCustomIspAll(api, "demo"); err != nil {
		t.Fatalf("DelCustomIspAll: %v", err)
	}
	dels := f.callsFor(FUNC_NAME_CUSTOM_ISP, "del")
	if len(dels) != 1 {
		t.Fatalf("del calls = %d, want 1 (loop must converge)", len(dels))
	}
	if dels[0].Param["id"] != "21,22,24" {
		t.Errorf("del id csv = %v, want 21,22,24", dels[0].Param["id"])
	}
	remain := f.rowsOf(FUNC_NAME_CUSTOM_ISP)
	if len(remain) != 1 || remain[0]["id"] != 23 {
		t.Errorf("remaining rows = %v, want only unmanaged id 23", remain)
	}
}

// routeObjectRows v4/v6 混合种子：1/2 号 plain 分片、R?? 随机后缀分片（截断兼容）、
// 精确名分组、非受管分组，以及 v6 分片。
// Mixed v4/v6 seeds: plain shards 1/2, an R?? random-suffix shard (truncation
// compat), an exact-name group, an unmanaged group, plus a v6 shard.
func routeObjectRows() []map[string]any {
	return []map[string]any{
		{"id": 21, "group_name": "IKBdemo1", "type": 0, "comment": "",
			"group_value": []any{map[string]any{"ip": "1.1.1.1", "comment": ""}, map[string]any{"ip": "2.2.2.2", "comment": ""}}},
		{"id": 22, "group_name": "IKBdemo2", "type": 0, "comment": "",
			"group_value": []any{map[string]any{"ip": "3.3.3.3", "comment": ""}}},
		{"id": 25, "group_name": BuildIndexedIpGroupTagName("trunk", 2), "type": 0, "comment": "",
			"group_value": []any{map[string]any{"ip": "5.5.5.5", "comment": ""}}},
		{"id": 23, "group_name": "home", "type": 0, "comment": "",
			"group_value": []any{map[string]any{"ip": "192.168.0.0/16", "comment": ""}}},
		{"id": 26, "group_name": "IKBhome1", "type": 0, "comment": "",
			"group_value": []any{map[string]any{"ip": "10.0.0.0/8", "comment": ""}}},
		{"id": 24, "group_name": "user-group", "type": 0, "comment": "keep",
			"group_value": []any{map[string]any{"ip": "9.9.9.9", "comment": ""}}},
		{"id": 31, "group_name": "IKBdemo1", "type": 1, "comment": "",
			"group_value": []any{map[string]any{"ipv6": "fd00::/8", "comment": ""}}},
	}
}

// TestRouteObjectCrud 覆盖 ip_group/ipv6_group（ip_group.rs / ipv6_group.rs）：
// show FILTER type=0/1、group_value 展平、add/edit 请求体、GetMap 索引解析
// （含 R?? 后缀尾数字兜底）、ResolveRuleReference 精确名优先。
// TestRouteObjectCrud covers ip_group/ipv6_group: the show FILTER type=0/1,
// group_value flattening, add/edit bodies, GetMap index parsing (including the
// R?? trailing-digit fallback) and exact-name-first ResolveRuleReference.
func TestRouteObjectCrud(t *testing.T) {
	f := newFakeIkuai(t, map[string][]map[string]any{FUNC_NAME_ROUTE_OBJECT: routeObjectRows()})
	api := f.client()

	v4, err := ShowIpGroupByTagName(api, "demo")
	if err != nil {
		t.Fatalf("ShowIpGroupByTagName: %v", err)
	}
	if len(v4) != 2 || v4[0].ID != 21 || v4[1].ID != 22 {
		t.Fatalf("v4 rows = %+v, want ids 21,22", v4)
	}
	if v4[0].AddrPool != "1.1.1.1,2.2.2.2" || v4[0].GroupName != "IKBdemo1" || v4[0].Type != 0 {
		t.Errorf("v4 row 0 = %+v, want flattened pool/IKBdemo1/type 0", v4[0])
	}
	call := f.lastCall()
	if len(call.Param) != 3 || call.Param["TYPE"] != "total,data" || call.Param["limit"] != "0,1000" ||
		call.Param["FILTER1"] != "type,=,0" {
		t.Errorf("v4 show param = %v, want TYPE/limit/FILTER1=type,=,0", call.Param)
	}

	ipv6, err := ShowIpv6GroupByTagName(api, "demo")
	if err != nil {
		t.Fatalf("ShowIpv6GroupByTagName: %v", err)
	}
	if len(ipv6) != 1 || ipv6[0].ID != 31 || ipv6[0].AddrPool != "fd00::/8" || ipv6[0].Type != 1 {
		t.Fatalf("v6 rows = %+v, want single id 31 with fd00::/8 type 1", ipv6)
	}
	if f.lastCall().Param["FILTER1"] != "type,=,1" {
		t.Errorf("v6 show FILTER1 = %v, want type,=,1", f.lastCall().Param["FILTER1"])
	}

	if err := AddIpGroup(api, "demo", " 1.1.1.1, 2.2.2.1 ,,", 1); err != nil {
		t.Fatalf("AddIpGroup: %v", err)
	}
	call = f.lastCall()
	if call.Action != "add" || len(call.Param) != 4 {
		t.Fatalf("v4 add = %+v, want exactly group_name/type/group_value/comment", call)
	}
	if call.Param["group_name"] != "IKBdemo2" || call.Param["type"] != float64(0) || call.Param["comment"] != "" {
		t.Errorf("v4 add scalars = %v", call.Param)
	}
	wantRaw := `{"func_name":"route_object","action":"add","param":{"comment":"","group_name":"IKBdemo2",` +
		`"group_value":[{"comment":"","ip":"1.1.1.1"},{"comment":"","ip":"2.2.2.1"}],"type":0}}`
	if call.Raw != wantRaw {
		t.Errorf("v4 add wire body =\n%s\nwant\n%s", call.Raw, wantRaw)
	}

	if err := EditIpGroup(api, "demo", "3.3.3.3", 0, 21); err != nil {
		t.Fatalf("EditIpGroup: %v", err)
	}
	call = f.lastCall()
	if call.Action != "edit" || len(call.Param) != 5 || call.Param["id"] != float64(21) {
		t.Errorf("v4 edit = %+v, want 5 keys with id 21", call)
	}

	if err := AddIpv6Group(api, "demo", "fd00::/8", 0); err != nil {
		t.Fatalf("AddIpv6Group: %v", err)
	}
	call = f.lastCall()
	if call.Param["type"] != float64(1) || call.Param["group_name"] != "IKBdemo1" {
		t.Errorf("v6 add scalars = %v, want type 1 / IKBdemo1", call.Param)
	}
	gv, _ := call.Param["group_value"].([]any)
	if len(gv) != 1 || gv[0].(map[string]any)["ipv6"] != "fd00::/8" {
		t.Errorf("v6 group_value = %v, want [{ipv6:fd00::/8,comment:\"\"}]", call.Param["group_value"])
	}

	if err := EditIpv6Group(api, "demo", "fd01::/16", 0, 31); err != nil {
		t.Fatalf("EditIpv6Group: %v", err)
	}
	if got := f.lastCall().Param["id"]; got != float64(31) {
		t.Errorf("v6 edit id = %v, want 31", got)
	}

	gotMap, err := GetIpGroupMap(api, "demo")
	if err != nil {
		t.Fatalf("GetIpGroupMap: %v", err)
	}
	if len(gotMap) != 2 || gotMap[1] != 21 || gotMap[2] != 22 {
		t.Errorf("GetIpGroupMap = %v, want {1:21, 2:22}", gotMap)
	}
	trunkMap, err := GetIpGroupMap(api, "trunk")
	if err != nil {
		t.Fatalf("GetIpGroupMap(trunk): %v", err)
	}
	if len(trunkMap) != 1 || trunkMap[3] != 25 {
		t.Errorf("trunk map = %v, want {3:25} via trailing-digit fallback", trunkMap)
	}
	v6Map, err := GetIpv6GroupMap(api, "demo")
	if err != nil {
		t.Fatalf("GetIpv6GroupMap: %v", err)
	}
	if len(v6Map) != 1 || v6Map[1] != 31 {
		t.Errorf("GetIpv6GroupMap = %v, want {1:31}", v6Map)
	}

	withName, err := GetIpGroupMapWithName(api, "demo")
	if err != nil {
		t.Fatalf("GetIpGroupMapWithName: %v", err)
	}
	if len(withName) != 2 || withName[1].ID != 21 || withName[1].Name != "IKBdemo1" || withName[2].ID != 22 {
		t.Errorf("GetIpGroupMapWithName = %+v, want {1:{21,IKBdemo1}, 2:{22,IKBdemo2}}", withName)
	}

	exact, err := ResolveRuleReferenceIpGroupNames(api, "home")
	if err != nil {
		t.Fatalf("ResolveRuleReferenceIpGroupNames(home): %v", err)
	}
	if len(exact) != 1 || exact[0] != "home" {
		t.Errorf("resolve(home) = %v, want exact [home] only (managed IKBhome1 must not leak)", exact)
	}
	managed, err := ResolveRuleReferenceIpGroupNames(api, "demo")
	if err != nil {
		t.Fatalf("ResolveRuleReferenceIpGroupNames(demo): %v", err)
	}
	if len(managed) != 2 || managed[0] != "IKBdemo1" || managed[1] != "IKBdemo2" {
		t.Errorf("resolve(demo) = %v, want [IKBdemo1 IKBdemo2]", managed)
	}
	empty, err := ResolveRuleReferenceIpGroupNames(api, "  ")
	if err != nil {
		t.Fatalf("ResolveRuleReferenceIpGroupNames(blank): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("resolve(blank) = %v, want empty", empty)
	}
	none, err := ResolveRuleReferenceIpGroupNames(api, "nope")
	if err != nil {
		t.Fatalf("ResolveRuleReferenceIpGroupNames(nope): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("resolve(nope) = %v, want empty", none)
	}

	names, err := GetAllIkuaiBypassIpGroupNamesByName(api, "demo")
	if err != nil {
		t.Fatalf("GetAllIkuaiBypassIpGroupNamesByName: %v", err)
	}
	if len(names) != 2 || names[0] != "IKBdemo1" || names[1] != "IKBdemo2" {
		t.Errorf("names by name = %v, want [IKBdemo1 IKBdemo2]", names)
	}
}

// TestRouteObjectClean v4/v6 清理循环只删受管分片，R?? 后缀名因不含 cleanTag 而保留。
// TestRouteObjectClean: the v4/v6 clean loops drop only managed shards whose
// name/comment matches the clean tag; the R??-suffixed name survives (no containment).
func TestRouteObjectClean(t *testing.T) {
	f := newFakeIkuai(t, map[string][]map[string]any{FUNC_NAME_ROUTE_OBJECT: routeObjectRows()})
	api := f.client()

	if err := DelIkuaiBypassIpGroup(api, "demo"); err != nil {
		t.Fatalf("DelIkuaiBypassIpGroup: %v", err)
	}
	dels := f.callsFor(FUNC_NAME_ROUTE_OBJECT, "del")
	if len(dels) != 1 {
		t.Fatalf("v4 del calls = %d, want 1", len(dels))
	}
	if dels[0].Param["id"] != "21,22" {
		t.Errorf("v4 del ids = %v, want 21,22 (R??/home/user survive)", dels[0].Param["id"])
	}

	if err := DelIkuaiBypassIpv6Group(api, "demo"); err != nil {
		t.Fatalf("DelIkuaiBypassIpv6Group: %v", err)
	}
	dels = f.callsFor(FUNC_NAME_ROUTE_OBJECT, "del")
	if len(dels) != 2 || dels[1].Param["id"] != "31" {
		t.Errorf("v6 del calls = %+v, want second del with id 31", dels)
	}
	if remain := f.rowsOf(FUNC_NAME_ROUTE_OBJECT); len(remain) != 4 {
		t.Errorf("remaining rows = %d, want 4 (trunk/home/IKBhome/user-group kept)", len(remain))
	}
}

// streamDomainRows 域名分流种子：51/55 同名验证 insert 后见覆盖、54 无尾缀不可解析、
// 52 非受管被过滤。
// Stream-domain seeds: 51/55 share a tagname (insert keeps the last), 54 lacks a
// numeric suffix (unparsable), 52 is unmanaged and filtered out.
func streamDomainRows() []map[string]any {
	weekly := func() map[string]any {
		return map[string]any{"custom": []any{map[string]any{"type": "weekly", "weekdays": "1234567",
			"start_time": "00:00", "end_time": "23:59", "comment": ""}}, "object": []any{}}
	}
	return []map[string]any{
		{"id": 51, "enabled": "yes", "tagname": "IKBdemo1", "interface": "wan1", "comment": "IkuaiBypass",
			"src_addr": map[string]any{"custom": []any{"192.168.1.0/24"}, "object": []any{map[string]any{"type": 0, "gid": "IPGP41", "gp_name": "IKBhome1"}}},
			"domain":   map[string]any{"custom": []any{"a.com", "b.com"}, "object": []any{}},
			"time":     weekly(),
			"prio":     31},
		{"id": 52, "enabled": "yes", "tagname": "other-rule", "interface": "wan1", "comment": "",
			"src_addr": map[string]any{"custom": []any{}, "object": []any{}},
			"domain":   map[string]any{"custom": []any{"x.com"}, "object": []any{}},
			"time":     weekly()},
		{"id": 54, "enabled": "yes", "tagname": "IKBdemo", "interface": "wan1", "comment": "",
			"src_addr": map[string]any{"custom": []any{}, "object": []any{}},
			"domain":   map[string]any{"custom": []any{}, "object": []any{}},
			"time":     weekly()},
		{"id": 55, "enabled": "yes", "tagname": "IKBdemo1", "interface": "wan2", "comment": "IkuaiBypass-2",
			"src_addr": map[string]any{"custom": []any{}, "object": []any{}},
			"domain":   map[string]any{"custom": []any{}, "object": []any{}},
			"time":     weekly()},
	}
}

// TestStreamDomainSpec 覆盖 stream_domain.rs：固定字段请求体（enabled/prio 31/周 1234567
// 全天/comment=NewComment）、resolve_src_addrs 的 custom/object 分离与 IPGP<id> 对象结构、
// 分组引用精确名优先、无匹配报错、show 展平与 time 还原、GetMap 后见覆盖、清理循环。
// TestStreamDomainSpec covers stream_domain.rs: the fixed request fields
// (enabled/prio 31/weekly 1234567 all-day/comment=NewComment), resolve_src_addrs
// custom/object separation with the IPGP<id> object shape, exact-first group
// references, the no-match error, show flattening with time restore, the
// last-wins GetMap and the clean loop.
func TestStreamDomainSpec(t *testing.T) {
	f := newFakeIkuai(t, map[string][]map[string]any{
		FUNC_NAME_ROUTE_OBJECT: {
			{"id": 41, "group_name": "IKBhome1", "type": 0, "comment": "",
				"group_value": []any{map[string]any{"ip": "10.0.0.0/8", "comment": ""}}},
			{"id": 23, "group_name": "home", "type": 0, "comment": "",
				"group_value": []any{map[string]any{"ip": "192.168.0.0/16", "comment": ""}}},
		},
		FUNC_NAME_STREAM_DOMAIN: streamDomainRows(),
	})
	api := f.client()

	spec := StreamDomainSpec{
		Iface: "wan1", Tag: "demo", Index: 1,
		SrcAddr: "192.168.1.0/24, IKBhome1",
		Domains: " example.com, ,baidu.com ",
	}
	if err := AddStreamDomain(api, spec); err != nil {
		t.Fatalf("AddStreamDomain: %v", err)
	}
	call := f.lastCall()
	if call.FuncName != FUNC_NAME_STREAM_DOMAIN || call.Action != "add" {
		t.Fatalf("call = %s/%s, want stream_domain/add", call.FuncName, call.Action)
	}
	if len(call.Param) != 8 {
		t.Fatalf("add param keys = %v, want exactly 8 fixed fields", call.Param)
	}
	if call.Param["enabled"] != "yes" || call.Param["prio"] != float64(31) || call.Param["comment"] != "IkuaiBypass" {
		t.Errorf("enabled/prio/comment = %v/%v/%v", call.Param["enabled"], call.Param["prio"], call.Param["comment"])
	}
	if call.Param["tagname"] != "IKBdemo2" || call.Param["interface"] != "wan1" {
		t.Errorf("tagname/interface = %v/%v, want IKBdemo2/wan1", call.Param["tagname"], call.Param["interface"])
	}
	srcAddr, _ := call.Param["src_addr"].(map[string]any)
	srcCustom, _ := srcAddr["custom"].([]any)
	srcObjects, _ := srcAddr["object"].([]any)
	if len(srcCustom) != 1 || srcCustom[0] != "192.168.1.0/24" {
		t.Errorf("src custom = %v, want [192.168.1.0/24]", srcCustom)
	}
	if len(srcObjects) != 1 || srcObjects[0].(map[string]any)["gid"] != "IPGP41" ||
		srcObjects[0].(map[string]any)["gp_name"] != "IKBhome1" ||
		srcObjects[0].(map[string]any)["type"] != float64(0) {
		t.Errorf("src objects = %v, want [{type:0,gid:IPGP41,gp_name:IKBhome1}]", srcObjects)
	}
	domain, _ := call.Param["domain"].(map[string]any)
	if dCustom, _ := domain["custom"].([]any); len(dCustom) != 2 || dCustom[0] != "example.com" || dCustom[1] != "baidu.com" {
		t.Errorf("domain custom = %v, want [example.com baidu.com]", dCustom)
	}
	if dObj, _ := domain["object"].([]any); len(dObj) != 0 {
		t.Errorf("domain object = %v, want empty", dObj)
	}
	timeBlock, _ := call.Param["time"].(map[string]any)
	timeCustom, _ := timeBlock["custom"].([]any)
	if len(timeCustom) != 1 {
		t.Fatalf("time.custom = %v, want one weekly entry", timeBlock["custom"])
	}
	weekly, _ := timeCustom[0].(map[string]any)
	if weekly["type"] != "weekly" || weekly["weekdays"] != "1234567" ||
		weekly["start_time"] != "00:00" || weekly["end_time"] != "23:59" || weekly["comment"] != "" {
		t.Errorf("time.custom[0] = %v", weekly)
	}

	optSpec := StreamDomainSpec{Iface: "wan2", Tag: "demo", Index: 0, SrcAddrOptIpgroup: "home", Domains: "a.com"}
	if err := AddStreamDomain(api, optSpec); err != nil {
		t.Fatalf("AddStreamDomain(opt ipgroup): %v", err)
	}
	call = f.lastCall()
	srcAddr, _ = call.Param["src_addr"].(map[string]any)
	if srcCustom, _ = srcAddr["custom"].([]any); len(srcCustom) != 0 {
		t.Errorf("opt-path custom = %v, want empty", srcCustom)
	}
	if srcObjects, _ = srcAddr["object"].([]any); len(srcObjects) != 1 ||
		srcObjects[0].(map[string]any)["gid"] != "IPGP23" || srcObjects[0].(map[string]any)["gp_name"] != "home" {
		t.Errorf("opt-path objects = %v, want [{gid:IPGP23,gp_name:home}] (exact name first)", srcObjects)
	}

	err := AddStreamDomain(api, StreamDomainSpec{Iface: "wan1", Tag: "demo", SrcAddrOptIpgroup: "nope", Domains: "a.com"})
	if err == nil || !strings.Contains(err.Error(), "no matching source IP groups found for stream-domain reference: nope") {
		t.Fatalf("unresolved opt ipgroup error = %v", err)
	}

	editSpec := StreamDomainSpec{Iface: "wan1", Tag: "demo", Index: 0, SrcAddr: "192.168.1.0/24", Domains: "a.com"}
	if err := EditStreamDomain(api, editSpec, 51); err != nil {
		t.Fatalf("EditStreamDomain: %v", err)
	}
	if got := f.lastCall().Param["id"]; got != float64(51) {
		t.Errorf("edit id = %v, want 51", got)
	}

	rows, err := ShowStreamDomainByTagName(api, "demo")
	if err != nil {
		t.Fatalf("ShowStreamDomainByTagName: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("show rows = %d, want 3 (51/54/55 managed)", len(rows))
	}
	first := rows[0]
	if first.ID != 51 || first.Enabled != "yes" || first.Interface != "wan1" || first.Comment != "IkuaiBypass" {
		t.Errorf("row 51 = %+v", first)
	}
	if first.SrcAddr != "192.168.1.0/24,IKBhome1" {
		t.Errorf("row 51 src = %q, want custom,then-object join", first.SrcAddr)
	}
	if first.Domain != "a.com,b.com" || first.Week != "1234567" || first.Time != "00:00-23:59" {
		t.Errorf("row 51 domain/week/time = %q/%q/%q", first.Domain, first.Week, first.Time)
	}
	if len(f.lastCall().Param) != 2 || f.lastCall().Param["TYPE"] != "total,data" || f.lastCall().Param["limit"] != "0,1000" {
		t.Errorf("show param = %v, want TYPE/limit only", f.lastCall().Param)
	}

	domainMap, err := GetStreamDomainMap(api, "demo")
	if err != nil {
		t.Fatalf("GetStreamDomainMap: %v", err)
	}
	if len(domainMap) != 1 || domainMap[1] != 55 {
		t.Errorf("GetStreamDomainMap = %v, want {1:55} (insert keeps the last row)", domainMap)
	}

	if err := DelStreamDomain(api, "51,52"); err != nil {
		t.Fatalf("DelStreamDomain: %v", err)
	}
	if got := f.lastCall().Param["id"]; got != "51,52" {
		t.Errorf("del id = %v, want 51,52", got)
	}

	if err := DelStreamDomainAll(api, "demo"); err != nil {
		t.Fatalf("DelStreamDomainAll: %v", err)
	}
	dels := f.callsFor(FUNC_NAME_STREAM_DOMAIN, "del")
	if len(dels) != 2 || dels[1].Param["id"] != "54,55" {
		t.Fatalf("DelStreamDomainAll del calls = %+v, want second batch 54,55", dels)
	}
	if remain := f.rowsOf(FUNC_NAME_STREAM_DOMAIN); len(remain) != 0 {
		t.Errorf("remaining rows = %v, want empty after 51/52 manual + 54/55 batch", remain)
	}
}

// TestStreamIpPortSpec 覆盖 stream_ipport.rs：默认 protocol tcp+udp、area_code/dst_type 空、
// tagname 非分片、forward_type 解析失败回退 0、src/dst custom+object 分离、show 展平。
// TestStreamIpPortSpec covers stream_ipport.rs: the tcp+udp default protocol,
// empty area_code/dst_type, the non-indexed tagname, forward_type parse fallback
// to 0, src/dst custom+object separation and show flattening.
func TestStreamIpPortSpec(t *testing.T) {
	f := newFakeIkuai(t, map[string][]map[string]any{
		FUNC_NAME_ROUTE_OBJECT: {
			{"id": 41, "group_name": "IKBhome1", "type": 0, "comment": "",
				"group_value": []any{map[string]any{"ip": "10.0.0.0/8", "comment": ""}}},
		},
		FUNC_NAME_STREAM_IPPORT: {
			{"id": 61, "enabled": "yes", "tagname": "IKBdemo1", "interface": "wan1", "nexthop": "10.0.0.1",
				"comment": "IkuaiBypass", "iface_band": 2, "mode": 1, "protocol": "tcp+udp", "type": 1,
				"src_addr":     map[string]any{"custom": []any{"192.168.1.0/24"}, "object": []any{}},
				"dst_addr":     map[string]any{"custom": []any{"8.8.8.8"}, "object": []any{map[string]any{"type": 0, "gid": "IPGP41", "gp_name": "IKBhome1"}}},
				"src_addr_inv": 1, "dst_addr_inv": 0,
				"time": map[string]any{"custom": []any{map[string]any{"type": "weekly", "weekdays": "1234567",
					"start_time": "08:00", "end_time": "22:00", "comment": ""}}, "object": []any{}}},
			{"id": 62, "enabled": "yes", "tagname": "other", "interface": "wan1", "nexthop": "",
				"comment": "", "iface_band": 0, "mode": 0, "protocol": "tcp", "type": 0,
				"src_addr": map[string]any{"custom": []any{}, "object": []any{}},
				"dst_addr": map[string]any{"custom": []any{}, "object": []any{}},
				"time":     map[string]any{"custom": []any{}, "object": []any{}}},
		},
	})
	api := f.client()

	spec := StreamIpPortSpec{
		ForwardType: "1", Iface: "wan1", DstAddr: "8.8.8.8", SrcAddr: "192.168.1.0/24, IKBhome1",
		SrcAddrInv: 1, Nexthop: "10.0.0.1", Tag: "demo", DstAddrInv: 0,
		Prio: 5, Mode: 1, IfaceBand: 0, Protocol: "",
	}
	if err := AddStreamIpPort(api, spec); err != nil {
		t.Fatalf("AddStreamIpPort: %v", err)
	}
	call := f.lastCall()
	if call.FuncName != FUNC_NAME_STREAM_IPPORT || call.Action != "add" {
		t.Fatalf("call = %s/%s, want stream_ipport/add", call.FuncName, call.Action)
	}
	if len(call.Param) != 19 {
		t.Fatalf("add param keys = %d (%v), want exactly 19 fixed fields", len(call.Param), call.Param)
	}
	if call.Param["protocol"] != "tcp+udp" {
		t.Errorf("default protocol = %v, want tcp+udp", call.Param["protocol"])
	}
	if call.Param["area_code"] != "" || call.Param["dst_type"] != "" {
		t.Errorf("area_code/dst_type = %v/%v, want empty", call.Param["area_code"], call.Param["dst_type"])
	}
	if call.Param["tagname"] != "IKBdemo" {
		t.Errorf("tagname = %v, want IKBdemo (non-indexed)", call.Param["tagname"])
	}
	if call.Param["type"] != float64(1) || call.Param["mode"] != float64(1) || call.Param["prio"] != float64(5) {
		t.Errorf("type/mode/prio = %v/%v/%v", call.Param["type"], call.Param["mode"], call.Param["prio"])
	}
	if call.Param["src_addr_inv"] != float64(1) || call.Param["dst_addr_inv"] != float64(0) {
		t.Errorf("src/dst inv = %v/%v", call.Param["src_addr_inv"], call.Param["dst_addr_inv"])
	}
	srcAddr, _ := call.Param["src_addr"].(map[string]any)
	if custom, _ := srcAddr["custom"].([]any); len(custom) != 1 || custom[0] != "192.168.1.0/24" {
		t.Errorf("src custom = %v", custom)
	}
	if objects, _ := srcAddr["object"].([]any); len(objects) != 1 || objects[0].(map[string]any)["gid"] != "IPGP41" {
		t.Errorf("src objects = %v, want [IPGP41 ref]", objects)
	}
	dstAddr, _ := call.Param["dst_addr"].(map[string]any)
	if custom, _ := dstAddr["custom"].([]any); len(custom) != 1 || custom[0] != "8.8.8.8" {
		t.Errorf("dst custom = %v", custom)
	}
	if objects, _ := dstAddr["object"].([]any); len(objects) != 0 {
		t.Errorf("dst objects = %v, want empty", objects)
	}
	for _, key := range []string{"src_port", "dst_port"} {
		block, _ := call.Param[key].(map[string]any)
		if len(block) != 2 || len(block["custom"].([]any)) != 0 || len(block["object"].([]any)) != 0 {
			t.Errorf("%s = %v, want empty custom+object", key, block)
		}
	}

	explicit := spec
	explicit.Protocol = "tcp"
	explicit.ForwardType = "abc"
	if err := AddStreamIpPort(api, explicit); err != nil {
		t.Fatalf("AddStreamIpPort(explicit): %v", err)
	}
	call = f.lastCall()
	if call.Param["protocol"] != "tcp" {
		t.Errorf("explicit protocol = %v, want tcp", call.Param["protocol"])
	}
	if call.Param["type"] != float64(0) {
		t.Errorf("unparsable forward_type = %v, want 0", call.Param["type"])
	}

	if err := EditStreamIpPort(api, spec, 61); err != nil {
		t.Fatalf("EditStreamIpPort: %v", err)
	}
	if got := f.lastCall().Param["id"]; got != float64(61) {
		t.Errorf("edit id = %v, want 61", got)
	}

	if err := DelStreamIpPort(api, "61"); err != nil {
		t.Fatalf("DelStreamIpPort: %v", err)
	}

	f2 := newFakeIkuai(t, map[string][]map[string]any{
		FUNC_NAME_STREAM_IPPORT: {
			{"id": 61, "enabled": "yes", "tagname": "IKBdemo1", "interface": "wan1", "nexthop": "10.0.0.1",
				"comment": "IkuaiBypass", "iface_band": 2, "mode": 1, "protocol": "tcp+udp", "type": 1,
				"src_addr":     map[string]any{"custom": []any{"192.168.1.0/24"}, "object": []any{}},
				"dst_addr":     map[string]any{"custom": []any{"8.8.8.8"}, "object": []any{map[string]any{"type": 0, "gid": "IPGP41", "gp_name": "IKBhome1"}}},
				"src_addr_inv": 1, "dst_addr_inv": 0,
				"time": map[string]any{"custom": []any{map[string]any{"type": "weekly", "weekdays": "1234567",
					"start_time": "08:00", "end_time": "22:00", "comment": ""}}, "object": []any{}}},
			{"id": 62, "enabled": "yes", "tagname": "other", "interface": "wan1", "nexthop": "",
				"comment": "", "iface_band": 0, "mode": 0, "protocol": "tcp", "type": 0,
				"src_addr": map[string]any{"custom": []any{}, "object": []any{}},
				"dst_addr": map[string]any{"custom": []any{}, "object": []any{}},
				"time":     map[string]any{"custom": []any{}, "object": []any{}}},
		},
	})
	api2 := f2.client()
	rows, err := ShowStreamIpPortByTagName(api2, "demo")
	if err != nil {
		t.Fatalf("ShowStreamIpPortByTagName: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != 61 {
		t.Fatalf("show rows = %+v, want single id 61", rows)
	}
	got := rows[0]
	if got.Protocol != "tcp+udp" || got.Nexthop != "10.0.0.1" || got.IfaceBand != 2 ||
		got.Mode != 1 || got.Type != 1 || got.SrcAddrInv != 1 || got.DstAddrInv != 0 {
		t.Errorf("row scalars = %+v", got)
	}
	if got.SrcAddr != "192.168.1.0/24" || got.DstAddr != "8.8.8.8,IKBhome1" {
		t.Errorf("row addrs = %q/%q", got.SrcAddr, got.DstAddr)
	}
	if got.Week != "1234567" || got.Time != "08:00-22:00" {
		t.Errorf("row week/time = %q/%q", got.Week, got.Time)
	}

	ipportMap, err := GetStreamIpPortMap(api2, "demo")
	if err != nil {
		t.Fatalf("GetStreamIpPortMap: %v", err)
	}
	if len(ipportMap) != 1 || ipportMap["IKBdemo1"] != 61 {
		t.Errorf("GetStreamIpPortMap = %v, want {IKBdemo1:61}", ipportMap)
	}

	if err := DelIkuaiBypassStreamIpPort(api2, "demo"); err != nil {
		t.Fatalf("DelIkuaiBypassStreamIpPort: %v", err)
	}
	dels := f2.callsFor(FUNC_NAME_STREAM_IPPORT, "del")
	if len(dels) != 1 || dels[0].Param["id"] != "61" {
		t.Fatalf("del calls = %+v, want single batch id 61", dels)
	}
	if remain := f2.rowsOf(FUNC_NAME_STREAM_IPPORT); len(remain) != 1 || remain[0]["id"] != 62 {
		t.Errorf("remaining rows = %v, want only unmanaged id 62", remain)
	}
}

// TestIsManaged 反向用例来自 types.rs L6-17 标记清单与 clean.rs L42-47：
// `joyanhui/ikuai-bypass-2` 这类存量备注必须命中受管，否则清理会漏删。
// TestIsManaged reverse cases come from the marker list of types.rs L6-17 and
// clean.rs L42-47: legacy comments like `joyanhui/ikuai-bypass-2` must be
// recognized as managed, otherwise cleanup would miss them.
func TestIsManaged(t *testing.T) {
	cases := []struct {
		comment, name string
		want          bool
	}{
		{"IkuaiBypass", "", true},
		{"joyanhui/ikuai-bypass-2", "", true},
		{"IKUAI_BYPASS_demo", "", true},
		{"别家的备注", "", false},
		{"", "IKBdemo1", true},
		{"别家的备注", "IKBdemo1", true},
		{"别家的备注", "user-rule", false},
	}
	for _, tc := range cases {
		if got := IsManaged(tc.comment, tc.name); got != tc.want {
			t.Errorf("IsManaged(%q, %q) = %v, want %v", tc.comment, tc.name, got, tc.want)
		}
	}
}

// TestMatchCleanTag 对齐 clean.rs L13-35/L49-52：空 tag 与非受管恒不删，
// cleanAll 删全部受管，否则按等于/包含匹配 legacy 备注或 current 名字。
// TestMatchCleanTag mirrors clean.rs L13-35/L49-52: an empty tag or an unmanaged
// rule never deletes; cleanAll deletes every managed rule; otherwise the legacy
// comment or current name must equal or contain the tag.
func TestMatchCleanTag(t *testing.T) {
	cases := []struct {
		cleanTag, legacy, current string
		want                      bool
	}{
		{"", "IkuaiBypass", "IKBdemo1", false},
		{"demo", "joyanhui/ikuai-bypass-demo", "", true},
		{"demo", "别家的备注", "我的规则", false},
		{"cleanAll", "joyanhui/ikuai-bypass-2", "", true},
		{"cleanAll", "别家的备注", "我的规则", false},
		{"demo", "IkuaiBypass", "IKBdemo1", true},
		{"demo", "IkuaiBypass", "other-managed-by-comment", false},
		{" demo ", "IkuaiBypass-2", "IKBdemo2", true},
		{"joyanhui/ikuai-bypass", "joyanhui/ikuai-bypass", "user-rule", true},
		{"IKBdemo1", "", "IKBdemo1", true},
	}
	for _, tc := range cases {
		if got := MatchCleanTag(tc.cleanTag, tc.legacy, tc.current); got != tc.want {
			t.Errorf("MatchCleanTag(%q, %q, %q) = %v, want %v",
				tc.cleanTag, tc.legacy, tc.current, got, tc.want)
		}
	}
}

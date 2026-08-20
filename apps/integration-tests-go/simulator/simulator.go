// simulator.go Go 版爱快模拟器（行为对齐 rust_archive/apps-integration-tests/src/ikuai_simulator/mod.rs）。
// 以 httptest 提供真实 HTTP 服务：/Action/login 校验 md5/base64 双字段并发会话
// cookie；/Action/call 按 func_name+action 分发内存存储（custom_isp /
// route_object(type 0/1) / stream_domain / stream_ipport），show 支持 FILTER1
// 的 "type,=,N" 过滤；每次 login/call 追加一条 JSONL 审计记录供断言。
// A Go iKuai simulator (behaviorally aligned with the Rust simulator in
// rust_archive/apps-integration-tests/src/ikuai_simulator/mod.rs). It serves real HTTP via
// httptest: /Action/login validates the md5/base64 pair and issues a session
// cookie; /Action/call dispatches on func_name+action over in-memory stores
// (custom_isp / route_object(type 0/1) / stream_domain / stream_ipport) with
// the FILTER1 "type,=,N" filter on show; every login/call appends one JSONL
// audit record for assertions.
package simulator

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
)

// 爱快功能名常量，与 internal/ikuai/types.go 的契约一致（此处独立定义，
// 保持模拟器为语言无关的 HTTP 契约件，不反向依赖被测实现）。
// iKuai func_name constants matching the contract of internal/ikuai/types.go
// (declared locally so the simulator stays a language-agnostic HTTP contract
// piece with no dependency on the implementation under test).
const (
	funcNameCustomIsp    = "custom_isp"
	funcNameRouteObject  = "route_object"
	funcNameStreamDomain = "stream_domain"
	funcNameStreamIpPort = "stream_ipport"
)

// AuditEntry 一次 login / call 的审计记录；Param 保留请求原样（数字以
// json.Number 透传），JSONL 断言与状态断言共用。
// AuditEntry is the audit record of one login / call; Param keeps the request
// verbatim (numbers pass through as json.Number) for JSONL and state assertions.
type AuditEntry struct {
	Seq      int64          `json:"seq"`
	Kind     string         `json:"kind"`
	FuncName string         `json:"func_name,omitempty"`
	Action   string         `json:"action,omitempty"`
	Param    map[string]any `json:"param,omitempty"`
	Code     int64          `json:"code"`
	Message  string         `json:"message"`
}

// Simulator 内存态爱快模拟器；所有状态由单一互斥锁保护，天然支持
// 二进制子进程与测试断言客户端的交错访问。
// Simulator is the in-memory iKuai simulator; one mutex guards all state so
// the binary under test and the asserting test client can interleave freely.
type Simulator struct {
	srv  *httptest.Server
	mu   sync.Mutex
	user struct {
		username string
		password string
	}
	nextID      int64
	nextSession int64
	sessions    map[string]struct{}
	failures    map[string]int
	audit       []AuditEntry

	customIsps    []customIspRecord
	routeObjects  []routeObjectRecord
	streamDomains []streamDomainRecord
	streamIpPorts []streamIpPortRecord
}

// Start 用给定凭据启动模拟器（监听 127.0.0.1 随机端口）。
// Start boots the simulator with the given credentials (random 127.0.0.1 port).
func Start(username, password string) *Simulator {
	s := &Simulator{
		sessions: make(map[string]struct{}),
		failures: make(map[string]int),
	}
	s.user.username = username
	s.user.password = password
	// 初始即空切片而非 nil：show 的 results.data 必须序列化为 []，
	// 客户端把 null 判为不可解码（decodeResultsData 的 data must be an array）。
	// Start with empty (not nil) slices: results.data must serialize as []
	// because the client treats null as undecodable (decodeResultsData's
	// "data must be an array").
	s.customIsps = make([]customIspRecord, 0)
	s.routeObjects = make([]routeObjectRecord, 0)
	s.streamDomains = make([]streamDomainRecord, 0)
	s.streamIpPorts = make([]streamIpPortRecord, 0)
	mux := http.NewServeMux()
	mux.HandleFunc("/Action/login", s.handleLogin)
	mux.HandleFunc("/Action/call", s.handleCall)
	s.srv = httptest.NewServer(mux)
	return s
}

// URL 返回模拟器基地址（如 http://127.0.0.1:port）。
// URL returns the simulator base URL (e.g. http://127.0.0.1:port).
func (s *Simulator) URL() string {
	return s.srv.URL
}

// Close 停止模拟器。
// Close shuts the simulator down.
func (s *Simulator) Close() {
	s.srv.Close()
}

// InjectFailure 注入失败：接下来 count 次 (funcName, action) 调用直接返回
// code 1 且不改动状态（用于分片失败路径等补测）。
// InjectFailure injects failures: the next `count` calls matching
// (funcName, action) return code 1 without mutating state (used by the
// chunk-failure-path tests).
func (s *Simulator) InjectFailure(funcName, action string, count int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[funcName+"\x00"+action] += count
}

// Audit 返回审计记录快照。
// Audit returns a snapshot of the audit trail.
func (s *Simulator) Audit() []AuditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AuditEntry, len(s.audit))
	copy(out, s.audit)
	return out
}

// Calls 返回指定 func_name+action 的审计记录（按发生顺序）。
// Calls returns the audit entries of one func_name+action in order.
func (s *Simulator) Calls(funcName, action string) []AuditEntry {
	var out []AuditEntry
	for _, e := range s.Audit() {
		if e.FuncName == funcName && e.Action == action {
			out = append(out, e)
		}
	}
	return out
}

// AuditJSONL 把审计轨迹渲染为 JSONL 文本（每行一个 JSON 对象）。
// AuditJSONL renders the audit trail as JSONL text (one JSON object per line).
func (s *Simulator) AuditJSONL() string {
	var sb strings.Builder
	for _, e := range s.Audit() {
		b, err := json.Marshal(e)
		if err != nil {
			continue
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// ---- 记录形状（show 行的 JSON 键即 Go 客户端解码契约） ----
// ---- Record shapes (the show-row JSON keys are the Go client's decode contract) ----

type customIspRecord struct {
	Ipgroup string `json:"ipgroup"`
	Time    string `json:"time"`
	ID      int64  `json:"id"`
	Comment string `json:"comment"`
	Name    string `json:"name"`
}

type routeObjectRecord struct {
	ID         int64               `json:"id"`
	GroupName  string              `json:"group_name"`
	Type       int64               `json:"type"`
	GroupValue []map[string]string `json:"group_value"`
	Comment    string              `json:"comment"`
}

type streamDomainRecord struct {
	ID        int64     `json:"id"`
	Enabled   string    `json:"enabled"`
	Tagname   string    `json:"tagname"`
	Interface string    `json:"interface"`
	Comment   string    `json:"comment"`
	SrcAddr   addrBlock `json:"src_addr"`
	Domain    addrBlock `json:"domain"`
	Time      timeBlock `json:"time"`
}

type streamIpPortRecord struct {
	ID         int64     `json:"id"`
	Enabled    string    `json:"enabled"`
	Tagname    string    `json:"tagname"`
	Interface  string    `json:"interface"`
	Nexthop    string    `json:"nexthop"`
	Comment    string    `json:"comment"`
	IfaceBand  int64     `json:"iface_band"`
	Mode       int64     `json:"mode"`
	Protocol   string    `json:"protocol"`
	Type       int64     `json:"type"`
	SrcAddr    addrBlock `json:"src_addr"`
	DstAddr    addrBlock `json:"dst_addr"`
	SrcAddrInv int64     `json:"src_addr_inv"`
	DstAddrInv int64     `json:"dst_addr_inv"`
	Time       timeBlock `json:"time"`
}

// addrBlock 保留地址块 custom/object 的原始 JSON 原样回显；缺省补 "[]"，
// 与 Rust 模拟器 json!([]) 的缺省语义一致。
// addrBlock keeps the raw custom/object JSON for verbatim echo; a missing side
// defaults to "[]"，matching the json!([]) default of the Rust simulator.
type addrBlock struct {
	Custom json.RawMessage `json:"custom"`
	Object json.RawMessage `json:"object"`
}

func newAddrBlock(param map[string]any, key string) (addrBlock, error) {
	raw, ok := param[key].(map[string]any)
	if !ok {
		return addrBlock{}, fmt.Errorf("missing %s", key)
	}
	blank := json.RawMessage("[]")
	block := addrBlock{Custom: blank, Object: blank}
	if v, ok := raw["custom"]; ok {
		if b, err := json.Marshal(v); err == nil {
			block.Custom = b
		}
	}
	if v, ok := raw["object"]; ok {
		if b, err := json.Marshal(v); err == nil {
			block.Object = b
		}
	}
	return block, nil
}

type timeCustom struct {
	Weekdays  string `json:"weekdays"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
	Type      string `json:"type"`
	Comment   string `json:"comment"`
}

type timeBlock struct {
	Custom []timeCustom    `json:"custom"`
	Object json.RawMessage `json:"object"`
}

func newTimeBlock(param map[string]any) (timeBlock, error) {
	raw, ok := param["time"].(map[string]any)
	if !ok {
		return timeBlock{}, fmt.Errorf("missing time")
	}
	items, ok := raw["custom"].([]any)
	if !ok {
		return timeBlock{}, fmt.Errorf("missing time.custom")
	}
	block := timeBlock{Custom: make([]timeCustom, 0, len(items)), Object: json.RawMessage("[]")}
	for _, item := range items {
		m, _ := item.(map[string]any)
		block.Custom = append(block.Custom, timeCustom{
			Weekdays:  optStr(m, "weekdays"),
			StartTime: optStr(m, "start_time"),
			EndTime:   optStr(m, "end_time"),
			Type:      optStr(m, "type"),
			Comment:   optStr(m, "comment"),
		})
	}
	if v, ok := raw["object"]; ok {
		if b, err := json.Marshal(v); err == nil {
			block.Object = b
		}
	}
	return block, nil
}

// ---- HTTP 信封与响应 ----
// ---- HTTP envelope and responses ----

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

type apiError struct {
	Code    int64  `json:"code"`
	Message string `json:"message"`
}

func okBody() any {
	return apiError{Code: 0, Message: "Success"}
}

func rowidBody(rowid int64) any {
	return struct {
		Code    int64  `json:"code"`
		Message string `json:"message"`
		RowID   int64  `json:"rowid"`
	}{Code: 0, Message: "Success", RowID: rowid}
}

func showBody(rows any) any {
	return struct {
		Code    int64  `json:"code"`
		Message string `json:"message"`
		Results struct {
			Total int `json:"total"`
			Data  any `json:"data"`
		} `json:"results"`
	}{Code: 0, Message: "Success", Results: struct {
		Total int `json:"total"`
		Data  any `json:"data"`
	}{Total: countRows(rows), Data: rows}}
}

// countRows 仅用于填充 show 的 total 字段；show 路径传入的均为切片。
// countRows only fills the show total field; every show path passes a slice.
func countRows(rows any) int {
	switch t := rows.(type) {
	case []customIspRecord:
		return len(t)
	case []routeObjectRecord:
		return len(t)
	case []streamDomainRecord:
		return len(t)
	case []streamIpPortRecord:
		return len(t)
	default:
		return 0
	}
}

// ---- /Action/login ----

func (s *Simulator) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Passwd   string `json:"passwd"`
		Pass     string `json:"pass"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.appendAudit("login", "", "", nil, 1, "malformed login body")
		writeJSON(w, apiError{Code: 1, Message: "malformed login body"})
		return
	}

	s.mu.Lock()
	sum := md5.Sum([]byte(s.user.password))
	expectedMd5 := hex.EncodeToString(sum[:])
	expectedPass := base64.StdEncoding.EncodeToString([]byte("salt_11" + s.user.password))
	if req.Username != s.user.username || req.Passwd != expectedMd5 || req.Pass != expectedPass {
		s.appendAuditLocked("login", "", "", nil, 1, "login failed")
		s.mu.Unlock()
		writeJSON(w, apiError{Code: 1, Message: "login failed"})
		return
	}
	s.nextSession++
	token := fmt.Sprintf("sim-session-%d", s.nextSession)
	s.sessions[token] = struct{}{}
	s.appendAuditLocked("login", "", "", nil, 0, "Success")
	s.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     "sess_key",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
	})
	writeJSON(w, okBody())
}

// ---- /Action/call ----

func (s *Simulator) handleCall(w http.ResponseWriter, r *http.Request) {
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	var req struct {
		FuncName string         `json:"func_name"`
		Action   string         `json:"action"`
		Param    map[string]any `json:"param"`
	}
	if err := dec.Decode(&req); err != nil {
		s.appendAudit("call", "", "", nil, 1, "malformed call body")
		writeJSON(w, apiError{Code: 1, Message: "malformed call body"})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.validSessionLocked(r) {
		s.appendAuditLocked("call", req.FuncName, req.Action, req.Param, 1, "会话已过期")
		writeJSON(w, apiError{Code: 1, Message: "会话已过期"})
		return
	}

	key := req.FuncName + "\x00" + req.Action
	if s.failures[key] > 0 {
		s.failures[key]--
		msg := "injected failure: " + req.FuncName + "/" + req.Action
		s.appendAuditLocked("call", req.FuncName, req.Action, req.Param, 1, msg)
		writeJSON(w, apiError{Code: 1, Message: msg})
		return
	}

	body, errBody := s.dispatchLocked(req.FuncName, req.Action, req.Param)
	if errBody != nil {
		s.appendAuditLocked("call", req.FuncName, req.Action, req.Param, 1, errBody.Message)
		writeJSON(w, errBody)
		return
	}
	s.appendAuditLocked("call", req.FuncName, req.Action, req.Param, 0, "Success")
	writeJSON(w, body)
}

// dispatchLocked 按 func_name+action 分发；错误一律以 code 1 的 HTTP 200
// 响应呈现（对齐真机行为）。
// dispatchLocked routes on func_name+action; errors always surface as
// HTTP 200 with code 1 (mirroring the real device).
func (s *Simulator) dispatchLocked(funcName, action string, param map[string]any) (any, *apiError) {
	switch funcName {
	case funcNameCustomIsp:
		return s.dispatchCustomIsp(action, param)
	case funcNameRouteObject:
		return s.dispatchRouteObject(action, param)
	case funcNameStreamDomain:
		return s.dispatchStreamDomain(action, param)
	case funcNameStreamIpPort:
		return s.dispatchStreamIpPort(action, param)
	default:
		return nil, &apiError{Code: 1, Message: "unsupported call"}
	}
}

func (s *Simulator) validSessionLocked(r *http.Request) bool {
	for _, c := range r.Cookies() {
		if c.Name == "sess_key" {
			_, ok := s.sessions[c.Value]
			return ok
		}
	}
	return false
}

// ---- custom_isp ----

func (s *Simulator) dispatchCustomIsp(action string, param map[string]any) (any, *apiError) {
	switch action {
	case "show":
		return showBody(s.customIsps), nil
	case "add":
		name, err := reqStr(param, "name")
		if err != nil {
			return nil, toAPIError(err)
		}
		ipgroup, err := reqStr(param, "ipgroup")
		if err != nil {
			return nil, toAPIError(err)
		}
		comment, err := reqStr(param, "comment")
		if err != nil {
			return nil, toAPIError(err)
		}
		id := s.nextRowIDLocked()
		s.customIsps = append(s.customIsps, customIspRecord{
			ID: id, Name: name, Ipgroup: ipgroup, Comment: comment, Time: "",
		})
		return rowidBody(id), nil
	case "edit":
		id, err := reqI64(param, "id")
		if err != nil {
			return nil, toAPIError(err)
		}
		name, err := reqStr(param, "name")
		if err != nil {
			return nil, toAPIError(err)
		}
		ipgroup, err := reqStr(param, "ipgroup")
		if err != nil {
			return nil, toAPIError(err)
		}
		comment, err := reqStr(param, "comment")
		if err != nil {
			return nil, toAPIError(err)
		}
		for i := range s.customIsps {
			if s.customIsps[i].ID == id {
				s.customIsps[i].Name = name
				s.customIsps[i].Ipgroup = ipgroup
				s.customIsps[i].Comment = comment
				return rowidBody(id), nil
			}
		}
		return nil, &apiError{Code: 1, Message: "custom_isp id not found"}
	case "del":
		ids, err := reqIDCSV(param)
		if err != nil {
			return nil, toAPIError(err)
		}
		s.customIsps = retainExcept(s.customIsps, ids)
		return okBody(), nil
	default:
		return nil, &apiError{Code: 1, Message: "unsupported call"}
	}
}

// retainExcept 泛型删除辅助：保留 id 不在删除集合中的行。
// retainExcept is the generic delete helper: keep rows whose id is not in the set.
func retainExcept[T interface{ rowID() int64 }](rows []T, ids map[int64]struct{}) []T {
	// 重建切片而非原位过滤：del 之后仍保持非 nil 空切片，show 的 data 不会
	// 退化成 null。
	// Rebuild instead of in-place filtering: the slice stays non-nil after a
	// del so show data never degrades to null.
	out := make([]T, 0, len(rows))
	for _, row := range rows {
		if _, drop := ids[row.rowID()]; !drop {
			out = append(out, row)
		}
	}
	return out
}

func (r customIspRecord) rowID() int64    { return r.ID }
func (r routeObjectRecord) rowID() int64  { return r.ID }
func (r streamDomainRecord) rowID() int64 { return r.ID }
func (r streamIpPortRecord) rowID() int64 { return r.ID }

// ---- route_object ----

func (s *Simulator) dispatchRouteObject(action string, param map[string]any) (any, *apiError) {
	switch action {
	case "show":
		kind, err := parseFilterKind(param)
		if err != nil {
			return nil, toAPIError(err)
		}
		rows := make([]routeObjectRecord, 0, len(s.routeObjects))
		for _, item := range s.routeObjects {
			if kind == nil || item.Type == *kind {
				rows = append(rows, item)
			}
		}
		return showBody(rows), nil
	case "add":
		groupName, err := reqStr(param, "group_name")
		if err != nil {
			return nil, toAPIError(err)
		}
		kind, err := reqI64(param, "type")
		if err != nil {
			return nil, toAPIError(err)
		}
		value, err := parseGroupValue(param)
		if err != nil {
			return nil, toAPIError(err)
		}
		id := s.nextRowIDLocked()
		s.routeObjects = append(s.routeObjects, routeObjectRecord{
			ID: id, GroupName: groupName, Type: kind, GroupValue: value, Comment: optStr(param, "comment"),
		})
		return rowidBody(id), nil
	case "edit":
		id, err := reqI64(param, "id")
		if err != nil {
			return nil, toAPIError(err)
		}
		groupName, err := reqStr(param, "group_name")
		if err != nil {
			return nil, toAPIError(err)
		}
		kind, err := reqI64(param, "type")
		if err != nil {
			return nil, toAPIError(err)
		}
		value, err := parseGroupValue(param)
		if err != nil {
			return nil, toAPIError(err)
		}
		for i := range s.routeObjects {
			if s.routeObjects[i].ID == id {
				s.routeObjects[i].GroupName = groupName
				s.routeObjects[i].Type = kind
				s.routeObjects[i].GroupValue = value
				s.routeObjects[i].Comment = optStr(param, "comment")
				return rowidBody(id), nil
			}
		}
		return nil, &apiError{Code: 1, Message: "route_object id not found"}
	case "del":
		ids, err := reqIDCSV(param)
		if err != nil {
			return nil, toAPIError(err)
		}
		s.routeObjects = retainExcept(s.routeObjects, ids)
		return okBody(), nil
	default:
		return nil, &apiError{Code: 1, Message: "unsupported call"}
	}
}

// ---- stream_domain ----

func (s *Simulator) dispatchStreamDomain(action string, param map[string]any) (any, *apiError) {
	switch action {
	case "show":
		return showBody(s.streamDomains), nil
	case "add":
		rec, err := s.newStreamDomain(param)
		if err != nil {
			return nil, toAPIError(err)
		}
		rec.ID = s.nextRowIDLocked()
		s.streamDomains = append(s.streamDomains, rec)
		return rowidBody(rec.ID), nil
	case "edit":
		id, err := reqI64(param, "id")
		if err != nil {
			return nil, toAPIError(err)
		}
		rec, err := s.newStreamDomain(param)
		if err != nil {
			return nil, toAPIError(err)
		}
		for i := range s.streamDomains {
			if s.streamDomains[i].ID == id {
				rec.ID = id
				s.streamDomains[i] = rec
				return rowidBody(id), nil
			}
		}
		return nil, &apiError{Code: 1, Message: "stream_domain id not found"}
	case "del":
		ids, err := reqIDCSV(param)
		if err != nil {
			return nil, toAPIError(err)
		}
		s.streamDomains = retainExcept(s.streamDomains, ids)
		return okBody(), nil
	default:
		return nil, &apiError{Code: 1, Message: "unsupported call"}
	}
}

func (s *Simulator) newStreamDomain(param map[string]any) (streamDomainRecord, error) {
	tagname, err := reqStr(param, "tagname")
	if err != nil {
		return streamDomainRecord{}, err
	}
	iface, err := reqStr(param, "interface")
	if err != nil {
		return streamDomainRecord{}, err
	}
	srcAddr, err := newAddrBlock(param, "src_addr")
	if err != nil {
		return streamDomainRecord{}, err
	}
	domain, err := newAddrBlock(param, "domain")
	if err != nil {
		return streamDomainRecord{}, err
	}
	timeBlk, err := newTimeBlock(param)
	if err != nil {
		return streamDomainRecord{}, err
	}
	return streamDomainRecord{
		Enabled:   optStrDefault(param, "enabled", "yes"),
		Tagname:   tagname,
		Interface: iface,
		Comment:   optStr(param, "comment"),
		SrcAddr:   srcAddr,
		Domain:    domain,
		Time:      timeBlk,
	}, nil
}

// ---- stream_ipport ----

func (s *Simulator) dispatchStreamIpPort(action string, param map[string]any) (any, *apiError) {
	switch action {
	case "show":
		return showBody(s.streamIpPorts), nil
	case "add":
		rec, err := s.newStreamIpPort(param)
		if err != nil {
			return nil, toAPIError(err)
		}
		rec.ID = s.nextRowIDLocked()
		s.streamIpPorts = append(s.streamIpPorts, rec)
		return rowidBody(rec.ID), nil
	case "edit":
		id, err := reqI64(param, "id")
		if err != nil {
			return nil, toAPIError(err)
		}
		rec, err := s.newStreamIpPort(param)
		if err != nil {
			return nil, toAPIError(err)
		}
		for i := range s.streamIpPorts {
			if s.streamIpPorts[i].ID == id {
				rec.ID = id
				s.streamIpPorts[i] = rec
				return rowidBody(id), nil
			}
		}
		return nil, &apiError{Code: 1, Message: "stream_ipport id not found"}
	case "del":
		ids, err := reqIDCSV(param)
		if err != nil {
			return nil, toAPIError(err)
		}
		s.streamIpPorts = retainExcept(s.streamIpPorts, ids)
		return okBody(), nil
	default:
		return nil, &apiError{Code: 1, Message: "unsupported call"}
	}
}

func (s *Simulator) newStreamIpPort(param map[string]any) (streamIpPortRecord, error) {
	tagname, err := reqStr(param, "tagname")
	if err != nil {
		return streamIpPortRecord{}, err
	}
	iface, err := reqStr(param, "interface")
	if err != nil {
		return streamIpPortRecord{}, err
	}
	nexthop, err := reqStr(param, "nexthop")
	if err != nil {
		return streamIpPortRecord{}, err
	}
	ifaceBand, err := reqI64(param, "iface_band")
	if err != nil {
		return streamIpPortRecord{}, err
	}
	mode, err := reqI64(param, "mode")
	if err != nil {
		return streamIpPortRecord{}, err
	}
	kind, err := reqI64(param, "type")
	if err != nil {
		return streamIpPortRecord{}, err
	}
	srcInv, err := reqI64(param, "src_addr_inv")
	if err != nil {
		return streamIpPortRecord{}, err
	}
	dstInv, err := reqI64(param, "dst_addr_inv")
	if err != nil {
		return streamIpPortRecord{}, err
	}
	srcAddr, err := newAddrBlock(param, "src_addr")
	if err != nil {
		return streamIpPortRecord{}, err
	}
	dstAddr, err := newAddrBlock(param, "dst_addr")
	if err != nil {
		return streamIpPortRecord{}, err
	}
	timeBlk, err := newTimeBlock(param)
	if err != nil {
		return streamIpPortRecord{}, err
	}
	return streamIpPortRecord{
		Enabled:    optStrDefault(param, "enabled", "yes"),
		Tagname:    tagname,
		Interface:  iface,
		Nexthop:    nexthop,
		Comment:    optStr(param, "comment"),
		IfaceBand:  ifaceBand,
		Mode:       mode,
		Protocol:   optStrDefault(param, "protocol", "tcp+udp"),
		Type:       kind,
		SrcAddr:    srcAddr,
		DstAddr:    dstAddr,
		SrcAddrInv: srcInv,
		DstAddrInv: dstInv,
		Time:       timeBlk,
	}, nil
}

// ---- 参数解析辅助（对齐 Rust 模拟器的 required/optional 语义） ----
// ---- Param helpers (mirroring the Rust simulator's required/optional semantics) ----

func reqStr(param map[string]any, key string) (string, error) {
	v, ok := param[key].(string)
	if !ok {
		return "", fmt.Errorf("missing %s", key)
	}
	return v, nil
}

func optStr(param map[string]any, key string) string {
	v, _ := param[key].(string)
	return v
}

func optStrDefault(param map[string]any, key, def string) string {
	if v, ok := param[key].(string); ok {
		return v
	}
	return def
}

func reqI64(param map[string]any, key string) (int64, error) {
	switch t := param[key].(type) {
	case json.Number:
		if v, err := t.Int64(); err == nil {
			return v, nil
		}
		return 0, fmt.Errorf("invalid %s", key)
	case float64:
		return int64(t), nil
	case int64:
		return t, nil
	default:
		return 0, fmt.Errorf("missing %s", key)
	}
}

func reqIDCSV(param map[string]any) (map[int64]struct{}, error) {
	raw, err := reqStr(param, "id")
	if err != nil {
		return nil, err
	}
	ids := make(map[int64]struct{})
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, convErr := strconv.ParseInt(part, 10, 64)
		if convErr != nil {
			return nil, fmt.Errorf("invalid id csv")
		}
		ids[id] = struct{}{}
	}
	return ids, nil
}

// parseFilterKind 只识别 route_object show 的 FILTER1 "type,=,N"（客户端
// 固定下发该形式），其余忽略为不过滤。
// parseFilterKind understands only the route_object show FILTER1 "type,=,N"
// (the exact form the client sends); anything else means no filtering.
func parseFilterKind(param map[string]any) (*int64, error) {
	filter, ok := param["FILTER1"].(string)
	if !ok {
		return nil, nil
	}
	parts := strings.Split(filter, ",")
	if len(parts) < 3 {
		return nil, nil
	}
	if parts[0] != "type" || parts[1] != "=" {
		return nil, nil
	}
	v, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid FILTER1 type")
	}
	return &v, nil
}

func parseGroupValue(param map[string]any) ([]map[string]string, error) {
	arr, ok := param["group_value"].([]any)
	if !ok {
		return nil, fmt.Errorf("missing group_value")
	}
	out := make([]map[string]string, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid group_value item")
		}
		row := make(map[string]string, len(m))
		for k, v := range m {
			if s, ok := v.(string); ok {
				row[k] = s
			} else {
				row[k] = ""
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// ---- 审计与工具 ----
// ---- Audit and utilities ----

func (s *Simulator) nextRowIDLocked() int64 {
	s.nextID++
	return s.nextID
}

func (s *Simulator) appendAuditLocked(kind, funcName, action string, param map[string]any, code int64, message string) {
	s.audit = append(s.audit, AuditEntry{
		Seq:      int64(len(s.audit) + 1),
		Kind:     kind,
		FuncName: funcName,
		Action:   action,
		Param:    param,
		Code:     code,
		Message:  message,
	})
}

// appendAudit 独立加锁版本（decode 失败路径未持锁时使用）。
// appendAudit takes the lock itself (for the decode-failure path, unlocked).
func (s *Simulator) appendAudit(kind, funcName, action string, param map[string]any, code int64, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendAuditLocked(kind, funcName, action, param, code, message)
}

func toAPIError(err error) *apiError {
	return &apiError{Code: 1, Message: err.Error()}
}

// simulator_test.go 模拟器自测：login 双字段校验、会话 cookie、四类对象
// 的 show/add/edit/del、FILTER1 过滤、失败注入与 JSONL 审计。
// Simulator self-test: login dual-field validation, session cookie, the four
// object families' show/add/edit/del, FILTER1 filtering, failure injection,
// and the JSONL audit trail.
package simulator

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"testing"
)

const (
	simUser = "admin"
	simPass = "admin888"
)

// md5Hex / base64Salt11 与 internal/ikuai 客户端相同的凭据推导（测试内显式
// 展开，避免模拟器自测反向依赖被测实现）。
// md5Hex / base64Salt11 replicate the credential derivation of the
// internal/ikuai client (spelled out in-test so the simulator self-test never
// depends back on the implementation under test).
func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func base64Salt11(password string) string {
	return base64.StdEncoding.EncodeToString([]byte("salt_11" + password))
}

// simClient 独立 cookie jar 的客户端（区分"带会话"与"无会话"两类请求）。
// simClient is a client with its own cookie jar (separating session and
// session-less requests).
func simClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	return &http.Client{Transport: &http.Transport{Proxy: nil}, Jar: jar}
}

type callResp struct {
	Code    int64  `json:"code"`
	Message string `json:"message"`
	RowID   *int64 `json:"rowid"`
	Results *struct {
		Total int               `json:"total"`
		Data  []json.RawMessage `json:"data"`
	} `json:"results"`
}

func postJSON(t *testing.T, client *http.Client, url string, body any) callResp {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	resp, err := client.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s: unexpected status %d", url, resp.StatusCode)
	}
	var out callResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response of %s: %v", url, err)
	}
	return out
}

func loginBody(password string) map[string]string {
	return map[string]string{
		"username": simUser,
		"passwd":   md5Hex(password),
		"pass":     base64Salt11(password),
	}
}

func TestSimulatorLoginAndCall(t *testing.T) {
	sim := Start(simUser, simPass)
	defer sim.Close()
	base := sim.URL()

	// 1) 错误密码必须 code 1。
	// 1) A wrong password must yield code 1.
	bad := postJSON(t, simClient(t), base+"/Action/login", loginBody("wrong-pass"))
	if bad.Code != 1 {
		t.Fatalf("login with wrong password: expected code 1, got %d (%s)", bad.Code, bad.Message)
	}

	// 2) 正确登录 code 0 且种下会话 cookie。
	// 2) A correct login yields code 0 and seeds the session cookie.
	authed := simClient(t)
	good := postJSON(t, authed, base+"/Action/login", loginBody(simPass))
	if good.Code != 0 {
		t.Fatalf("login with correct password: expected code 0, got %d (%s)", good.Code, good.Message)
	}

	// 3) 无会话调用必须 code 1（会话已过期）。
	// 3) A session-less call must yield code 1 (expired session).
	anon := postJSON(t, simClient(t), base+"/Action/call", map[string]any{
		"func_name": funcNameCustomIsp, "action": "show", "param": map[string]any{},
	})
	if anon.Code != 1 || anon.Message != "会话已过期" {
		t.Fatalf("session-less call: expected code 1 会话已过期, got %d (%s)", anon.Code, anon.Message)
	}

	call := func(funcName, action string, param map[string]any) callResp {
		return postJSON(t, authed, base+"/Action/call", map[string]any{
			"func_name": funcName, "action": action, "param": param,
		})
	}

	// 4) custom_isp add/edit/del 全链路。
	// 4) The full custom_isp add/edit/del chain.
	if resp := call(funcNameCustomIsp, "show", map[string]any{"TYPE": "total,data", "limit": "0,1000"}); resp.Code != 0 || resp.Results.Total != 0 {
		t.Fatalf("custom_isp show on empty store: code=%d total=%d", resp.Code, totalOf(resp))
	}
	added := call(funcNameCustomIsp, "add", map[string]any{
		"name": "IKBSimIsp", "ipgroup": "10.0.0.0/24,10.0.1.0/24", "comment": "IkuaiBypass",
	})
	if added.Code != 0 || added.RowID == nil || *added.RowID != 1 {
		t.Fatalf("custom_isp add: expected code 0 rowid 1, got code=%d rowid=%v", added.Code, added.RowID)
	}
	if resp := call(funcNameCustomIsp, "edit", map[string]any{
		"id": 1, "name": "IKBSimIsp", "ipgroup": "10.0.9.0/24", "comment": "IkuaiBypass",
	}); resp.Code != 0 {
		t.Fatalf("custom_isp edit: expected code 0, got %d (%s)", resp.Code, resp.Message)
	}
	if resp := call(funcNameCustomIsp, "edit", map[string]any{
		"id": 999, "name": "x", "ipgroup": "y", "comment": "z",
	}); resp.Code != 1 {
		t.Fatalf("custom_isp edit on missing id: expected code 1, got %d", resp.Code)
	}

	// 5) route_object 双类型 + FILTER1 过滤。
	// 5) route_object dual types plus the FILTER1 filter.
	for _, kind := range []int{0, 1} {
		resp := call(funcNameRouteObject, "add", map[string]any{
			"group_name": "IKBSimGroup", "type": kind,
			"group_value": []map[string]string{{"ip": "198.51.100.1", "comment": ""}},
			"comment":     "",
		})
		if resp.Code != 0 || resp.RowID == nil {
			t.Fatalf("route_object add type %d: code=%d rowid=%v", kind, resp.Code, resp.RowID)
		}
	}
	v4 := call(funcNameRouteObject, "show", map[string]any{
		"TYPE": "total,data", "limit": "0,1000", "FILTER1": "type,=,0",
	})
	if v4.Code != 0 || v4.Results == nil || v4.Results.Total != 1 {
		t.Fatalf("route_object show FILTER1 type=0: expected 1 row, got total=%d", totalOf(v4))
	}
	all := call(funcNameRouteObject, "show", map[string]any{"TYPE": "total,data", "limit": "0,1000"})
	if all.Results.Total != 2 {
		t.Fatalf("route_object show without filter: expected 2 rows, got %d", totalOf(all))
	}

	// 6) stream_domain / stream_ipport add + show 回显。
	// 6) stream_domain / stream_ipport add plus show echo.
	sd := call(funcNameStreamDomain, "add", map[string]any{
		"enabled": "yes", "tagname": "IKBSimDom", "interface": "wan2", "comment": "IkuaiBypass",
		"src_addr": map[string]any{"custom": []string{"192.168.1.10-192.168.1.20"}, "object": []any{}},
		"domain":   map[string]any{"custom": []string{"a.example", "b.example"}, "object": []any{}},
		"time": map[string]any{
			"custom": []map[string]string{{"type": "weekly", "weekdays": "1234567", "start_time": "00:00", "end_time": "23:59", "comment": ""}},
			"object": []any{},
		},
		"prio": 31,
	})
	if sd.Code != 0 {
		t.Fatalf("stream_domain add: expected code 0, got %d (%s)", sd.Code, sd.Message)
	}
	if resp := call(funcNameStreamDomain, "show", map[string]any{"TYPE": "total,data", "limit": "0,1000"}); resp.Results.Total != 1 {
		t.Fatalf("stream_domain show: expected 1 row, got %d", totalOf(resp))
	}
	sp := call(funcNameStreamIpPort, "add", map[string]any{
		"enabled": "yes", "tagname": "IKBSimRoute", "interface": "", "nexthop": "192.168.1.2",
		"comment": "IkuaiBypass", "iface_band": 0, "mode": 0, "type": 1,
		"src_addr":     map[string]any{"custom": []string{}, "object": []any{}},
		"dst_addr":     map[string]any{"custom": []string{}, "object": []map[string]any{{"type": 0, "gid": "IPGP2", "gp_name": "IKBSimGroup"}}},
		"src_addr_inv": 0, "dst_addr_inv": 0,
		"time": map[string]any{
			"custom": []map[string]string{{"type": "weekly", "weekdays": "1234567", "start_time": "00:00", "end_time": "23:59", "comment": ""}},
			"object": []any{},
		},
	})
	if sp.Code != 0 {
		t.Fatalf("stream_ipport add: expected code 0, got %d (%s)", sp.Code, sp.Message)
	}
	if resp := call(funcNameStreamIpPort, "show", map[string]any{"TYPE": "total,data", "limit": "0,1000"}); resp.Results.Total != 1 {
		t.Fatalf("stream_ipport show: expected 1 row, got %d", totalOf(resp))
	}

	// 7) 失败注入：第一次 add code 1 且不落库，第二次恢复 code 0。
	// 7) Failure injection: the first add fails with code 1 without persisting,
	//    the second succeeds again.
	sim.InjectFailure(funcNameCustomIsp, "add", 1)
	before := call(funcNameCustomIsp, "show", map[string]any{"TYPE": "total,data", "limit": "0,1000"})
	failAdd := call(funcNameCustomIsp, "add", map[string]any{
		"name": "IKBFail", "ipgroup": "203.0.113.0/24", "comment": "IkuaiBypass",
	})
	if failAdd.Code != 1 {
		t.Fatalf("injected custom_isp add: expected code 1, got %d", failAdd.Code)
	}
	after := call(funcNameCustomIsp, "show", map[string]any{"TYPE": "total,data", "limit": "0,1000"})
	if after.Results.Total != before.Results.Total {
		t.Fatalf("injected failure mutated state: before=%d after=%d", before.Results.Total, after.Results.Total)
	}
	okAdd := call(funcNameCustomIsp, "add", map[string]any{
		"name": "IKBFail", "ipgroup": "203.0.113.0/24", "comment": "IkuaiBypass",
	})
	if okAdd.Code != 0 {
		t.Fatalf("custom_isp add after injected failure: expected code 0, got %d (%s)", okAdd.Code, okAdd.Message)
	}

	// 8) del 批量 CSV 清空（id 全局递增，按实际 rowid 拼接）。
	// 8) Batch CSV del clears the store (ids are globally increasing; join the
	//    actual rowids).
	if okAdd.RowID == nil || *okAdd.RowID <= 0 {
		t.Fatalf("custom_isp add rowid missing: %v", okAdd.RowID)
	}
	delCSV := "1," + strconv.FormatInt(*okAdd.RowID, 10)
	if resp := call(funcNameCustomIsp, "del", map[string]any{"id": delCSV}); resp.Code != 0 {
		t.Fatalf("custom_isp del: expected code 0, got %d (%s)", resp.Code, resp.Message)
	}
	if resp := call(funcNameCustomIsp, "show", map[string]any{"TYPE": "total,data", "limit": "0,1000"}); resp.Results.Total != 0 {
		t.Fatalf("custom_isp show after del: expected 0 rows, got %d", totalOf(resp))
	}

	// 9) 未知 func_name 与审计 JSONL。
	// 9) Unknown func_name plus the audit JSONL.
	if resp := call("mystery_fn", "show", map[string]any{}); resp.Code != 1 {
		t.Fatalf("unknown func_name: expected code 1, got %d", resp.Code)
	}
	adds := sim.Calls(funcNameCustomIsp, "add")
	if len(adds) != 3 {
		t.Fatalf("expected 3 custom_isp add audit entries, got %d", len(adds))
	}
	if adds[1].Code != 1 || adds[2].Code != 0 {
		t.Fatalf("audit codes out of order: [%v]", adds)
	}
	if jsonl := sim.AuditJSONL(); jsonl == "" || !bytes.Contains([]byte(jsonl), []byte(`"func_name":"custom_isp"`)) {
		t.Fatalf("audit JSONL missing custom_isp entries:\n%s", jsonl)
	}
}

// totalOf show 响应的行数（Results 缺失时为 0）。
// totalOf returns the row count of a show response (0 when Results is absent).
func totalOf(resp callResp) int {
	if resp.Results == nil {
		return 0
	}
	return resp.Results.Total
}

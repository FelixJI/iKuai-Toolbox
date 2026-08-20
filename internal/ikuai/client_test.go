// client_test.go 爱快 API 客户端测试（httptest 假爱快），行为对齐 rust_archive/crates/core/src/ikuai/types.rs L142-239。
// iKuai API client tests against an httptest fake, aligned with rust_archive/crates/core/src/ikuai/types.rs L142-239.
package ikuai

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// md5PassKnown md5("pass")，独立于实现硬编码，避免用被测函数自证。
// md5PassKnown is md5("pass"), hardcoded independently of the implementation under test.
const md5PassKnown = "1a1dc91c907325c69271ddf0c944bc72"

// base64SaltPass base64("salt_11"+"pass")，对齐 types.rs L158。
// base64SaltPass is base64("salt_11"+"pass"), mirroring types.rs L158.
const base64SaltPass = "c2FsdF8xMXBhc3M="

// readJSONBody 读取并解析 JSON 请求体 / read and decode a JSON request body.
func readJSONBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode body %q: %v", body, err)
	}
	return m
}

// TestLoginFlow 对齐 types.rs L156-173：登录 body 四字段齐全、密码双重编码、code!=0 => api 错误。
func TestLoginFlow(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/Action/login" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q, want application/json", ct)
		}
		gotBody = readJSONBody(t, r)
		io.WriteString(w, `{"code":0,"message":"ok"}`)
	}))
	defer srv.Close()

	c, err := NewIKuaiClient(srv.URL)
	if err != nil {
		t.Fatalf("NewIKuaiClient: %v", err)
	}
	if err := c.Login("admin", "pass"); err != nil {
		t.Fatalf("Login: %v", err)
	}

	want := map[string]any{
		"passwd":            md5PassKnown,
		"pass":              base64SaltPass,
		"remember_password": "",
		"username":          "admin",
	}
	if len(gotBody) != len(want) {
		t.Fatalf("login body = %v, want exactly %v", gotBody, want)
	}
	for k, v := range want {
		if gotBody[k] != v {
			t.Errorf("login body[%q] = %v, want %v", k, gotBody[k], v)
		}
	}
}

// TestLoginApiError 登录返回 code!=0 时必须报 api 错误且携带爱快 message（types.rs L169-171）。
func TestLoginApiError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"code":1,"message":"login failed"}`)
	}))
	defer srv.Close()

	c, _ := NewIKuaiClient(srv.URL)
	err := c.Login("admin", "pass")
	var ikErr *IKuaiError
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if ok := asIKuaiError(err, &ikErr); !ok || ikErr.Kind != ErrKindApi {
		t.Fatalf("want IKuaiError api, got %v", err)
	}
	if ikErr.Msg != "login failed" || ikErr.Error() != "api error: login failed" {
		t.Fatalf("error message = %q / %q", ikErr.Msg, ikErr.Error())
	}
}

// asIKuaiError 类型断言辅助 / type-assert helper.
func asIKuaiError(err error, target **IKuaiError) bool {
	ikErr, ok := err.(*IKuaiError)
	if ok {
		*target = ikErr
	}
	return ok
}

// TestLoginInvalidResponse 非法 JSON 响应 => invalid_response 错误（types.rs L214-220）。
func TestLoginInvalidResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<html>not json</html>")
	}))
	defer srv.Close()

	c, _ := NewIKuaiClient(srv.URL)
	err := c.Login("admin", "pass")
	var ikErr *IKuaiError
	if !asIKuaiError(err, &ikErr) || ikErr.Kind != ErrKindInvalidResponse {
		t.Fatalf("want IKuaiError invalid_response, got %v", err)
	}
	if !strings.HasPrefix(ikErr.Msg, "decode error: ") || !strings.Contains(ikErr.Msg, "body: <html>not json</html>") {
		t.Fatalf("msg = %q", ikErr.Msg)
	}
}

// TestLoginMissingRequiredField 缺 message 字段同样视为不可解码（Rust serde 必填字段语义）。
func TestLoginMissingRequiredField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"code":0}`)
	}))
	defer srv.Close()

	c, _ := NewIKuaiClient(srv.URL)
	err := c.Login("admin", "pass")
	var ikErr *IKuaiError
	if !asIKuaiError(err, &ikErr) || ikErr.Kind != ErrKindInvalidResponse {
		t.Fatalf("want IKuaiError invalid_response, got %v", err)
	}
}

// TestLoginHttpErrorStatus 非 2xx 状态码 => api 错误，文案 "http status <status>: <body>"（types.rs L203-208）。
func TestLoginHttpErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, "oops")
	}))
	defer srv.Close()

	c, _ := NewIKuaiClient(srv.URL)
	err := c.Login("admin", "pass")
	var ikErr *IKuaiError
	if !asIKuaiError(err, &ikErr) || ikErr.Kind != ErrKindApi {
		t.Fatalf("want IKuaiError api, got %v", err)
	}
	if !strings.HasPrefix(ikErr.Msg, "http status 500 ") || !strings.HasSuffix(ikErr.Msg, ": oops") {
		t.Fatalf("msg = %q", ikErr.Msg)
	}
}

// TestLoginHttpTransportError 连接不可达 => http 错误（types.rs L30-33 Http 变体）。
func TestLoginHttpTransportError(t *testing.T) {
	c, err := NewIKuaiClient("http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("NewIKuaiClient: %v", err)
	}
	err = c.Login("admin", "pass")
	var ikErr *IKuaiError
	if !asIKuaiError(err, &ikErr) || ikErr.Kind != ErrKindHttp {
		t.Fatalf("want IKuaiError http, got %v", err)
	}
	if !strings.HasPrefix(ikErr.Error(), "http error: ") {
		t.Fatalf("error = %q", ikErr.Error())
	}
}

// TestLoginBaseURLTrailingSlash baseUrl 尾部斜杠需被剥掉（types.rs L166 trim_end_matches('/')）。
func TestLoginBaseURLTrailingSlash(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		io.WriteString(w, `{"code":0,"message":"ok"}`)
	}))
	defer srv.Close()

	c, _ := NewIKuaiClient(srv.URL + "///")
	if err := c.Login("admin", "pass"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if len(paths) != 1 || paths[0] != "/Action/login" {
		t.Fatalf("paths = %v, want [/Action/login]", paths)
	}
}

// TestLoginForcesDirectConnection Transport.Proxy 必须为 nil：
// 设置不可达的 HTTP_PROXY 后仍能直连 httptest 服务（types.rs L150-152 no_proxy）。
func TestLoginForcesDirectConnection(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("http_proxy", "http://127.0.0.1:1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"code":0,"message":"ok"}`)
	}))
	defer srv.Close()

	c, _ := NewIKuaiClient(srv.URL)
	if err := c.Login("admin", "pass"); err != nil {
		t.Fatalf("must connect directly ignoring proxy env, got %v", err)
	}
}

// TestNewIKuaiClientTransport 白盒校验超时与直连配置（types.rs L143-154）。
func TestNewIKuaiClientTransport(t *testing.T) {
	c, err := NewIKuaiClient("http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("NewIKuaiClient: %v", err)
	}
	if c.hc.Timeout != 30*time.Second {
		t.Errorf("client timeout = %v, want 30s", c.hc.Timeout)
	}
	if c.hc.Jar == nil {
		t.Error("client must carry a cookie jar")
	}
	tr, ok := c.hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T", c.hc.Transport)
	}
	if tr.Proxy != nil {
		t.Error("transport must force direct connection (Proxy nil)")
	}
}

// TestCallEnvelope 对齐 types.rs L175-193：call 信封透传、results/rowid 解析、cookie jar 维持会话。
func TestCallEnvelope(t *testing.T) {
	var mu sync.Mutex
	var callBody map[string]any
	var callCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Action/login":
			// 模拟爱快下发会话 cookie / emulate the iKuai session cookie.
			http.SetCookie(w, &http.Cookie{Name: "sess", Value: "abc123", Path: "/"})
			io.WriteString(w, `{"code":0,"message":"ok"}`)
		case "/Action/call":
			mu.Lock()
			callBody = readJSONBody(t, r)
			callCookie = ""
			if ck, err := r.Cookie("sess"); err == nil {
				callCookie = ck.Value
			}
			mu.Unlock()
			io.WriteString(w, `{"code":0,"message":"ok","results":{"total":2,"data":[{"name":"a"},{"name":"b"}]},"rowid":42}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c, _ := NewIKuaiClient(srv.URL)
	if err := c.Login("admin", "pass"); err != nil {
		t.Fatalf("Login: %v", err)
	}

	param := map[string]any{"TYPE": "total", "ORDER_BY": "id", "ORDER": "asc", "LIMIT": float64(2)}
	var resp CallResp
	if err := c.Call(FUNC_NAME_ROUTE_OBJECT, "show", param, &resp); err != nil {
		t.Fatalf("Call: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if callBody["func_name"] != FUNC_NAME_ROUTE_OBJECT || callBody["action"] != "show" {
		t.Errorf("call body func/action = %v/%v", callBody["func_name"], callBody["action"])
	}
	gotParam, _ := callBody["param"].(map[string]any)
	if len(gotParam) != len(param) {
		t.Errorf("param passthrough = %v, want %v", gotParam, param)
	}
	for k, v := range param {
		if gotParam[k] != v {
			t.Errorf("param[%q] = %v, want %v", k, gotParam[k], v)
		}
	}
	if callCookie != "abc123" {
		t.Errorf("session cookie after login = %q, want abc123 (jar must persist)", callCookie)
	}
	if resp.Code != 0 || resp.Message != "ok" {
		t.Errorf("resp code/message = %d/%q", resp.Code, resp.Message)
	}
	if resp.Results == nil || resp.Results.Total == nil || *resp.Results.Total != 2 {
		t.Errorf("results.total = %+v, want 2", resp.Results)
	}
	var rows []map[string]any
	if err := json.Unmarshal(resp.Results.Data, &rows); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if len(rows) != 2 || rows[0]["name"] != "a" || rows[1]["name"] != "b" {
		t.Errorf("data = %v", rows)
	}
	if resp.RowID == nil || *resp.RowID != 42 {
		t.Errorf("rowid = %+v, want 42", resp.RowID)
	}
}

// TestCallApiError code!=0 => api 错误且消息为爱快 message（types.rs L189-191）。
func TestCallApiError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"code":42,"message":"boom"}`)
	}))
	defer srv.Close()

	c, _ := NewIKuaiClient(srv.URL)
	var resp CallResp
	err := c.Call(FUNC_NAME_CUSTOM_ISP, "show", map[string]any{}, &resp)
	var ikErr *IKuaiError
	if !asIKuaiError(err, &ikErr) || ikErr.Kind != ErrKindApi {
		t.Fatalf("want IKuaiError api, got %v", err)
	}
	if ikErr.Error() != "api error: boom" {
		t.Fatalf("error = %q", ikErr.Error())
	}
}

// TestCallLongBodyTrimmed 长错误 body 截断到 200 字节 + "..."（types.rs L222-239）。
func TestCallLongBodyTrimmed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, strings.Repeat("z", 300))
	}))
	defer srv.Close()

	c, _ := NewIKuaiClient(srv.URL)
	var resp CallResp
	err := c.Call(FUNC_NAME_STREAM_DOMAIN, "show", nil, &resp)
	var ikErr *IKuaiError
	if !asIKuaiError(err, &ikErr) || ikErr.Kind != ErrKindApi {
		t.Fatalf("want IKuaiError api, got %v", err)
	}
	// trim_body：200 字节正文 + "..."（共 203 字节）。
	// trim_body: 200 bytes of body plus "..." (203 bytes total).
	if !strings.HasSuffix(ikErr.Msg, "...") || strings.Count(ikErr.Msg, "z") != 200 {
		t.Errorf("msg = %q (len=%d)", ikErr.Msg, len(ikErr.Msg))
	}
}

// TestCallMissingResultsData results 存在但 data 缺失 => 不可解码（Rust CallRespData data 必填语义）。
func TestCallMissingResultsData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"code":0,"message":"ok","results":{"total":1}}`)
	}))
	defer srv.Close()

	c, _ := NewIKuaiClient(srv.URL)
	var resp CallResp
	err := c.Call(FUNC_NAME_STREAM_IPPORT, "show", nil, &resp)
	var ikErr *IKuaiError
	if !asIKuaiError(err, &ikErr) || ikErr.Kind != ErrKindInvalidResponse {
		t.Fatalf("want IKuaiError invalid_response, got %v", err)
	}
}

// TestCallEnvelopeJSONFieldNames 信封 JSON 键名与 Rust serde 逐字对齐（types.rs L40-64），
// 且参数值不做 HTML 转义（serde_json 语义，< > & 保持字面字节）。
func TestCallEnvelopeJSONFieldNames(t *testing.T) {
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		io.WriteString(w, `{"code":0,"message":"ok"}`)
	}))
	defer srv.Close()

	c, _ := NewIKuaiClient(srv.URL)
	var resp CallResp
	if err := c.Call("f", "a", nil, &resp); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if string(raw) != `{"func_name":"f","action":"a","param":null}` {
		t.Errorf("wire body = %s", raw)
	}

	if err := c.Call("f", "a", map[string]any{"comment": "a<b&c>d"}, &resp); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if string(raw) != `{"func_name":"f","action":"a","param":{"comment":"a<b&c>d"}}` {
		t.Errorf("wire body = %s", raw)
	}
}

// TestLoginWireBytes 登录请求体必须与 Rust serde_json 产物逐字节一致：
// 键按字典序（BTreeMap）、无 HTML 转义、无多余空白。
func TestLoginWireBytes(t *testing.T) {
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		io.WriteString(w, `{"code":0,"message":"ok"}`)
	}))
	defer srv.Close()

	c, _ := NewIKuaiClient(srv.URL)
	if err := c.Login("ad<min", "p&a>ss"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	want := `{"pass":"` + base64.StdEncoding.EncodeToString([]byte("salt_11p&a>ss")) +
		`","passwd":"` + MD5Hex("p&a>ss") +
		`","remember_password":"","username":"ad<min"}`
	if string(raw) != want {
		t.Errorf("wire body = %s, want %s", raw, want)
	}
}

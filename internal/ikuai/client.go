// client.go 爱快 API 客户端（登录 + 通用 call），行为对齐 rust_archive/crates/core/src/ikuai/types.rs L142-239。
// iKuai API client (login + generic call), behaviorally aligned with types.rs L142-239.
package ikuai

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"
	"unicode/utf8"
)

// IKuaiClient 携带共享 cookie jar 的爱快会话客户端，对齐 types.rs L24-28。
// IKuaiClient is an iKuai session client with a shared cookie jar, mirroring types.rs L24-28.
type IKuaiClient struct {
	baseUrl string
	hc      *http.Client
}

// NewIKuaiClient 连接 5s / 总超时 30s / 强制直连（Proxy=nil），cookie jar 维持会话。
// 对齐 types.rs L143-154：避免网络异常卡死，且不走系统/自定义代理。
// NewIKuaiClient uses a 5s connect / 30s total timeout and forces a direct
// connection (Proxy=nil) with a cookie jar, mirroring types.rs L143-154:
// never hang forever, never leak through system/custom proxies.
func NewIKuaiClient(baseUrl string) (*IKuaiClient, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("create cookie jar: %w", err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	hc := &http.Client{Jar: jar, Timeout: 30 * time.Second, Transport: transport}
	return &IKuaiClient{baseUrl: baseUrl, hc: hc}, nil
}

// Login 对齐 types.rs L156-173：POST /Action/login，
// body {passwd: md5hex(pwd), pass: base64("salt_11"+pwd), remember_password: "", username}，
// 响应 code!=0 => api 错误。
// Login mirrors types.rs L156-173: POST /Action/login with
// {passwd: md5hex(pwd), pass: base64("salt_11"+pwd), remember_password: "", username};
// a non-zero result code yields an api error.
func (c *IKuaiClient) Login(username, password string) error {
	req := map[string]string{
		"passwd":            MD5Hex(password),
		"pass":              base64.StdEncoding.EncodeToString([]byte("salt_11" + password)),
		"remember_password": "",
		"username":          username,
	}
	var resp CallResp
	if err := c.postJSONAndParse(c.actionURL("/Action/login"), req, &resp); err != nil {
		return err
	}
	if resp.Code != 0 {
		return &IKuaiError{Kind: ErrKindApi, Msg: resp.Message}
	}
	return nil
}

// Call 对齐 types.rs L175-193：POST /Action/call，信封 CallReq{func_name, action, param}，
// code!=0 => api(message)；Results.Data 保持原始 JSON 由各模块自行解码。
// Call mirrors types.rs L175-193: POST /Action/call with the CallReq{func_name, action, param}
// envelope; a non-zero code yields api(message); Results.Data stays raw for per-module decoding.
func (c *IKuaiClient) Call(funcName, action string, param map[string]any, out *CallResp) error {
	if out == nil {
		return errors.New("ikuai: Call requires a non-nil out")
	}
	req := CallReq{FuncName: funcName, Action: action, Param: param}
	if err := c.postJSONAndParse(c.actionURL("/Action/call"), req, out); err != nil {
		return err
	}
	if out.Code != 0 {
		return &IKuaiError{Kind: ErrKindApi, Msg: out.Message}
	}
	return nil
}

// actionURL 拼接动作地址并剥掉 baseUrl 尾部斜杠，对齐 types.rs L166/L181 的 trim_end_matches('/')。
// actionURL joins the action path after stripping trailing slashes, mirroring types.rs L166/L181.
func (c *IKuaiClient) actionURL(path string) string {
	return strings.TrimRight(c.baseUrl, "/") + path
}

// postJSONAndParse 发送 JSON、读取文本并解析为 CallResp。
// postJSONAndParse posts JSON, reads the text body and decodes it into a CallResp.
func (c *IKuaiClient) postJSONAndParse(url string, body any, out *CallResp) error {
	text, err := c.postJSONText(url, body)
	if err != nil {
		return err
	}
	return parseCallResponse(text, out)
}

// postJSONText 对齐 types.rs L195-211：POST JSON，非 2xx => api 错误 "http status <status>: <body>"。
// postJSONText mirrors types.rs L195-211: POST JSON; a non-2xx status yields the api error "http status <status>: <body>".
func (c *IKuaiClient) postJSONText(url string, body any) (string, error) {
	payload, err := marshalNoHTMLEscape(body)
	if err != nil {
		return "", &IKuaiError{Kind: ErrKindHttp, Msg: err.Error()}
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", &IKuaiError{Kind: ErrKindHttp, Msg: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", &IKuaiError{Kind: ErrKindHttp, Msg: err.Error()}
	}
	defer resp.Body.Close()
	text, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", &IKuaiError{Kind: ErrKindHttp, Msg: err.Error()}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", &IKuaiError{Kind: ErrKindApi, Msg: fmt.Sprintf("http status %s: %s", resp.Status, trimBody(string(text)))}
	}
	return string(text), nil
}

// parseCallResponse 对齐 types.rs L214-220：解码失败 => invalid_response "decode error: ... body: ..."。
// code/message 为 Rust 必填字段，results.data 同理；缺失同样视为不可解码。
// parseCallResponse mirrors types.rs L214-220: a decode failure yields the invalid_response
// "decode error: ... body: ..." error. code/message (and results.data) are required in Rust;
// missing keys are treated as undecodable as well.
func parseCallResponse(text string, out *CallResp) error {
	var wire struct {
		Code    *int64        `json:"code"`
		Message *string       `json:"message"`
		Results *CallRespData `json:"results"`
		RowID   *int64        `json:"rowid"`
	}
	if err := json.Unmarshal([]byte(text), &wire); err != nil {
		return &IKuaiError{Kind: ErrKindInvalidResponse, Msg: fmt.Sprintf("decode error: %v body: %s", err, trimBody(text))}
	}
	missing := ""
	switch {
	case wire.Code == nil:
		missing = "code"
	case wire.Message == nil:
		missing = "message"
	case wire.Results != nil && wire.Results.Data == nil:
		missing = "data"
	}
	if missing != "" {
		return &IKuaiError{Kind: ErrKindInvalidResponse, Msg: fmt.Sprintf("decode error: missing field `%s` body: %s", missing, trimBody(text))}
	}
	out.Code = *wire.Code
	out.Message = *wire.Message
	out.Results = wire.Results
	out.RowID = wire.RowID
	return nil
}

// decodeResultsData 复刻 Rust 各模块 call::<Vec<Row>> 之后的取数语义：
// results 缺失 => invalid_response "missing results"（custom_isp.rs L33-35 等），
// data 非数组/为 null => invalid_response "decode error: ..."（types.rs L175-193 泛型解码）。
// decodeResultsData replicates what Rust modules get after call::<Vec<Row>>:
// missing results => invalid_response "missing results" (custom_isp.rs L33-35 etc.),
// non-array or null data => invalid_response "decode error: ..." (the generic
// decode inside types.rs L175-193).
func decodeResultsData(resp *CallResp, out any) error {
	if resp.Results == nil {
		return &IKuaiError{Kind: ErrKindInvalidResponse, Msg: "missing results"}
	}
	if string(bytes.TrimSpace(resp.Results.Data)) == "null" {
		return &IKuaiError{Kind: ErrKindInvalidResponse, Msg: "decode error: data must be an array"}
	}
	if err := json.Unmarshal(resp.Results.Data, out); err != nil {
		return &IKuaiError{Kind: ErrKindInvalidResponse, Msg: "decode error: " + err.Error()}
	}
	return nil
}

// trimBody 对齐 types.rs L222-239：trim 后超过 200 字节则 UTF-8 安全截断并追加 "..."。
// trimBody mirrors types.rs L222-239: trim, then truncate UTF-8-safely at 200 bytes and append "...".
func trimBody(text string) string {
	trimmed := strings.TrimSpace(text)
	const limit = 200
	if len(trimmed) <= limit {
		return trimmed
	}
	end := 0
	for i, ch := range trimmed {
		next := i + utf8.RuneLen(ch)
		if next > limit {
			break
		}
		end = next
	}
	return trimmed[:end] + "..."
}

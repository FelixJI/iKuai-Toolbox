// types.go 爱快模块公共常量与信封类型，行为对齐 crates/core/src/ikuai/types.rs。
// Public constants and call envelope types for the iKuai module,
// behaviorally aligned with crates/core/src/ikuai/types.rs.
package ikuai

import "encoding/json"

// 标签/备注标记常量，对齐 types.rs L6-10。
// Tag/comment marker constants mirroring types.rs L6-10.
const (
	NamePrefixIKB      = "IKB"
	CommentIkuaiBypass = "IKUAI_BYPASS"
	LegacyRepoComment  = "joyanhui/ikuai-bypass"
	NewComment         = "IkuaiBypass"
	CleanModeAll       = "cleanAll"
)

// 爱快功能名常量，对齐 types.rs L19-22。
// iKuai func_name constants mirroring types.rs L19-22.
const (
	FUNC_NAME_ROUTE_OBJECT  = "route_object"
	FUNC_NAME_CUSTOM_ISP    = "custom_isp"
	FUNC_NAME_STREAM_DOMAIN = "stream_domain"
	FUNC_NAME_STREAM_IPPORT = "stream_ipport"
)

// IKuaiError 错误种类，对齐 types.rs L30-38 的 IKuaiError 枚举。
// Error kinds mirroring the IKuaiError enum in types.rs L30-38.
const (
	ErrKindHttp            = "http"
	ErrKindApi             = "api"
	ErrKindInvalidResponse = "invalid_response"
)

// ManagedCommentMarkers 返回清理/更新时需要兼容的历史备注标记（顺序固定）。
// 新写入只使用 NewComment；对齐 types.rs L12-17。
// ManagedCommentMarkers returns the comment markers matched during clean/update
// (fixed order); new records are written with NewComment only. Mirrors types.rs L12-17.
func ManagedCommentMarkers() [3]string {
	return [3]string{NewComment, LegacyRepoComment, CommentIkuaiBypass}
}

// IKuaiError 对齐 types.rs L30-38：http（传输层）/ api（爱快返回码）/ invalid_response（响应不可解码）。
// IKuaiError mirrors types.rs L30-38: http (transport), api (iKuai result code), invalid_response (undecodable body).
type IKuaiError struct {
	Kind string
	Msg  string
}

// Error 对齐 Rust Display 文案：http error/api error/invalid response 前缀 + 消息。
// Error mirrors the Rust Display strings with the http error/api error/invalid response prefixes.
func (e *IKuaiError) Error() string {
	switch e.Kind {
	case ErrKindHttp:
		return "http error: " + e.Msg
	case ErrKindApi:
		return "api error: " + e.Msg
	case ErrKindInvalidResponse:
		return "invalid response: " + e.Msg
	default:
		return e.Msg
	}
}

// CallReq 对齐 types.rs L40-45 的调用请求信封。
// CallReq mirrors the call request envelope in types.rs L40-45.
type CallReq struct {
	FuncName string         `json:"func_name"`
	Action   string         `json:"action"`
	Param    map[string]any `json:"param"`
}

// CallRespData 对齐 types.rs L58-64：total 可缺省，data 由各模块自行解码。
// CallRespData mirrors types.rs L58-64: total is optional, data stays raw for per-module decoding.
type CallRespData struct {
	Total *int64          `json:"total"`
	Data  json.RawMessage `json:"data"`
}

// CallResp 对齐 types.rs L47-56：code/message 必填，results/rowid 可缺省。
// CallResp mirrors types.rs L47-56: code/message required, results/rowid optional.
type CallResp struct {
	Code    int64         `json:"code"`
	Message string        `json:"message"`
	Results *CallRespData `json:"results"`
	RowID   *int64        `json:"rowid"`
}

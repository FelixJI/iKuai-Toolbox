// custom_isp.go 自定义运营商 CRUD 与分片索引解析，行为对齐 crates/core/src/ikuai/custom_isp.rs。
// Custom ISP CRUD plus chunk-index parsing, aligned with custom_isp.rs.
package ikuai

import (
	"strconv"
	"strings"
)

// CustomIspData 对齐 types.rs L66-73 的 CustomIspData（show 响应行直解）。
// CustomIspData mirrors CustomIspData in types.rs L66-73 (decoded show rows).
type CustomIspData struct {
	Ipgroup string `json:"ipgroup"`
	Time    string `json:"time"`
	ID      int64  `json:"id"`
	Comment string `json:"comment"`
	Name    string `json:"name"`
}

// ShowCustomIspByTagName 对齐 custom_isp.rs L22-41：show 参数固定 TYPE/limit 两键，
// 返回按标签过滤（MatchTagNameFilter）后的行。
// ShowCustomIspByTagName mirrors custom_isp.rs L22-41: show carries exactly the
// TYPE/limit params, returning rows filtered by tag (MatchTagNameFilter).
func ShowCustomIspByTagName(api *IKuaiClient, tagName string) ([]CustomIspData, error) {
	param := map[string]any{"TYPE": "total,data", "limit": "0,1000"}
	var resp CallResp
	if err := api.Call(FUNC_NAME_CUSTOM_ISP, "show", param, &resp); err != nil {
		return nil, err
	}
	var rows []CustomIspData
	if err := decodeResultsData(&resp, &rows); err != nil {
		return nil, err
	}
	out := make([]CustomIspData, 0, len(rows))
	for _, d := range rows {
		if MatchTagNameFilter(tagName, d.Name, d.Comment) {
			out = append(out, d)
		}
	}
	return out, nil
}

// AddCustomIsp 对齐 custom_isp.rs L43-58：add 只带 name/ipgroup/comment 三键，
// ipgroup 仅做首尾 trim，comment 写入分片标记。
// AddCustomIsp mirrors custom_isp.rs L43-58: add carries exactly name/ipgroup/comment,
// ipgroup is only end-trimmed, and comment carries the chunk marker.
func AddCustomIsp(api *IKuaiClient, tag, ipgroup string, index int64) error {
	param := map[string]any{
		"name":    BuildTagName(tag),
		"ipgroup": strings.TrimSpace(ipgroup),
		"comment": BuildCustomIspChunkComment(index),
	}
	var resp CallResp
	return api.Call(FUNC_NAME_CUSTOM_ISP, "add", param, &resp)
}

// EditCustomIsp 对齐 custom_isp.rs L60-77：add 三键 + id。
// EditCustomIsp mirrors custom_isp.rs L60-77: the add fields plus id.
func EditCustomIsp(api *IKuaiClient, tag, ipgroup string, index, id int64) error {
	param := map[string]any{
		"name":    BuildTagName(tag),
		"ipgroup": strings.TrimSpace(ipgroup),
		"comment": BuildCustomIspChunkComment(index),
		"id":      id,
	}
	var resp CallResp
	return api.Call(FUNC_NAME_CUSTOM_ISP, "edit", param, &resp)
}

// DelCustomIsp 对齐 custom_isp.rs L79-87：del 只带 id CSV。
// DelCustomIsp mirrors custom_isp.rs L79-87: del carries only the id CSV.
func DelCustomIsp(api *IKuaiClient, idCSV string) error {
	var resp CallResp
	return api.Call(FUNC_NAME_CUSTOM_ISP, "del", map[string]any{"id": idCSV}, &resp)
}

// GetCustomIspMap 对齐 custom_isp.rs L89-105：先按备注解析分片序号，失败回退
// 名称解析；同一序号保留先见 id（entry.or_insert 语义）。
// GetCustomIspMap mirrors custom_isp.rs L89-105: parse the chunk index from the
// comment first, falling back to the name; the first id seen per index wins
// (entry.or_insert semantics).
func GetCustomIspMap(api *IKuaiClient, tag string) (map[int64]int64, error) {
	data, err := ShowCustomIspByTagName(api, "")
	if err != nil {
		return nil, err
	}
	out := make(map[int64]int64)
	for _, d := range data {
		if !MatchTagNameFilter(tag, d.Name, d.Comment) {
			continue
		}
		idx, ok := ParseCustomIspChunkIndexFromComment(d.Comment)
		if !ok {
			idx, ok = ParseCustomIspChunkIndexFromName(d.Name, tag)
		}
		if !ok {
			continue
		}
		if _, exists := out[idx]; !exists {
			out[idx] = d.ID
		}
	}
	return out, nil
}

// DelCustomIspAll 对齐 custom_isp.rs L107-120：循环 show→按 MatchCleanTag 圈定受管行
// →批量 del，直到无可删行；非受管规则永不入删。
// DelCustomIspAll mirrors custom_isp.rs L107-120: loop show → collect managed rows
// via MatchCleanTag → batch del, until nothing matches; unmanaged rules are never dropped.
func DelCustomIspAll(api *IKuaiClient, cleanTag string) error {
	for {
		data, err := ShowCustomIspByTagName(api, "")
		if err != nil {
			return err
		}
		var ids []string
		for _, d := range data {
			if MatchCleanTag(cleanTag, d.Comment, d.Name) {
				ids = append(ids, strconv.FormatInt(d.ID, 10))
			}
		}
		if len(ids) == 0 {
			return nil
		}
		if err := DelCustomIsp(api, strings.Join(ids, ",")); err != nil {
			return err
		}
	}
}

// BuildCustomIspChunkComment 对齐 custom_isp.rs L122-128：chunk=index+1，1 档裸
// NewComment，其余 "NewComment-N"。
// BuildCustomIspChunkComment mirrors custom_isp.rs L122-128: chunk=index+1, the
// bare NewComment for chunk 1 and "NewComment-N" otherwise.
func BuildCustomIspChunkComment(index int64) string {
	chunk := index + 1
	if chunk <= 1 {
		return NewComment
	}
	return NewComment + "-" + strconv.FormatInt(chunk, 10)
}

// ParseCustomIspChunkIndexFromComment 对齐 custom_isp.rs L130-158：依次尝试三类
// 历史标记（NewComment / joyanhui/ikuai-bypass / IKUAI_BYPASS），前缀重复出现全部
// 剥离，尾随 '-'/'_' 全部剥离；纯前缀或空尾缀 => 1，可解析正整数 => 该值。
// ParseCustomIspChunkIndexFromComment mirrors custom_isp.rs L130-158: try the three
// historical markers in order, stripping every repeated prefix occurrence and every
// leading '-'/'_'; a bare prefix or empty suffix yields 1, a positive integer yields itself.
func ParseCustomIspChunkIndexFromComment(comment string) (int64, bool) {
	c := strings.TrimSpace(comment)
	if c == "" {
		return 0, false
	}
	// New chunks use `IkuaiBypass[-N]`, but update/cleanup must still parse old markers.
	// 新分片使用 `IkuaiBypass[-N]`，但更新/清理仍需解析旧备注标记。
	for _, prefix := range ManagedCommentMarkers() {
		if c == prefix {
			return 1, true
		}
		if !strings.HasPrefix(c, prefix) {
			continue
		}
		suffix := trimStartMatchesPrefix(c, prefix)
		suffix = strings.TrimLeft(suffix, "-")
		suffix = strings.TrimLeft(suffix, "_")
		suffix = strings.TrimSpace(suffix)
		if suffix == "" {
			return 1, true
		}
		if v, err := strconv.ParseInt(suffix, 10, 64); err == nil && v > 0 {
			return v, true
		}
	}
	return 0, false
}

// ParseCustomIspChunkIndexFromName 对齐 custom_isp.rs L160-171：剥掉（可重复的）
// 基础名后缀，剩余部分必须是正整数。
// ParseCustomIspChunkIndexFromName mirrors custom_isp.rs L160-171: strip the
// (repeatable) base name prefix, the remainder must be a positive integer.
func ParseCustomIspChunkIndexFromName(name, tag string) (int64, bool) {
	base := BuildTagName(tag)
	suffix := trimStartMatchesPrefix(strings.TrimSpace(name), base)
	if suffix == "" {
		return 0, false
	}
	v, err := strconv.ParseInt(suffix, 10, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}

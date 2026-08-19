// stream_ipport.go 端口分流 CRUD，行为对齐 rust_archive/crates/core/src/ikuai/stream_ipport.rs。
// Stream ip/port CRUD, aligned with stream_ipport.rs.
package ikuai

import (
	"strconv"
	"strings"
)

// StreamIpPortData 对齐 types.rs L112-140 的 StreamIpPortData（custom/object 已合并为 CSV）。
// StreamIpPortData mirrors StreamIpPortData in types.rs L112-140 (custom/object merged into CSV).
type StreamIpPortData struct {
	Protocol   string `json:"protocol"`
	TagName    string `json:"tagname"`
	SrcPort    string `json:"src_port"`
	ID         int64  `json:"id"`
	Enabled    string `json:"enabled"`
	Week       string `json:"week"`
	Comment    string `json:"comment"`
	Time       string `json:"time"`
	SrcAddrInv int64  `json:"src_addr_inv"`
	DstAddrInv int64  `json:"dst_addr_inv"`
	Nexthop    string `json:"nexthop"`
	IfaceBand  int64  `json:"iface_band"`
	Interface  string `json:"interface"`
	Mode       int64  `json:"mode"`
	SrcAddr    string `json:"src_addr"`
	DstPort    string `json:"dst_port"`
	DstAddr    string `json:"dst_addr"`
	Type       int64  `json:"type"`
}

// StreamIpPortSpec 对齐 stream_ipport.rs L136-149 的新增/编辑入参。
// StreamIpPortSpec mirrors the add/edit inputs of stream_ipport.rs L136-149.
type StreamIpPortSpec struct {
	ForwardType string
	Iface       string
	DstAddr     string
	SrcAddr     string
	SrcAddrInv  int64
	Nexthop     string
	Tag         string
	DstAddrInv  int64
	Prio        int64
	Mode        int64
	IfaceBand   int64
	Protocol    string
}

// streamIpPortRow show 响应原始行（stream_ipport.rs L31-55），comment/*_inv 缺省为 0/空。
// streamIpPortRow is the raw show row (stream_ipport.rs L31-55); comment and the
// *_inv fields default to empty/zero.
type streamIpPortRow struct {
	ID         int64        `json:"id"`
	Enabled    string       `json:"enabled"`
	Tagname    string       `json:"tagname"`
	Interface  string       `json:"interface"`
	Nexthop    string       `json:"nexthop"`
	Comment    string       `json:"comment"`
	IfaceBand  int64        `json:"iface_band"`
	Mode       int64        `json:"mode"`
	Protocol   string       `json:"protocol"`
	Type       int64        `json:"type"`
	SrcAddr    addrBlockRaw `json:"src_addr"`
	DstAddr    addrBlockRaw `json:"dst_addr"`
	SrcAddrInv int64        `json:"src_addr_inv"`
	DstAddrInv int64        `json:"dst_addr_inv"`
	Time       timeBlockRaw `json:"time"`
}

// ShowStreamIpPortByTagName 对齐 stream_ipport.rs L69-120：src/dst 各自 custom+object
// 合并展平，week/time 从 time.custom 首条还原。
// ShowStreamIpPortByTagName mirrors stream_ipport.rs L69-120: src and dst each merge
// custom+object into a flat CSV; week/time restored from the first time.custom entry.
func ShowStreamIpPortByTagName(api *IKuaiClient, tagName string) ([]StreamIpPortData, error) {
	param := map[string]any{"TYPE": "total,data", "limit": "0,1000"}
	var resp CallResp
	if err := api.Call(FUNC_NAME_STREAM_IPPORT, "show", param, &resp); err != nil {
		return nil, err
	}
	var rows []streamIpPortRow
	if err := decodeResultsData(&resp, &rows); err != nil {
		return nil, err
	}
	out := make([]StreamIpPortData, 0, len(rows))
	for _, d := range rows {
		if !MatchTagNameFilter(tagName, d.Tagname, d.Comment) {
			continue
		}
		srcs := append(ToStringList(d.SrcAddr.Custom), ToStringList(d.SrcAddr.Object)...)
		dsts := append(ToStringList(d.DstAddr.Custom), ToStringList(d.DstAddr.Object)...)
		item := StreamIpPortData{
			ID:         d.ID,
			Enabled:    d.Enabled,
			Comment:    d.Comment,
			TagName:    d.Tagname,
			Interface:  d.Interface,
			SrcAddrInv: d.SrcAddrInv,
			DstAddrInv: d.DstAddrInv,
			Nexthop:    d.Nexthop,
			IfaceBand:  d.IfaceBand,
			Mode:       d.Mode,
			Protocol:   d.Protocol,
			Type:       d.Type,
			SrcAddr:    strings.Join(srcs, ","),
			DstAddr:    strings.Join(dsts, ","),
		}
		if len(d.Time.Custom) > 0 {
			item.Week = d.Time.Custom[0].Weekdays
			item.Time = d.Time.Custom[0].StartTime + "-" + d.Time.Custom[0].EndTime
		}
		out = append(out, item)
	}
	return out, nil
}

// GetStreamIpPortMap 对齐 stream_ipport.rs L122-134：{tagname: id}，后见覆盖。
// GetStreamIpPortMap mirrors stream_ipport.rs L122-134: {tagname: id}, last wins.
func GetStreamIpPortMap(api *IKuaiClient, tag string) (map[string]int64, error) {
	data, err := ShowStreamIpPortByTagName(api, "")
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64)
	for _, d := range data {
		if MatchTagNameFilter(tag, d.TagName, d.Comment) {
			out[d.TagName] = d.ID
		}
	}
	return out, nil
}

// buildStreamIpPortParam 对齐 stream_ipport.rs L151-203 的固定请求体：
// tagname=BuildTagName（不分片）、protocol 空时默认 tcp+udp、area_code/dst_type 空、
// forward_type 解析失败回退 0。
// buildStreamIpPortParam mirrors the fixed body of stream_ipport.rs L151-203:
// tagname=BuildTagName (non-indexed), protocol defaults to tcp+udp when empty,
// empty area_code/dst_type, and forward_type parse failures fall back to 0.
func buildStreamIpPortParam(api *IKuaiClient, spec StreamIpPortSpec) (map[string]any, error) {
	// Rust `parse().unwrap_or(0)`：不 trim，解析失败即 0。
	// Rust `parse().unwrap_or(0)`: no trimming; any parse failure means 0.
	fType, _ := strconv.ParseInt(spec.ForwardType, 10, 64)
	srcCustom, srcObjNames := CategorizeAddrs(splitTrimmed(spec.SrcAddr))
	dstCustom, dstObjNames := CategorizeAddrs(splitTrimmed(spec.DstAddr))
	srcObjects, err := resolveIpGroupObjects(api, srcObjNames)
	if err != nil {
		return nil, err
	}
	dstObjects, err := resolveIpGroupObjects(api, dstObjNames)
	if err != nil {
		return nil, err
	}
	protocol := spec.Protocol
	if protocol == "" {
		protocol = "tcp+udp"
	}
	return map[string]any{
		"enabled":    "yes",
		"tagname":    BuildTagName(spec.Tag),
		"interface":  spec.Iface,
		"nexthop":    spec.Nexthop,
		"iface_band": spec.IfaceBand,
		// Keep new remark alnum-only for firmware fields that reject `/` and other symbols.
		// 新备注仅使用字母数字，兼容部分固件对备注字符集的限制。
		"comment":  NewComment,
		"type":     fType,
		"mode":     spec.Mode,
		"protocol": protocol,
		"src_addr": map[string]any{"custom": srcCustom, "object": srcObjects},
		"dst_addr": map[string]any{"custom": dstCustom, "object": dstObjects},
		// iKuai uses *_inv numeric flags for inverse matching on address blocks.
		// 爱快通过 *_inv 数值字段表达源/目的地址块的反向匹配。
		"src_addr_inv": spec.SrcAddrInv,
		"dst_addr_inv": spec.DstAddrInv,
		"src_port":     map[string]any{"custom": []string{}, "object": []any{}},
		"dst_port":     map[string]any{"custom": []string{}, "object": []any{}},
		"time":         weeklyTimeBlock(),
		"prio":         spec.Prio,
		"area_code":    "",
		"dst_type":     "",
	}, nil
}

// AddStreamIpPort 对齐 stream_ipport.rs L151-203。
// AddStreamIpPort mirrors stream_ipport.rs L151-203.
func AddStreamIpPort(api *IKuaiClient, spec StreamIpPortSpec) error {
	param, err := buildStreamIpPortParam(api, spec)
	if err != nil {
		return err
	}
	var resp CallResp
	return api.Call(FUNC_NAME_STREAM_IPPORT, "add", param, &resp)
}

// EditStreamIpPort 对齐 stream_ipport.rs L205-259：add 字段 + id。
// EditStreamIpPort mirrors stream_ipport.rs L205-259: the add fields plus id.
func EditStreamIpPort(api *IKuaiClient, spec StreamIpPortSpec, id int64) error {
	param, err := buildStreamIpPortParam(api, spec)
	if err != nil {
		return err
	}
	param["id"] = id
	var resp CallResp
	return api.Call(FUNC_NAME_STREAM_IPPORT, "edit", param, &resp)
}

// DelStreamIpPort 对齐 stream_ipport.rs L261-269：del 只带 id CSV。
// DelStreamIpPort mirrors stream_ipport.rs L261-269: del carries only the id CSV.
func DelStreamIpPort(api *IKuaiClient, idCSV string) error {
	var resp CallResp
	return api.Call(FUNC_NAME_STREAM_IPPORT, "del", map[string]any{"id": idCSV}, &resp)
}

// DelIkuaiBypassStreamIpPort 对齐 stream_ipport.rs L271-287：循环 show→MatchCleanTag→批量 del。
// DelIkuaiBypassStreamIpPort mirrors stream_ipport.rs L271-287: loop show → collect
// via MatchCleanTag → batch del.
func DelIkuaiBypassStreamIpPort(api *IKuaiClient, cleanTag string) error {
	for {
		data, err := ShowStreamIpPortByTagName(api, "")
		if err != nil {
			return err
		}
		var ids []string
		for _, d := range data {
			if MatchCleanTag(cleanTag, d.Comment, d.TagName) {
				ids = append(ids, strconv.FormatInt(d.ID, 10))
			}
		}
		if len(ids) == 0 {
			return nil
		}
		if err := DelStreamIpPort(api, strings.Join(ids, ",")); err != nil {
			return err
		}
	}
}

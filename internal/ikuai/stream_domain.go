// stream_domain.go 域名分流 CRUD，行为对齐 crates/core/src/ikuai/stream_domain.rs。
// Stream-domain CRUD, aligned with stream_domain.rs.
package ikuai

import (
	"encoding/json"
	"strconv"
	"strings"
)

// StreamDomainData 对齐 types.rs L75-88 的 StreamDomainData（custom/object 已合并为 CSV）。
// StreamDomainData mirrors StreamDomainData in types.rs L75-88 (custom/object merged into CSV).
type StreamDomainData struct {
	Week      string `json:"week"`
	Comment   string `json:"comment"`
	TagName   string `json:"tagname"`
	Domain    string `json:"domain"`
	SrcAddr   string `json:"src_addr"`
	Interface string `json:"interface"`
	Time      string `json:"time"`
	ID        int64  `json:"id"`
	Enabled   string `json:"enabled"`
}

// StreamDomainSpec 对齐 stream_domain.rs L104-111 的新增/编辑入参。
// StreamDomainSpec mirrors the add/edit inputs of stream_domain.rs L104-111.
type StreamDomainSpec struct {
	Iface             string
	Tag               string
	SrcAddr           string
	SrcAddrOptIpgroup string
	Domains           string
	Index             int64
}

// addrBlockRaw 保留地址块的 custom/object 原始 JSON，交由 ToStringList 统一转换
// （stream_domain.rs L12-16 的 Value 语义）。
// addrBlockRaw keeps the raw custom/object JSON of an address block for
// ToStringList conversion (the Value semantics of stream_domain.rs L12-16).
type addrBlockRaw struct {
	Custom json.RawMessage `json:"custom"`
	Object json.RawMessage `json:"object"`
}

// timeBlockRaw 对齐 stream_domain.rs L25-30：仅消费 time.custom 首个周期条目。
// timeBlockRaw mirrors stream_domain.rs L25-30: only the first time.custom entry is consumed.
type timeBlockRaw struct {
	Custom []struct {
		Weekdays  string `json:"weekdays"`
		StartTime string `json:"start_time"`
		EndTime   string `json:"end_time"`
	} `json:"custom"`
}

// streamDomainRow show 响应原始行（stream_domain.rs L32-45），comment 缺省为空。
// streamDomainRow is the raw show row (stream_domain.rs L32-45); comment defaults to empty.
type streamDomainRow struct {
	ID        int64        `json:"id"`
	Enabled   string       `json:"enabled"`
	Tagname   string       `json:"tagname"`
	Interface string       `json:"interface"`
	Comment   string       `json:"comment"`
	SrcAddr   addrBlockRaw `json:"src_addr"`
	Domain    addrBlockRaw `json:"domain"`
	Time      timeBlockRaw `json:"time"`
}

// ShowStreamDomainByTagName 对齐 stream_domain.rs L59-102：custom+object 合并展平，
// week/time 从 time.custom 首条还原为 "weekdays" 与 "start-end"。
// ShowStreamDomainByTagName mirrors stream_domain.rs L59-102: custom+object merge
// and flatten; week/time restored from the first time.custom entry as "weekdays"
// and "start-end".
func ShowStreamDomainByTagName(api *IKuaiClient, tagName string) ([]StreamDomainData, error) {
	param := map[string]any{"TYPE": "total,data", "limit": "0,1000"}
	var resp CallResp
	if err := api.Call(FUNC_NAME_STREAM_DOMAIN, "show", param, &resp); err != nil {
		return nil, err
	}
	var rows []streamDomainRow
	if err := decodeResultsData(&resp, &rows); err != nil {
		return nil, err
	}
	out := make([]StreamDomainData, 0, len(rows))
	for _, d := range rows {
		if !MatchTagNameFilter(tagName, d.Tagname, d.Comment) {
			continue
		}
		srcs := append(ToStringList(d.SrcAddr.Custom), ToStringList(d.SrcAddr.Object)...)
		domains := append(ToStringList(d.Domain.Custom), ToStringList(d.Domain.Object)...)
		item := StreamDomainData{
			ID:        d.ID,
			Enabled:   d.Enabled,
			Comment:   d.Comment,
			TagName:   d.Tagname,
			Interface: d.Interface,
			Domain:    strings.Join(domains, ","),
			SrcAddr:   strings.Join(srcs, ","),
		}
		if len(d.Time.Custom) > 0 {
			item.Week = d.Time.Custom[0].Weekdays
			item.Time = d.Time.Custom[0].StartTime + "-" + d.Time.Custom[0].EndTime
		}
		out = append(out, item)
	}
	return out, nil
}

// weeklyTimeBlock 固定的全天周期块（stream_domain.rs L135/L167）。
// weeklyTimeBlock is the fixed all-day weekly block (stream_domain.rs L135/L167).
func weeklyTimeBlock() map[string]any {
	return map[string]any{
		"custom": []map[string]string{{
			"type":       "weekly",
			"weekdays":   "1234567",
			"start_time": "00:00",
			"end_time":   "23:59",
			"comment":    "",
		}},
		"object": []any{},
	}
}

// splitTrimmed 逗号分词：逐项 trim、丢弃空项（stream_domain.rs L119-125 等）。
// splitTrimmed splits on commas, trimming and dropping blanks
// (stream_domain.rs L119-125 etc.).
func splitTrimmed(s string) []string {
	out := make([]string, 0)
	for _, part := range strings.Split(strings.TrimSpace(s), ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// buildStreamDomainParam 对齐 stream_domain.rs L113-142 的固定请求体：
// enabled=yes、tagname=BuildIndexedTagName、comment=NewComment、prio=31、周 1234567 全天。
// buildStreamDomainParam mirrors the fixed body of stream_domain.rs L113-142:
// enabled=yes, tagname=BuildIndexedTagName, comment=NewComment, prio=31 and the
// all-day weekly 1234567 block.
func buildStreamDomainParam(api *IKuaiClient, spec StreamDomainSpec) (map[string]any, error) {
	srcCustom, srcObjects, err := resolveSrcAddrs(api, spec.SrcAddr, spec.SrcAddrOptIpgroup)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"enabled":   "yes",
		"tagname":   BuildIndexedTagName(spec.Tag, spec.Index),
		"interface": spec.Iface,
		"src_addr":  map[string]any{"custom": srcCustom, "object": srcObjects},
		"domain":    map[string]any{"custom": splitTrimmed(spec.Domains), "object": []any{}},
		// Keep new remark alnum-only for firmware fields that reject `/` and other symbols.
		// 新备注仅使用字母数字，兼容部分固件对备注字符集的限制。
		"comment": NewComment,
		"time":    weeklyTimeBlock(),
		"prio":    31,
	}, nil
}

// AddStreamDomain 对齐 stream_domain.rs L113-142。
// AddStreamDomain mirrors stream_domain.rs L113-142.
func AddStreamDomain(api *IKuaiClient, spec StreamDomainSpec) error {
	param, err := buildStreamDomainParam(api, spec)
	if err != nil {
		return err
	}
	var resp CallResp
	return api.Call(FUNC_NAME_STREAM_DOMAIN, "add", param, &resp)
}

// EditStreamDomain 对齐 stream_domain.rs L144-175：add 字段 + id。
// EditStreamDomain mirrors stream_domain.rs L144-175: the add fields plus id.
func EditStreamDomain(api *IKuaiClient, spec StreamDomainSpec, id int64) error {
	param, err := buildStreamDomainParam(api, spec)
	if err != nil {
		return err
	}
	param["id"] = id
	var resp CallResp
	return api.Call(FUNC_NAME_STREAM_DOMAIN, "edit", param, &resp)
}

// DelStreamDomain 对齐 stream_domain.rs L177-185：del 只带 id CSV。
// DelStreamDomain mirrors stream_domain.rs L177-185: del carries only the id CSV.
func DelStreamDomain(api *IKuaiClient, idCSV string) error {
	var resp CallResp
	return api.Call(FUNC_NAME_STREAM_DOMAIN, "del", map[string]any{"id": idCSV}, &resp)
}

// GetStreamDomainMap 对齐 stream_domain.rs L187-204：剥掉基础名后尾缀必须整段可解析，
// 同名尾缀后见覆盖（insert 语义）。
// GetStreamDomainMap mirrors stream_domain.rs L187-204: the suffix after the base
// name must parse as a whole; later rows overwrite earlier ones (insert semantics).
func GetStreamDomainMap(api *IKuaiClient, tag string) (map[int64]int64, error) {
	data, err := ShowStreamDomainByTagName(api, "")
	if err != nil {
		return nil, err
	}
	base := BuildTagName(tag)
	out := make(map[int64]int64)
	for _, d := range data {
		if !MatchTagNameFilter(tag, d.TagName, d.Comment) {
			continue
		}
		suffix := trimStartMatchesPrefix(strings.TrimSpace(d.TagName), base)
		if idx, err := strconv.ParseInt(suffix, 10, 64); err == nil {
			out[idx] = d.ID
		}
	}
	return out, nil
}

// DelStreamDomainAll 对齐 stream_domain.rs L206-219：循环 show→MatchCleanTag→批量 del。
// DelStreamDomainAll mirrors stream_domain.rs L206-219: loop show → collect via
// MatchCleanTag → batch del.
func DelStreamDomainAll(api *IKuaiClient, cleanTag string) error {
	for {
		data, err := ShowStreamDomainByTagName(api, "")
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
		if err := DelStreamDomain(api, strings.Join(ids, ",")); err != nil {
			return err
		}
	}
}

// resolveSrcAddrs 对齐 stream_domain.rs L221-254：优先展开分组引用（SrcAddrOptIpgroup），
// 无任何命中直接报 api 错误；否则按 CategorizeAddrs 拆分自定义地址与分组对象引用。
// resolveSrcAddrs mirrors stream_domain.rs L221-254: expand group references first
// (SrcAddrOptIpgroup), failing with an api error when nothing resolves; otherwise
// split custom addresses from group object references via CategorizeAddrs.
func resolveSrcAddrs(api *IKuaiClient, srcAddr, srcAddrOptIpgroup string) ([]string, []map[string]any, error) {
	if strings.TrimSpace(srcAddrOptIpgroup) != "" {
		var resolved []string
		for _, name := range strings.Split(srcAddrOptIpgroup, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			matches, err := ResolveRuleReferenceIpGroupNames(api, name)
			if err != nil {
				return nil, nil, err
			}
			resolved = append(resolved, matches...)
		}
		if len(resolved) == 0 {
			return nil, nil, &IKuaiError{Kind: ErrKindApi,
				Msg: "no matching source IP groups found for stream-domain reference: " + srcAddrOptIpgroup}
		}
		objects, err := resolveIpGroupObjects(api, resolved)
		if err != nil {
			return nil, nil, err
		}
		return []string{}, objects, nil
	}

	var srcList []string
	if s := strings.TrimSpace(srcAddr); s != "" {
		for _, part := range strings.Split(s, ",") {
			srcList = append(srcList, strings.TrimSpace(part))
		}
	}
	custom, objNames := CategorizeAddrs(srcList)
	objects, err := resolveIpGroupObjects(api, objNames)
	if err != nil {
		return nil, nil, err
	}
	return custom, objects, nil
}

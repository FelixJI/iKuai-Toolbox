// ipv6_group.go IPv6 分组（route_object type=1）CRUD，与 ip_group 对称，
// 行为对齐 rust_archive/crates/core/src/ikuai/ipv6_group.rs。
// IPv6 group (route_object type=1) CRUD, symmetric with ip_group, aligned with ipv6_group.rs.
package ikuai

import (
	"strconv"
	"strings"
)

// Ipv6GroupData 对齐 types.rs L101-110 的 Ipv6GroupData（group_value 已展平）。
// Ipv6GroupData mirrors Ipv6GroupData in types.rs L101-110 (group_value flattened).
type Ipv6GroupData struct {
	AddrPool  string `json:"addr_pool"`
	Comment   string `json:"comment"`
	GroupName string `json:"group_name"`
	ID        int64  `json:"id"`
	Type      int64  `json:"type"`
}

// Ipv6GroupEntry GetIpv6GroupMapWithName 的值：分片序号 => 分组 id + 原始分组名。
// Ipv6GroupEntry is the value of GetIpv6GroupMapWithName: chunk index => group id + original name.
type Ipv6GroupEntry struct {
	ID   int64
	Name string
}

// ShowIpv6GroupByTagName 对齐 ipv6_group.rs L33-70：FILTER1 固定 "type,=,1"，
// group_value 取 "ipv6" 键展平。
// ShowIpv6GroupByTagName mirrors ipv6_group.rs L33-70: FILTER1 is always "type,=,1";
// group_value entries flatten via their "ipv6" key.
func ShowIpv6GroupByTagName(api *IKuaiClient, tagName string) ([]Ipv6GroupData, error) {
	param := map[string]any{"TYPE": "total,data", "limit": "0,1000", "FILTER1": "type,=,1"}
	var resp CallResp
	if err := api.Call(FUNC_NAME_ROUTE_OBJECT, "show", param, &resp); err != nil {
		return nil, err
	}
	var rows []routeObjectRow
	if err := decodeResultsData(&resp, &rows); err != nil {
		return nil, err
	}
	out := make([]Ipv6GroupData, 0, len(rows))
	for _, d := range rows {
		if !MatchTagNameFilter(tagName, d.GroupName, d.Comment) {
			continue
		}
		ips := make([]string, 0, len(d.GroupValue))
		for _, v := range d.GroupValue {
			if ip, ok := v["ipv6"]; ok {
				ips = append(ips, ip)
			}
		}
		out = append(out, Ipv6GroupData{
			ID:        d.ID,
			GroupName: d.GroupName,
			AddrPool:  strings.Join(ips, ","),
			Comment:   d.Comment,
			Type:      d.Type,
		})
	}
	return out, nil
}

// ShowIpv6GroupByName 对齐 ipv6_group.rs L72-77。
// ShowIpv6GroupByName mirrors ipv6_group.rs L72-77.
func ShowIpv6GroupByName(api *IKuaiClient, name string) ([]Ipv6GroupData, error) {
	return ShowIpv6GroupByTagName(api, name)
}

// AddIpv6Group 对齐 ipv6_group.rs L79-86。
// AddIpv6Group mirrors ipv6_group.rs L79-86.
func AddIpv6Group(api *IKuaiClient, tag, addrPool string, index int64) error {
	return AddIpv6GroupNamed(api, BuildIndexedTagName(tag, index), addrPool)
}

// EditIpv6Group 对齐 ipv6_group.rs L88-96。
// EditIpv6Group mirrors ipv6_group.rs L88-96.
func EditIpv6Group(api *IKuaiClient, tag, addrPool string, index, id int64) error {
	return EditIpv6GroupNamed(api, BuildIndexedTagName(tag, index), addrPool, id)
}

// buildIpv6GroupValue 对齐 ipv6_group.rs L103-111：每项 {ipv6, comment:""}。
// buildIpv6GroupValue mirrors ipv6_group.rs L103-111: one {ipv6, comment:""} per item.
func buildIpv6GroupValue(addrPool string) []map[string]string {
	ips := make([]map[string]string, 0)
	for _, ip := range strings.Split(addrPool, ",") {
		if ip = strings.TrimSpace(ip); ip != "" {
			ips = append(ips, map[string]string{"ipv6": ip, "comment": ""})
		}
	}
	return ips
}

// AddIpv6GroupNamed 对齐 ipv6_group.rs L98-122：v6 请求体固定
// group_name/type=1/group_value{ipv6}/comment=""。
// AddIpv6GroupNamed mirrors ipv6_group.rs L98-122: the v6 body is always
// group_name/type=1/group_value{ipv6}/comment="".
func AddIpv6GroupNamed(api *IKuaiClient, groupName, addrPool string) error {
	param := map[string]any{
		"group_name":  groupName,
		"type":        1,
		"group_value": buildIpv6GroupValue(addrPool),
		"comment":     "",
	}
	var resp CallResp
	return api.Call(FUNC_NAME_ROUTE_OBJECT, "add", param, &resp)
}

// EditIpv6GroupNamed 对齐 ipv6_group.rs L124-150：add 字段 + id。
// EditIpv6GroupNamed mirrors ipv6_group.rs L124-150: the add fields plus id.
func EditIpv6GroupNamed(api *IKuaiClient, groupName, addrPool string, id int64) error {
	param := map[string]any{
		"group_name":  groupName,
		"type":        1,
		"group_value": buildIpv6GroupValue(addrPool),
		"comment":     "",
		"id":          id,
	}
	var resp CallResp
	return api.Call(FUNC_NAME_ROUTE_OBJECT, "edit", param, &resp)
}

// DelIpv6Group 对齐 ipv6_group.rs L152-160。
// DelIpv6Group mirrors ipv6_group.rs L152-160.
func DelIpv6Group(api *IKuaiClient, idCSV string) error {
	var resp CallResp
	return api.Call(FUNC_NAME_ROUTE_OBJECT, "del", map[string]any{"id": idCSV}, &resp)
}

// GetIpv6GroupMap 对齐 ipv6_group.rs L162-168：{分片序号: 分组 id}。
// GetIpv6GroupMap mirrors ipv6_group.rs L162-168: {chunk index: group id}.
func GetIpv6GroupMap(api *IKuaiClient, tag string) (map[int64]int64, error) {
	withName, err := GetIpv6GroupMapWithName(api, tag)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]int64, len(withName))
	for k, e := range withName {
		out[k] = e.ID
	}
	return out, nil
}

// GetIpv6GroupMapWithName 对齐 ipv6_group.rs L210-225：过滤受管分组并解析分片序号，
// 同一序号保留先见（entry.or_insert）。
// GetIpv6GroupMapWithName mirrors ipv6_group.rs L210-225: filter managed groups and
// parse chunk indexes; the first entry per index wins (entry.or_insert).
func GetIpv6GroupMapWithName(api *IKuaiClient, tag string) (map[int64]Ipv6GroupEntry, error) {
	data, err := ShowIpv6GroupByTagName(api, "")
	if err != nil {
		return nil, err
	}
	out := make(map[int64]Ipv6GroupEntry)
	for _, d := range data {
		if !MatchTagNameFilter(tag, d.GroupName, d.Comment) {
			continue
		}
		if idx, ok := ParseIndexFromGroupName(tag, d.GroupName); ok {
			if _, exists := out[idx]; !exists {
				out[idx] = Ipv6GroupEntry{ID: d.ID, Name: d.GroupName}
			}
		}
	}
	return out, nil
}

// DelIkuaiBypassIpv6Group 对齐 ipv6_group.rs L227-243：循环 show→MatchCleanTag→批量 del。
// DelIkuaiBypassIpv6Group mirrors ipv6_group.rs L227-243: loop show → collect via
// MatchCleanTag → batch del.
func DelIkuaiBypassIpv6Group(api *IKuaiClient, cleanTag string) error {
	for {
		data, err := ShowIpv6GroupByTagName(api, "")
		if err != nil {
			return err
		}
		var ids []string
		for _, d := range data {
			if MatchCleanTag(cleanTag, d.Comment, d.GroupName) {
				ids = append(ids, strconv.FormatInt(d.ID, 10))
			}
		}
		if len(ids) == 0 {
			return nil
		}
		if err := DelIpv6Group(api, strings.Join(ids, ",")); err != nil {
			return err
		}
	}
}

// GetAllIkuaiBypassIpv6GroupNamesByName 对齐 ipv6_group.rs L245-255：按名称查询后二次过滤。
// GetAllIkuaiBypassIpv6GroupNamesByName mirrors ipv6_group.rs L245-255: query by
// name then filter once more.
func GetAllIkuaiBypassIpv6GroupNamesByName(api *IKuaiClient, name string) ([]string, error) {
	data, err := ShowIpv6GroupByName(api, name)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(data))
	for _, d := range data {
		if MatchTagNameFilter(name, d.GroupName, d.Comment) {
			out = append(out, d.GroupName)
		}
	}
	return out, nil
}

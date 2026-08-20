// ip_group.go IPv4 分组（route_object type=0）CRUD 与索引解析，
// 行为对齐 rust_archive/crates/core/src/ikuai/ip_group.rs。
// IPv4 group (route_object type=0) CRUD and index parsing, aligned with ip_group.rs.
package ikuai

import (
	"strconv"
	"strings"
)

// IpGroupData 对齐 types.rs L90-99 的 IpGroupData（group_value 已展平为 AddrPool CSV）。
// IpGroupData mirrors IpGroupData in types.rs L90-99 (group_value flattened to AddrPool CSV).
type IpGroupData struct {
	AddrPool  string `json:"addr_pool"`
	Comment   string `json:"comment"`
	GroupName string `json:"group_name"`
	ID        int64  `json:"id"`
	Type      int64  `json:"type"`
}

// IpGroupEntry GetIpGroupMapWithName 的值：分片序号 => 分组 id + 原始分组名。
// IpGroupEntry is the value of GetIpGroupMapWithName: chunk index => group id + original name.
type IpGroupEntry struct {
	ID   int64
	Name string
}

// routeObjectRow show 响应原始行（ip_group.rs L7-17），comment 缺省为空。
// routeObjectRow is the raw show row (ip_group.rs L7-17); comment defaults to empty.
type routeObjectRow struct {
	ID         int64               `json:"id"`
	GroupName  string              `json:"group_name"`
	Type       int64               `json:"type"`
	GroupValue []map[string]string `json:"group_value"`
	Comment    string              `json:"comment"`
}

// ShowIpGroupByTagName 对齐 ip_group.rs L33-70：FILTER1 固定 "type,=,0"，
// 按 MatchTagNameFilter 过滤并把 group_value 的 ip 键展平为逗号拼接。
// ShowIpGroupByTagName mirrors ip_group.rs L33-70: FILTER1 is always "type,=,0";
// rows are filtered by MatchTagNameFilter and their group_value "ip" keys are
// flattened into a comma-joined pool.
func ShowIpGroupByTagName(api *IKuaiClient, tagName string) ([]IpGroupData, error) {
	param := map[string]any{"TYPE": "total,data", "limit": "0,1000", "FILTER1": "type,=,0"}
	var resp CallResp
	if err := api.Call(FUNC_NAME_ROUTE_OBJECT, "show", param, &resp); err != nil {
		return nil, err
	}
	var rows []routeObjectRow
	if err := decodeResultsData(&resp, &rows); err != nil {
		return nil, err
	}
	out := make([]IpGroupData, 0, len(rows))
	for _, d := range rows {
		if !MatchTagNameFilter(tagName, d.GroupName, d.Comment) {
			continue
		}
		ips := make([]string, 0, len(d.GroupValue))
		for _, v := range d.GroupValue {
			if ip, ok := v["ip"]; ok {
				ips = append(ips, ip)
			}
		}
		out = append(out, IpGroupData{
			ID:        d.ID,
			GroupName: d.GroupName,
			AddrPool:  strings.Join(ips, ","),
			Comment:   d.Comment,
			Type:      d.Type,
		})
	}
	return out, nil
}

// ShowIpGroupByName 对齐 ip_group.rs L72-77：按精确/模糊名称复用标签查询。
// ShowIpGroupByName mirrors ip_group.rs L72-77: reuse the tag query with the given name.
func ShowIpGroupByName(api *IKuaiClient, name string) ([]IpGroupData, error) {
	return ShowIpGroupByTagName(api, name)
}

// AddIpGroup 对齐 ip_group.rs L79-86：分组名用 BuildIndexedTagName(tag, index)。
// AddIpGroup mirrors ip_group.rs L79-86: the group name is BuildIndexedTagName(tag, index).
func AddIpGroup(api *IKuaiClient, tag, addrPool string, index int64) error {
	return AddIpGroupNamed(api, BuildIndexedTagName(tag, index), addrPool)
}

// EditIpGroup 对齐 ip_group.rs L88-96。
// EditIpGroup mirrors ip_group.rs L88-96.
func EditIpGroup(api *IKuaiClient, tag, addrPool string, index, id int64) error {
	return EditIpGroupNamed(api, BuildIndexedTagName(tag, index), addrPool, id)
}

// buildIpGroupValue 把地址池 CSV 转为 route_object 的 group_value
// （ip_group.rs L103-111）：逐项 trim、跳过空项，每项 {ip, comment:""}。
// buildIpGroupValue turns the addr-pool CSV into the route_object group_value
// (ip_group.rs L103-111): trim each item, skip blanks, one {ip, comment:""} per item.
func buildIpGroupValue(addrPool string) []map[string]string {
	ips := make([]map[string]string, 0)
	for _, ip := range strings.Split(addrPool, ",") {
		if ip = strings.TrimSpace(ip); ip != "" {
			ips = append(ips, map[string]string{"ip": ip, "comment": ""})
		}
	}
	return ips
}

// AddIpGroupNamed 对齐 ip_group.rs L98-124：v4 请求体固定
// group_name/type=0/group_value/comment=""（v4 分组故意留空 comment，
// 爱快 4.x 的 IP 分组 comment 行为不稳定）。
// AddIpGroupNamed mirrors ip_group.rs L98-124: the v4 body is always
// group_name/type=0/group_value/comment="" (the v4 comment is intentionally empty:
// iKuai v4.x IP-group comment handling is unstable).
func AddIpGroupNamed(api *IKuaiClient, groupName, addrPool string) error {
	param := map[string]any{
		"group_name":  groupName,
		"type":        0,
		"group_value": buildIpGroupValue(addrPool),
		"comment":     "",
	}
	var resp CallResp
	return api.Call(FUNC_NAME_ROUTE_OBJECT, "add", param, &resp)
}

// EditIpGroupNamed 对齐 ip_group.rs L126-152：add 字段 + id。
// EditIpGroupNamed mirrors ip_group.rs L126-152: the add fields plus id.
func EditIpGroupNamed(api *IKuaiClient, groupName, addrPool string, id int64) error {
	param := map[string]any{
		"group_name":  groupName,
		"type":        0,
		"group_value": buildIpGroupValue(addrPool),
		"comment":     "",
		"id":          id,
	}
	var resp CallResp
	return api.Call(FUNC_NAME_ROUTE_OBJECT, "edit", param, &resp)
}

// DelIpGroup 对齐 ip_group.rs L154-162：del 只带 id CSV。
// DelIpGroup mirrors ip_group.rs L154-162: del carries only the id CSV.
func DelIpGroup(api *IKuaiClient, idCSV string) error {
	var resp CallResp
	return api.Call(FUNC_NAME_ROUTE_OBJECT, "del", map[string]any{"id": idCSV}, &resp)
}

// GetIpGroupMap 对齐 ip_group.rs L164-170：{分片序号: 分组 id}。
// GetIpGroupMap mirrors ip_group.rs L164-170: {chunk index: group id}.
func GetIpGroupMap(api *IKuaiClient, tag string) (map[int64]int64, error) {
	withName, err := GetIpGroupMapWithName(api, tag)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]int64, len(withName))
	for k, e := range withName {
		out[k] = e.ID
	}
	return out, nil
}

// parseTrailingDigits 对齐 ip_group.rs L172-189：提取并解析结尾连续 ASCII 数字。
// parseTrailingDigits mirrors ip_group.rs L172-189: extract and parse the run of
// trailing ASCII digits.
func parseTrailingDigits(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	start := len(s)
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] < '0' || s[i] > '9' {
			break
		}
		start = i
	}
	if start == len(s) {
		return 0, false
	}
	v, err := strconv.ParseInt(s[start:], 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// ParseIndexFromGroupName 对齐 ip_group.rs L191-212：剥掉基础名后剩余部分整段解析，
// 失败再取尾数字；名字不以基础名开头（如被截断）时对完整名字取尾数字兜底；
// 名字恰为基础名（剩余为空）时判定失败。
// ParseIndexFromGroupName mirrors ip_group.rs L191-212: parse the remainder after
// stripping the base name as a whole, falling back to its trailing digits; when the
// name does not start with the base (e.g. truncated), use the trailing digits of the
// full name; a name exactly equal to the base yields no index.
func ParseIndexFromGroupName(tag, groupName string) (int64, bool) {
	name := strings.TrimSpace(groupName)
	if name == "" {
		return 0, false
	}
	base := BuildTagName(tag)
	if rest, ok := strings.CutPrefix(name, base); ok {
		rest = strings.TrimSpace(rest)
		if rest == "" {
			return 0, false
		}
		if idx, err := strconv.ParseInt(rest, 10, 64); err == nil {
			return idx, true
		}
		if idx, ok := parseTrailingDigits(rest); ok {
			return idx, true
		}
	}
	// Fallback for truncated base names: parse trailing digits on the full name.
	// 截断场景兜底：从完整名称尾部提取数字。
	return parseTrailingDigits(name)
}

// GetIpGroupMapWithName 对齐 ip_group.rs L214-229：过滤受管分组并解析分片序号，
// 同一序号保留先见（entry.or_insert）。
// GetIpGroupMapWithName mirrors ip_group.rs L214-229: filter managed groups and
// parse chunk indexes; the first entry per index wins (entry.or_insert).
func GetIpGroupMapWithName(api *IKuaiClient, tag string) (map[int64]IpGroupEntry, error) {
	data, err := ShowIpGroupByTagName(api, "")
	if err != nil {
		return nil, err
	}
	out := make(map[int64]IpGroupEntry)
	for _, d := range data {
		if !MatchTagNameFilter(tag, d.GroupName, d.Comment) {
			continue
		}
		if idx, ok := ParseIndexFromGroupName(tag, d.GroupName); ok {
			if _, exists := out[idx]; !exists {
				out[idx] = IpGroupEntry{ID: d.ID, Name: d.GroupName}
			}
		}
	}
	return out, nil
}

// DelIkuaiBypassIpGroup 对齐 ip_group.rs L231-247：循环 show→MatchCleanTag 圈定→批量 del。
// DelIkuaiBypassIpGroup mirrors ip_group.rs L231-247: loop show → collect via
// MatchCleanTag → batch del.
func DelIkuaiBypassIpGroup(api *IKuaiClient, cleanTag string) error {
	for {
		data, err := ShowIpGroupByTagName(api, "")
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
		if err := DelIpGroup(api, strings.Join(ids, ",")); err != nil {
			return err
		}
	}
}

// GetAllIkuaiBypassIpGroupNamesByName 对齐 ip_group.rs L249-259：按名称查询后二次过滤。
// GetAllIkuaiBypassIpGroupNamesByName mirrors ip_group.rs L249-259: query by name
// then filter once more.
func GetAllIkuaiBypassIpGroupNamesByName(api *IKuaiClient, name string) ([]string, error) {
	data, err := ShowIpGroupByName(api, name)
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

// ResolveRuleReferenceIpGroupNames 对齐 ip_group.rs L261-301：规则引用优先精确匹配
// 用户手工维护的分组名；没有精确命中时回退为按 IKB 标签展开托管分片。
// ResolveRuleReferenceIpGroupNames mirrors ip_group.rs L261-301: rule references
// prefer exact user-maintained group names and only fall back to expanding the
// managed IKB shards by tag when nothing matches exactly.
func ResolveRuleReferenceIpGroupNames(api *IKuaiClient, name string) ([]string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return []string{}, nil
	}
	data, err := ShowIpGroupByTagName(api, "")
	if err != nil {
		return nil, err
	}
	var exact []string
	exactSeen := make(map[string]struct{})
	var managed []string
	managedSeen := make(map[string]struct{})
	for _, d := range data {
		groupName := d.GroupName
		if strings.TrimSpace(groupName) == name {
			if _, dup := exactSeen[groupName]; !dup {
				exactSeen[groupName] = struct{}{}
				exact = append(exact, groupName)
			}
			continue
		}
		if MatchTagNameFilter(name, strings.TrimSpace(groupName), d.Comment) {
			if _, dup := managedSeen[groupName]; !dup {
				managedSeen[groupName] = struct{}{}
				managed = append(managed, groupName)
			}
		}
	}
	if len(exact) > 0 {
		return exact, nil
	}
	return managed, nil
}

// resolveIpGroupObjects 对齐 stream_domain.rs L256-275 / stream_ipport.rs L289-308：
// 按分组名查 id，生成 {"type":0,"gid":"IPGP<id>","gp_name":name} 对象引用；
// 找不到的名字直接跳过。
// resolveIpGroupObjects mirrors stream_domain.rs L256-275 / stream_ipport.rs
// L289-308: map names to ids and emit {"type":0,"gid":"IPGP<id>","gp_name":name}
// object references; unknown names are skipped.
func resolveIpGroupObjects(api *IKuaiClient, names []string) ([]map[string]any, error) {
	out := make([]map[string]any, 0)
	if len(names) == 0 {
		return out, nil
	}
	groups, err := ShowIpGroupByTagName(api, "")
	if err != nil {
		return nil, err
	}
	idByName := make(map[string]int64, len(groups))
	for _, g := range groups {
		idByName[g.GroupName] = g.ID
	}
	for _, name := range names {
		if id, ok := idByName[name]; ok {
			out = append(out, map[string]any{
				"type":    0,
				"gid":     "IPGP" + strconv.FormatInt(id, 10),
				"gp_name": name,
			})
		}
	}
	return out, nil
}

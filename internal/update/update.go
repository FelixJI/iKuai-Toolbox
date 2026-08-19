// update.go 更新主流程的五个单条目更新函数 + 下载/清洗/分片/导出基础设施，
// 行为对齐 crates/core/src/update.rs（L389-1045）。
// 核心数据安全约束：每个更新函数第一步 httpGet，失败在任何 API 调用之前
// return（Safe-Before）；分片循环 Edit 优先（命中传既有 id；ip/ipv6 分组
// 沿用既有分组名保持名称不变）；循环后冗余分片合并 CSV 一次 Del。
// The five per-entry update functions plus the download/clean/chunk/export
// infrastructure, aligned with crates/core/src/update.rs (L389-1045).
// Core data-safety invariants: every updater starts with httpGet and returns
// before any API call on failure (Safe-Before); the chunk loop prefers Edit
// (existing id on hit; ip/ipv6 groups reuse the existing group name so names
// stay stable); leftover chunks after the loop are deleted in one batched CSV del.
package update

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/FelixJI/iKuai-Toolbox/internal/config"
	"github.com/FelixJI/iKuai-Toolbox/internal/ikuai"
	"github.com/FelixJI/iKuai-Toolbox/internal/netx"
)

// LogLevel 日志级别，值与 JSON 标签对齐 crates/core/src/logger.rs L8-14。
// 本任务先在包内定义最小形状，Task 6 的 logger 复用同一形状。
// LogLevel mirrors crates/core/src/logger.rs L8-14; this task defines the
// minimal shape in-package and Task 6's logger reuses it.
type LogLevel string

const (
	LevelInfo    LogLevel = "Info"
	LevelSuccess LogLevel = "Success"
	LevelWarn    LogLevel = "Warn"
	LevelError   LogLevel = "Error"
)

// LogRecord 五字段日志记录（ts/module/tag/level/detail），对齐 logger.rs L16-23。
// LogRecord is the five-field log record (ts/module/tag/level/detail), mirroring logger.rs L16-23.
type LogRecord struct {
	Ts     string   `json:"ts"`
	Module string   `json:"module"`
	Tag    string   `json:"tag"`
	Level  LogLevel `json:"level"`
	Detail string   `json:"detail"`
}

// LogSink 日志接收函数；必须非 nil（对齐 Rust Arc<dyn Fn(LogRecord)> 的必传约定）。
// LogSink receives every log record; it must be non-nil (matching the mandatory
// Arc<dyn Fn(LogRecord)> convention of the Rust version).
type LogSink func(rec LogRecord)

// UpdateOptions 更新选项，对齐 update.rs L10-19。
// UpdateOptions mirrors update.rs L10-19.
type UpdateOptions struct {
	// ExportPath 域名分流规则列表导出目录（尽力而为，不影响更新流程）。
	// Export directory for stream-domain rule lists (best-effort).
	ExportPath string

	// IpGroupNameAddRandomSuffix IP 分组名称是否增加确定性随机后缀
	// （对应 CLI --isIpGroupNameAddRandomSuff）。
	// Whether ip-group/ipv6-group names carry the deterministic random-like
	// suffix (the CLI --isIpGroupNameAddRandomSuff flag).
	IpGroupNameAddRandomSuffix bool
}

// UpdateError 错误种类，对齐 update.rs L21-31 的枚举。
// UpdateError kinds mirror the enum of update.rs L21-31.
const (
	ErrKindLoginParams   = "login_params"
	ErrKindIkuai         = "ikuai"
	ErrKindDownload      = "download"
	ErrKindInvalidModule = "invalid_module"
)

// UpdateError 统一错误：Kind 取上述常量，Msg 为底层错误文案。
// UpdateError is the unified error: Kind is one of the constants above, Msg the underlying text.
type UpdateError struct {
	Kind string
	Msg  string
}

// Error 对齐 Rust thiserror Display 前缀。
// Error mirrors the Rust thiserror Display prefixes.
func (e *UpdateError) Error() string {
	switch e.Kind {
	case ErrKindLoginParams:
		return "login params error: " + e.Msg
	case ErrKindIkuai:
		return "ikuai error: " + e.Msg
	case ErrKindDownload:
		return "download error: " + e.Msg
	case ErrKindInvalidModule:
		return "invalid -m parameter: " + e.Msg
	default:
		return e.Msg
	}
}

// ikuaiErr 把爱快客户端错误包装为 UpdateError{ikuai}。
// ikuaiErr wraps an iKuai client error into UpdateError{ikuai}.
func ikuaiErr(err error) *UpdateError {
	return &UpdateError{Kind: ErrKindIkuai, Msg: err.Error()}
}

// downloadErr 构造 UpdateError{download}。
// downloadErr builds an UpdateError{download}.
func downloadErr(msg string) *UpdateError {
	return &UpdateError{Kind: ErrKindDownload, Msg: msg}
}

// logger 模块级日志器，对齐 logger.rs L27-66 的 Logger（module + sink）。
// logger is the per-module logger mirroring Logger of logger.rs L27-66 (module + sink).
type logger struct {
	module string
	sink   LogSink
}

// newLogger 绑定模块名与 sink。
// newLogger binds a module name to a sink.
func newLogger(module string, sink LogSink) *logger {
	return &logger{module: module, sink: sink}
}

// emit 填充本地时间戳后投递一条记录，时间格式对齐 logger.rs L58
// （%Y/%m/%d %H:%M:%S）。
// emit stamps the local time and delivers one record; the format mirrors
// logger.rs L58 (%Y/%m/%d %H:%M:%S).
func (l *logger) emit(level LogLevel, tag, detail string) {
	l.sink(LogRecord{
		Ts:     time.Now().Format("2006/01/02 15:04:05"),
		Module: l.module,
		Tag:    tag,
		Level:  level,
		Detail: detail,
	})
}

func (l *logger) info(tag, detail string)    { l.emit(LevelInfo, tag, detail) }
func (l *logger) success(tag, detail string) { l.emit(LevelSuccess, tag, detail) }
func (l *logger) warn(tag, detail string)    { l.emit(LevelWarn, tag, detail) }
func (l *logger) error(tag, detail string)   { l.emit(LevelError, tag, detail) }

// streamDomainUpdate 域名分流单条目入参，对齐 update.rs L33-39。
// streamDomainUpdate is the per-entry stream-domain input, mirroring update.rs L33-39.
type streamDomainUpdate struct {
	iface          string
	tag            string
	srcAddrIpgroup string
	srcAddr        string
	url            string
}

// streamIpPortUpdate 端口分流单条目入参，对齐 update.rs L41-55。
// streamIpPortUpdate is the per-entry stream-ipport input, mirroring update.rs L41-55.
type streamIpPortUpdate struct {
	forwardType       string
	tag               string
	iface             string
	nexthop           string
	srcAddr           string
	srcAddrOptIpgroup string
	srcAddrInv        int64
	ipGroupName       string
	dstAddrInv        int64
	prio              int64
	mode              int64
	ifaceband         int64
	protocol          string
}

// httpGet 下载规则资源（update.rs L389-435）：经 netx.PlanRuleFetch 规划，
// connect 10s / 总 120s，非 2xx 或网络错误 => download 错误。
// httpGet downloads a rule resource (update.rs L389-435): planned through
// netx.PlanRuleFetch with a 10s connect / 120s overall budget; a non-2xx or
// network failure yields a download error.
func httpGet(cfg *config.Config, sink LogSink, originalURL string) ([]byte, *UpdateError) {
	netCfg := netx.NetConfigFromConfig(cfg)
	plan := netx.PlanRuleFetch(netCfg, originalURL)
	httpLogger := newLogger("HTTP:资源下载", sink)
	via := ""
	switch plan.Proxy {
	case netx.ProxyDirect:
		via = "直连"
	case netx.ProxySystem:
		via = "系统代理"
	case netx.ProxyCustom:
		via = "自定义代理"
	}
	gh := ""
	if plan.UsedGithubProxy {
		gh = " (ghproxy)"
	}
	httpLogger.info("HTTP:资源下载", fmt.Sprintf("http.get '%s' via=%s%s", originalURL, via, gh))

	// 避免远程资源不可达时无限期等待（对齐 reqwest connect_timeout 10s / timeout 120s）。
	// Avoid hanging forever on remote resources (mirrors reqwest's 10s connect / 120s total).
	hc := &http.Client{Timeout: 120 * time.Second}
	netx.ApplyProxyChoice(hc, netCfg, plan.Proxy)
	// ApplyProxyChoice 只替换 *http.Transport，补上连接超时；120s 总超时始终兜底。
	// ApplyProxyChoice only swaps in a *http.Transport, so re-attach the dialer;
	// the 120s client timeout remains the safety net.
	if tr, ok := hc.Transport.(*http.Transport); ok {
		tr.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}

	resp, err := hc.Get(plan.URL)
	if err != nil {
		return nil, downloadErr(err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, downloadErr(resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, downloadErr(err.Error())
	}
	return body, nil
}

// splitLines 对齐 Rust str::lines：\n 分割、剥行尾 \r、末尾换行不产生空行、
// 非 UTF-8 字节按 from_utf8_lossy 替换 U+FFFD。
// splitLines mirrors Rust str::lines: split on \n, strip trailing \r per line,
// a trailing newline yields no extra empty element, and invalid UTF-8 bytes
// are replaced with U+FFFD like from_utf8_lossy.
func splitLines(body []byte) []string {
	s := strings.ToValidUTF8(string(body), "\uFFFD")
	lines := strings.Split(s, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// removeIpv6AndEmpty 去 # 注释、去空行，仅保留不含 ':' 的 v4 行（update.rs L442-457）。
// removeIpv6AndEmpty strips # comments and blanks, keeping only lines without
// ':' (update.rs L442-457).
func removeIpv6AndEmpty(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, ip := range lines {
		if idx := strings.IndexByte(ip, '#'); idx >= 0 {
			ip = ip[:idx]
		}
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		if !strings.Contains(ip, ":") {
			out = append(out, ip)
		}
	}
	return out
}

// removeIpv4AndEmpty 去 # 注释、去空行，仅保留含 ':' 的 v6 行（update.rs L459-474）。
// removeIpv4AndEmpty strips # comments and blanks, keeping only lines with ':'
// (update.rs L459-474).
func removeIpv4AndEmpty(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, ip := range lines {
		if idx := strings.IndexByte(ip, '#'); idx >= 0 {
			ip = ip[:idx]
		}
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		if strings.Contains(ip, ":") {
			out = append(out, ip)
		}
	}
	return out
}

// filterDomains 域名行清洗：trim、# 截断再 trim、含 '_' 过滤（update.rs L476-496）。
// filterDomains cleans domain lines: trim, cut at # and re-trim, drop lines
// containing '_' (update.rs L476-496).
func filterDomains(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, d := range lines {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if idx := strings.IndexByte(d, '#'); idx >= 0 {
			d = strings.TrimSpace(d[:idx])
		}
		if d == "" {
			continue
		}
		if strings.Contains(d, "_") {
			continue
		}
		out = append(out, d)
	}
	return out
}

// exportStreamDomains 把域名列表写为 <dir>/stream-domain_<iface>_<tag>.txt
// （update.rs L498-532）：目录名清洗，空 token 回退 iface/tag；空目录返回空路径。
// exportStreamDomains writes the domain list to <dir>/stream-domain_<iface>_<tag>.txt
// (update.rs L498-532): tokens are sanitized with empty fallbacks iface/tag;
// a blank directory returns an empty path.
func exportStreamDomains(exportPath, iface, tag string, domains []string) (string, error) {
	dir := strings.TrimSpace(exportPath)
	if dir == "" {
		return "", nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	ifaceToken := ikuai.SanitizeTagName(iface)
	if ifaceToken == "" {
		ifaceToken = "iface"
	}
	tagToken := ikuai.SanitizeTagName(tag)
	if tagToken == "" {
		tagToken = "tag"
	}

	path := filepath.Join(dir, fmt.Sprintf("stream-domain_%s_%s.txt", ifaceToken, tagToken))
	var sb strings.Builder
	for _, d := range domains {
		sb.WriteString(d)
		sb.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// Group 按 MaxNumberOfOneRecords 分片（update.rs L534-544）：n 下限 1，
// 尾片取余；空输入返回空。
// Group chunks items per MaxNumberOfOneRecords (update.rs L534-544): n has a
// floor of 1, the tail keeps the remainder; empty input yields no chunks.
func Group[T any](items []T, n int) [][]T {
	if n < 1 {
		n = 1
	}
	out := make([][]T, 0, (len(items)+n-1)/n)
	for i := 0; i < len(items); i += n {
		end := i + n
		if end > len(items) {
			end = len(items)
		}
		out = append(out, items[i:end:end])
	}
	return out
}

// sleep 仅在正时长时休眠（update.rs L546-550）。
// sleep only pauses for positive durations (update.rs L546-550).
func sleep(d time.Duration) {
	if d > 0 {
		time.Sleep(d)
	}
}

// sortedIndexes 返回 map 键升序，保证冗余清理的日志与删除顺序确定。
// sortedIndexes returns the map keys ascending so the cleanup logs and the
// delete CSV have a deterministic order.
func sortedIndexes[V any](m map[int64]V) []int64 {
	keys := make([]int64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

// updateCustomIsp 更新单个自定义运营商分片集（update.rs L552-636）。
// Safe-Before：httpGet 失败在任何 API 调用之前 return。
// updateCustomIsp refreshes one custom-ISP chunk set (update.rs L552-636).
// Safe-Before: an httpGet failure returns before any API call.
func updateCustomIsp(cfg *config.Config, api *ikuai.IKuaiClient, sink LogSink, tag, url string) *UpdateError {
	ispLogger := newLogger("ISP:运营商分流", sink)
	body, err := httpGet(cfg, sink, url)
	if err != nil {
		return err
	}
	ips := removeIpv6AndEmpty(splitLines(body))
	ispLogger.info("STAT:规则统计", fmt.Sprintf("Fetched %d IPs for %s", len(ips), tag))

	m, mapErr := ikuai.GetCustomIspMap(api, tag)
	if mapErr != nil {
		return ikuaiErr(mapErr)
	}
	groups := Group(ips, cfg.MaxNumberOfOneRecords.Isp)
	for i, chunk := range groups {
		index := int64(i + 1)
		ipGroup := strings.Join(chunk, ",")
		var res error
		if id, ok := m[index]; ok {
			delete(m, index)
			ispLogger.info("EDIT:正在修改", fmt.Sprintf("[%d/%d] %s: updating chunk %d (ID: %d)...",
				i+1, len(groups), tag, index, id))
			res = ikuai.EditCustomIsp(api, tag, ipGroup, int64(i), id)
		} else {
			ispLogger.info("ADD:正在添加", fmt.Sprintf("[%d/%d] %s: adding chunk %d...",
				i+1, len(groups), tag, index))
			res = ikuai.AddCustomIsp(api, tag, ipGroup, int64(i))
		}
		if res != nil {
			ispLogger.error("UPDATE:更新失败", fmt.Sprintf("[%d/%d] %s: failed: %s",
				i+1, len(groups), tag, res))
			sleep(time.Duration(cfg.AddErrRetryWait))
		} else {
			sleep(time.Duration(cfg.AddWait))
		}
	}

	if len(m) > 0 {
		extra := make([]string, 0, len(m))
		for _, idx := range sortedIndexes(m) {
			id := m[idx]
			ispLogger.info("CLEAN:冗余删除", fmt.Sprintf("%s: chunk %d (ID: %d) is no longer needed, deleting...",
				tag, idx, id))
			extra = append(extra, strconv.FormatInt(id, 10))
		}
		if delErr := ikuai.DelCustomIsp(api, strings.Join(extra, ",")); delErr != nil {
			ispLogger.error("CLEAN:删除失败", fmt.Sprintf("%s: failed to delete extra rules: %s", tag, delErr))
		} else {
			ispLogger.success("CLEAN:清理成功", fmt.Sprintf("%s: deleted %d extra rules", tag, len(extra)))
		}
	}

	return nil
}

// updateStreamDomain 更新单个域名分流分片集（update.rs L638-765）。
// Safe-Before：httpGet 失败在任何 API 调用之前 return。
// updateStreamDomain refreshes one stream-domain chunk set (update.rs L638-765).
// Safe-Before: an httpGet failure returns before any API call.
func updateStreamDomain(cfg *config.Config, api *ikuai.IKuaiClient, opts *UpdateOptions, sink LogSink, input streamDomainUpdate) *UpdateError {
	domainLogger := newLogger("DOMAIN:域名分流", sink)
	body, err := httpGet(cfg, sink, input.url)
	if err != nil {
		return err
	}
	domains := filterDomains(splitLines(body))
	domainLogger.success("PARSE:解析成功", fmt.Sprintf("%s %s: obtained %d valid domains",
		input.iface, input.tag, len(domains)))

	// 规则导出（尽力而为，用于调试/人工检查，不影响更新流程）。
	// Best-effort export for debugging/manual inspection, never blocking the update.
	if strings.TrimSpace(opts.ExportPath) != "" {
		if p, expErr := exportStreamDomains(opts.ExportPath, input.iface, input.tag, domains); expErr != nil {
			domainLogger.warn("EXPORT:导出失败", fmt.Sprintf("exportPath='%s' error=%s", opts.ExportPath, expErr))
		} else if p != "" {
			domainLogger.info("EXPORT:导出成功", fmt.Sprintf("path='%s'", p))
		}
	}

	m, mapErr := ikuai.GetStreamDomainMap(api, input.tag)
	if mapErr != nil {
		return ikuaiErr(mapErr)
	}
	groups := Group(domains, cfg.MaxNumberOfOneRecords.Domain)
	for i, chunk := range groups {
		index := int64(i + 1)
		name := ikuai.BuildIndexedTagName(input.tag, int64(i))
		joined := strings.Join(chunk, ",")
		spec := ikuai.StreamDomainSpec{
			Iface:             input.iface,
			Tag:               input.tag,
			SrcAddr:           input.srcAddr,
			SrcAddrOptIpgroup: input.srcAddrIpgroup,
			Domains:           joined,
			Index:             int64(i),
		}
		var res error
		if id, ok := m[index]; ok {
			delete(m, index)
			domainLogger.info("EDIT:正在修改", fmt.Sprintf("[%d/%d] %s %s: updating %s (ID: %d)...",
				i+1, len(groups), input.iface, input.tag, name, id))
			res = ikuai.EditStreamDomain(api, spec, id)
		} else {
			domainLogger.info("ADD:正在添加", fmt.Sprintf("[%d/%d] %s %s: adding %s...",
				i+1, len(groups), input.iface, input.tag, name))
			res = ikuai.AddStreamDomain(api, spec)
		}
		if res != nil {
			domainLogger.error("UPDATE:更新失败", fmt.Sprintf("[%d/%d] %s %s: failed: %s",
				i+1, len(groups), input.iface, input.tag, res))
			sleep(time.Duration(cfg.AddErrRetryWait))
		} else {
			sleep(time.Duration(cfg.AddWait))
		}
	}

	if len(m) > 0 {
		extra := make([]string, 0, len(m))
		for _, idx := range sortedIndexes(m) {
			id := m[idx]
			domainLogger.info("CLEAN:冗余删除", fmt.Sprintf("%s: chunk %d (ID: %d) is no longer needed, deleting...",
				input.tag, idx, id))
			extra = append(extra, strconv.FormatInt(id, 10))
		}
		if delErr := ikuai.DelStreamDomain(api, strings.Join(extra, ",")); delErr != nil {
			domainLogger.error("CLEAN:删除失败",
				fmt.Sprintf("%s: failed to delete extra domain rules: %s", input.tag, delErr))
		} else {
			domainLogger.success("CLEAN:清理成功",
				fmt.Sprintf("%s: deleted %d extra domain rules", input.tag, len(extra)))
		}
	}

	return nil
}

// updateIpGroup 更新单个 IPv4 分组分片集（update.rs L767-850）。
// Safe-Before：httpGet 失败在任何 API 调用之前 return；Edit 沿用既有分组名。
// updateIpGroup refreshes one IPv4 group chunk set (update.rs L767-850).
// Safe-Before: an httpGet failure returns before any API call; Edit reuses the
// existing group name.
func updateIpGroup(cfg *config.Config, api *ikuai.IKuaiClient, opts *UpdateOptions, sink LogSink, tag, url string) *UpdateError {
	ipLogger := newLogger("IP:IP分组", sink)
	body, err := httpGet(cfg, sink, url)
	if err != nil {
		return err
	}
	ips := removeIpv6AndEmpty(splitLines(body))
	groups := Group(ips, cfg.MaxNumberOfOneRecords.Ipv4)
	ipLogger.success("PARSE:解析成功", fmt.Sprintf("%s: obtained new data", tag))

	m, mapErr := ikuai.GetIpGroupMapWithName(api, tag)
	if mapErr != nil {
		return ikuaiErr(mapErr)
	}
	ipLogger.info("QUERY:查询成功", fmt.Sprintf("%s: found %d existing groups", tag, len(m)))

	for i, chunk := range groups {
		index := int64(i + 1)
		name := ikuai.BuildIndexedTagName(tag, int64(i))
		if opts.IpGroupNameAddRandomSuffix {
			name = ikuai.BuildIndexedIpGroupTagName(tag, int64(i))
		}
		joined := strings.Join(chunk, ",")
		var res error
		if entry, ok := m[index]; ok {
			delete(m, index)
			ipLogger.info("EDIT:正在修改", fmt.Sprintf("[%d/%d] %s: updating %s (ID: %d)...",
				i+1, len(groups), tag, entry.Name, entry.ID))
			res = ikuai.EditIpGroupNamed(api, entry.Name, joined, entry.ID)
		} else {
			ipLogger.info("ADD:正在添加", fmt.Sprintf("[%d/%d] %s: adding %s...",
				i+1, len(groups), tag, name))
			res = ikuai.AddIpGroupNamed(api, name, joined)
		}
		if res != nil {
			ipLogger.error("UPDATE:更新失败", fmt.Sprintf("[%d/%d] %s: failed, error: %s",
				i+1, len(groups), tag, res))
			sleep(time.Duration(cfg.AddErrRetryWait))
		}
	}

	if len(m) > 0 {
		extra := make([]string, 0, len(m))
		for _, idx := range sortedIndexes(m) {
			extra = append(extra, strconv.FormatInt(m[idx].ID, 10))
		}
		ipLogger.info("CLEAN:冗余删除", fmt.Sprintf("%s: %d groups are no longer needed, deleting IDs: %s",
			tag, len(m), strings.Join(extra, ",")))
		if delErr := ikuai.DelIpGroup(api, strings.Join(extra, ",")); delErr != nil {
			ipLogger.error("CLEAN:删除失败", fmt.Sprintf("%s: failed to delete extra groups: %s", tag, delErr))
		} else {
			ipLogger.success("CLEAN:清理成功", fmt.Sprintf("%s: deleted %d extra groups", tag, len(extra)))
		}
	}

	return nil
}

// updateIpv6Group 更新单个 IPv6 分组分片集（update.rs L852-935），与 v4 对称。
// updateIpv6Group refreshes one IPv6 group chunk set (update.rs L852-935), symmetric to v4.
func updateIpv6Group(cfg *config.Config, api *ikuai.IKuaiClient, opts *UpdateOptions, sink LogSink, tag, url string) *UpdateError {
	ipv6Logger := newLogger("IPV6:IPv6分组", sink)
	body, err := httpGet(cfg, sink, url)
	if err != nil {
		return err
	}
	ips := removeIpv4AndEmpty(splitLines(body))
	groups := Group(ips, cfg.MaxNumberOfOneRecords.Ipv6)
	ipv6Logger.success("PARSE:解析成功", fmt.Sprintf("%s: obtained new data", tag))

	m, mapErr := ikuai.GetIpv6GroupMapWithName(api, tag)
	if mapErr != nil {
		return ikuaiErr(mapErr)
	}
	ipv6Logger.info("QUERY:查询成功", fmt.Sprintf("%s: found %d existing IPv6 groups", tag, len(m)))

	for i, chunk := range groups {
		index := int64(i + 1)
		name := ikuai.BuildIndexedTagName(tag, int64(i))
		if opts.IpGroupNameAddRandomSuffix {
			name = ikuai.BuildIndexedIpGroupTagName(tag, int64(i))
		}
		joined := strings.Join(chunk, ",")
		var res error
		if entry, ok := m[index]; ok {
			delete(m, index)
			ipv6Logger.info("EDIT:正在修改", fmt.Sprintf("[%d/%d] %s: updating %s (ID: %d)...",
				i+1, len(groups), tag, entry.Name, entry.ID))
			res = ikuai.EditIpv6GroupNamed(api, entry.Name, joined, entry.ID)
		} else {
			ipv6Logger.info("ADD:正在添加", fmt.Sprintf("[%d/%d] %s: adding %s...",
				i+1, len(groups), tag, name))
			res = ikuai.AddIpv6GroupNamed(api, name, joined)
		}
		if res != nil {
			ipv6Logger.error("UPDATE:更新失败", fmt.Sprintf("[%d/%d] %s: failed, error: %s",
				i+1, len(groups), tag, res))
			sleep(time.Duration(cfg.AddErrRetryWait))
		}
	}

	if len(m) > 0 {
		extra := make([]string, 0, len(m))
		for _, idx := range sortedIndexes(m) {
			extra = append(extra, strconv.FormatInt(m[idx].ID, 10))
		}
		ipv6Logger.info("CLEAN:冗余删除", fmt.Sprintf("%s: %d IPv6 groups are no longer needed, deleting IDs: %s",
			tag, len(m), strings.Join(extra, ",")))
		if delErr := ikuai.DelIpv6Group(api, strings.Join(extra, ",")); delErr != nil {
			ipv6Logger.error("CLEAN:删除失败", fmt.Sprintf("%s: failed to delete extra IPv6 groups: %s", tag, delErr))
		} else {
			ipv6Logger.success("CLEAN:清理成功", fmt.Sprintf("%s: deleted %d extra IPv6 groups", tag, len(extra)))
		}
	}

	return nil
}

// updateStreamIpport 更新单条端口分流规则（update.rs L937-1045）：每 tag 单条，
// map 取第一条命中 Edit 否则 Add；ip-group 引用先 Resolve 展开，为空则跳过
// 不报错。
// updateStreamIpport refreshes one stream-ipport rule (update.rs L937-1045):
// one rule per tag, Edit on the first map hit otherwise Add; ip-group
// references resolve first and an empty expansion skips without an error.
func updateStreamIpport(cfg *config.Config, api *ikuai.IKuaiClient, sink LogSink, input streamIpPortUpdate) *UpdateError {
	streamLogger := newLogger("STREAM:端口分流", sink)

	dstAddr := ""
	if strings.TrimSpace(input.ipGroupName) == "" {
		streamLogger.info("CHECK:参数校验", "ip-group parameter is empty")
	} else {
		var dstGroups []string
		for _, item := range strings.Split(input.ipGroupName, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			matches, resErr := ikuai.ResolveRuleReferenceIpGroupNames(api, item)
			if resErr != nil {
				return ikuaiErr(resErr)
			}
			dstGroups = append(dstGroups, matches...)
		}
		if len(dstGroups) == 0 {
			streamLogger.info("SKIP:跳过操作", fmt.Sprintf(
				"No matching destination IP groups found, skipping port streaming rule addition. ip-group: %s",
				input.ipGroupName))
			return nil
		}
		dstAddr = strings.Join(dstGroups, ",")
	}

	srcAddr := input.srcAddr
	if strings.TrimSpace(input.srcAddrOptIpgroup) != "" {
		var srcGroups []string
		for _, item := range strings.Split(input.srcAddrOptIpgroup, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			matches, resErr := ikuai.ResolveRuleReferenceIpGroupNames(api, item)
			if resErr != nil {
				return ikuaiErr(resErr)
			}
			srcGroups = append(srcGroups, matches...)
		}
		if len(srcGroups) > 0 {
			srcAddr = strings.Join(srcGroups, ",")
		} else {
			streamLogger.info("SKIP:跳过操作", fmt.Sprintf(
				"No matching source IP groups found, skipping port streaming rule addition. srcAddrOptIpGroup: %s",
				input.srcAddrOptIpgroup))
			return nil
		}
	}

	streamMap, mapErr := ikuai.GetStreamIpPortMap(api, input.tag)
	if mapErr != nil {
		return ikuaiErr(mapErr)
	}
	var foundName string
	var foundID int64
	found := false
	// HashMap into_iter().next()：任取一条（单 tag 通常仅一条）。
	// HashMap into_iter().next(): take an arbitrary entry (one rule per tag in practice).
	for name, id := range streamMap {
		foundName, foundID, found = name, id, true
		break
	}
	spec := ikuai.StreamIpPortSpec{
		ForwardType: input.forwardType,
		Iface:       input.iface,
		DstAddr:     dstAddr,
		SrcAddr:     srcAddr,
		SrcAddrInv:  input.srcAddrInv,
		Nexthop:     input.nexthop,
		Tag:         input.tag,
		DstAddrInv:  input.dstAddrInv,
		Prio:        input.prio,
		Mode:        input.mode,
		IfaceBand:   input.ifaceband,
		Protocol:    input.protocol,
	}

	var res error
	if found {
		streamLogger.info("EDIT:正在修改", fmt.Sprintf("[1/1] %s: updating existing rule %s (ID: %d)...",
			input.tag, foundName, foundID))
		res = ikuai.EditStreamIpPort(api, spec, foundID)
	} else {
		streamLogger.info("ADD:正在添加", fmt.Sprintf("[1/1] %s: adding new rule...", input.tag))
		res = ikuai.AddStreamIpPort(api, spec)
	}

	if res != nil {
		streamLogger.error("UPDATE:更新失败", fmt.Sprintf("[1/1] %s: failed: %s", input.tag, res))
		sleep(time.Duration(cfg.AddErrRetryWait))
		return ikuaiErr(res)
	}

	streamLogger.success("UPDATE:更新成功", fmt.Sprintf("[1/1] %s: updated successfully", input.tag))
	return nil
}

// updateIspdomain 执行 ispdomain 模块（update.rs L213-275）：先逐条更新
// custom-isp，再逐条更新 stream-domain，单条失败只记日志不中断。
// updateIspdomain runs the ispdomain module (update.rs L213-275): custom-ISP
// entries first, then stream-domain entries; a single failure logs without
// aborting the pass.
func updateIspdomain(cfg *config.Config, api *ikuai.IKuaiClient, opts *UpdateOptions, sink LogSink) {
	isp := newLogger("ISP:运营商分流", sink)
	domain := newLogger("DOMAIN:域名分流", sink)
	sys := newLogger("SYS:系统组件", sink)

	for _, item := range cfg.CustomIsp {
		isp.info("UPDATE:开始更新", fmt.Sprintf("Updating %s...", item.Tag))
		if err := updateCustomIsp(cfg, api, sink, item.Tag, item.URL); err != nil {
			isp.error("UPDATE:更新失败", fmt.Sprintf("Failed to update custom ISP '%s': %s", item.Tag, err))
		} else {
			isp.success("UPDATE:更新成功", fmt.Sprintf("Successfully updated custom ISP '%s'", item.Tag))
		}
	}

	for _, item := range cfg.StreamDomain {
		domain.info("UPDATE:开始更新", fmt.Sprintf("Updating %s (Interface: %s, Tag: %s)...",
			item.URL, item.Interface, item.Tag))
		input := streamDomainUpdate{
			iface:          item.Interface,
			tag:            item.Tag,
			srcAddrIpgroup: item.SrcAddrOptIpGroup,
			srcAddr:        item.SrcAddr,
			url:            item.URL,
		}
		if err := updateStreamDomain(cfg, api, opts, sink, input); err != nil {
			domain.error("UPDATE:更新失败",
				fmt.Sprintf("Failed to update domain streaming for tag %s: %s", item.Tag, err))
		} else {
			domain.success("UPDATE:更新成功",
				fmt.Sprintf("Successfully updated domain streaming for tag %s", item.Tag))
		}
	}

	sys.success("DONE:任务完成", "ISP and Domain streaming update tasks completed")
}

// updateIpgroup 执行 ipgroup 模块（update.rs L277-358）：先逐条更新 IPv4 分组，
// 再逐条更新端口分流；端口分流规则名 opt-tagname 优先，否则 interface+nexthop。
// updateIpgroup runs the ipgroup module (update.rs L277-358): IPv4 groups first,
// then stream-ipport rules; the rule name prefers opt-tagname, else interface+nexthop.
func updateIpgroup(cfg *config.Config, api *ikuai.IKuaiClient, opts *UpdateOptions, sink LogSink) {
	ipLogger := newLogger("IP:IP分组", sink)
	streamLogger := newLogger("STREAM:端口分流", sink)

	for _, item := range cfg.IpGroup {
		if err := updateIpGroup(cfg, api, opts, sink, item.Tag, item.URL); err != nil {
			ipLogger.error("UPDATE:更新失败",
				fmt.Sprintf("Failed to add IP group '%s@%s': %s", item.Tag, item.URL, err))
		} else {
			ipLogger.success("UPDATE:更新成功",
				fmt.Sprintf("Successfully updated IP group '%s@%s'", item.Tag, item.URL))
		}
	}

	for _, item := range cfg.StreamIpPort {
		tag := item.Interface + item.Nexthop
		if strings.TrimSpace(item.OptTagName) != "" {
			tag = item.OptTagName
		}
		if strings.TrimSpace(tag) == "" {
			streamLogger.error("VALID:参数校验", fmt.Sprintf(
				"Rule name and IpGroup cannot both be empty, skipping: interface='%s' nexthop='%s'",
				item.Interface, item.Nexthop))
			continue
		}

		streamLogger.info("UPDATE:开始更新", fmt.Sprintf("Updating port streaming for tag %s...", tag))
		input := streamIpPortUpdate{
			forwardType:       item.Type,
			tag:               tag,
			iface:             item.Interface,
			nexthop:           item.Nexthop,
			srcAddr:           item.SrcAddr,
			srcAddrOptIpgroup: item.SrcAddrOptIpGroup,
			srcAddrInv:        item.SrcAddrInv,
			ipGroupName:       item.IPGroup,
			dstAddrInv:        item.DstAddrInv,
			prio:              item.Prio,
			mode:              item.Mode,
			ifaceband:         item.IfaceBand,
			protocol:          item.Protocol,
		}
		routeName := item.Interface + item.Nexthop
		if err := updateStreamIpport(cfg, api, sink, input); err != nil {
			streamLogger.error("UPDATE:更新失败",
				fmt.Sprintf("Failed to update port streaming '%s@%s': %s", routeName, item.IPGroup, err))
		} else {
			streamLogger.success("UPDATE:更新成功",
				fmt.Sprintf("Successfully updated port streaming '%s@%s'", routeName, item.IPGroup))
		}
	}
}

// updateIpv6group 执行 ipv6group 模块（update.rs L360-387）：逐条更新 IPv6 分组。
// updateIpv6group runs the ipv6group module (update.rs L360-387): IPv6 group entries one by one.
func updateIpv6group(cfg *config.Config, api *ikuai.IKuaiClient, opts *UpdateOptions, sink LogSink) {
	ipv6Logger := newLogger("IPV6:IPv6分组", sink)
	for _, item := range cfg.Ipv6Group {
		if err := updateIpv6Group(cfg, api, opts, sink, item.Tag, item.URL); err != nil {
			ipv6Logger.error("UPDATE:更新失败",
				fmt.Sprintf("Failed to add IPv6 group '%s@%s': %s", item.Tag, item.URL, err))
		} else {
			ipv6Logger.success("UPDATE:更新成功",
				fmt.Sprintf("Successfully updated IPv6 group '%s@%s'", item.Tag, item.URL))
		}
	}
}

// ExportStreamDomainToTxt 将 stream-domain 规则列表导出为纯文本 TXT
// （update.rs L128-211，便于调试/人工导入）；任一条目失败 => download 错误。
// ExportStreamDomainToTxt exports stream-domain rule lists into plain TXT
// files (update.rs L128-211, for debugging/manual import); any failing entry
// surfaces as a download error.
func ExportStreamDomainToTxt(cfg *config.Config, exportPath string, sink LogSink) *UpdateError {
	exportPath = strings.TrimSpace(exportPath)
	if exportPath == "" {
		return downloadErr("exportPath is empty")
	}

	domain := newLogger("DOMAIN:域名分流", sink)
	domain.info("EXPORT:开始导出", fmt.Sprintf("exportPath='%s' items=%d (stream-domain)",
		exportPath, len(cfg.StreamDomain)))

	if len(cfg.StreamDomain) == 0 {
		domain.warn("EXPORT:无可导出项", "stream-domain is empty")
		return nil
	}

	failed := 0
	for _, item := range cfg.StreamDomain {
		iface := strings.TrimSpace(item.Interface)
		tag := strings.TrimSpace(item.Tag)
		url := strings.TrimSpace(item.URL)
		if url == "" {
			failed++
			domain.error("EXPORT:导出失败", fmt.Sprintf("interface='%s' tag='%s' error=empty_url", iface, tag))
			continue
		}

		domain.info("EXPORT:开始导出", fmt.Sprintf("interface='%s' tag='%s' url='%s'", iface, tag, url))
		body, err := httpGet(cfg, sink, url)
		if err != nil {
			failed++
			domain.error("EXPORT:导出失败", fmt.Sprintf("interface='%s' tag='%s' error=%s", iface, tag, err))
			continue
		}

		domains := filterDomains(splitLines(body))
		p, expErr := exportStreamDomains(exportPath, iface, tag, domains)
		if expErr != nil {
			failed++
			domain.error("EXPORT:导出失败", fmt.Sprintf("interface='%s' tag='%s' error=%s", iface, tag, expErr))
			continue
		}
		domain.success("EXPORT:导出成功", fmt.Sprintf("domains=%d path='%s'", len(domains), p))
	}

	if failed > 0 {
		domain.error("EXPORT:导出完成", fmt.Sprintf("failed=%d", failed))
		return downloadErr(fmt.Sprintf("export finished with %d failures", failed))
	}

	domain.success("EXPORT:导出完成", "OK")
	return nil
}

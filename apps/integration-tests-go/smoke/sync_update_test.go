// sync_update_test.go 同规模更新走原地 edit 的冒烟。
// 覆盖点对齐 apps/integration-tests/tests/rule_sync_update_in_place_smoke.rs。
// sync_update_test.go Smoke for same-scale updates going through in-place
// edits. Coverage mirrors
// apps/integration-tests/tests/rule_sync_update_in_place_smoke.rs.
package smoke

import (
	"fmt"
	"testing"

	"github.com/FelixJI/iKuai-Toolbox/internal/ikuai"
)

// 覆盖点：
// 1) once -m iip 全链路创建规则；
// 2) 第二次同步走原地 edit（ID 稳定）；
// 3) custom-isp / stream-domain / ip-group / ipv6-group / stream-ipport 全量断言。
// Coverage:
// 1) Full iip sync creates rules.
// 2) Second sync updates in place with stable IDs.
// 3) Asserts all major rule types end-to-end.
func TestRuleSyncUpdateInPlaceSmoke(t *testing.T) {
	h := startHarness(t)

	h.fixture.setText(t, "/sync/isp.txt", "1.1.1.0/24\n2.2.2.0/24\n")
	h.fixture.setText(t, "/sync/domain.txt", "example.com\nfoo.example\n")
	h.fixture.setText(t, "/sync/ipv4.txt", "8.8.8.8\n9.9.9.0/24\n")
	h.fixture.setText(t, "/sync/ipv6.txt", "2001:db8::1\n2001:db8::/64\n")

	extra := fmt.Sprintf(`custom-isp:
  - tag: SyncIsp
    url: %q
stream-domain:
  - interface: wan2
    src-addr: 192.168.1.10-192.168.1.20
    src-addr-opt-ipgroup: ""
    url: %q
    tag: SyncDom
ip-group:
  - tag: Sync4
    url: %q
ipv6-group:
  - tag: Sync6
    url: %q
stream-ipport:
  - type: "1"
    opt-tagname: SyncRoute
    interface: ""
    nexthop: 192.168.1.2
    src-addr: 192.168.1.10-192.168.1.20
    src-addr-opt-ipgroup: ""
    src-addr-inv: 1
    ip-group: Sync4
    dst-addr-inv: 1
    mode: 0
    ifaceband: 0
`,
		h.fixture.url("/sync/isp.txt"),
		h.fixture.url("/sync/domain.txt"),
		h.fixture.url("/sync/ipv4.txt"),
		h.fixture.url("/sync/ipv6.txt"),
	)
	cfgPath := h.writeConfig("sync.yml", renderTestConfig(h.sim.URL(), harnessUser, harnessPass, extra))

	h.runCLISuccess("initial sync", "-c", cfgPath, "-r", "once", "-m", "iip")

	api := h.loginAPI()

	customIsp := showCustomIsp(t, api, "SyncIsp")
	if len(customIsp) != 1 {
		t.Fatalf("expected one custom ISP chunk, got %d", len(customIsp))
	}
	if got, want := csvItems(customIsp[0].Ipgroup), []string{"1.1.1.0/24", "2.2.2.0/24"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("custom ISP ipgroup: got %v, want %v", got, want)
	}
	if customIsp[0].Comment != ikuai.NewComment {
		t.Fatalf("custom ISP comment: got %q, want %q", customIsp[0].Comment, ikuai.NewComment)
	}
	customIspID := customIsp[0].ID

	streamDomains := showStreamDomain(t, api, "SyncDom")
	if len(streamDomains) != 1 {
		t.Fatalf("expected one stream-domain chunk, got %d", len(streamDomains))
	}
	if streamDomains[0].Interface != "wan2" {
		t.Fatalf("stream-domain interface: got %q, want wan2", streamDomains[0].Interface)
	}
	if got, want := csvItems(streamDomains[0].Domain), []string{"example.com", "foo.example"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("stream-domain content: got %v, want %v", got, want)
	}
	if streamDomains[0].Comment != ikuai.NewComment {
		t.Fatalf("stream-domain comment: got %q, want %q", streamDomains[0].Comment, ikuai.NewComment)
	}
	streamDomainID := streamDomains[0].ID

	ipv4Groups := showIpGroup(t, api, "Sync4")
	if len(ipv4Groups) != 1 {
		t.Fatalf("expected one IPv4 group chunk, got %d", len(ipv4Groups))
	}
	if got, want := csvItems(ipv4Groups[0].AddrPool), []string{"8.8.8.8", "9.9.9.0/24"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("IPv4 addr_pool: got %v, want %v", got, want)
	}
	ipv4GroupID := ipv4Groups[0].ID

	ipv6Groups := showIpv6Group(t, api, "Sync6")
	if len(ipv6Groups) != 1 {
		t.Fatalf("expected one IPv6 group chunk, got %d", len(ipv6Groups))
	}
	if got, want := csvItems(ipv6Groups[0].AddrPool), []string{"2001:db8::/64", "2001:db8::1"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("IPv6 addr_pool: got %v, want %v", got, want)
	}
	ipv6GroupID := ipv6Groups[0].ID

	streamIpports := showStreamIpPort(t, api, "SyncRoute")
	if len(streamIpports) != 1 {
		t.Fatalf("expected one stream-ipport rule, got %d", len(streamIpports))
	}
	if streamIpports[0].Nexthop != "192.168.1.2" {
		t.Fatalf("stream-ipport nexthop: got %q, want 192.168.1.2", streamIpports[0].Nexthop)
	}
	if streamIpports[0].SrcAddrInv != 1 || streamIpports[0].DstAddrInv != 1 {
		t.Fatalf("stream-ipport inv flags: src=%d dst=%d, want 1/1",
			streamIpports[0].SrcAddrInv, streamIpports[0].DstAddrInv)
	}
	streamIpportID := streamIpports[0].ID

	h.fixture.setText(t, "/sync/isp.txt", "1.1.1.0/24\n3.3.3.0/24\n")
	h.fixture.setText(t, "/sync/domain.txt", "bar.example\nupdated.example\n")
	h.fixture.setText(t, "/sync/ipv4.txt", "4.4.4.4\n5.5.5.0/24\n")
	h.fixture.setText(t, "/sync/ipv6.txt", "2001:db8:1::1\n2001:db8:1::/64\n")

	h.runCLISuccess("second sync", "-c", cfgPath, "-r", "once", "-m", "iip")

	api = h.loginAPI()

	customIsp = showCustomIsp(t, api, "SyncIsp")
	if len(customIsp) != 1 {
		t.Fatalf("re-query custom ISP: expected 1, got %d", len(customIsp))
	}
	if customIsp[0].ID != customIspID {
		t.Fatalf("custom ISP should edit in-place: before=%d after=%d", customIspID, customIsp[0].ID)
	}
	if got, want := csvItems(customIsp[0].Ipgroup), []string{"1.1.1.0/24", "3.3.3.0/24"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("custom ISP ipgroup after edit: got %v, want %v", got, want)
	}

	streamDomains = showStreamDomain(t, api, "SyncDom")
	if len(streamDomains) != 1 {
		t.Fatalf("re-query stream-domain: expected 1, got %d", len(streamDomains))
	}
	if streamDomains[0].ID != streamDomainID {
		t.Fatalf("stream-domain should edit in-place: before=%d after=%d", streamDomainID, streamDomains[0].ID)
	}
	if got, want := csvItems(streamDomains[0].Domain), []string{"bar.example", "updated.example"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("stream-domain content after edit: got %v, want %v", got, want)
	}

	ipv4Groups = showIpGroup(t, api, "Sync4")
	if len(ipv4Groups) != 1 {
		t.Fatalf("re-query IPv4 groups: expected 1, got %d", len(ipv4Groups))
	}
	if ipv4Groups[0].ID != ipv4GroupID {
		t.Fatalf("IPv4 group should edit in-place: before=%d after=%d", ipv4GroupID, ipv4Groups[0].ID)
	}
	if got, want := csvItems(ipv4Groups[0].AddrPool), []string{"4.4.4.4", "5.5.5.0/24"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("IPv4 addr_pool after edit: got %v, want %v", got, want)
	}

	ipv6Groups = showIpv6Group(t, api, "Sync6")
	if len(ipv6Groups) != 1 {
		t.Fatalf("re-query IPv6 groups: expected 1, got %d", len(ipv6Groups))
	}
	if ipv6Groups[0].ID != ipv6GroupID {
		t.Fatalf("IPv6 group should edit in-place: before=%d after=%d", ipv6GroupID, ipv6Groups[0].ID)
	}
	if got, want := csvItems(ipv6Groups[0].AddrPool), []string{"2001:db8:1::/64", "2001:db8:1::1"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("IPv6 addr_pool after edit: got %v, want %v", got, want)
	}

	streamIpports = showStreamIpPort(t, api, "SyncRoute")
	if len(streamIpports) != 1 {
		t.Fatalf("re-query stream-ipport: expected 1, got %d", len(streamIpports))
	}
	if streamIpports[0].ID != streamIpportID {
		t.Fatalf("stream-ipport should edit in-place: before=%d after=%d", streamIpportID, streamIpports[0].ID)
	}
	if streamIpports[0].Nexthop != "192.168.1.2" {
		t.Fatalf("stream-ipport nexthop after edit: got %q", streamIpports[0].Nexthop)
	}
	if streamIpports[0].SrcAddrInv != 1 || streamIpports[0].DstAddrInv != 1 {
		t.Fatalf("stream-ipport inv flags after edit: src=%d dst=%d",
			streamIpports[0].SrcAddrInv, streamIpports[0].DstAddrInv)
	}
}

// 覆盖点：
// 1) 多分片创建（isp/domain/ipv4/ipv6）；
// 2) 第二次同步缩容后，首分片原地更新且冗余分片被删除；
// 3) 同时覆盖注释/空行/非法项过滤后的分片逻辑。
// Coverage:
// 1) Multi-chunk creation across isp/domain/ipv4/ipv6.
// 2) Shrink sync keeps the first chunk in place and deletes redundant chunks.
// 3) Exercises chunking after filtering comments/blank/invalid lines.
func TestChunkedSyncShrinkCleanupSmoke(t *testing.T) {
	h := startHarness(t)

	h.fixture.setText(t, "/chunk/isp.txt",
		"10.0.0.0/24\n10.0.1.0/24 # keep\n\n2001:db8::1\n10.0.2.0/24\n10.0.3.0/24\n# comment\n10.0.4.0/24\n")
	h.fixture.setText(t, "/chunk/domain.txt",
		"alpha.example\nbeta.example\n_skip.example\n\ngamma.example # trailing\ndelta.example\nepsilon.example\n")
	h.fixture.setText(t, "/chunk/ipv4.txt",
		"1.1.1.1\n1.1.1.2\n\n2001:db8::2\n1.1.1.3\n1.1.1.4\n1.1.1.5\n")
	h.fixture.setText(t, "/chunk/ipv6.txt",
		"2001:db8::1\n2001:db8::2\n1.1.1.1\n2001:db8::3\n\n2001:db8::4\n2001:db8::5\n")

	extra := fmt.Sprintf(`custom-isp:
  - tag: ChunkIsp
    url: %q
stream-domain:
  - interface: wan2
    src-addr: 192.168.50.10-192.168.50.20
    src-addr-opt-ipgroup: ""
    url: %q
    tag: ChunkDom
ip-group:
  - tag: Chunk4
    url: %q
ipv6-group:
  - tag: Chunk6
    url: %q
MaxNumberOfOneRecords:
  Isp: 2
  Ipv4: 2
  Ipv6: 2
  Domain: 2
`,
		h.fixture.url("/chunk/isp.txt"),
		h.fixture.url("/chunk/domain.txt"),
		h.fixture.url("/chunk/ipv4.txt"),
		h.fixture.url("/chunk/ipv6.txt"),
	)
	cfgPath := h.writeConfig("chunked-sync.yml", renderTestConfig(h.sim.URL(), harnessUser, harnessPass, extra))

	h.runCLISuccess("multi-chunk sync", "-c", cfgPath, "-r", "once", "-m", "iip")

	api := h.loginAPI()

	customIsp := showCustomIsp(t, api, "ChunkIsp")
	if len(customIsp) != 3 {
		t.Fatalf("expected three custom ISP chunks, got %d", len(customIsp))
	}
	if got, want := csvItems(customIsp[0].Ipgroup), []string{"10.0.0.0/24", "10.0.1.0/24"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("first custom ISP chunk: got %v, want %v", got, want)
	}
	customIspFirstID := customIsp[0].ID

	streamDomains := showStreamDomain(t, api, "ChunkDom")
	if len(streamDomains) != 3 {
		t.Fatalf("expected three stream-domain chunks, got %d", len(streamDomains))
	}
	if got, want := csvItems(streamDomains[0].Domain), []string{"alpha.example", "beta.example"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("first stream-domain chunk: got %v, want %v", got, want)
	}
	streamDomainFirstID := streamDomains[0].ID

	ipv4Groups := showIpGroup(t, api, "Chunk4")
	if len(ipv4Groups) != 3 {
		t.Fatalf("expected three IPv4 group chunks, got %d", len(ipv4Groups))
	}
	if got, want := csvItems(ipv4Groups[0].AddrPool), []string{"1.1.1.1", "1.1.1.2"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("first IPv4 chunk: got %v, want %v", got, want)
	}
	ipv4FirstID := ipv4Groups[0].ID

	ipv6Groups := showIpv6Group(t, api, "Chunk6")
	if len(ipv6Groups) != 3 {
		t.Fatalf("expected three IPv6 group chunks, got %d", len(ipv6Groups))
	}
	if got, want := csvItems(ipv6Groups[0].AddrPool), []string{"2001:db8::1", "2001:db8::2"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("first IPv6 chunk: got %v, want %v", got, want)
	}
	ipv6FirstID := ipv6Groups[0].ID

	h.fixture.setText(t, "/chunk/isp.txt", "10.0.9.0/24\n10.0.9.1/24\n2001:db8::dead\n")
	h.fixture.setText(t, "/chunk/domain.txt", "renew.example\nsteady.example\n_still_skip.example\n")
	h.fixture.setText(t, "/chunk/ipv4.txt", "9.9.9.1\n9.9.9.2\n2001:db8::beef\n")
	h.fixture.setText(t, "/chunk/ipv6.txt", "2001:db8:9::1\n2001:db8:9::2\n9.9.9.9\n")

	h.runCLISuccess("shrink sync", "-c", cfgPath, "-r", "once", "-m", "iip")

	api = h.loginAPI()

	customIsp = showCustomIsp(t, api, "ChunkIsp")
	if len(customIsp) != 1 {
		t.Fatalf("extra custom ISP chunks should be deleted, got %d", len(customIsp))
	}
	if customIsp[0].ID != customIspFirstID {
		t.Fatalf("first custom ISP chunk should update in place: before=%d after=%d", customIspFirstID, customIsp[0].ID)
	}
	if got, want := csvItems(customIsp[0].Ipgroup), []string{"10.0.9.0/24", "10.0.9.1/24"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("custom ISP content after shrink: got %v, want %v", got, want)
	}

	streamDomains = showStreamDomain(t, api, "ChunkDom")
	if len(streamDomains) != 1 {
		t.Fatalf("extra stream-domain chunks should be deleted, got %d", len(streamDomains))
	}
	if streamDomains[0].ID != streamDomainFirstID {
		t.Fatalf("first stream-domain chunk should update in place: before=%d after=%d", streamDomainFirstID, streamDomains[0].ID)
	}
	if got, want := csvItems(streamDomains[0].Domain), []string{"renew.example", "steady.example"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("stream-domain content after shrink: got %v, want %v", got, want)
	}

	ipv4Groups = showIpGroup(t, api, "Chunk4")
	if len(ipv4Groups) != 1 {
		t.Fatalf("extra IPv4 chunks should be deleted, got %d", len(ipv4Groups))
	}
	if ipv4Groups[0].ID != ipv4FirstID {
		t.Fatalf("first IPv4 chunk should update in place: before=%d after=%d", ipv4FirstID, ipv4Groups[0].ID)
	}
	if got, want := csvItems(ipv4Groups[0].AddrPool), []string{"9.9.9.1", "9.9.9.2"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("IPv4 content after shrink: got %v, want %v", got, want)
	}

	ipv6Groups = showIpv6Group(t, api, "Chunk6")
	if len(ipv6Groups) != 1 {
		t.Fatalf("extra IPv6 chunks should be deleted, got %d", len(ipv6Groups))
	}
	if ipv6Groups[0].ID != ipv6FirstID {
		t.Fatalf("first IPv6 chunk should update in place: before=%d after=%d", ipv6FirstID, ipv6Groups[0].ID)
	}
	if got, want := csvItems(ipv6Groups[0].AddrPool), []string{"2001:db8:9::1", "2001:db8:9::2"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("IPv6 content after shrink: got %v, want %v", got, want)
	}
}

// 覆盖点（迁移台账 Task 5 遗留补测）：
// 1) 模拟器注入 add 失败（code 1），第 1 片写入失败；
// 2) 分片循环不中断：第 2 片仍执行并落库；
// 3) 审计 JSONL 同时记录失败与成功两次 add，供精确断言。
// Coverage (the Task 5 ledger leftover):
// 1) The simulator injects an add failure (code 1) so chunk 1 cannot persist.
// 2) The chunk loop continues: chunk 2 still executes and lands.
// 3) The audit JSONL records both the failed and the successful add.
func TestChunkFailureContinuesSmoke(t *testing.T) {
	h := startHarness(t)

	h.fixture.setText(t, "/chunkfail/isp.txt", "1.2.3.4\n1.2.3.5\n1.2.3.6\n1.2.3.7\n")
	extra := fmt.Sprintf(`custom-isp:
  - tag: FailIsp
    url: %q
MaxNumberOfOneRecords:
  Isp: 2
`, h.fixture.url("/chunkfail/isp.txt"))
	cfgPath := h.writeConfig("chunk-failure.yml", renderTestConfig(h.sim.URL(), harnessUser, harnessPass, extra))

	// 第 1 次 add 返回 code 1 且不落库；第 2 片恢复正常。
	// The first add answers code 1 without persisting; chunk 2 recovers.
	h.sim.InjectFailure("custom_isp", "add", 1)

	res := h.runCLISuccess("chunk-failure sync", "-c", cfgPath, "-r", "once", "-m", "ispdomain")

	adds := h.sim.Calls("custom_isp", "add")
	if len(adds) != 2 {
		t.Fatalf("expected 2 custom_isp add audit entries (chunk 1 failed + chunk 2), got %d:\n%s",
			len(adds), h.sim.AuditJSONL())
	}
	if adds[0].Code != 1 {
		t.Fatalf("chunk 1 add should have failed with code 1, got %d (%s)", adds[0].Code, adds[0].Message)
	}
	if adds[1].Code != 0 {
		t.Fatalf("chunk 2 add should still run and succeed, got code %d (%s)", adds[1].Code, adds[1].Message)
	}
	firstIpgroup, _ := adds[0].Param["ipgroup"].(string)
	secondIpgroup, _ := adds[1].Param["ipgroup"].(string)
	if firstIpgroup != "1.2.3.4,1.2.3.5" || secondIpgroup != "1.2.3.6,1.2.3.7" {
		t.Fatalf("add audit ipgroups out of order: first=%q second=%q", firstIpgroup, secondIpgroup)
	}
	if !res.stdoutContains("UPDATE:更新失败") || !res.stdoutContains("injected failure") {
		t.Fatalf("stdout should log the chunk failure and injected cause, got:\n%s", res.stdout)
	}

	// 状态验证：仅第 2 片落库。
	// State check: only chunk 2 persisted.
	rows := showCustomIsp(t, h.loginAPI(), "FailIsp")
	if len(rows) != 1 {
		t.Fatalf("expected exactly the second chunk to persist, got %d rows", len(rows))
	}
	if got, want := csvItems(rows[0].Ipgroup), []string{"1.2.3.6", "1.2.3.7"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("persisted chunk content: got %v, want %v", got, want)
	}
}

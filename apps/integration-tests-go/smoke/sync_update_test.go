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

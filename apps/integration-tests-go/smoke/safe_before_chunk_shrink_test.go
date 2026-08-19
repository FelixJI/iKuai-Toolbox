// safe_before_chunk_shrink_test.go 多分片 Safe-Before 冒烟：上游本应缩容但
// 下载失败时，旧分片数量、ID 与内容都不被清理或误改。
// 覆盖点对齐 apps/integration-tests/tests/safe_before_chunk_shrink_smoke.rs。
// safe_before_chunk_shrink_test.go Multi-chunk Safe-Before smoke: when a shrink
// would reduce chunks but the upstream fetch fails, the old chunk counts, IDs,
// and contents all survive. Coverage mirrors
// apps/integration-tests/tests/safe_before_chunk_shrink_smoke.rs.
package smoke

import (
	"fmt"
	"testing"

	"github.com/FelixJI/iKuai-Toolbox/internal/ikuai"
)

// 覆盖点：
// 1) 多分片规则同步成功后形成稳定的旧状态；
// 2) 第二次本应缩容到更少分片，但上游下载失败；
// 3) 验证 Safe-Before：旧分片数量、ID 与内容都不被清理或误改。
// Coverage:
// 1) Multi-chunk rules establish a stable baseline.
// 2) A later shrink would reduce chunks, but upstream fetch fails.
// 3) Safe-Before preserves old chunk counts, IDs, and contents.
func TestSafeBeforeChunkShrinkSmoke(t *testing.T) {
	h := startHarness(t)

	h.fixture.setText(t, "/safe-chunk/isp.txt",
		"10.20.0.1\n10.20.0.2\n10.20.0.3\n10.20.0.4\n10.20.0.5\n")
	h.fixture.setText(t, "/safe-chunk/domain.txt",
		"safe-a.example\nsafe-b.example\nsafe-c.example\nsafe-d.example\nsafe-e.example\n")
	h.fixture.setText(t, "/safe-chunk/ipv4.txt",
		"100.80.0.1\n100.80.0.2\n100.80.0.3\n100.80.0.4\n100.80.0.5\n")
	h.fixture.setText(t, "/safe-chunk/ipv6.txt",
		"2001:db8:20::1\n2001:db8:20::2\n2001:db8:20::3\n2001:db8:20::4\n2001:db8:20::5\n")

	extra := fmt.Sprintf(`custom-isp:
  - tag: SafeChunkIsp
    url: %q
stream-domain:
  - interface: wan2
    src-addr: 192.168.88.10-192.168.88.20
    src-addr-opt-ipgroup: ""
    url: %q
    tag: SafeChunkDom
ip-group:
  - tag: SafeChunk4
    url: %q
ipv6-group:
  - tag: SafeChunk6
    url: %q
stream-ipport:
  - type: "1"
    opt-tagname: SafeChunkRoute
    interface: ""
    nexthop: 192.168.1.2
    src-addr: 192.168.88.10-192.168.88.20
    src-addr-opt-ipgroup: ""
    ip-group: SafeChunk4
    mode: 0
    ifaceband: 0
MaxNumberOfOneRecords:
  Isp: 2
  Ipv4: 2
  Ipv6: 2
  Domain: 2
`,
		h.fixture.url("/safe-chunk/isp.txt"),
		h.fixture.url("/safe-chunk/domain.txt"),
		h.fixture.url("/safe-chunk/ipv4.txt"),
		h.fixture.url("/safe-chunk/ipv6.txt"),
	)
	cfgPath := h.writeConfig("safe-before-chunk.yml", renderTestConfig(h.sim.URL(), harnessUser, harnessPass, extra))

	h.runCLISuccess("multi-chunk sync", "-c", cfgPath, "-r", "once", "-m", "iip")

	api := h.loginAPI()
	beforeCustom := showCustomIsp(t, api, "SafeChunkIsp")
	beforeDomain := showStreamDomain(t, api, "SafeChunkDom")
	beforeIpv4 := showIpGroup(t, api, "SafeChunk4")
	beforeIpv6 := showIpv6Group(t, api, "SafeChunk6")
	beforeRoute := showStreamIpPort(t, api, "SafeChunkRoute")

	if len(beforeCustom) != 3 || len(beforeDomain) != 3 || len(beforeIpv4) != 3 || len(beforeIpv6) != 3 {
		t.Fatalf("baseline chunk counts: isp=%d domain=%d ipv4=%d ipv6=%d (all want 3)",
			len(beforeCustom), len(beforeDomain), len(beforeIpv4), len(beforeIpv6))
	}
	if len(beforeRoute) != 1 {
		t.Fatalf("baseline stream-ipport count: got %d, want 1", len(beforeRoute))
	}

	h.fixture.setStatus(t, "/safe-chunk/isp.txt", 503, "shrink would reduce chunks to one\n")
	h.fixture.setStatus(t, "/safe-chunk/domain.txt", 503, "shrink would reduce chunks to one\n")
	h.fixture.setStatus(t, "/safe-chunk/ipv4.txt", 503, "shrink would reduce chunks to one\n")
	h.fixture.setStatus(t, "/safe-chunk/ipv6.txt", 503, "shrink would reduce chunks to one\n")

	h.runCLISuccess("503 retry sync", "-c", cfgPath, "-r", "once", "-m", "iip")

	api = h.loginAPI()
	afterCustom := showCustomIsp(t, api, "SafeChunkIsp")
	afterDomain := showStreamDomain(t, api, "SafeChunkDom")
	afterIpv4 := showIpGroup(t, api, "SafeChunk4")
	afterIpv6 := showIpv6Group(t, api, "SafeChunk6")
	afterRoute := showStreamIpPort(t, api, "SafeChunkRoute")

	if got, want := idsOf(afterCustom, func(r ikuai.CustomIspData) int64 { return r.ID }),
		idsOf(beforeCustom, func(r ikuai.CustomIspData) int64 { return r.ID }); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("Safe-Before should keep all custom ISP chunk IDs: before=%v after=%v", want, got)
	}
	if got, want := idsOf(afterDomain, func(r ikuai.StreamDomainData) int64 { return r.ID }),
		idsOf(beforeDomain, func(r ikuai.StreamDomainData) int64 { return r.ID }); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("Safe-Before should keep all stream-domain chunk IDs: before=%v after=%v", want, got)
	}
	if got, want := idsOf(afterIpv4, func(r ikuai.IpGroupData) int64 { return r.ID }),
		idsOf(beforeIpv4, func(r ikuai.IpGroupData) int64 { return r.ID }); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("Safe-Before should keep all IPv4 chunk IDs: before=%v after=%v", want, got)
	}
	if got, want := idsOf(afterIpv6, func(r ikuai.Ipv6GroupData) int64 { return r.ID }),
		idsOf(beforeIpv6, func(r ikuai.Ipv6GroupData) int64 { return r.ID }); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("Safe-Before should keep all IPv6 chunk IDs: before=%v after=%v", want, got)
	}

	if got, want := mapRows(afterCustom, func(r ikuai.CustomIspData) []string { return csvItems(r.Ipgroup) }),
		mapRows(beforeCustom, func(r ikuai.CustomIspData) []string { return csvItems(r.Ipgroup) }); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("Safe-Before should keep custom ISP chunk contents: before=%v after=%v", want, got)
	}
	if got, want := mapRows(afterDomain, func(r ikuai.StreamDomainData) []string { return csvItems(r.Domain) }),
		mapRows(beforeDomain, func(r ikuai.StreamDomainData) []string { return csvItems(r.Domain) }); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("Safe-Before should keep stream-domain chunk contents: before=%v after=%v", want, got)
	}
	if got, want := mapRows(afterIpv4, func(r ikuai.IpGroupData) []string { return csvItems(r.AddrPool) }),
		mapRows(beforeIpv4, func(r ikuai.IpGroupData) []string { return csvItems(r.AddrPool) }); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("Safe-Before should keep IPv4 chunk contents: before=%v after=%v", want, got)
	}
	if got, want := mapRows(afterIpv6, func(r ikuai.Ipv6GroupData) []string { return csvItems(r.AddrPool) }),
		mapRows(beforeIpv6, func(r ikuai.Ipv6GroupData) []string { return csvItems(r.AddrPool) }); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("Safe-Before should keep IPv6 chunk contents: before=%v after=%v", want, got)
	}

	if len(afterRoute) != 1 {
		t.Fatalf("post-503 stream-ipport count: got %d, want 1", len(afterRoute))
	}
	if afterRoute[0].ID != beforeRoute[0].ID {
		t.Fatalf("stream-ipport id changed: before=%d after=%d", beforeRoute[0].ID, afterRoute[0].ID)
	}
	if got, want := csvItems(afterRoute[0].DstAddr), csvItems(beforeRoute[0].DstAddr); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("stream-ipport dst_addr changed: before=%v after=%v", want, got)
	}
}

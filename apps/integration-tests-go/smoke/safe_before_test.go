// safe_before_test.go Safe-Before 冒烟：源 503 后再次同步，旧规则零变动。
// 覆盖点对齐 apps/integration-tests/tests/safe_before_smoke.rs。
// safe_before_test.go Safe-Before smoke: re-sync while the source answers 503
// and the existing rules must stay untouched. Coverage mirrors
// apps/integration-tests/tests/safe_before_smoke.rs.
package smoke

import (
	"fmt"
	"testing"
)

// 覆盖点：
// 1) 首次同步成功写入规则；
// 2) 远程源返回 503 后再次同步；
// 3) 验证 Safe-Before：旧规则不被清理。
// Coverage:
// 1) Initial successful sync.
// 2) Retry sync with upstream 503.
// 3) Verifies Safe-Before keeps existing rules.
func TestSafeBeforeSmoke(t *testing.T) {
	h := startHarness(t)

	h.fixture.setText(t, "/safe/isp.txt", "10.0.0.0/24\n10.0.1.0/24\n")
	h.fixture.setText(t, "/safe/domain.txt", "safe.example\nkeep.example\n")
	h.fixture.setText(t, "/safe/ipv4.txt", "100.64.0.1\n100.64.0.2\n")
	h.fixture.setText(t, "/safe/ipv6.txt", "2001:db8:2::1\n2001:db8:2::/64\n")

	extra := fmt.Sprintf(`custom-isp:
  - tag: SafeIsp
    url: %q
stream-domain:
  - interface: wan2
    src-addr: 192.168.9.10-192.168.9.20
    src-addr-opt-ipgroup: ""
    url: %q
    tag: SafeDom
ip-group:
  - tag: Safe4
    url: %q
ipv6-group:
  - tag: Safe6
    url: %q
stream-ipport:
  - type: "1"
    opt-tagname: SafeRoute
    interface: ""
    nexthop: 192.168.1.2
    src-addr: 192.168.9.10-192.168.9.20
    src-addr-opt-ipgroup: ""
    ip-group: Safe4
    mode: 0
    ifaceband: 0
`,
		h.fixture.url("/safe/isp.txt"),
		h.fixture.url("/safe/domain.txt"),
		h.fixture.url("/safe/ipv4.txt"),
		h.fixture.url("/safe/ipv6.txt"),
	)
	cfgPath := h.writeConfig("safe-before.yml", renderTestConfig(h.sim.URL(), harnessUser, harnessPass, extra))

	h.runCLISuccess("initial sync", "-c", cfgPath, "-r", "once", "-m", "iip")

	api := h.loginAPI()
	beforeCustom := showCustomIsp(t, api, "SafeIsp")
	beforeDomain := showStreamDomain(t, api, "SafeDom")
	beforeIpv4 := showIpGroup(t, api, "Safe4")
	beforeIpv6 := showIpv6Group(t, api, "Safe6")
	beforeStream := showStreamIpPort(t, api, "SafeRoute")

	if len(beforeCustom) != 1 || len(beforeDomain) != 1 || len(beforeIpv4) != 1 || len(beforeIpv6) != 1 || len(beforeStream) != 1 {
		t.Fatalf("initial sync sizes: isp=%d domain=%d ipv4=%d ipv6=%d stream=%d (all want 1)",
			len(beforeCustom), len(beforeDomain), len(beforeIpv4), len(beforeIpv6), len(beforeStream))
	}

	h.fixture.setStatus(t, "/safe/isp.txt", 503, "custom isp download failed\n")
	h.fixture.setStatus(t, "/safe/domain.txt", 503, "domain download failed\n")
	h.fixture.setStatus(t, "/safe/ipv4.txt", 503, "ipv4 download failed\n")
	h.fixture.setStatus(t, "/safe/ipv6.txt", 503, "ipv6 download failed\n")

	h.runCLISuccess("503 retry sync", "-c", cfgPath, "-r", "once", "-m", "iip")

	api = h.loginAPI()
	afterCustom := showCustomIsp(t, api, "SafeIsp")
	afterDomain := showStreamDomain(t, api, "SafeDom")
	afterIpv4 := showIpGroup(t, api, "Safe4")
	afterIpv6 := showIpv6Group(t, api, "Safe6")
	afterStream := showStreamIpPort(t, api, "SafeRoute")

	if len(afterCustom) != 1 || len(afterDomain) != 1 || len(afterIpv4) != 1 || len(afterIpv6) != 1 || len(afterStream) != 1 {
		t.Fatalf("post-503 sizes: isp=%d domain=%d ipv4=%d ipv6=%d stream=%d (all want 1)",
			len(afterCustom), len(afterDomain), len(afterIpv4), len(afterIpv6), len(afterStream))
	}

	if afterCustom[0].ID != beforeCustom[0].ID {
		t.Fatalf("custom ISP id changed: before=%d after=%d", beforeCustom[0].ID, afterCustom[0].ID)
	}
	if got, want := csvItems(afterCustom[0].Ipgroup), csvItems(beforeCustom[0].Ipgroup); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("custom ISP ipgroup changed: before=%v after=%v", want, got)
	}
	if afterDomain[0].ID != beforeDomain[0].ID {
		t.Fatalf("stream-domain id changed: before=%d after=%d", beforeDomain[0].ID, afterDomain[0].ID)
	}
	if got, want := csvItems(afterDomain[0].Domain), csvItems(beforeDomain[0].Domain); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("stream-domain content changed: before=%v after=%v", want, got)
	}
	if afterIpv4[0].ID != beforeIpv4[0].ID {
		t.Fatalf("ip-group id changed: before=%d after=%d", beforeIpv4[0].ID, afterIpv4[0].ID)
	}
	if got, want := csvItems(afterIpv4[0].AddrPool), csvItems(beforeIpv4[0].AddrPool); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("ip-group addr_pool changed: before=%v after=%v", want, got)
	}
	if afterIpv6[0].ID != beforeIpv6[0].ID {
		t.Fatalf("ipv6-group id changed: before=%d after=%d", beforeIpv6[0].ID, afterIpv6[0].ID)
	}
	if got, want := csvItems(afterIpv6[0].AddrPool), csvItems(beforeIpv6[0].AddrPool); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("ipv6-group addr_pool changed: before=%v after=%v", want, got)
	}
	if afterStream[0].ID != beforeStream[0].ID {
		t.Fatalf("stream-ipport id changed: before=%d after=%d", beforeStream[0].ID, afterStream[0].ID)
	}
	if afterStream[0].Nexthop != beforeStream[0].Nexthop {
		t.Fatalf("stream-ipport nexthop changed: before=%s after=%s", beforeStream[0].Nexthop, afterStream[0].Nexthop)
	}
}

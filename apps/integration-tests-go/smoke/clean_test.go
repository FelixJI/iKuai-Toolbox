// clean_test.go 清理模式冒烟：cleanAll 全量清理与按 tag 定向清理。
// 覆盖点对齐 rust_archive/apps-integration-tests/tests/clean_all_smoke.rs 与
// clean_mode_smoke.rs。
// clean_test.go Clean-mode smokes: the cleanAll wipe and the targeted per-tag
// clean. Coverage mirrors clean_all_smoke.rs and clean_mode_smoke.rs of the
// Rust suite.
package smoke

import (
	"fmt"
	"testing"

	"github.com/FelixJI/iKuai-Toolbox/internal/ikuai"
)

// seedCleanFixtures 布置双 tag 六个列表源（对齐两个 Rust clean smoke 的
// fixture 清单）。
// seedCleanFixtures lays out the six dual-tag list sources (the fixture set of
// the two Rust clean smokes).
func seedCleanFixtures(h *harness, prefix string) {
	h.fixture.setText(h.t, "/"+prefix+"/a-isp.txt", "10.1.1.0/24\n")
	h.fixture.setText(h.t, "/"+prefix+"/b-isp.txt", "10.2.2.0/24\n")
	h.fixture.setText(h.t, "/"+prefix+"/a-domain.txt", "a.example\n")
	h.fixture.setText(h.t, "/"+prefix+"/b-domain.txt", "b.example\n")
	h.fixture.setText(h.t, "/"+prefix+"/a-ipv4.txt", "172.16.10.1\n")
	h.fixture.setText(h.t, "/"+prefix+"/b-ipv4.txt", "172.16.20.1\n")
}

// renderDualTagConfig 双 tag 清理用配置模板：两组 isp/domain/ip-group +
// 两条 stream-ipport（路径前缀独立传参，cleanall 与 clean 各自独立）。
// renderDualTagConfig is the dual-tag clean config template: two ISP/domain/
// ip-group sets plus two stream-ipport rules (the path prefix is a parameter so
// cleanall and clean keep independent sources).
func renderDualTagConfig(h *harness, pathPrefix, tagA, tagB string) string {
	aIsp := h.fixture.url("/" + pathPrefix + "/a-isp.txt")
	bIsp := h.fixture.url("/" + pathPrefix + "/b-isp.txt")
	aDomain := h.fixture.url("/" + pathPrefix + "/a-domain.txt")
	bDomain := h.fixture.url("/" + pathPrefix + "/b-domain.txt")
	aIpv4 := h.fixture.url("/" + pathPrefix + "/a-ipv4.txt")
	bIpv4 := h.fixture.url("/" + pathPrefix + "/b-ipv4.txt")
	return fmt.Sprintf(`custom-isp:
  - tag: %s
    url: %q
  - tag: %s
    url: %q
stream-domain:
  - interface: wan2
    src-addr: 192.168.10.10-192.168.10.20
    src-addr-opt-ipgroup: ""
    url: %q
    tag: %s
  - interface: wan2
    src-addr: 192.168.20.10-192.168.20.20
    src-addr-opt-ipgroup: ""
    url: %q
    tag: %s
ip-group:
  - tag: %s
    url: %q
  - tag: %s
    url: %q
stream-ipport:
  - type: "1"
    opt-tagname: %sFlow
    interface: ""
    nexthop: 192.168.1.2
    src-addr: 192.168.10.10-192.168.10.20
    src-addr-opt-ipgroup: ""
    ip-group: %s
    mode: 0
    ifaceband: 0
  - type: "1"
    opt-tagname: %sFlow
    interface: ""
    nexthop: 192.168.1.3
    src-addr: 192.168.20.10-192.168.20.20
    src-addr-opt-ipgroup: ""
    ip-group: %s
    mode: 0
    ifaceband: 0
`,
		tagA, aIsp,
		tagB, bIsp,
		aDomain, tagA,
		bDomain, tagB,
		tagA, aIpv4,
		tagB, bIpv4,
		tagA, tagA,
		tagB, tagB,
	)
}

// 覆盖点：
// 1) cleanAll 全量清理路径；
// 2) 多类规则（isp/domain/ip-group/stream-ipport）清空验证。
// Coverage:
// 1) cleanAll flow.
// 2) Verifies all managed rule categories are removed.
func TestCleanAllSmoke(t *testing.T) {
	h := startHarness(t)
	seedCleanFixtures(h, "cleanall")
	extra := renderDualTagConfig(h, "cleanall", "AllA", "AllB")
	cfgPath := h.writeConfig("clean-all.yml", renderTestConfig(h.sim.URL(), harnessUser, harnessPass, extra))

	h.runCLISuccess("seed sync", "-c", cfgPath, "-r", "once", "-m", "ii")
	h.runCLISuccess("cleanAll", "-c", cfgPath, "-r", "clean", "-tag", ikuai.CleanModeAll)

	api := h.loginAPI()
	for _, tag := range []string{"AllA", "AllB"} {
		if rows := showCustomIsp(t, api, tag); len(rows) != 0 {
			t.Fatalf("custom isp %s should be cleaned after cleanAll, got %d rows", tag, len(rows))
		}
		if rows := showStreamDomain(t, api, tag); len(rows) != 0 {
			t.Fatalf("stream-domain %s should be cleaned after cleanAll, got %d rows", tag, len(rows))
		}
		if rows := showIpGroup(t, api, tag); len(rows) != 0 {
			t.Fatalf("ip-group %s should be cleaned after cleanAll, got %d rows", tag, len(rows))
		}
	}
	for _, tag := range []string{"AllAFlow", "AllBFlow"} {
		if rows := showStreamIpPort(t, api, tag); len(rows) != 0 {
			t.Fatalf("stream-ipport %s should be cleaned after cleanAll, got %d rows", tag, len(rows))
		}
	}
}

// 覆盖点：
// 1) clean 缺少 -tag 必须失败；
// 2) 指定 tag 清理仅影响目标规则；
// 3) 非目标 tag 规则必须保留。
// Coverage:
// 1) clean without -tag fails.
// 2) Targeted clean removes only the selected tag's rules.
// 3) Non-target rules remain unchanged.
func TestCleanModeSmoke(t *testing.T) {
	h := startHarness(t)
	seedCleanFixtures(h, "clean")
	cfgPath := h.writeConfig("clean.yml",
		renderTestConfig(h.sim.URL(), harnessUser, harnessPass, renderDualTagConfig(h, "clean", "ClnA", "ClnB")))

	failure := h.runCLIFailure("clean without tag", "-c", cfgPath, "-r", "clean")
	if !failure.stderrContains("Clean mode requires -tag") {
		t.Fatalf("expected stderr to contain 'Clean mode requires -tag', got:\n%s", failure.stderr)
	}

	h.runCLISuccess("seed sync", "-c", cfgPath, "-r", "once", "-m", "ii")

	api := h.loginAPI()
	for _, tag := range []string{"ClnA", "ClnB"} {
		if rows := showCustomIsp(t, api, tag); len(rows) != 1 {
			t.Fatalf("seeded custom isp %s: got %d rows, want 1", tag, len(rows))
		}
		if rows := showStreamDomain(t, api, tag); len(rows) != 1 {
			t.Fatalf("seeded stream-domain %s: got %d rows, want 1", tag, len(rows))
		}
		if rows := showIpGroup(t, api, tag); len(rows) != 1 {
			t.Fatalf("seeded ip-group %s: got %d rows, want 1", tag, len(rows))
		}
	}
	for _, tag := range []string{"ClnAFlow", "ClnBFlow"} {
		if rows := showStreamIpPort(t, api, tag); len(rows) != 1 {
			t.Fatalf("seeded stream-ipport %s: got %d rows, want 1", tag, len(rows))
		}
	}

	h.runCLISuccess("targeted clean", "-c", cfgPath, "-r", "clean", "-tag", "ClnA")

	api = h.loginAPI()
	if rows := showCustomIsp(t, api, "ClnA"); len(rows) != 0 {
		t.Fatalf("ClnA custom isp should be cleaned, got %d rows", len(rows))
	}
	if rows := showStreamDomain(t, api, "ClnA"); len(rows) != 0 {
		t.Fatalf("ClnA domain should be cleaned, got %d rows", len(rows))
	}
	if rows := showIpGroup(t, api, "ClnA"); len(rows) != 0 {
		t.Fatalf("ClnA ip-group should be cleaned, got %d rows", len(rows))
	}
	if rows := showStreamIpPort(t, api, "ClnAFlow"); len(rows) != 0 {
		t.Fatalf("ClnAFlow should be cleaned, got %d rows", len(rows))
	}

	if rows := showCustomIsp(t, api, "ClnB"); len(rows) != 1 {
		t.Fatalf("ClnB custom isp should survive the ClnA clean, got %d rows", len(rows))
	}
	if rows := showStreamDomain(t, api, "ClnB"); len(rows) != 1 {
		t.Fatalf("ClnB domain should survive the ClnA clean, got %d rows", len(rows))
	}
	if rows := showIpGroup(t, api, "ClnB"); len(rows) != 1 {
		t.Fatalf("ClnB ip-group should survive the ClnA clean, got %d rows", len(rows))
	}
	if rows := showStreamIpPort(t, api, "ClnBFlow"); len(rows) != 1 {
		t.Fatalf("ClnBFlow should survive the ClnA clean, got %d rows", len(rows))
	}
}

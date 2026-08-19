// export_stream_domain_test.go 域名规则导出冒烟。
// 覆盖点对齐 apps/integration-tests/tests/export_stream_domain_smoke.rs。
// export_stream_domain_test.go Stream-domain export smoke. Coverage mirrors
// apps/integration-tests/tests/export_stream_domain_smoke.rs.
package smoke

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 覆盖点：
// 1) CLI 导出模式 exportDomainSteamToTxt；
// 2) 导出文件命名和内容过滤（注释/空行/非法域名）；
// 3) 导出完成 banner。
// Coverage:
// 1) CLI exportDomainSteamToTxt mode.
// 2) Output file naming and domain filtering.
// 3) Export completion banner.
func TestExportStreamDomainSmoke(t *testing.T) {
	h := startHarness(t)

	h.fixture.setText(t, "/export/domains.txt", "foo.example\n# comment\n_bad.example\nbar.example\n\n")

	extra := fmt.Sprintf(`stream-domain:
  - interface: wan2
    src-addr: 192.168.1.10-192.168.1.20
    src-addr-opt-ipgroup: ""
    url: %q
    tag: ExportTag
`, h.fixture.url("/export/domains.txt"))
	cfgPath := h.writeConfig("export.yml", renderTestConfig(h.sim.URL(), harnessUser, harnessPass, extra))

	exportDir := filepath.Join(h.dir, "export-out")

	res := h.runCLISuccess("export", "-c", cfgPath, "-r", "exportDomainSteamToTxt", "-exportPath", exportDir)
	if !res.stdoutContains("[END:导出完毕]") {
		t.Fatalf("expected stdout to contain '[END:导出完毕]', got:\n%s", res.stdout)
	}

	content, err := os.ReadFile(filepath.Join(exportDir, "stream-domain_wan2_ExportTag.txt"))
	if err != nil {
		t.Fatalf("failed to read export file: %v", err)
	}
	text := string(content)
	for _, want := range []string{"foo.example\n", "bar.example\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("export content missing %q, got:\n%s", want, text)
		}
	}
	if strings.Contains(text, "_bad.example") {
		t.Fatalf("export content should filter invalid domains, got:\n%s", text)
	}
}

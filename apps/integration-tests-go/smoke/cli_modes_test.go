// cli_modes_test.go CLI 参数路径冒烟：非法参数分支、once 别名与 cronAft 启动。
// 覆盖点对齐 apps/integration-tests/tests/cli_modes_smoke.rs。
// cli_modes_test.go CLI parameter-path smoke: the invalid-argument branches,
// the once alias, and the cronAft startup. Coverage mirrors
// apps/integration-tests/tests/cli_modes_smoke.rs.
package smoke

import (
	"fmt"
	"testing"
)

// 覆盖点：
// 1) CLI 参数错误分支（invalid -r / invalid -m / clean 缺 tag / -r web 已移除）；
// 2) once 别名模式（-r 1）；
// 3) cronAft 启动路径。
// Coverage:
// 1) CLI invalid-arg branches.
// 2) once alias mode (-r 1).
// 3) cronAft startup path.
func TestCliModesSmoke(t *testing.T) {
	h := startHarness(t)

	h.fixture.setText(t, "/modes/isp.txt", "1.1.1.0/24\n2.2.2.0/24\n")
	h.fixture.setText(t, "/modes/domain.txt", "foo.example\nbar.example\n")
	h.fixture.setText(t, "/modes/ipv4.txt", "8.8.8.8\n9.9.9.0/24\n")

	extra := fmt.Sprintf(`custom-isp:
  - tag: ModesIsp
    url: %q
stream-domain:
  - interface: wan2
    src-addr: 192.168.1.10-192.168.1.20
    src-addr-opt-ipgroup: ""
    url: %q
    tag: ModesDom
ip-group:
  - tag: Modes4
    url: %q
`,
		h.fixture.url("/modes/isp.txt"),
		h.fixture.url("/modes/domain.txt"),
		h.fixture.url("/modes/ipv4.txt"),
	)
	cfgPath := h.writeConfig("cli-modes.yml", renderTestConfig(h.sim.URL(), harnessUser, harnessPass, extra))

	invalidR := h.runCLIFailure("invalid -r", "-c", cfgPath, "-r", "invalid-mode")
	if !invalidR.stderrContains("Invalid -r parameter") {
		t.Fatalf("expected stderr to contain 'Invalid -r parameter', got:\n%s", invalidR.stderr)
	}

	removedWeb := h.runCLIFailure("-r web removed", "-c", cfgPath, "-r", "web")
	if !removedWeb.stderrContains("-r web 已移除") {
		t.Fatalf("expected stderr to contain '-r web 已移除', got:\n%s", removedWeb.stderr)
	}

	invalidM := h.runCLIFailure("invalid -m", "-c", cfgPath, "-r", "once", "-m", "invalid-module")
	if !invalidM.stderrContains("Invalid -m parameter") {
		t.Fatalf("expected stderr to contain 'Invalid -m parameter', got:\n%s", invalidM.stderr)
	}

	cleanMissingTag := h.runCLIFailure("clean missing tag", "-c", cfgPath, "-r", "clean")
	if !cleanMissingTag.stderrContains("Clean mode requires -tag") {
		t.Fatalf("expected stderr to contain 'Clean mode requires -tag', got:\n%s", cleanMissingTag.stderr)
	}

	onceAlias := h.runCLISuccess("once alias -r 1", "-c", cfgPath, "-r", "1", "-m", "ii")
	if !onceAlias.stdoutContains("[END:运行完毕]") {
		t.Fatalf("expected stdout to contain '[END:运行完毕]', got:\n%s", onceAlias.stdout)
	}

	cronAft := h.runCLISuccess("cronAft startup", "-c", cfgPath, "-r", "cronAft", "-m", "ii")
	if !cronAft.stdoutContains("CronAft mode") {
		t.Fatalf("expected stdout to contain 'CronAft mode', got:\n%s", cronAft.stdout)
	}
}

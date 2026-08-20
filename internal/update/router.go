// router.go 默认网关探测，行为对齐 rust_archive/crates/core/src/router.rs：
// 仅 Linux 读取 /proc/net/route，取 dest=00000000 且 flags 含网关位（0x2）
// 的行，第三列小端十六进制还原为点分 IPv4。
// Default-gateway detection aligned with router.rs: Linux only, reading
// /proc/net/route for the dest=00000000 row whose flags carry the gateway bit
// (0x2), decoding the little-endian hex third column into a dotted IPv4.
package update

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// errGatewayNotFound 对齐 router.rs L9-10 的 GatewayError::NotFound。
// errGatewayNotFound mirrors GatewayError::NotFound of router.rs L9-10.
var errGatewayNotFound = errors.New("default gateway not found")

// GetGatewayV4 返回默认网关 IPv4（router.rs L13-27）：非 Linux 平台恒为
// NotFound；Linux 读取 /proc/net/route，IO 失败 => ReadFailed，无可匹配行
// => NotFound。
// GetGatewayV4 returns the default-gateway IPv4 (router.rs L13-27): non-Linux
// platforms always fail with NotFound; on Linux an IO failure yields
// ReadFailed and no matching row yields NotFound.
func GetGatewayV4() (string, error) {
	if runtime.GOOS != "linux" {
		return "", errGatewayNotFound
	}
	content, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return "", fmt.Errorf("read route table failed: %w", err)
	}
	if ip, ok := parseProcNetRouteGateway(string(content)); ok {
		return ip, nil
	}
	return "", errGatewayNotFound
}

// parseProcNetRouteGateway 解析路由表文本（router.rs L29-62）：跳过表头，
// 目的地址 00000000 且 flags&0x2 的行取网关列；flags/gateway 十六进制
// 解析失败时整个函数短路失败（对齐 Rust `?` 语义）。
// parseProcNetRouteGateway parses the route-table text (router.rs L29-62):
// skip the header, keep rows with destination 00000000 whose flags carry 0x2
// and take their gateway column; a hex parse failure of flags/gateway
// short-circuits the whole function (the Rust `?` semantics).
func parseProcNetRouteGateway(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if i == 0 {
			continue
		}
		cols := strings.Fields(line)
		if len(cols) < 4 {
			continue
		}
		if cols[1] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(cols[3], 16, 16)
		if err != nil {
			return "", false
		}
		if flags&0x2 == 0 {
			continue
		}
		gw, err := strconv.ParseUint(cols[2], 16, 32)
		if err != nil {
			return "", false
		}
		return fmt.Sprintf("%d.%d.%d.%d",
			gw&0xFF, (gw>>8)&0xFF, (gw>>16)&0xFF, (gw>>24)&0xFF), true
	}
	return "", false
}

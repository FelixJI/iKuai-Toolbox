// static.go 内嵌静态资源与 SPA fallback，行为对齐 apps/cli/src/embedded.rs：
// 未命中回退 index.html，index.html 亦缺失才 404 "Not found"；
// MIME 按扩展名映射，其余 octet-stream。
// Embedded static assets and the SPA fallback, aligned with
// apps/cli/src/embedded.rs: a miss falls back to index.html and only a
// missing index.html answers 404 "Not found"; MIME types map by extension
// with everything else falling to octet-stream.
package webserver

import (
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"

	ikuaitoolbox "github.com/FelixJI/iKuai-Toolbox"
)

// distFS 内嵌 dist 子树（go:embed 根在仓库根包，见 embed.go）。
// 变量形式便于契约测试注入空树验证 404 路径。
// distFS is the embedded dist subtree (the go:embed root lives in the
// repository root package; see embed.go). Kept as variables so contract
// tests can inject an empty tree to verify the 404 path.
var distFS, distFSErr = fs.Sub(ikuaitoolbox.FrontendFS, "frontends/app/dist")

// handleStatic "/" 兜底：先精确找文件，未命中回退 index.html（embedded.rs L31-53）。
// handleStatic is the "/" catch-all: exact file lookup first, then the
// index.html SPA fallback (embedded.rs L31-53).
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if distFSErr != nil {
		writeText(w, http.StatusNotFound, "Not found")
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		name = "index.html"
	}
	// fs.ValidPath 拒绝 ".."、绝对路径等非法片段，等价于 embed FS 的查找约束。
	// fs.ValidPath rejects "..", absolute paths and other illegal segments,
	// matching the lookup constraints of an embed FS.
	if fs.ValidPath(name) {
		if data, err := fs.ReadFile(distFS, name); err == nil {
			w.Header().Set("Content-Type", mimeType(name))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
			return
		}
	}
	// SPA fallback: serve index.html for client-side routing.
	if data, err := fs.ReadFile(distFS, "index.html"); err == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return
	}
	writeText(w, http.StatusNotFound, "Not found")
}

// mimeType 按扩展名映射 MIME（embedded.rs L9-29；大小写敏感与 Rust
// ends_with 一致）。
// mimeType maps an extension to its MIME type (embedded.rs L9-29;
// case-sensitive to match the Rust ends_with checks).
func mimeType(name string) string {
	switch filepath.Ext(name) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js":
		return "text/javascript"
	case ".css":
		return "text/css"
	case ".png":
		return "image/png"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	case ".webp":
		return "image/webp"
	case ".json":
		return "application/json"
	default:
		return "application/octet-stream"
	}
}

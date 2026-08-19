// auth.go 动态 BasicAuth 中间件，行为对齐 apps/cli/src/web.rs L451-519：
// 每请求重读当前配置的 webui.user/pass；user 空放行；否则常数时间比较，
// 失败 401 + WWW-Authenticate + "Unauthorized"。
// Dynamic BasicAuth middleware aligned with apps/cli/src/web.rs L451-519:
// every request re-reads webui.user/pass from the live config; a blank user
// passes through; otherwise a constant-time comparison guards entry and any
// failure answers 401 + WWW-Authenticate + "Unauthorized".
package webserver

import (
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
)

// withBasicAuth 给 next 包一层动态 BasicAuth（凭据随配置即时生效）。
// withBasicAuth wraps next with the dynamic BasicAuth layer (credentials
// follow config updates immediately).
func (s *Server) withBasicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := s.cfgHolder.Load()
		user, pass := cfg.WebUI.User, cfg.WebUI.Pass
		if user == "" {
			next.ServeHTTP(w, r)
			return
		}
		u, p, ok := decodeBasicAuth(r.Header.Get("Authorization"))
		// subtle.ConstantTimeCompare 仅在同长度同内容时返回 1；
		// 长度差异直接判不等（对齐任务书指定的 crypto/subtle 方案）。
		// subtle.ConstantTimeCompare returns 1 only for equal-length,
		// equal-content inputs; a length mismatch is a mismatch (per the
		// crypto/subtle approach mandated by the task brief).
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), []byte(user)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(pass)) != 1 {
			unauthorized(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// decodeBasicAuth 解析 "Basic base64(user:pass)" 头；任意一步失败返回 ok=false。
// 用户名/密码按第一个冒号二分（对齐 web.rs splitn(2, ':')），密码可含冒号。
// decodeBasicAuth parses the "Basic base64(user:pass)" header; any failed
// step yields ok=false. The pair splits on the first colon (mirroring the
// splitn(2, ':') of web.rs) so passwords may contain colons.
func decodeBasicAuth(authz string) (user, pass string, ok bool) {
	const prefix = "Basic "
	if !strings.HasPrefix(authz, prefix) {
		return "", "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(authz[len(prefix):])
	if err != nil {
		return "", "", false
	}
	user, pass, _ = strings.Cut(string(decoded), ":")
	return user, pass, true
}

// unauthorized 401 响应（web.rs L512-519）。
// unauthorized renders the 401 response (web.rs L512-519).
func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
	writeText(w, http.StatusUnauthorized, "Unauthorized")
}

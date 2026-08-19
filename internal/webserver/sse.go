// sse.go /api/runtime/logs/stream 的 SSE 流式日志，行为对齐 web.rs
// L436-449：订阅日志广播转 data:<LogRecord JSON> 行，http.Flusher 即时下推，
// 空闲按 axum KeepAlive 语义发送 ":" 注释行保活；客户端断开即取消订阅。
// The SSE log stream of /api/runtime/logs/stream, aligned with web.rs
// L436-449: the log broadcast is rendered as data:<LogRecord JSON> lines and
// flushed immediately via http.Flusher; idle periods emit ":" keep-alive
// comments per axum's KeepAlive semantics; a client disconnect cancels the
// subscription right away.
package webserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// sseKeepAliveInterval 保活注释间隔（包级变量便于测试注入更短周期）。
// sseKeepAliveInterval is the keep-alive comment interval (a package-level
// variable so tests can inject a shorter period).
var sseKeepAliveInterval = 15 * time.Second

// handleLogsStream GET /api/runtime/logs/stream。
// handleLogsStream GET /api/runtime/logs/stream.
func (s *Server) handleLogsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeText(w, http.StatusInternalServerError, "Streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, cancel := s.rt.SubscribeLogs()
	defer cancel()

	keepAlive := time.NewTicker(sseKeepAliveInterval)
	defer keepAlive.Stop()

	// SubscribeLogs 的 cancel 不关闭 channel：消费端必须同时 select
	// r.Context().Done()，否则客户端断开后 goroutine 会永久阻塞泄漏。
	// SubscribeLogs' cancel does not close the channel: the consumer must
	// also select on r.Context().Done(), otherwise the goroutine leaks
	// forever after a client disconnect.
	for {
		select {
		case <-r.Context().Done():
			return
		case rec := <-ch:
			// 序列化失败退化为空 data 行（对齐 unwrap_or_default）。
			// A marshal failure degrades to an empty data line (mirroring
			// unwrap_or_default).
			data, err := json.Marshal(rec)
			if err != nil {
				data = nil
			}
			fmt.Fprintf(w, "data:%s\n\n", data)
			flusher.Flush()
		case <-keepAlive.C:
			fmt.Fprint(w, ":\n\n")
			flusher.Flush()
		}
	}
}

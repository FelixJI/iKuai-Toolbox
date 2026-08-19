// broker.go 日志代理：5000 条环形缓冲 + 订阅广播（channel 容量 512，满则
// 丢弃不阻塞），行为对齐 rust_archive/crates/core/src/runtime.rs L26-75 的 LogBroker。
// Log broker: a 5000-entry ring buffer plus broadcast subscriptions
// (channels of capacity 512, dropping instead of blocking when full),
// aligned with LogBroker of rust_archive/crates/core/src/runtime.rs L26-75.
package runtime

import (
	"sync"

	"github.com/FelixJI/iKuai-Toolbox/internal/logger"
)

const (
	// brokerMaxLines 环形缓冲容量，对齐 runtime.rs L115 的 5000。
	// brokerMaxLines is the ring capacity mirroring the 5000 of runtime.rs L115.
	brokerMaxLines = 5000

	// brokerSubsCap 每个订阅 channel 的容量，对齐 runtime.rs L34 的 512。
	// brokerSubsCap is each subscription channel's capacity, mirroring the
	// 512 of runtime.rs L34.
	brokerSubsCap = 512
)

// logBroker 互斥锁保护的环形缓冲 + 订阅者集合。
// logBroker is a mutex-guarded ring buffer plus a subscriber set.
type logBroker struct {
	mu    sync.Mutex
	max   int
	buf   []logger.LogRecord
	start int
	count int
	subs  map[chan logger.LogRecord]struct{}
}

// newLogBroker 构造指定容量的代理（容量下限 1，对齐 max_lines.max(1)）。
// newLogBroker builds a broker with the given capacity (floor of 1, matching
// max_lines.max(1)).
func newLogBroker(maxLines int) *logBroker {
	if maxLines < 1 {
		maxLines = 1
	}
	return &logBroker{
		max:  maxLines,
		buf:  make([]logger.LogRecord, maxLines),
		subs: make(map[chan logger.LogRecord]struct{}),
	}
}

// append 追加一条记录：环形缓冲满时覆盖最旧条目；随后向所有订阅者
// 非阻塞广播（满则丢弃）。发送在锁内完成以保证广播顺序与写入一致。
// append stores one record (overwriting the oldest entry once the ring
// wraps) and broadcasts to every subscriber non-blockingly (dropping when
// full). Sends happen under the lock so broadcast order matches write order.
func (b *logBroker) append(rec logger.LogRecord) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.count == b.max {
		b.buf[b.start] = rec
		b.start = (b.start + 1) % b.max
	} else {
		b.buf[(b.start+b.count)%b.max] = rec
		b.count++
	}

	for ch := range b.subs {
		select {
		case ch <- rec:
		default:
		}
	}
}

// tail 返回最新 n 条（升序）；n 下限 1、上限缓冲现存条数，对齐
// runtime.rs L57-70 的 n.max(1).min(len)。
// tail returns the newest n records in ascending order; n clamps to at least
// 1 and at most the buffered count, mirroring n.max(1).min(len) of runtime.rs
// L57-70.
func (b *logBroker) tail(n int) []logger.LogRecord {
	b.mu.Lock()
	defer b.mu.Unlock()

	if n < 1 {
		n = 1
	}
	if n > b.count {
		n = b.count
	}
	out := make([]logger.LogRecord, 0, n)
	begin := b.start + b.count - n
	for i := 0; i < n; i++ {
		out = append(out, b.buf[(begin+i)%b.max])
	}
	return out
}

// subscribe 注册一个容量 512 的广播 channel，返回 channel 与取消函数。
// subscribe registers a broadcast channel of capacity 512 and returns it
// together with its cancel func.
func (b *logBroker) subscribe() (<-chan logger.LogRecord, func()) {
	ch := make(chan logger.LogRecord, brokerSubsCap)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, ch)
			b.mu.Unlock()
		})
	}
	return ch, cancel
}

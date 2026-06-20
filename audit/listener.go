package audit

import (
	"context"

	"github.com/apus-run/better-token/core"
)

// Sink 是审计事件的输出端口（日志、消息队列、外部审计系统等）。
type Sink interface {
	Write(ctx context.Context, event AuditEvent) error
}

// Listener 把 core.Event 映射为 AuditEvent 并写入 Sink，实现 core.Listener。
type Listener struct {
	sink   Sink
	mapper Mapper
}

// Option 定制审计监听器。
type Option func(*Listener)

// WithMapper 覆盖默认的事件映射。
func WithMapper(mapper Mapper) Option {
	return func(l *Listener) {
		if mapper != nil {
			l.mapper = mapper
		}
	}
}

// New 构造一个审计监听器。sink 为 nil 时回退到默认 slog Sink。
func New(sink Sink, opts ...Option) *Listener {
	if sink == nil {
		sink = NewSlogSink(nil)
	}
	l := &Listener{sink: sink, mapper: DefaultMapper}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// Handle 实现 core.Listener。
func (l *Listener) Handle(ctx context.Context, event core.Event) error {
	return l.sink.Write(ctx, l.mapper(event))
}

var _ core.Listener = (*Listener)(nil)

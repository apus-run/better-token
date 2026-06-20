package audit

import (
	"context"
	"log/slog"
)

// SlogSink 是默认 Sink，把审计事件写入 *slog.Logger。
type SlogSink struct {
	logger *slog.Logger
	level  slog.Level
}

// NewSlogSink 构造一个 slog Sink。logger 为 nil 时使用 slog.Default()。
func NewSlogSink(logger *slog.Logger) *SlogSink {
	if logger == nil {
		logger = slog.Default()
	}
	return &SlogSink{logger: logger, level: slog.LevelInfo}
}

// Write 实现 Sink。
func (s *SlogSink) Write(ctx context.Context, event AuditEvent) error {
	attrs := []slog.Attr{
		slog.String("type", string(event.Type)),
		slog.Time("time", event.Time),
		slog.String("result", event.Result),
	}
	if event.LoginID != "" {
		attrs = append(attrs, slog.String("login_id", event.LoginID))
	}
	if event.LoginType != "" {
		attrs = append(attrs, slog.String("login_type", event.LoginType))
	}
	if event.Token != "" {
		attrs = append(attrs, slog.String("token", string(event.Token)))
	}
	if event.Device != "" {
		attrs = append(attrs, slog.String("device", event.Device))
	}
	if event.IP != "" {
		attrs = append(attrs, slog.String("ip", event.IP))
	}
	s.logger.LogAttrs(ctx, s.level, "audit", attrs...)
	return nil
}

var _ Sink = (*SlogSink)(nil)

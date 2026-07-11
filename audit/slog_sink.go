package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"

	"github.com/apus-run/better-token/core"
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
		// token 是 bearer 凭证，绝不写入明文——日志常汇聚到访问面更宽的
		// 聚合系统，读日志即可重放会话。只记录 SHA-256 指纹前缀，够做关联排障
		// 又不可反推 token。
		attrs = append(attrs, slog.String("token_fp", tokenFingerprint(event.Token)))
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

// tokenFingerprint 返回 token 的 SHA-256 十六进制指纹前缀（16 hex = 64 bit），
// 用于日志关联而不泄露原始 bearer token。
func tokenFingerprint(token core.TokenValue) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:16]
}

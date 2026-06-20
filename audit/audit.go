// Package audit 提供基于 core.Event 的结构化审计能力：独立的 AuditEventType 与
// AuditEvent 模型，以及实现 core.Listener 的审计监听器。可注册到同步或异步 EventBus，
// 监听器异常不会影响主认证流程。
package audit

import (
	"time"

	"github.com/apus-run/better-token/core"
)

// AuditEventType 是与运行态 core.EventType 解耦的审计事件类型。
type AuditEventType string

const (
	AuditLogin          AuditEventType = "audit.login"
	AuditLogout         AuditEventType = "audit.logout"
	AuditKickOut        AuditEventType = "audit.kick_out"
	AuditReplaced       AuditEventType = "audit.replaced"
	AuditRenew          AuditEventType = "audit.renew"
	AuditRefreshIssued  AuditEventType = "audit.refresh_issued"
	AuditRefresh        AuditEventType = "audit.refresh"
	AuditRefreshRevoked AuditEventType = "audit.refresh_revoked"
	AuditNonceConsumed  AuditEventType = "audit.nonce_consumed"
	AuditOnline         AuditEventType = "audit.online"
	AuditOffline        AuditEventType = "audit.offline"
	AuditUnknown        AuditEventType = "audit.unknown"
)

// AuditEvent 是结构化审计记录。
type AuditEvent struct {
	Type      AuditEventType  `json:"type"`
	LoginID   string          `json:"login_id,omitempty"`
	LoginType string          `json:"login_type,omitempty"`
	Token     core.TokenValue `json:"token,omitempty"`
	Device    string          `json:"device,omitempty"`
	IP        string          `json:"ip,omitempty"`
	Time      time.Time       `json:"time"`
	Result    string          `json:"result"`
	Detail    map[string]any  `json:"detail,omitempty"`
}

// Mapper 把 core.Event 映射为 AuditEvent。
type Mapper func(core.Event) AuditEvent

var eventTypeToAudit = map[core.EventType]AuditEventType{
	core.EventLogin:          AuditLogin,
	core.EventLogout:         AuditLogout,
	core.EventKickOut:        AuditKickOut,
	core.EventReplaced:       AuditReplaced,
	core.EventRenewTimeout:   AuditRenew,
	core.EventRefreshIssued:  AuditRefreshIssued,
	core.EventRefresh:        AuditRefresh,
	core.EventRefreshRevoked: AuditRefreshRevoked,
	core.EventNonceConsumed:  AuditNonceConsumed,
	core.EventOnline:         AuditOnline,
	core.EventOffline:        AuditOffline,
}

// MapEventType 把运行态事件类型映射为审计类型，未知类型返回 AuditUnknown。
func MapEventType(t core.EventType) AuditEventType {
	if mapped, ok := eventTypeToAudit[t]; ok {
		return mapped
	}
	return AuditUnknown
}

// DefaultMapper 是默认映射：透传主体/token/时间，从 Metadata 提取 device/ip。
func DefaultMapper(ev core.Event) AuditEvent {
	ae := AuditEvent{
		Type:      MapEventType(ev.Type),
		LoginID:   ev.LoginID,
		LoginType: ev.LoginType,
		Token:     ev.Token,
		Time:      ev.Time,
		Result:    "success",
		Detail:    ev.Metadata,
	}
	if ev.Metadata != nil {
		ae.Device, _ = ev.Metadata["device"].(string)
		ae.IP, _ = ev.Metadata["ip"].(string)
	}
	return ae
}

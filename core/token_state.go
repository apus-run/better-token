package core

import (
	"encoding/json"
	"time"
)

type TokenValue string

// TokenKind 区分统一 TokenState 承载的 token 语义。
type TokenKind string

const (
	TokenKindAccess  TokenKind = "access"
	TokenKindRefresh TokenKind = "refresh"
	TokenKindNonce   TokenKind = "nonce"
)

// TokenStatus 表示 TokenState 的生命周期状态。expired 不落库，由 ExpiresAt 计算。
type TokenStatus string

const (
	TokenStatusActive   TokenStatus = "active"
	TokenStatusRevoked  TokenStatus = "revoked"
	TokenStatusConsumed TokenStatus = "consumed"
)

// RefreshInfo 仅在 Kind==refresh 时有效。
type RefreshInfo struct {
	AccessToken TokenValue `json:"access_token,omitempty"`
	RotatedFrom TokenValue `json:"rotated_from,omitempty"`
	RotatedTo   TokenValue `json:"rotated_to,omitempty"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

func (i *RefreshInfo) Clone() *RefreshInfo {
	if i == nil {
		return nil
	}
	clone := *i
	clone.LastUsedAt = cloneTimePtrUTC(i.LastUsedAt)
	clone.RevokedAt = cloneTimePtrUTC(i.RevokedAt)
	return &clone
}

// NonceInfo 仅在 Kind==nonce 时有效。
type NonceInfo struct {
	Purpose    string     `json:"purpose,omitempty"`
	ConsumedAt *time.Time `json:"consumed_at,omitempty"`
}

func (i *NonceInfo) Clone() *NonceInfo {
	if i == nil {
		return nil
	}
	clone := *i
	clone.ConsumedAt = cloneTimePtrUTC(i.ConsumedAt)
	return &clone
}

// OnlineInfo 是 access token 的在线投影。
type OnlineInfo struct {
	OnlineAt     *time.Time `json:"online_at,omitempty"`
	OfflineAt    *time.Time `json:"offline_at,omitempty"`
	ConnectionID string     `json:"connection_id,omitempty"`
	UserAgent    string     `json:"user_agent,omitempty"`
	IP           string     `json:"ip,omitempty"`
}

func (i *OnlineInfo) Clone() *OnlineInfo {
	if i == nil {
		return nil
	}
	clone := *i
	clone.OnlineAt = cloneTimePtrUTC(i.OnlineAt)
	clone.OfflineAt = cloneTimePtrUTC(i.OfflineAt)
	return &clone
}

// TokenState 是统一 token 状态模型，用 Kind + Status + 可选 Info 表达
// access / refresh / nonce / online 的公共与差异生命周期字段。
type TokenState struct {
	Token        TokenValue      `json:"token"`
	Kind         TokenKind       `json:"kind"`
	LoginID      string          `json:"login_id"`
	LoginType    string          `json:"login_type"`
	Device       string          `json:"device,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	LastActiveAt time.Time       `json:"last_active_at"`
	ExpiresAt    *time.Time      `json:"expires_at,omitempty"`
	Status       TokenStatus     `json:"status"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`

	Refresh *RefreshInfo `json:"refresh,omitempty"`
	Nonce   *NonceInfo   `json:"nonce,omitempty"`
	Online  *OnlineInfo  `json:"online,omitempty"`
}

// Normalize 为 Kind/Status/LoginType 回填默认值，兼容缺失这些字段的旧序列化数据。
func (s *TokenState) Normalize() {
	if s == nil {
		return
	}
	if s.Kind == "" {
		s.Kind = TokenKindAccess
	}
	if s.Status == "" {
		s.Status = TokenStatusActive
	}
	if s.LoginType == "" {
		s.LoginType = DefaultLoginType
	}
}

func (s TokenState) IsExpired(now time.Time) bool {
	return lifetimeOf(s.ExpiresAt).IsExpired(now)
}

func (s TokenState) IsRevoked() bool {
	return s.Status == TokenStatusRevoked
}

func (s TokenState) IsConsumed() bool {
	return s.Status == TokenStatusConsumed
}

func (s TokenState) IsActive(now time.Time) bool {
	return !s.IsExpired(now) &&
		s.Status != TokenStatusRevoked &&
		s.Status != TokenStatusConsumed
}

// Subject 返回该 token 状态所归属的登录主体。
func (s TokenState) Subject() LoginSubject {
	return LoginSubject{LoginID: s.LoginID, LoginType: s.LoginType}
}

func (s *TokenState) Touch(now time.Time) {
	s.LastActiveAt = now.UTC()
}

func (s *TokenState) MarkRevoked(now time.Time) {
	s.Status = TokenStatusRevoked
	if s.Refresh != nil {
		s.Refresh.RevokedAt = utcTimePtr(now)
	}
}

func (s *TokenState) MarkConsumed(now time.Time) {
	s.Status = TokenStatusConsumed
	if s.Nonce != nil {
		s.Nonce.ConsumedAt = utcTimePtr(now)
	}
}

func (s *TokenState) MarkOnline(now time.Time, info OnlineInfo) {
	info.OnlineAt = utcTimePtr(now)
	if info.OfflineAt == nil && s.Online != nil {
		info.OfflineAt = cloneTimePtrUTC(s.Online.OfflineAt)
	}
	s.Online = info.Clone()
	s.Status = TokenStatusActive
	s.LastActiveAt = now.UTC()
}

func (s *TokenState) MarkOffline(now time.Time) {
	if s.Online == nil {
		s.Online = &OnlineInfo{}
	}
	s.Online.OfflineAt = utcTimePtr(now)
	s.LastActiveAt = now.UTC()
}

func (s TokenState) EffectiveExpiresAt(now time.Time, ttl time.Duration) *time.Time {
	return lifetimeOf(s.ExpiresAt).EffectiveExpiresAt(now, ttl)
}

func (s *TokenState) Clone() *TokenState {
	if s == nil {
		return nil
	}
	clone := *s
	clone.Metadata = cloneRawMessage(s.Metadata)
	clone.ExpiresAt = cloneTimePtrUTC(s.ExpiresAt)
	clone.Refresh = s.Refresh.Clone()
	clone.Nonce = s.Nonce.Clone()
	clone.Online = s.Online.Clone()
	return &clone
}

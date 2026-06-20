package core

import (
	"encoding/json"
	"time"
)

type TokenValue string

type TokenState struct {
	Token        TokenValue      `json:"token"`
	LoginID      string          `json:"login_id"`
	LoginType    string          `json:"login_type"`
	Device       string          `json:"device,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	LastActiveAt time.Time       `json:"last_active_at"`
	ExpiresAt    *time.Time      `json:"expires_at,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
}

func (s TokenState) IsExpired(now time.Time) bool {
	return s.ExpiresAt != nil && !now.UTC().Before(s.ExpiresAt.UTC())
}

// Subject 返回该 token 状态所归属的登录主体。
func (s TokenState) Subject() LoginSubject {
	return LoginSubject{LoginID: s.LoginID, LoginType: s.LoginType}
}

func (s *TokenState) Touch(now time.Time) {
	s.LastActiveAt = now.UTC()
}

func (s *TokenState) Clone() *TokenState {
	if s == nil {
		return nil
	}
	clone := *s
	clone.Metadata = cloneRawMessage(s.Metadata)
	if s.ExpiresAt != nil {
		expiresAt := s.ExpiresAt.UTC()
		clone.ExpiresAt = &expiresAt
	}
	return &clone
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

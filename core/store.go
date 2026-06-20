package core

import (
	"context"
	"time"
)

// Store 是登录态的持久化端口：以领域语义（TokenState / Session / LoginSubject）
// 表达存取，索引维护交由各实现负责。
type Store interface {
	SaveTokenState(ctx context.Context, state *TokenState, ttl time.Duration) error
	GetTokenState(ctx context.Context, token TokenValue) (*TokenState, bool, error)
	DeleteTokenState(ctx context.Context, token TokenValue) error

	FindTokenStates(ctx context.Context, subject LoginSubject) ([]*TokenState, error)
	DeleteTokenStates(ctx context.Context, subject LoginSubject) error

	SaveSession(ctx context.Context, session *Session, ttl time.Duration) error
	GetSession(ctx context.Context, subject LoginSubject) (*Session, bool, error)
	DeleteSession(ctx context.Context, subject LoginSubject) error
}

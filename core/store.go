package core

import (
	"context"
	"time"
)

// Store 是登录态的统一持久化端口：以领域语义（TokenState / Session / LoginSubject）
// 表达存取，索引维护交由各实现负责。access / refresh / nonce 均以统一 TokenState
// 表达，通过 TokenKind 区分；refresh / nonce 不再有独立的存储端口。
type Store interface {
	SaveTokenState(ctx context.Context, state *TokenState, ttl time.Duration) error
	GetTokenState(ctx context.Context, token TokenValue) (*TokenState, bool, error)
	// ConsumeTokenState 原子地将 active 状态置为 consumed 并返回消费前快照；
	// 若已 consumed/revoked/过期，返回当前快照供上层判定；token 不存在时 found=false。
	ConsumeTokenState(ctx context.Context, token TokenValue) (*TokenState, bool, error)
	DeleteTokenState(ctx context.Context, token TokenValue) error

	// FindTokenStates / DeleteTokenStates 的 kinds 为空表示不过滤（覆盖该 subject 全部 kind）。
	FindTokenStates(ctx context.Context, subject LoginSubject, kinds ...TokenKind) ([]*TokenState, error)
	DeleteTokenStates(ctx context.Context, subject LoginSubject, kinds ...TokenKind) error

	SaveSession(ctx context.Context, session *Session, ttl time.Duration) error
	GetSession(ctx context.Context, subject LoginSubject) (*Session, bool, error)
	DeleteSession(ctx context.Context, subject LoginSubject) error
}

// AtomicStore describes atomic state transitions independently of Store.
// Manager uses it when the supplied Store also implements this capability.
// Operations must be atomic across every client of the store. Wrappers must
// forward all three operations to preserve these guarantees.
type AtomicStore interface {
	// CompareAndSwapTokenState updates only an unexpired state that still matches
	// the snapshot returned by GetTokenState. Both snapshots identify the same
	// token. Missing, expired or changed state returns false,nil; it never inserts.
	CompareAndSwapTokenState(ctx context.Context, expected, next *TokenState, ttl time.Duration) (bool, error)

	// ConsumeTokenStateOfKind checks the kind atomically with consumption. A
	// different kind is returned unchanged; other semantics match ConsumeTokenState.
	ConsumeTokenStateOfKind(ctx context.Context, token TokenValue, kind TokenKind) (*TokenState, bool, error)

	// EnsureSession initializes a missing/expired session or updates only the
	// lifetime of an existing session, preserving Data. ttl <= 0 means permanent.
	EnsureSession(ctx context.Context, subject LoginSubject, ttl time.Duration) error
}

// MatchKind 报告 kind 是否落在给定过滤集合内（空集合表示全部匹配）。
func MatchKind(kind TokenKind, kinds ...TokenKind) bool {
	if len(kinds) == 0 {
		return true
	}
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}

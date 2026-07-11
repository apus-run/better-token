package core_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/storage/memory"
)

// recordingStore 包装真实 Store，记录所有落库的键与序列化值，用于断言明文 token
// 绝不出现在存储层——这是 #2“哈希落库”的核心不变量。
type recordingStore struct {
	inner     core.Store
	persisted []string // 每次 Save 的键(state.Token) + 完整 JSON 值
	keys      []string // Get/Consume/Delete 收到的键
}

func newRecordingStore() *recordingStore {
	return &recordingStore{inner: memory.NewStore()}
}

func (s *recordingStore) SaveTokenState(ctx context.Context, state *core.TokenState, ttl time.Duration) error {
	s.persisted = append(s.persisted, string(state.Token))
	if blob, err := json.Marshal(state); err == nil {
		s.persisted = append(s.persisted, string(blob))
	}
	return s.inner.SaveTokenState(ctx, state, ttl)
}

func (s *recordingStore) GetTokenState(ctx context.Context, token core.TokenValue) (*core.TokenState, bool, error) {
	s.keys = append(s.keys, string(token))
	return s.inner.GetTokenState(ctx, token)
}

func (s *recordingStore) ConsumeTokenState(ctx context.Context, token core.TokenValue) (*core.TokenState, bool, error) {
	s.keys = append(s.keys, string(token))
	return s.inner.ConsumeTokenState(ctx, token)
}

func (s *recordingStore) DeleteTokenState(ctx context.Context, token core.TokenValue) error {
	s.keys = append(s.keys, string(token))
	return s.inner.DeleteTokenState(ctx, token)
}

func (s *recordingStore) FindTokenStates(ctx context.Context, subject core.LoginSubject, kinds ...core.TokenKind) ([]*core.TokenState, error) {
	return s.inner.FindTokenStates(ctx, subject, kinds...)
}

func (s *recordingStore) DeleteTokenStates(ctx context.Context, subject core.LoginSubject, kinds ...core.TokenKind) error {
	return s.inner.DeleteTokenStates(ctx, subject, kinds...)
}

func (s *recordingStore) SaveSession(ctx context.Context, session *core.Session, ttl time.Duration) error {
	if blob, err := json.Marshal(session); err == nil {
		s.persisted = append(s.persisted, string(blob))
	}
	return s.inner.SaveSession(ctx, session, ttl)
}

func (s *recordingStore) GetSession(ctx context.Context, subject core.LoginSubject) (*core.Session, bool, error) {
	return s.inner.GetSession(ctx, subject)
}

func (s *recordingStore) DeleteSession(ctx context.Context, subject core.LoginSubject) error {
	return s.inner.DeleteSession(ctx, subject)
}

// assertNoPlaintext 断言给定明文 token 未出现在任何落库的键或值中。
func (s *recordingStore) assertNoPlaintext(t *testing.T, plaintexts ...string) {
	t.Helper()
	for _, p := range plaintexts {
		for _, entry := range s.persisted {
			if strings.Contains(entry, p) {
				t.Fatalf("明文 token %q 出现在落库内容中: %s", p, entry)
			}
		}
		for _, k := range s.keys {
			if k == p {
				t.Fatalf("明文 token %q 被当作存储键使用", p)
			}
		}
	}
}

// TestNoPlaintextTokenAtRest 覆盖 access / refresh / nonce 全流程，断言：
// 1) 明文 token 绝不落库（键与值皆无）；2) 公开 API 仍返回明文 token（往返正确）。
func TestNoPlaintextTokenAtRest(t *testing.T) {
	const (
		accessPlain   = "access-plaintext-SECRET"
		refreshPlain  = "refresh-plaintext-SECRET"
		access2Plain  = "access2-plaintext-SECRET"
		refresh2Plain = "refresh2-plaintext-SECRET"
	)
	store := newRecordingStore()
	m := core.NewManager(store, core.WithConfig(core.Config{
		TokenName:    "token",
		Timeout:      time.Hour,
		Concurrent:   true,
		RequireNonce: true,
	}))
	ctx := context.Background()

	// nonce 流程
	nonce, err := m.GenerateNonce(ctx)
	if err != nil {
		t.Fatalf("GenerateNonce: %v", err)
	}
	if strings.TrimSpace(string(nonce)) == "" {
		t.Fatal("GenerateNonce returned empty")
	}

	// 带 nonce 的登录 + refresh 签发
	result, err := m.LoginWithRefresh(ctx, "1001", accessPlain, refreshPlain, core.WithNonce(nonce))
	if err != nil {
		t.Fatalf("LoginWithRefresh: %v", err)
	}
	// 公开 API 返回明文（往返正确）
	if result.TokenState.Token != accessPlain {
		t.Fatalf("login should return plaintext access, got %q", result.TokenState.Token)
	}
	if result.RefreshState.Token != refreshPlain {
		t.Fatalf("login should return plaintext refresh, got %q", result.RefreshState.Token)
	}

	// 用明文 access 校验通过
	if !m.IsValid(ctx, accessPlain) {
		t.Fatal("access token should validate by plaintext")
	}
	got, err := m.GetTokenState(ctx, accessPlain)
	if err != nil || got.Token != accessPlain {
		t.Fatalf("GetTokenState by plaintext failed: state=%v err=%v", got, err)
	}

	// 轮换 refresh
	rotated, err := m.Refresh(ctx, refreshPlain, access2Plain, core.WithNextRefreshToken(refresh2Plain))
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if rotated.TokenState.Token != access2Plain || rotated.RefreshState.Token != refresh2Plain {
		t.Fatalf("refresh should return plaintext tokens, got %#v", rotated)
	}

	// 关键断言：所有明文 token / nonce 均未落库
	store.assertNoPlaintext(t,
		accessPlain, refreshPlain, access2Plain, refresh2Plain, string(nonce),
	)
}

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/apus-run/better-token/pkg/option"
)

type Manager struct {
	config      Config
	store       Store
	atomicStore AtomicStore
	authorizer  Authorizer
	eventBus    EventBus
	runtime     Runtime
}

func NewManager(store Store, opts ...Option) *Manager {
	if store == nil {
		panic("core: nil store")
	}

	m := &Manager{
		config:     DefaultConfig(),
		store:      store,
		authorizer: NoopAuthorizer{},
		eventBus:   NewEventBus(),
		runtime:    DefaultRuntime(),
	}
	option.Apply(m, opts...)
	m.atomicStore, _ = m.store.(AtomicStore)
	m.config.Refresh = m.config.Refresh.withDefaults()
	m.config.Nonce = m.config.Nonce.withDefaults()
	return m
}

func (m *Manager) Config() Config {
	return m.config
}

func (m *Manager) Login(ctx context.Context, loginID string, token TokenValue, opts ...LoginOption) (*TokenState, error) {
	loginOpts := loginOptions{loginType: DefaultLoginType}
	option.Apply(&loginOpts, opts...)
	state, err := m.login(ctx, loginID, token, loginOpts, true)
	if err != nil {
		return nil, err
	}
	// 内部 state.Token 为存储键（哈希），交还调用方时注入其传入的明文 token。
	return withPlaintextToken(state, token), nil
}

func (m *Manager) login(ctx context.Context, loginID string, token TokenValue, loginOpts loginOptions, enforceNonce bool) (*TokenState, error) {
	ctx = normalizeContext(ctx)
	loginID = strings.TrimSpace(loginID)
	if loginID == "" {
		return nil, ErrEmptyLoginID
	}
	token = TokenValue(strings.TrimSpace(string(token)))
	if token == "" {
		return nil, ErrEmptyToken
	}

	if enforceNonce && m.config.RequireNonce {
		if loginOpts.nonce == "" {
			return nil, ErrEmptyNonce
		}
		if _, err := m.ConsumeNonce(ctx, loginOpts.nonce); err != nil {
			return nil, err
		}
	}

	now := m.now()
	subject := LoginSubject{LoginID: loginID, LoginType: loginOpts.loginType}.Normalize()

	if !m.config.Concurrent {
		if err := m.store.DeleteTokenStates(ctx, subject, TokenKindAccess); err != nil {
			return nil, err
		}
		if err := m.revokeRefreshExcept(ctx, subject, loginOpts.preserveRefresh); err != nil {
			return nil, err
		}
		m.publish(ctx, Event{
			Type:      EventReplaced,
			LoginID:   subject.LoginID,
			LoginType: subject.LoginType,
		})
	}

	var (
		expiresAt *time.Time
		ttl       time.Duration
	)
	if m.config.Timeout > 0 {
		ttl = m.config.Timeout
		exp := now.Add(ttl).UTC()
		expiresAt = &exp
	}
	state := &TokenState{
		Token:        storeKey(token),
		Kind:         TokenKindAccess,
		LoginID:      subject.LoginID,
		LoginType:    subject.LoginType,
		Device:       loginOpts.device,
		CreatedAt:    now,
		LastActiveAt: now,
		ExpiresAt:    expiresAt,
		Status:       TokenStatusActive,
		Metadata:     cloneRawMessage(loginOpts.metadata),
	}
	if err := m.store.SaveTokenState(ctx, state, ttl); err != nil {
		return nil, err
	}
	if err := m.syncSessionTTL(ctx, subject, ttl); err != nil {
		return nil, err
	}

	m.publish(ctx, Event{
		Type:      EventLogin,
		LoginID:   subject.LoginID,
		LoginType: subject.LoginType,
		Token:     token,
	})

	return state.Clone(), nil
}

func (m *Manager) GetTokenState(ctx context.Context, token TokenValue) (*TokenState, error) {
	state, err := m.getTokenState(ctx, token, true)
	if err != nil {
		return nil, err
	}
	// 内部 state.Token 为存储键（哈希），交还调用方时注入其传入的明文 token。
	return withPlaintextToken(state, token), nil
}

func (m *Manager) getTokenState(ctx context.Context, token TokenValue, allowAutoRenew bool) (*TokenState, error) {
	ctx = normalizeContext(ctx)
	token = TokenValue(strings.TrimSpace(string(token)))
	if token == "" {
		return nil, ErrEmptyToken
	}
	key := storeKey(token)

	state, ok, err := m.store.GetTokenState(ctx, key)
	if err != nil {
		return nil, err
	}
	if !ok || state == nil {
		return nil, ErrTokenNotFound
	}
	state.Normalize()

	if state.Status != TokenStatusActive {
		return nil, ErrTokenInvalid
	}

	now := m.now()
	if state.IsExpired(now) {
		_ = m.store.DeleteTokenState(ctx, key)
		return nil, ErrTokenNotFound
	}

	if allowAutoRenew && m.config.AutoRenew && state.Kind == TokenKindAccess {
		expected := state.Clone()
		state.Touch(now)
		if m.config.ActiveTimeout > 0 {
			expiresAt := now.Add(m.config.ActiveTimeout).UTC()
			state.ExpiresAt = &expiresAt
		}
		ttl := time.Duration(0)
		if state.ExpiresAt != nil {
			ttl = state.ExpiresAt.Sub(now)
			if ttl <= 0 {
				ttl = 0
			}
		}
		if err := m.updateTokenState(ctx, expected, state, ttl); err != nil {
			return nil, err
		}
		if err := m.syncSessionTTL(ctx, state.Subject(), ttl); err != nil {
			return nil, err
		}
		m.publish(ctx, Event{
			Type:      EventRenewTimeout,
			LoginID:   state.LoginID,
			LoginType: state.LoginType,
			Token:     state.Token,
		})
	}

	return state.Clone(), nil
}

func (m *Manager) IsValid(ctx context.Context, token TokenValue) bool {
	_, err := m.GetTokenState(ctx, token)
	return err == nil
}

func (m *Manager) Renew(ctx context.Context, token TokenValue, ttl time.Duration) error {
	ctx = normalizeContext(ctx)
	state, err := m.getTokenState(ctx, token, false)
	if err != nil {
		return err
	}

	expected := state.Clone()
	now := m.now()
	state.Touch(now)
	if ttl > 0 {
		expiresAt := now.Add(ttl).UTC()
		state.ExpiresAt = &expiresAt
	} else {
		state.ExpiresAt = nil
	}
	if err := m.updateTokenState(ctx, expected, state, ttl); err != nil {
		return err
	}
	if err := m.syncSessionTTL(ctx, state.Subject(), ttl); err != nil {
		return err
	}
	m.publish(ctx, Event{
		Type:      EventRenewTimeout,
		LoginID:   state.LoginID,
		LoginType: state.LoginType,
		Token:     state.Token,
	})
	return nil
}

func (m *Manager) syncSessionTTL(ctx context.Context, subject LoginSubject, ttl time.Duration) error {
	subject = subject.Normalize()
	if m.atomicStore != nil {
		return m.atomicStore.EnsureSession(ctx, subject, ttl)
	}
	session, ok, err := m.store.GetSession(ctx, subject)
	if err != nil {
		return err
	}
	if !ok {
		session = NewSessionForSubject(subject)
	}
	return m.store.SaveSession(ctx, session, ttl)
}

func (m *Manager) Logout(ctx context.Context, token TokenValue) error {
	ctx = normalizeContext(ctx)
	state, err := m.getTokenState(ctx, token, false)
	if err != nil {
		return err
	}
	// state.Token 已是存储键（哈希），直接按它删除，无需再对明文求键。
	if err := m.store.DeleteTokenState(ctx, state.Token); err != nil {
		return err
	}
	if err := m.revokeRefreshForAccessToken(ctx, state); err != nil {
		return err
	}
	m.publish(ctx, Event{
		Type:      EventLogout,
		LoginID:   state.LoginID,
		LoginType: state.LoginType,
		Token:     token,
	})
	return nil
}

func (m *Manager) LogoutByLoginID(ctx context.Context, loginID string, opts ...LogoutOption) error {
	ctx = normalizeContext(ctx)
	loginID = strings.TrimSpace(loginID)
	if loginID == "" {
		return ErrEmptyLoginID
	}

	logoutOpts := logoutOptions{loginType: DefaultLoginType}
	option.Apply(&logoutOpts, opts...)

	subject := LoginSubject{LoginID: loginID, LoginType: logoutOpts.loginType}.Normalize()
	if err := m.store.DeleteTokenStates(ctx, subject, TokenKindAccess); err != nil {
		return err
	}
	if err := m.revokeRefreshForSubject(ctx, subject); err != nil {
		return err
	}
	if logoutOpts.deleteSession {
		if err := m.store.DeleteSession(ctx, subject); err != nil {
			return err
		}
	}
	m.publish(ctx, Event{
		Type:      EventKickOut,
		LoginID:   subject.LoginID,
		LoginType: subject.LoginType,
	})
	return nil
}

// revokeRefreshForAccessToken 删除与给定 access token 关联的 refresh token 状态。
func (m *Manager) revokeRefreshForAccessToken(ctx context.Context, state *TokenState) error {
	if !m.config.Refresh.RevokeRefreshOnLogout || state == nil || state.Token == "" {
		return nil
	}
	states, err := m.store.FindTokenStates(ctx, state.Subject(), TokenKindRefresh)
	if err != nil {
		return err
	}
	for _, rs := range states {
		if rs == nil || rs.Refresh == nil || rs.Refresh.AccessToken != state.Token {
			continue
		}
		if err := m.store.DeleteTokenState(ctx, rs.Token); err != nil {
			return err
		}
	}
	return nil
}

// revokeRefreshForSubject 删除某登录主体下的全部 refresh token 状态。
func (m *Manager) revokeRefreshForSubject(ctx context.Context, subject LoginSubject) error {
	return m.revokeRefreshExcept(ctx, subject, "")
}

func (m *Manager) revokeRefreshExcept(ctx context.Context, subject LoginSubject, keep TokenValue) error {
	if !m.config.Refresh.RevokeRefreshOnLogout {
		return nil
	}
	if keep == "" {
		return m.store.DeleteTokenStates(ctx, subject.Normalize(), TokenKindRefresh)
	}
	states, err := m.store.FindTokenStates(ctx, subject.Normalize(), TokenKindRefresh)
	if err != nil {
		return err
	}
	for _, state := range states {
		if state != nil && state.Token != keep {
			if err := m.store.DeleteTokenState(ctx, state.Token); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) GetSession(ctx context.Context, loginID string, opts ...SessionOption) (*Session, error) {
	ctx = normalizeContext(ctx)
	loginID = strings.TrimSpace(loginID)
	if loginID == "" {
		return nil, ErrEmptySessionID
	}
	subject := sessionSubject(loginID, opts)
	session, ok, err := m.store.GetSession(ctx, subject)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrSessionNotFound
	}
	return session.Clone(), nil
}

func (m *Manager) SaveSession(ctx context.Context, session *Session) error {
	ctx = normalizeContext(ctx)
	if session == nil {
		return ErrEmptySessionID
	}
	session = session.Clone()
	session.Subject = session.Subject.Normalize()
	if session.Subject.IsZero() {
		return ErrEmptySessionID
	}
	if session.Data == nil {
		session.Data = make(map[string]any)
	}
	return m.store.SaveSession(ctx, session, m.config.Timeout)
}

func (m *Manager) DeleteSession(ctx context.Context, loginID string, opts ...SessionOption) error {
	ctx = normalizeContext(ctx)
	loginID = strings.TrimSpace(loginID)
	if loginID == "" {
		return ErrEmptySessionID
	}
	return m.store.DeleteSession(ctx, sessionSubject(loginID, opts))
}

func sessionSubject(loginID string, opts []SessionOption) LoginSubject {
	sessionOpts := sessionOptions{loginType: DefaultLoginType}
	option.Apply(&sessionOpts, opts...)
	return LoginSubject{LoginID: loginID, LoginType: sessionOpts.loginType}.Normalize()
}

func (m *Manager) CheckAuthority(ctx context.Context, authority Authority) error {
	ctx = normalizeContext(ctx)
	auth, err := RequireAuth(ctx)
	if err != nil {
		return err
	}
	authority.Value = strings.TrimSpace(authority.Value)
	if authority.Value == "" {
		return ErrEmptyAuthority
	}
	ok, err := m.authorizer.HasAuthority(ctx, auth.LoginID, authority)
	if err != nil {
		return err
	}
	if !ok {
		return AuthorityDeniedError{Authority: authority}
	}
	return nil
}

func (m *Manager) CheckPermission(ctx context.Context, permission string) error {
	return m.CheckAuthority(ctx, Permission(permission))
}

func (m *Manager) CheckRole(ctx context.Context, role string) error {
	return m.CheckAuthority(ctx, Role(role))
}

func (m *Manager) CheckAll(ctx context.Context, authorities ...Authority) error {
	for _, authority := range authorities {
		if err := m.CheckAuthority(ctx, authority); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) CheckAny(ctx context.Context, authorities ...Authority) error {
	var denied error
	for _, authority := range authorities {
		err := m.CheckAuthority(ctx, authority)
		if err == nil {
			return nil
		}
		if errors.Is(err, ErrAuthorityDenied) {
			denied = err
			continue
		}
		return err
	}
	if denied != nil {
		return denied
	}
	return ErrAuthorityDenied
}

func (m *Manager) now() time.Time {
	return m.runtime.Now().UTC()
}

func (m *Manager) publish(ctx context.Context, event Event) {
	if m.eventBus == nil {
		return
	}
	if event.Time.IsZero() {
		event.Time = m.now()
	}
	m.eventBus.Publish(ctx, event)
}

func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func (m *Manager) updateTokenState(ctx context.Context, expected, next *TokenState, ttl time.Duration) error {
	if m.atomicStore != nil {
		updated, err := m.atomicStore.CompareAndSwapTokenState(ctx, expected, next, ttl)
		if err != nil {
			return err
		}
		if !updated {
			return ErrTokenInvalid
		}
		return nil
	}
	// Preserve compatibility with existing third-party stores. They must opt into
	// AtomicStore to provide atomic read-modify-write guarantees.
	return m.store.SaveTokenState(ctx, next, ttl)
}

func (m *Manager) consumeTokenState(ctx context.Context, token TokenValue, kind TokenKind) (*TokenState, bool, error) {
	if m.atomicStore != nil {
		return m.atomicStore.ConsumeTokenStateOfKind(ctx, token, kind)
	}
	// Legacy stores retain their API. Preflight avoids wrong-kind side effects in
	// sequential calls; only the optional capability closes concurrent rebinding.
	state, found, err := m.store.GetTokenState(ctx, token)
	if err != nil || !found || state == nil {
		return state, found, err
	}
	state.Normalize()
	if state.Kind != kind || !state.IsActive(m.now()) {
		return state, true, nil
	}
	return m.store.ConsumeTokenState(ctx, token)
}

// storeKey 把明文 token 派生为存储键（十六进制 SHA-256）。
//
// token 是高熵不透明凭证，无需加盐即可抵御彩虹表；以哈希作为 Store 的物理键、
// 且不把明文写入持久化值，确保数据库/Redis 被拖库或备份泄露时也无法还原 token。
// 哈希是单向的，不影响按 token 查找：Manager 在每个接收明文 token 的入口处统一
// 派生存储键，Store 始终只见哈希、从不接触明文。
//
// 注意：该转换是本库的存储边界约定——TokenState.Token 在库内部恒为存储键（哈希），
// 明文仅作为局部变量存在，只有在把状态交还外部调用方时才由 withPlaintextToken 注入。
func storeKey(token TokenValue) TokenValue {
	trimmed := strings.TrimSpace(string(token))
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(trimmed))
	var encoded [sha256.Size * 2]byte
	hex.Encode(encoded[:], sum[:])
	return TokenValue(string(encoded[:]))
}

// withPlaintextToken 返回一个把 Token 字段还原为明文的克隆，仅用于把状态交还给
// 外部调用方。
//
// 危险约束：该克隆绝不能再存回 Store —— 一旦以明文 Token 落库，本文件建立的
// “明文永不落库”不变量即被打破。库内部所有会重新保存状态的路径都必须使用
// Token 为哈希的原状态，而非本函数的返回值。
func withPlaintextToken(state *TokenState, plaintext TokenValue) *TokenState {
	if state == nil {
		return nil
	}
	clone := state.Clone()
	clone.Token = TokenValue(strings.TrimSpace(string(plaintext)))
	return clone
}

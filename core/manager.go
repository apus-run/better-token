package core

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/apus-run/better-token/pkg/option"
)

type Manager struct {
	config        Config
	store         Store
	authorizer    Authorizer
	eventBus      EventBus
	runtime       Runtime
	refreshConfig RefreshConfig
	nonceConfig   NonceConfig
}

func NewManager(store Store, opts ...Option) *Manager {
	if store == nil {
		panic("core: nil store")
	}

	m := &Manager{
		config:        DefaultConfig(),
		store:         store,
		authorizer:    NoopAuthorizer{},
		eventBus:      NewEventBus(),
		runtime:       DefaultRuntime(),
		refreshConfig: DefaultRefreshConfig(),
		nonceConfig:   DefaultNonceConfig(),
	}
	option.Apply(m, opts...)
	m.refreshConfig = m.refreshConfig.withDefaults()
	m.nonceConfig = m.nonceConfig.withDefaults()
	return m
}

func (m *Manager) Config() Config {
	return m.config
}

func (m *Manager) Login(ctx context.Context, loginID string, token TokenValue, opts ...LoginOption) (*TokenState, error) {
	loginOpts := loginOptions{loginType: DefaultLoginType}
	option.Apply(&loginOpts, opts...)
	return m.login(ctx, loginID, token, loginOpts, true)
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
	if m.config.ShareToken {
		states, err := m.store.FindTokenStates(ctx, subject, TokenKindAccess)
		if err != nil {
			return nil, err
		}
		for _, state := range states {
			if state != nil && state.IsActive(now) {
				return state.Clone(), nil
			}
		}
	}

	if !m.config.Concurrent {
		if err := m.store.DeleteTokenStates(ctx, subject, TokenKindAccess); err != nil {
			return nil, err
		}
		if err := m.revokeRefreshForSubject(ctx, subject); err != nil {
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
		Token:        token,
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
	return m.getTokenState(ctx, token, true)
}

func (m *Manager) getTokenState(ctx context.Context, token TokenValue, allowAutoRenew bool) (*TokenState, error) {
	ctx = normalizeContext(ctx)
	token = TokenValue(strings.TrimSpace(string(token)))
	if token == "" {
		return nil, ErrEmptyToken
	}

	state, ok, err := m.store.GetTokenState(ctx, token)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrTokenNotFound
	}
	state.Normalize()

	if state.IsRevoked() || state.IsConsumed() {
		return nil, ErrTokenInvalid
	}

	now := m.now()
	if state.IsExpired(now) {
		_ = m.store.DeleteTokenState(ctx, token)
		return nil, ErrTokenNotFound
	}

	if allowAutoRenew && m.config.AutoRenew {
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
		if err := m.store.SaveTokenState(ctx, state, ttl); err != nil {
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

	now := m.now()
	state.Touch(now)
	if ttl > 0 {
		expiresAt := now.Add(ttl).UTC()
		state.ExpiresAt = &expiresAt
	} else {
		state.ExpiresAt = nil
	}
	if err := m.store.SaveTokenState(ctx, state, ttl); err != nil {
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
	if err := m.store.DeleteTokenState(ctx, token); err != nil {
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
	if !m.refreshConfig.RevokeRefreshOnLogout || state == nil || state.Token == "" {
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
	if !m.refreshConfig.RevokeRefreshOnLogout {
		return nil
	}
	return m.store.DeleteTokenStates(ctx, subject.Normalize(), TokenKindRefresh)
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

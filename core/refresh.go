package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/apus-run/better-token/pkg/option"
)

const DefaultRefreshTimeout = 7 * 24 * time.Hour

type RefreshConfig struct {
	Timeout                    time.Duration
	RotateRefreshToken         bool
	RevokeAccessTokenOnRefresh bool
	RevokeRefreshOnLogout      bool
}

func DefaultRefreshConfig() RefreshConfig {
	return RefreshConfig{
		Timeout:                    DefaultRefreshTimeout,
		RotateRefreshToken:         true,
		RevokeAccessTokenOnRefresh: true,
		RevokeRefreshOnLogout:      true,
	}
}

func (c RefreshConfig) withDefaults() RefreshConfig {
	defaults := DefaultRefreshConfig()
	if c.Timeout == 0 {
		c.Timeout = defaults.Timeout
	}
	if !c.RotateRefreshToken && !c.RevokeAccessTokenOnRefresh && !c.RevokeRefreshOnLogout {
		c.RotateRefreshToken = defaults.RotateRefreshToken
		c.RevokeAccessTokenOnRefresh = defaults.RevokeAccessTokenOnRefresh
		c.RevokeRefreshOnLogout = defaults.RevokeRefreshOnLogout
	}
	return c
}

// LoginResult 同时承载 access（Kind=access）与 refresh（Kind=refresh）状态。
type LoginResult struct {
	TokenState   *TokenState `json:"token_state"`
	RefreshState *TokenState `json:"refresh_state,omitempty"`
}

type RefreshFlowOption = option.Option[refreshFlowOptions]

type refreshFlowOptions struct {
	nextRefreshToken TokenValue
	device           string
	metadata         json.RawMessage
}

func WithNextRefreshToken(token TokenValue) RefreshFlowOption {
	return func(opts *refreshFlowOptions) {
		opts.nextRefreshToken = TokenValue(strings.TrimSpace(string(token)))
	}
}

func WithRefreshDevice(device string) RefreshFlowOption {
	return func(opts *refreshFlowOptions) {
		opts.device = strings.TrimSpace(device)
	}
}

func WithRefreshMetadata(metadata json.RawMessage) RefreshFlowOption {
	return func(opts *refreshFlowOptions) {
		opts.metadata = cloneRawMessage(metadata)
	}
}

// LoginWithRefresh 登录并签发关联的 refresh token。
func (m *Manager) LoginWithRefresh(ctx context.Context, loginID string, accessToken, refreshToken TokenValue, opts ...LoginOption) (*LoginResult, error) {
	ctx = normalizeContext(ctx)
	refreshToken = TokenValue(strings.TrimSpace(string(refreshToken)))
	if refreshToken == "" {
		return nil, ErrEmptyRefreshToken
	}

	state, err := m.Login(ctx, loginID, accessToken, opts...)
	if err != nil {
		return nil, err
	}

	refreshState := m.newRefreshState(refreshToken, state)
	if err := m.store.SaveTokenState(ctx, refreshState, m.remainingTTL(m.now(), refreshState)); err != nil {
		_ = m.Logout(ctx, state.Token)
		return nil, err
	}
	m.publish(ctx, Event{
		Type:      EventRefreshIssued,
		LoginID:   state.LoginID,
		LoginType: state.LoginType,
		Token:     state.Token,
	})
	return &LoginResult{TokenState: state.Clone(), RefreshState: refreshState.Clone()}, nil
}

func (m *Manager) newRefreshState(refreshToken TokenValue, access *TokenState) *TokenState {
	now := m.now()
	var expiresAt *time.Time
	if m.config.Refresh.Timeout > 0 {
		exp := now.Add(m.config.Refresh.Timeout).UTC()
		expiresAt = &exp
	}
	return &TokenState{
		Token:        refreshToken,
		Kind:         TokenKindRefresh,
		LoginID:      access.LoginID,
		LoginType:    access.LoginType,
		Device:       access.Device,
		CreatedAt:    now,
		LastActiveAt: now,
		ExpiresAt:    expiresAt,
		Status:       TokenStatusActive,
		Metadata:     cloneRawMessage(access.Metadata),
		Refresh:      &RefreshInfo{AccessToken: access.Token},
	}
}

// Refresh 用合法 refresh token 换新 access token。
func (m *Manager) Refresh(ctx context.Context, refreshToken, nextAccessToken TokenValue, opts ...RefreshFlowOption) (*LoginResult, error) {
	ctx = normalizeContext(ctx)
	refreshToken = TokenValue(strings.TrimSpace(string(refreshToken)))
	nextAccessToken = TokenValue(strings.TrimSpace(string(nextAccessToken)))
	if refreshToken == "" {
		return nil, ErrEmptyRefreshToken
	}
	if nextAccessToken == "" {
		return nil, ErrEmptyToken
	}

	flowOpts := refreshFlowOptions{}
	option.Apply(&flowOpts, opts...)
	if m.config.Refresh.RotateRefreshToken && flowOpts.nextRefreshToken == "" {
		return nil, ErrNextRefreshTokenRequired
	}
	if m.config.Refresh.RotateRefreshToken && flowOpts.nextRefreshToken == refreshToken {
		return nil, ErrNextRefreshTokenReuse
	}

	var (
		state *TokenState
		err   error
	)
	if m.config.Refresh.RotateRefreshToken {
		if state, err = m.loadUsableRefreshToken(ctx, refreshToken); err != nil {
			return nil, err
		}
		if err = m.validateNextAccessToken(state, nextAccessToken); err != nil {
			return nil, err
		}
		state, err = m.consumeUsableRefreshToken(ctx, refreshToken)
	} else {
		state, err = m.loadUsableRefreshToken(ctx, refreshToken)
	}
	if err != nil {
		return nil, err
	}
	if err = m.validateNextAccessToken(state, nextAccessToken); err != nil {
		return nil, err
	}

	if m.config.Refresh.RevokeAccessTokenOnRefresh && state.Refresh != nil && state.Refresh.AccessToken != "" {
		if err := m.Logout(ctx, state.Refresh.AccessToken); err != nil && !errors.Is(err, ErrTokenNotFound) {
			return nil, err
		}
	}

	device := state.Device
	if flowOpts.device != "" {
		device = flowOpts.device
	}
	metadata := cloneRawMessage(state.Metadata)
	if len(flowOpts.metadata) > 0 {
		metadata = cloneRawMessage(flowOpts.metadata)
	}
	tokenState, err := m.login(ctx, state.LoginID, nextAccessToken, loginOptions{
		loginType: state.LoginType,
		device:    device,
		metadata:  metadata,
	}, false)
	if err != nil {
		return nil, err
	}

	var nextRefreshState *TokenState
	now := m.now()
	if m.config.Refresh.RotateRefreshToken {
		nextRefreshState = m.newRefreshState(flowOpts.nextRefreshToken, tokenState)
		nextRefreshState.Refresh.RotatedFrom = refreshToken
		nextRefreshState.Refresh.LastUsedAt = utcTimePtr(now)
	} else {
		if state.Refresh == nil {
			state.Refresh = &RefreshInfo{}
		}
		state.Refresh.AccessToken = tokenState.Token
		state.Refresh.LastUsedAt = utcTimePtr(now)
		state.Device = tokenState.Device
		state.Metadata = cloneRawMessage(tokenState.Metadata)
		state.Touch(now)
		nextRefreshState = state
	}
	if err := m.store.SaveTokenState(ctx, nextRefreshState, m.remainingTTL(now, nextRefreshState)); err != nil {
		_ = m.Logout(ctx, tokenState.Token)
		return nil, err
	}

	m.publish(ctx, Event{
		Type:      EventRefresh,
		LoginID:   tokenState.LoginID,
		LoginType: tokenState.LoginType,
		Token:     tokenState.Token,
	})
	return &LoginResult{TokenState: tokenState.Clone(), RefreshState: nextRefreshState.Clone()}, nil
}

func (m *Manager) validateNextAccessToken(state *TokenState, nextAccessToken TokenValue) error {
	if !m.config.Refresh.RevokeAccessTokenOnRefresh || state == nil || state.Refresh == nil || state.Refresh.AccessToken == "" {
		return nil
	}
	if nextAccessToken == state.Refresh.AccessToken {
		return ErrNextAccessTokenReuse
	}
	return nil
}

func (m *Manager) loadUsableRefreshToken(ctx context.Context, refreshToken TokenValue) (*TokenState, error) {
	state, ok, err := m.store.GetTokenState(ctx, refreshToken)
	if err != nil {
		return nil, err
	}
	if !ok || state == nil {
		return nil, ErrRefreshTokenNotFound
	}
	state.Normalize()
	if state.Kind != TokenKindRefresh {
		return nil, ErrUnsupportedKind
	}
	if state.IsRevoked() || state.IsConsumed() {
		return nil, ErrRefreshTokenRevoked
	}
	if state.IsExpired(m.now()) {
		_ = m.store.DeleteTokenState(ctx, refreshToken)
		return nil, ErrRefreshTokenExpired
	}
	return state, nil
}

func (m *Manager) consumeUsableRefreshToken(ctx context.Context, refreshToken TokenValue) (*TokenState, error) {
	state, ok, err := m.store.ConsumeTokenState(ctx, refreshToken)
	if err != nil {
		return nil, err
	}
	if !ok || state == nil {
		return nil, ErrRefreshTokenNotFound
	}
	state.Normalize()
	if state.Kind != TokenKindRefresh {
		return nil, ErrUnsupportedKind
	}
	if state.IsRevoked() || state.IsConsumed() {
		return nil, ErrRefreshTokenRevoked
	}
	if state.IsExpired(m.now()) {
		return nil, ErrRefreshTokenExpired
	}
	return state, nil
}

// RevokeRefreshToken 撤销单个 refresh token。
func (m *Manager) RevokeRefreshToken(ctx context.Context, refreshToken TokenValue) error {
	ctx = normalizeContext(ctx)
	refreshToken = TokenValue(strings.TrimSpace(string(refreshToken)))
	if refreshToken == "" {
		return ErrEmptyRefreshToken
	}
	state, ok, err := m.store.GetTokenState(ctx, refreshToken)
	if err != nil {
		return err
	}
	if !ok || state == nil {
		return ErrRefreshTokenNotFound
	}
	state.Normalize()
	if state.Kind != TokenKindRefresh {
		return ErrUnsupportedKind
	}
	accessToken := TokenValue("")
	if state.Refresh != nil {
		accessToken = state.Refresh.AccessToken
	}
	state.MarkRevoked(m.now())
	if err := m.store.SaveTokenState(ctx, state, m.remainingTTL(m.now(), state)); err != nil {
		return err
	}
	m.publish(ctx, Event{
		Type:      EventRefreshRevoked,
		LoginID:   state.LoginID,
		LoginType: state.LoginType,
		Token:     accessToken,
	})
	return nil
}

// RevokeRefreshByLoginID 撤销某登录主体下的全部 refresh token。
func (m *Manager) RevokeRefreshByLoginID(ctx context.Context, loginID string, opts ...LogoutOption) error {
	ctx = normalizeContext(ctx)
	loginID = strings.TrimSpace(loginID)
	if loginID == "" {
		return ErrEmptyLoginID
	}
	logoutOpts := logoutOptions{loginType: DefaultLoginType}
	option.Apply(&logoutOpts, opts...)
	subject := LoginSubject{LoginID: loginID, LoginType: logoutOpts.loginType}.Normalize()
	if err := m.store.DeleteTokenStates(ctx, subject, TokenKindRefresh); err != nil {
		return err
	}
	m.publish(ctx, Event{
		Type:      EventRefreshRevoked,
		LoginID:   subject.LoginID,
		LoginType: subject.LoginType,
	})
	return nil
}

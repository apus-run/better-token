package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	if storeKey(accessToken) == storeKey(refreshToken) {
		return nil, ErrNextRefreshTokenReuse
	}

	// 走内部 login 拿到 Token 为存储键（哈希）的 access 状态，以便 refresh 关联链
	// （RefreshInfo.AccessToken）也存哈希键；交还调用方时再注入明文。
	loginOpts := loginOptions{loginType: DefaultLoginType}
	option.Apply(&loginOpts, opts...)
	accessState, err := m.login(ctx, loginID, accessToken, loginOpts, true)
	if err != nil {
		return nil, err
	}

	refreshState := m.newRefreshState(refreshToken, accessState)
	if err := m.store.SaveTokenState(ctx, refreshState, m.remainingTTL(m.now(), refreshState)); err != nil {
		_ = m.store.DeleteTokenState(ctx, accessState.Token) // 回滚 access，Token 已是哈希键
		return nil, err
	}
	m.publish(ctx, Event{
		Type:      EventRefreshIssued,
		LoginID:   accessState.LoginID,
		LoginType: accessState.LoginType,
		Token:     accessState.Token,
	})
	return &LoginResult{
		TokenState:   withPlaintextToken(accessState, accessToken),
		RefreshState: withPlaintextToken(refreshState, refreshToken),
	}, nil
}

func (m *Manager) newRefreshState(refreshToken TokenValue, access *TokenState) *TokenState {
	now := m.now()
	var expiresAt *time.Time
	if m.config.Refresh.Timeout > 0 {
		exp := now.Add(m.config.Refresh.Timeout).UTC()
		expiresAt = &exp
	}
	// access.Token 必须已是存储键（哈希）——所有调用方均经内部 login 构造 access。
	// 于是 refresh 状态的 Token 与其关联的 AccessToken 一致地以哈希落库，无明文。
	return &TokenState{
		Token:        storeKey(refreshToken),
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
func (m *Manager) Refresh(ctx context.Context, refreshToken, nextAccessToken TokenValue, opts ...RefreshFlowOption) (result *LoginResult, err error) {
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

	if nextAccessToken == refreshToken {
		return nil, ErrNextAccessTokenReuse
	}
	if m.config.Refresh.RotateRefreshToken && nextAccessToken == flowOpts.nextRefreshToken {
		return nil, ErrNextRefreshTokenReuse
	}
	state, err := m.loadUsableRefreshToken(ctx, refreshToken)
	if err != nil {
		return nil, err
	}
	if err = m.validateNextAccessToken(state, nextAccessToken, flowOpts.nextRefreshToken); err != nil {
		return nil, err
	}
	// Both modes claim the refresh before touching access/session state. In
	// reusable mode consumed is an in-flight claim, reactivated only by its owner.
	state, err = m.consumeUsableRefreshToken(ctx, refreshToken)
	if err != nil {
		return nil, err
	}
	expected := state.Clone()
	expected.MarkConsumed(m.now())
	restore := !m.config.Refresh.RotateRefreshToken
	if restore {
		original := state.Clone()
		defer func() {
			if !restore {
				return
			}
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if cleanupErr := m.updateTokenState(cleanup, expected, original, m.remainingTTL(m.now(), original)); cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("restore refresh token: %w", cleanupErr))
			}
		}()
	}
	if err = m.validateNextAccessToken(state, nextAccessToken, flowOpts.nextRefreshToken); err != nil {
		return nil, err
	}

	if m.config.Refresh.RevokeAccessTokenOnRefresh && state.Refresh != nil && state.Refresh.AccessToken != "" {
		// AccessToken 已是旧 access 的存储键（哈希），直接按键删除（幂等）并发布登出。
		if err := m.store.DeleteTokenState(ctx, state.Refresh.AccessToken); err != nil {
			return nil, err
		}
		m.publish(ctx, Event{
			Type:      EventLogout,
			LoginID:   state.LoginID,
			LoginType: state.LoginType,
		})
	}

	device := state.Device
	if flowOpts.device != "" {
		device = flowOpts.device
	}
	metadata := cloneRawMessage(state.Metadata)
	if len(flowOpts.metadata) > 0 {
		metadata = cloneRawMessage(flowOpts.metadata)
	}
	keepRefresh := TokenValue("")
	if !m.config.Refresh.RotateRefreshToken {
		keepRefresh = state.Token
	}
	tokenState, err := m.login(ctx, state.LoginID, nextAccessToken, loginOptions{
		preserveRefresh: keepRefresh,
		loginType:       state.LoginType,
		device:          device,
		metadata:        metadata,
	}, false)
	if err != nil {
		return nil, err
	}

	var nextRefreshState *TokenState
	now := m.now()
	if m.config.Refresh.RotateRefreshToken {
		nextRefreshState = m.newRefreshState(flowOpts.nextRefreshToken, tokenState)
		// RotatedFrom 是旧 refresh 的引用，同样以存储键（哈希）记录，避免明文落库。
		nextRefreshState.Refresh.RotatedFrom = storeKey(refreshToken)
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
	if m.config.Refresh.RotateRefreshToken {
		err = m.store.SaveTokenState(ctx, nextRefreshState, m.remainingTTL(now, nextRefreshState))
	} else {
		err = m.updateTokenState(ctx, expected, nextRefreshState, m.remainingTTL(now, nextRefreshState))
	}
	if err != nil {
		_ = m.store.DeleteTokenState(ctx, tokenState.Token) // 回滚新 access，Token 已是哈希键
		return nil, err
	}
	restore = false

	m.publish(ctx, Event{
		Type:      EventRefresh,
		LoginID:   tokenState.LoginID,
		LoginType: tokenState.LoginType,
		Token:     tokenState.Token,
	})
	// 轮换模式返回新签发的 refresh 明文；非轮换沿用调用方传入的 refresh 明文。
	plaintextRefresh := refreshToken
	if m.config.Refresh.RotateRefreshToken {
		plaintextRefresh = flowOpts.nextRefreshToken
	}
	return &LoginResult{
		TokenState:   withPlaintextToken(tokenState, nextAccessToken),
		RefreshState: withPlaintextToken(nextRefreshState, plaintextRefresh),
	}, nil
}

func (m *Manager) validateNextAccessToken(state *TokenState, nextAccessToken, nextRefreshToken TokenValue) error {
	if m.config.Refresh.RotateRefreshToken && state != nil && state.Refresh != nil && storeKey(nextRefreshToken) == state.Refresh.AccessToken {
		return ErrNextRefreshTokenReuse
	}
	if !m.config.Refresh.RevokeAccessTokenOnRefresh || state == nil || state.Refresh == nil || state.Refresh.AccessToken == "" {
		return nil
	}
	// state.Refresh.AccessToken 是旧 access 的存储键（哈希），故与新 access 的键比较。
	if storeKey(nextAccessToken) == state.Refresh.AccessToken {
		return ErrNextAccessTokenReuse
	}
	return nil
}

func (m *Manager) loadUsableRefreshToken(ctx context.Context, refreshToken TokenValue) (*TokenState, error) {
	key := storeKey(refreshToken)
	state, ok, err := m.store.GetTokenState(ctx, key)
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
	if state.Status != TokenStatusActive {
		return nil, ErrRefreshTokenRevoked
	}
	if state.IsExpired(m.now()) {
		_ = m.store.DeleteTokenState(ctx, key)
		return nil, ErrRefreshTokenExpired
	}
	return state, nil
}

func (m *Manager) consumeUsableRefreshToken(ctx context.Context, refreshToken TokenValue) (*TokenState, error) {
	state, ok, err := m.consumeTokenState(ctx, storeKey(refreshToken), TokenKindRefresh)
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
	if state.Status != TokenStatusActive {
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
	state, ok, err := m.store.GetTokenState(ctx, storeKey(refreshToken))
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
	expected := state.Clone()
	state.MarkRevoked(m.now())
	if err := m.updateTokenState(ctx, expected, state, m.remainingTTL(m.now(), state)); err != nil {
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

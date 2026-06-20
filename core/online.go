package core

import (
	"context"
	"strings"
	"time"

	"github.com/apus-run/better-token/pkg/option"
)

type ListTokenOption = option.Option[listTokenOptions]

type listTokenOptions struct {
	loginType string
}

func WithListLoginType(loginType string) ListTokenOption {
	return func(opts *listTokenOptions) {
		if value := strings.TrimSpace(loginType); value != "" {
			opts.loginType = value
		}
	}
}

// ListTokenStates 返回某登录主体下未过期的 access token 状态。
func (m *Manager) ListTokenStates(ctx context.Context, loginID string, opts ...ListTokenOption) ([]*TokenState, error) {
	ctx = normalizeContext(ctx)
	loginID = strings.TrimSpace(loginID)
	if loginID == "" {
		return nil, ErrEmptyLoginID
	}
	listOpts := listTokenOptions{loginType: DefaultLoginType}
	option.Apply(&listOpts, opts...)
	subject := LoginSubject{LoginID: loginID, LoginType: listOpts.loginType}.Normalize()
	states, err := m.store.FindTokenStates(ctx, subject, TokenKindAccess)
	if err != nil {
		return nil, err
	}
	result := make([]*TokenState, 0, len(states))
	now := m.now()
	for _, state := range states {
		if state == nil || state.IsExpired(now) {
			continue
		}
		result = append(result, state.Clone())
	}
	return result, nil
}

// LogoutByDevice 踢下线某登录主体在指定设备上的 access token。
func (m *Manager) LogoutByDevice(ctx context.Context, loginID string, device string, opts ...LogoutOption) error {
	ctx = normalizeContext(ctx)
	loginID = strings.TrimSpace(loginID)
	device = strings.TrimSpace(device)
	if loginID == "" {
		return ErrEmptyLoginID
	}
	if device == "" {
		return ErrEmptyDevice
	}

	logoutOpts := logoutOptions{loginType: DefaultLoginType}
	option.Apply(&logoutOpts, opts...)
	subject := LoginSubject{LoginID: loginID, LoginType: logoutOpts.loginType}.Normalize()
	states, err := m.store.FindTokenStates(ctx, subject, TokenKindAccess)
	if err != nil {
		return err
	}
	deleted := 0
	for _, state := range states {
		if state == nil || strings.TrimSpace(state.Device) != device {
			continue
		}
		if err := m.store.DeleteTokenState(ctx, state.Token); err != nil {
			return err
		}
		if err := m.revokeRefreshForAccessToken(ctx, state); err != nil {
			return err
		}
		deleted++
	}
	if deleted > 0 {
		m.publish(ctx, Event{
			Type:      EventKickOut,
			LoginID:   subject.LoginID,
			LoginType: subject.LoginType,
			Metadata: map[string]any{
				"device": device,
			},
		})
	}
	return nil
}

// MarkOnline 在 access token 的状态上记录上线投影。
func (m *Manager) MarkOnline(ctx context.Context, token TokenValue, info OnlineInfo) error {
	ctx = normalizeContext(ctx)
	state, err := m.getTokenState(ctx, token, false)
	if err != nil {
		return err
	}
	if state.Kind != TokenKindAccess {
		return ErrUnsupportedKind
	}
	now := m.now()
	state.MarkOnline(now, info)
	if err := m.store.SaveTokenState(ctx, state, m.remainingTTL(now, state)); err != nil {
		return err
	}
	m.publish(ctx, Event{
		Type:      EventOnline,
		LoginID:   state.LoginID,
		LoginType: state.LoginType,
		Token:     state.Token,
	})
	return nil
}

// MarkOffline 在 access token 的状态上记录下线投影。
func (m *Manager) MarkOffline(ctx context.Context, token TokenValue) error {
	ctx = normalizeContext(ctx)
	state, err := m.getTokenState(ctx, token, false)
	if err != nil {
		return err
	}
	if state.Kind != TokenKindAccess {
		return ErrUnsupportedKind
	}
	now := m.now()
	state.MarkOffline(now)
	if err := m.store.SaveTokenState(ctx, state, m.remainingTTL(now, state)); err != nil {
		return err
	}
	m.publish(ctx, Event{
		Type:      EventOffline,
		LoginID:   state.LoginID,
		LoginType: state.LoginType,
		Token:     state.Token,
	})
	return nil
}

func (m *Manager) remainingTTL(now time.Time, state *TokenState) time.Duration {
	if state == nil || state.ExpiresAt == nil {
		return 0
	}
	ttl := state.ExpiresAt.Sub(now.UTC())
	if ttl <= 0 {
		return 0
	}
	return ttl
}

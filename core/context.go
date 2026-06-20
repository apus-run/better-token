package core

import (
	"context"
	"encoding/json"
	"time"
)

type authContextKey struct{}

type AuthContext struct {
	Token     TokenValue      `json:"token"`
	Kind      TokenKind       `json:"kind,omitempty"`
	LoginID   string          `json:"login_id"`
	LoginType string          `json:"login_type"`
	Device    string          `json:"device,omitempty"`
	ExpiresAt *time.Time      `json:"expires_at,omitempty"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
}

func NewAuthContext(state *TokenState) *AuthContext {
	if state == nil {
		return nil
	}
	auth := &AuthContext{
		Token:     state.Token,
		Kind:      state.Kind,
		LoginID:   state.LoginID,
		LoginType: state.LoginType,
		Device:    state.Device,
		Metadata:  cloneRawMessage(state.Metadata),
	}
	if state.ExpiresAt != nil {
		expiresAt := state.ExpiresAt.UTC()
		auth.ExpiresAt = &expiresAt
	}
	return auth
}

func WithAuth(ctx context.Context, auth *AuthContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if auth == nil {
		return ctx
	}
	return context.WithValue(ctx, authContextKey{}, auth)
}

func AuthFromContext(ctx context.Context) (*AuthContext, bool) {
	if ctx == nil {
		return nil, false
	}
	auth, ok := ctx.Value(authContextKey{}).(*AuthContext)
	return auth, ok && auth != nil
}

func RequireAuth(ctx context.Context) (*AuthContext, error) {
	auth, ok := AuthFromContext(ctx)
	if !ok || auth.LoginID == "" {
		return nil, ErrNotLogin
	}
	return auth, nil
}

func LoginIDFromContext(ctx context.Context) (string, bool) {
	auth, ok := AuthFromContext(ctx)
	if !ok || auth.LoginID == "" {
		return "", false
	}
	return auth.LoginID, true
}

func RequireLoginID(ctx context.Context) (string, error) {
	loginID, ok := LoginIDFromContext(ctx)
	if !ok {
		return "", ErrNotLogin
	}
	return loginID, nil
}

func TokenFromContext(ctx context.Context) (TokenValue, bool) {
	auth, ok := AuthFromContext(ctx)
	if !ok || auth.Token == "" {
		return "", false
	}
	return auth.Token, true
}

func IsAuthenticated(ctx context.Context) bool {
	_, ok := LoginIDFromContext(ctx)
	return ok
}

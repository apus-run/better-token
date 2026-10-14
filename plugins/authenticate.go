package plugins

import (
	"context"

	"github.com/apus-run/better-token/core"
)

// Authenticate 用 Manager 校验 token 并构造认证上下文，要求 token 的 Kind==access。
func Authenticate(ctx context.Context, m *core.Manager, token core.TokenValue) (*core.AuthContext, error) {
	if m == nil {
		return nil, core.ErrNotLogin
	}
	state, err := m.GetTokenState(ctx, token)
	if err != nil {
		return nil, err
	}
	if state == nil || state.Kind != core.TokenKindAccess {
		return nil, core.ErrTokenInvalid
	}
	return core.NewAuthContext(state), nil
}

package core

import (
	"context"
	"time"
)

// TokenManager 是统一的登录态门面：access / refresh / nonce / online / session / 授权
// 全部通过 *Manager 暴露，不再有独立的 Refresh/Nonce Manager。
type TokenManager interface {
	// access
	Login(ctx context.Context, loginID string, token TokenValue, opts ...LoginOption) (*TokenState, error)
	Logout(ctx context.Context, token TokenValue) error
	LogoutByLoginID(ctx context.Context, loginID string, opts ...LogoutOption) error
	LogoutByDevice(ctx context.Context, loginID, device string, opts ...LogoutOption) error
	GetTokenState(ctx context.Context, token TokenValue) (*TokenState, error)
	IsValid(ctx context.Context, token TokenValue) bool
	Renew(ctx context.Context, token TokenValue, ttl time.Duration) error
	ListTokenStates(ctx context.Context, loginID string, opts ...ListTokenOption) ([]*TokenState, error)

	// refresh
	LoginWithRefresh(ctx context.Context, loginID string, accessToken, refreshToken TokenValue, opts ...LoginOption) (*LoginResult, error)
	Refresh(ctx context.Context, refreshToken, nextAccessToken TokenValue, opts ...RefreshFlowOption) (*LoginResult, error)
	RevokeRefreshToken(ctx context.Context, refreshToken TokenValue) error
	RevokeRefreshByLoginID(ctx context.Context, loginID string, opts ...LogoutOption) error

	// nonce
	GenerateNonce(ctx context.Context, opts ...GenerateNonceOption) (TokenValue, error)
	ConsumeNonce(ctx context.Context, nonce TokenValue) (*TokenState, error)

	// online
	MarkOnline(ctx context.Context, token TokenValue, info OnlineInfo) error
	MarkOffline(ctx context.Context, token TokenValue) error

	// session
	GetSession(ctx context.Context, loginID string, opts ...SessionOption) (*Session, error)
	SaveSession(ctx context.Context, session *Session) error
	DeleteSession(ctx context.Context, loginID string, opts ...SessionOption) error

	// authority
	CheckAuthority(ctx context.Context, authority Authority) error
	CheckPermission(ctx context.Context, permission string) error
	CheckRole(ctx context.Context, role string) error
	CheckAll(ctx context.Context, authorities ...Authority) error
	CheckAny(ctx context.Context, authorities ...Authority) error
}

var _ TokenManager = (*Manager)(nil)

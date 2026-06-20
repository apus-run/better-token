// Package plugins 提供框架无关的认证契约：token 提取规则（TokenLookup）与
// 认证内核（Authenticate），供 http / gin / gRPC 等各框架插件复用，避免重复实现。
package plugins

import (
	"context"
	"strings"

	"github.com/apus-run/better-token/core"
)

// Source 表示一个 token 提取来源。
type Source string

const (
	SourceHeader        Source = "header"
	SourceAuthorization Source = "authorization"
	SourceCookie        Source = "cookie"
	SourceQuery         Source = "query"
)

// DefaultAuthorizationKey 是默认的 Authorization 头名称。
const DefaultAuthorizationKey = "Authorization"

// DefaultOrder 是默认的 token 提取顺序。
var DefaultOrder = []Source{SourceHeader, SourceAuthorization, SourceCookie, SourceQuery}

// Getters 由各框架提供，把“按 key 取字符串”的能力适配给契约，
// 契约本身不依赖任何具体框架类型。任一 getter 为 nil 表示该来源不可用。
type Getters struct {
	Header func(key string) string
	Cookie func(key string) string
	Query  func(key string) string
}

// TokenLookup 描述从请求中提取 token 的规则。
type TokenLookup struct {
	TokenName        string
	TokenPrefix      string
	AuthorizationKey string
	Order            []Source
}

func (l TokenLookup) withDefaults() TokenLookup {
	if l.TokenName == "" {
		l.TokenName = core.DefaultTokenName
	}
	if l.AuthorizationKey == "" {
		l.AuthorizationKey = DefaultAuthorizationKey
	}
	if len(l.Order) == 0 {
		l.Order = DefaultOrder
	}
	return l
}

// Resolve 按配置的顺序从各来源解析 token，返回首个非空且通过前缀校验的 token。
func (l TokenLookup) Resolve(g Getters) (core.TokenValue, bool) {
	l = l.withDefaults()
	for _, source := range l.Order {
		switch source {
		case SourceHeader:
			if g.Header != nil {
				if token, ok := l.normalize(g.Header(l.TokenName)); ok {
					return token, true
				}
			}
		case SourceAuthorization:
			if g.Header != nil {
				if token, ok := l.normalize(g.Header(l.AuthorizationKey)); ok {
					return token, true
				}
			}
		case SourceCookie:
			if g.Cookie != nil {
				if token, ok := l.normalize(g.Cookie(l.TokenName)); ok {
					return token, true
				}
			}
		case SourceQuery:
			if g.Query != nil {
				if token, ok := l.normalize(g.Query(l.TokenName)); ok {
					return token, true
				}
			}
		}
	}
	return "", false
}

func (l TokenLookup) normalize(value string) (core.TokenValue, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	prefix := strings.TrimSpace(l.TokenPrefix)
	if prefix != "" {
		fields := strings.Fields(value)
		if len(fields) == 1 && strings.EqualFold(fields[0], prefix) {
			return "", false
		}
		if len(fields) >= 2 && strings.EqualFold(fields[0], prefix) {
			value = strings.TrimSpace(strings.TrimPrefix(value, fields[0]))
		}
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	return core.TokenValue(value), true
}

// Authenticate 用 Manager 校验 token 并构造认证上下文，要求 token 的 Kind==access。
func Authenticate(ctx context.Context, m *core.Manager, token core.TokenValue) (*core.AuthContext, error) {
	if m == nil {
		return nil, core.ErrNotLogin
	}
	state, err := m.GetTokenState(ctx, token)
	if err != nil {
		return nil, err
	}
	if state.Kind != core.TokenKindAccess {
		return nil, core.ErrTokenInvalid
	}
	return core.NewAuthContext(state), nil
}

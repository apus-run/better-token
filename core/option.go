package core

import (
	"encoding/json"
	"strings"

	"github.com/apus-run/better-token/pkg/option"
)

type Option = option.Option[Manager]

func WithConfig(config Config) Option {
	return func(m *Manager) {
		m.config = config
	}
}

func WithAuthorizer(authorizer Authorizer) Option {
	return func(m *Manager) {
		if authorizer != nil {
			m.authorizer = authorizer
		}
	}
}

func WithEventBus(eventBus EventBus) Option {
	return func(m *Manager) {
		if eventBus != nil {
			m.eventBus = eventBus
		}
	}
}

func WithNonceConfig(config NonceConfig) Option {
	return func(m *Manager) {
		m.config.Nonce = config.withDefaults()
	}
}

func WithRefreshConfig(config RefreshConfig) Option {
	return func(m *Manager) {
		m.config.Refresh = config.withDefaults()
	}
}

func WithRuntime(runtime Runtime) Option {
	return func(m *Manager) {
		runtime.ensureDefaults()
		m.runtime = runtime
	}
}

type LoginOption = option.Option[loginOptions]

type loginOptions struct {
	loginType string
	device    string
	metadata  json.RawMessage
	nonce     TokenValue
}

func WithLoginType(loginType string) LoginOption {
	return func(opts *loginOptions) {
		if value := strings.TrimSpace(loginType); value != "" {
			opts.loginType = value
		}
	}
}

func WithDevice(device string) LoginOption {
	return func(opts *loginOptions) {
		opts.device = strings.TrimSpace(device)
	}
}

func WithMetadata(metadata json.RawMessage) LoginOption {
	return func(opts *loginOptions) {
		opts.metadata = cloneRawMessage(metadata)
	}
}

func WithNonce(nonce TokenValue) LoginOption {
	return func(opts *loginOptions) {
		opts.nonce = TokenValue(strings.TrimSpace(string(nonce)))
	}
}

type LogoutOption = option.Option[logoutOptions]

type logoutOptions struct {
	loginType     string
	deleteSession bool
}

func WithLogoutLoginType(loginType string) LogoutOption {
	return func(opts *logoutOptions) {
		if value := strings.TrimSpace(loginType); value != "" {
			opts.loginType = value
		}
	}
}

func WithDeleteSession(deleteSession bool) LogoutOption {
	return func(opts *logoutOptions) {
		opts.deleteSession = deleteSession
	}
}

type SessionOption = option.Option[sessionOptions]

type sessionOptions struct {
	loginType string
}

// WithSessionLoginType 指定 Session 门面操作所针对的登录类型，
// 不设置时回退到默认登录类型。
func WithSessionLoginType(loginType string) SessionOption {
	return func(opts *sessionOptions) {
		if value := strings.TrimSpace(loginType); value != "" {
			opts.loginType = value
		}
	}
}

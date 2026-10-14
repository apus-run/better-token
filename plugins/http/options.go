package http

import (
	"net/http"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/pkg/option"
)

type Option = option.Option[options]

type options struct {
	tokenName        string
	tokenPrefix      string
	unauthorized     func(http.ResponseWriter, *http.Request)
	authorizationKey string
	allowQuery       bool
	readCookie       bool
	cookieConfig     CookieConfig
}

func WithTokenName(name string) Option {
	return func(opts *options) {
		if name != "" {
			opts.tokenName = name
		}
	}
}

func WithTokenPrefix(prefix string) Option {
	return func(opts *options) {
		opts.tokenPrefix = prefix
	}
}

func WithUnauthorized(handler func(http.ResponseWriter, *http.Request)) Option {
	return func(opts *options) {
		if handler != nil {
			opts.unauthorized = handler
		}
	}
}

// WithQueryToken 显式允许从 URL query 参数提取 token。默认关闭：query 里的 token
// 会泄漏到访问日志/浏览器历史/Referer。仅在确有需要（如无法改 header 的场景）
// 且理解该风险时启用。
func WithQueryToken() Option {
	return func(opts *options) {
		opts.allowQuery = true
	}
}

// WithCookieConfig shares the cookie name and attributes with the writer helpers.
// An explicit config with an empty Name uses the same default as its writer.
// Without this option, cookie extraction continues to follow TokenName.
func WithCookieConfig(config CookieConfig) Option {
	if config.Name == "" {
		config.Name = core.DefaultTokenName
	}
	return func(opts *options) { opts.cookieConfig = config }
}

// WithCookieRead enables or disables cookie credentials. The default is enabled.
func WithCookieRead(enabled bool) Option {
	return func(opts *options) { opts.readCookie = enabled }
}

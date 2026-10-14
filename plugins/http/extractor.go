package http

import (
	"net/http"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/pkg/option"
	"github.com/apus-run/better-token/plugins"
)

type Extractor struct {
	opts   options
	lookup plugins.TokenLookup
}

func NewExtractor(opts ...Option) Extractor {
	extractor := Extractor{
		opts: options{
			tokenName:        core.DefaultTokenName,
			authorizationKey: plugins.DefaultAuthorizationKey,
			readCookie:       true,
			cookieConfig:     DefaultCookieConfig(),
		},
	}
	extractor.opts.cookieConfig.Name = ""
	option.Apply(&extractor.opts, opts...)
	if extractor.opts.tokenName == "" {
		extractor.opts.tokenName = core.DefaultTokenName
	}
	if extractor.opts.authorizationKey == "" {
		extractor.opts.authorizationKey = plugins.DefaultAuthorizationKey
	}
	if extractor.opts.cookieConfig.Name == "" {
		extractor.opts.cookieConfig.Name = extractor.opts.tokenName
	}
	extractor.lookup = plugins.TokenLookup{
		TokenName:        extractor.opts.tokenName,
		TokenPrefix:      extractor.opts.tokenPrefix,
		AuthorizationKey: extractor.opts.authorizationKey,
	}
	if extractor.opts.allowQuery {
		// 显式 opt-in：在安全默认顺序之后追加 query 来源。
		extractor.lookup.Order = append(append([]plugins.Source{}, plugins.DefaultOrder...), plugins.SourceQuery)
	}
	return extractor
}

// OptionsFromConfig 把 core.Config 中与 token 提取相关的字段翻译成 Option，
// 置于用户 opts 之前以便被覆盖。供中间件在装配处把领域配置映射到提取器。
func OptionsFromConfig(config core.Config, opts ...Option) []Option {
	return append([]Option{
		WithTokenName(config.TokenName),
		WithTokenPrefix(config.TokenPrefix),
	}, opts...)
}

func (e Extractor) ExtractToken(r *http.Request) (core.TokenValue, bool) {
	if r == nil {
		return "", false
	}
	var cookie func(string) string
	if e.opts.readCookie {
		cookie = func(string) string {
			if value, err := r.Cookie(e.opts.cookieConfig.Name); err == nil {
				return value.Value
			}
			return ""
		}
	}
	return e.lookup.Resolve(plugins.Getters{Header: r.Header.Get, Cookie: cookie, Query: r.URL.Query().Get})
}

func (e Extractor) Unauthorized() func(http.ResponseWriter, *http.Request) {
	if e.opts.unauthorized != nil {
		return e.opts.unauthorized
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}
}

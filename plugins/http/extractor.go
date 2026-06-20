package http

import (
	"net/http"
	"strings"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/pkg/option"
)

type Extractor struct {
	opts options
}

func NewExtractor(opts ...Option) Extractor {
	extractor := Extractor{
		opts: options{
			tokenName:        core.DefaultTokenName,
			authorizationKey: "Authorization",
		},
	}
	option.Apply(&extractor.opts, opts...)
	if extractor.opts.tokenName == "" {
		extractor.opts.tokenName = core.DefaultTokenName
	}
	if extractor.opts.authorizationKey == "" {
		extractor.opts.authorizationKey = "Authorization"
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

	if token, ok := e.normalize(r.Header.Get(e.opts.tokenName)); ok {
		return token, true
	}
	if token, ok := e.normalize(r.Header.Get(e.opts.authorizationKey)); ok {
		return token, true
	}
	if cookie, err := r.Cookie(e.opts.tokenName); err == nil {
		if token, ok := e.normalize(cookie.Value); ok {
			return token, true
		}
	}
	if token, ok := e.normalize(r.URL.Query().Get(e.opts.tokenName)); ok {
		return token, true
	}

	return "", false
}

func (e Extractor) Unauthorized() func(http.ResponseWriter, *http.Request) {
	if e.opts.unauthorized != nil {
		return e.opts.unauthorized
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}
}

func (e Extractor) normalize(value string) (core.TokenValue, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}

	prefix := strings.TrimSpace(e.opts.tokenPrefix)
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

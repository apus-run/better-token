package http

import (
	"net/http"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/plugins"
)

func Middleware(manager *core.Manager, opts ...Option) func(http.Handler) http.Handler {
	config := core.DefaultConfig()
	if manager != nil {
		config = manager.Config()
	}
	extractor := NewExtractor(OptionsFromConfig(config, opts...)...)
	unauthorized := extractor.Unauthorized()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if manager == nil {
				unauthorized(w, r)
				return
			}
			token, ok := extractor.ExtractToken(r)
			if !ok {
				unauthorized(w, r)
				return
			}
			auth, err := plugins.Authenticate(r.Context(), manager, token)
			if err != nil {
				unauthorized(w, r)
				return
			}
			ctx := core.WithAuth(r.Context(), auth)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

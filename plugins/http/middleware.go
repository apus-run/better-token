package http

import (
	"net/http"

	"github.com/apus-run/better-token/core"
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
			state, err := manager.GetTokenState(r.Context(), token)
			if err != nil {
				unauthorized(w, r)
				return
			}
			ctx := core.WithAuth(r.Context(), core.NewAuthContext(state))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

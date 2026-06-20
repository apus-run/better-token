package http

import (
	"net/http"

	"github.com/apus-run/better-token/pkg/option"
)

type Option = option.Option[options]

type options struct {
	tokenName        string
	tokenPrefix      string
	unauthorized     func(http.ResponseWriter, *http.Request)
	authorizationKey string
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

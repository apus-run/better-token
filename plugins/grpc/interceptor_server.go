// Package grpc 提供 better-token 的 gRPC 认证拦截器：server 端校验 metadata 中的
// token，client 端把 token 注入 outgoing metadata。复用 plugins 的提取契约。
package grpc

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/plugins"
)

// DefaultMetadataKey 是默认的 token metadata 键。
const DefaultMetadataKey = "authorization"

type options struct {
	metadataKey string
	lookup      plugins.TokenLookup
}

// Option 定制 server 端拦截器。
type Option func(*options)

// WithMetadataKey 覆盖默认 token metadata 键。
func WithMetadataKey(key string) Option {
	return func(o *options) {
		if key = strings.TrimSpace(key); key != "" {
			o.metadataKey = strings.ToLower(key)
		}
	}
}

// WithTokenLookup 覆盖默认提取规则（用于处理前缀等）。
func WithTokenLookup(lookup plugins.TokenLookup) Option {
	return func(o *options) {
		o.lookup = lookup
	}
}

func newOptions(opts ...Option) options {
	o := options{metadataKey: DefaultMetadataKey}
	for _, opt := range opts {
		opt(&o)
	}
	if o.metadataKey == "" {
		o.metadataKey = DefaultMetadataKey
	}
	o.lookup.AuthorizationKey = o.metadataKey
	o.lookup.TokenName = o.metadataKey
	o.lookup.Order = []plugins.Source{plugins.SourceHeader}
	return o
}

func (o options) authenticate(ctx context.Context, m *core.Manager) (context.Context, error) {
	if m == nil {
		return nil, status.Error(codes.Unauthenticated, core.ErrNotLogin.Error())
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing metadata")
	}
	token, ok := o.lookup.Resolve(plugins.Getters{
		Header: func(key string) string {
			values := md.Get(strings.ToLower(key))
			if len(values) == 0 {
				return ""
			}
			return values[0]
		},
	})
	if !ok {
		return nil, status.Error(codes.Unauthenticated, core.ErrNotLogin.Error())
	}
	auth, err := plugins.Authenticate(ctx, m, token)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, err.Error())
	}
	return core.WithAuth(ctx, auth), nil
}

// UnaryServerInterceptor 返回校验 token 并注入 AuthContext 的一元拦截器。
func UnaryServerInterceptor(m *core.Manager, opts ...Option) grpc.UnaryServerInterceptor {
	o := newOptions(opts...)
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		authCtx, err := o.authenticate(ctx, m)
		if err != nil {
			return nil, err
		}
		return handler(authCtx, req)
	}
}

// StreamServerInterceptor 返回校验 token 并注入 AuthContext 的流式拦截器。
func StreamServerInterceptor(m *core.Manager, opts ...Option) grpc.StreamServerInterceptor {
	o := newOptions(opts...)
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		authCtx, err := o.authenticate(ss.Context(), m)
		if err != nil {
			return err
		}
		return handler(srv, &wrappedStream{ServerStream: ss, ctx: authCtx})
	}
}

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context { return w.ctx }

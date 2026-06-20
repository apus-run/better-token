package grpc

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/apus-run/better-token/core"
)

// TokenSource 从 context 提供要注入的 token。默认实现走 core.TokenFromContext。
type TokenSource func(ctx context.Context) (core.TokenValue, bool)

type clientOptions struct {
	metadataKey string
	tokenPrefix string
	source      TokenSource
}

// ClientOption 定制 client 端拦截器。
type ClientOption func(*clientOptions)

// WithClientMetadataKey 覆盖默认 token metadata 键。
func WithClientMetadataKey(key string) ClientOption {
	return func(o *clientOptions) {
		if key = strings.TrimSpace(key); key != "" {
			o.metadataKey = strings.ToLower(key)
		}
	}
}

// WithClientTokenPrefix 设置注入时的 token 前缀（如 "Bearer"）。
func WithClientTokenPrefix(prefix string) ClientOption {
	return func(o *clientOptions) {
		o.tokenPrefix = strings.TrimSpace(prefix)
	}
}

// WithTokenSource 覆盖默认 token 来源。
func WithTokenSource(source TokenSource) ClientOption {
	return func(o *clientOptions) {
		if source != nil {
			o.source = source
		}
	}
}

func newClientOptions(opts ...ClientOption) clientOptions {
	o := clientOptions{
		metadataKey: DefaultMetadataKey,
		source:      func(ctx context.Context) (core.TokenValue, bool) { return core.TokenFromContext(ctx) },
	}
	for _, opt := range opts {
		opt(&o)
	}
	if o.metadataKey == "" {
		o.metadataKey = DefaultMetadataKey
	}
	return o
}

func (o clientOptions) inject(ctx context.Context) context.Context {
	token, ok := o.source(ctx)
	if !ok || token == "" {
		return ctx // 无 token 时透传，鉴权交由服务端
	}
	value := string(token)
	if o.tokenPrefix != "" {
		value = o.tokenPrefix + " " + value
	}
	return metadata.AppendToOutgoingContext(ctx, o.metadataKey, value)
}

// UnaryClientInterceptor 返回把 token 注入 outgoing metadata 的一元客户端拦截器。
func UnaryClientInterceptor(opts ...ClientOption) grpc.UnaryClientInterceptor {
	o := newClientOptions(opts...)
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, callOpts ...grpc.CallOption) error {
		return invoker(o.inject(ctx), method, req, reply, cc, callOpts...)
	}
}

// StreamClientInterceptor 返回把 token 注入 outgoing metadata 的流式客户端拦截器。
func StreamClientInterceptor(opts ...ClientOption) grpc.StreamClientInterceptor {
	o := newClientOptions(opts...)
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, callOpts ...grpc.CallOption) (grpc.ClientStream, error) {
		return streamer(o.inject(ctx), desc, cc, method, callOpts...)
	}
}

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/apus-run/better-token/pkg/option"
	random "github.com/apus-run/better-token/pkg/rand"
)

const (
	DefaultNonceTimeout = 5 * time.Minute
	DefaultNonceLength  = 32
)

type NonceConfig struct {
	Timeout time.Duration
	Length  int
}

func DefaultNonceConfig() NonceConfig {
	return NonceConfig{
		Timeout: DefaultNonceTimeout,
		Length:  DefaultNonceLength,
	}
}

func (c NonceConfig) withDefaults() NonceConfig {
	if c.Timeout <= 0 {
		c.Timeout = DefaultNonceTimeout
	}
	if c.Length <= 0 {
		c.Length = DefaultNonceLength
	}
	return c
}

type GenerateNonceOption = option.Option[generateNonceOptions]

type generateNonceOptions struct {
	subject  LoginSubject
	purpose  string
	metadata json.RawMessage
}

func WithNonceSubject(subject LoginSubject) GenerateNonceOption {
	return func(opts *generateNonceOptions) {
		opts.subject = subject.Normalize()
	}
}

func WithNoncePurpose(purpose string) GenerateNonceOption {
	return func(opts *generateNonceOptions) {
		opts.purpose = strings.TrimSpace(purpose)
	}
}

func WithNonceMetadata(metadata json.RawMessage) GenerateNonceOption {
	return func(opts *generateNonceOptions) {
		opts.metadata = cloneRawMessage(metadata)
	}
}

// GenerateNonce 生成一个一次性 nonce，以 Kind==nonce 的 TokenState 承载。
func (m *Manager) GenerateNonce(ctx context.Context, opts ...GenerateNonceOption) (TokenValue, error) {
	ctx = normalizeContext(ctx)
	if err := ctxErr(ctx); err != nil {
		return "", err
	}
	nonce, err := random.RandomString(m.config.Nonce.Length)
	if err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	if nonce == "" {
		return "", ErrEmptyNonce
	}

	generateOpts := generateNonceOptions{}
	option.Apply(&generateOpts, opts...)
	now := m.now()
	expiresAt := now.Add(m.config.Nonce.Timeout).UTC()
	subject := generateOpts.subject
	state := &TokenState{
		Token:        TokenValue(nonce),
		Kind:         TokenKindNonce,
		LoginID:      subject.LoginID,
		LoginType:    subject.LoginType,
		CreatedAt:    now,
		LastActiveAt: now,
		ExpiresAt:    &expiresAt,
		Status:       TokenStatusActive,
		Metadata:     cloneRawMessage(generateOpts.metadata),
		Nonce:        &NonceInfo{Purpose: generateOpts.purpose},
	}
	if err := m.store.SaveTokenState(ctx, state, m.config.Nonce.Timeout); err != nil {
		return "", err
	}
	return state.Token, nil
}

// ConsumeNonce 原子地消费一次性 nonce，重放/过期返回明确错误，返回消费后的 TokenState。
func (m *Manager) ConsumeNonce(ctx context.Context, nonce TokenValue) (*TokenState, error) {
	ctx = normalizeContext(ctx)
	nonce = TokenValue(strings.TrimSpace(string(nonce)))
	if nonce == "" {
		return nil, ErrEmptyNonce
	}
	state, ok, err := m.store.ConsumeTokenState(ctx, nonce)
	if err != nil {
		return nil, err
	}
	if !ok || state == nil {
		return nil, ErrNonceNotFound
	}
	state.Normalize()
	if state.Kind != TokenKindNonce {
		return nil, ErrUnsupportedKind
	}
	if state.IsConsumed() {
		return nil, ErrNonceReplayed
	}
	if state.IsExpired(m.now()) {
		return nil, ErrNonceExpired
	}
	state.MarkConsumed(m.now())
	m.publish(ctx, Event{
		Type:      EventNonceConsumed,
		LoginID:   state.LoginID,
		LoginType: state.LoginType,
		Token:     state.Token,
	})
	return state.Clone(), nil
}

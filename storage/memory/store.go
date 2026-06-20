package memory

import (
	"context"
	"sync"
	"time"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/pkg/option"
)

var _ core.Store = (*Store)(nil)

type Store struct {
	mu       sync.RWMutex
	tokens   map[core.TokenValue]*tokenItem
	indexes  map[core.LoginSubject]map[core.TokenValue]struct{}
	sessions map[core.LoginSubject]*sessionItem
	now      core.NowFunc
}

type tokenItem struct {
	state     *core.TokenState
	expiresAt *time.Time
}

type sessionItem struct {
	session   *core.Session
	expiresAt *time.Time
}

// Option 定制内存 Store 的构造行为。
type Option = option.Option[Store]

// WithRuntime 注入运行时（主要是自定义时钟），用于测试中冻结时间。
// runtime.Now 为 nil 时保持默认时钟不变。
func WithRuntime(runtime core.Runtime) Option {
	return func(s *Store) {
		if runtime.Now != nil {
			s.now = runtime.Now
		}
	}
}

func NewStore(opts ...Option) *Store {
	s := &Store{
		tokens:   make(map[core.TokenValue]*tokenItem),
		indexes:  make(map[core.LoginSubject]map[core.TokenValue]struct{}),
		sessions: make(map[core.LoginSubject]*sessionItem),
		now:      core.DefaultRuntime().Now,
	}
	option.Apply(s, opts...)
	return s
}

func (s *Store) SaveTokenState(ctx context.Context, state *core.TokenState, ttl time.Duration) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if state == nil {
		return core.ErrEmptyToken
	}
	clone := state.Clone()
	if clone.Token == "" {
		return core.ErrEmptyToken
	}
	if clone.LoginID == "" {
		return core.ErrEmptyLoginID
	}
	if clone.LoginType == "" {
		clone.LoginType = core.DefaultLoginType
	}

	expiresAt := expirationFromState(s.now(), clone, ttl)

	s.mu.Lock()
	defer s.mu.Unlock()

	if old, ok := s.tokens[clone.Token]; ok && old.state != nil {
		s.removeIndexLocked(old.state.Subject(), clone.Token)
	}

	s.tokens[clone.Token] = &tokenItem{
		state:     clone,
		expiresAt: expiresAt,
	}
	s.addIndexLocked(clone.Subject(), clone.Token)
	return nil
}

func (s *Store) GetTokenState(ctx context.Context, token core.TokenValue) (*core.TokenState, bool, error) {
	if err := contextErr(ctx); err != nil {
		return nil, false, err
	}

	now := s.now()
	s.mu.RLock()
	item, ok := s.tokens[token]
	if !ok {
		s.mu.RUnlock()
		return nil, false, nil
	}
	if !item.isExpired(now) {
		state := item.state.Clone()
		s.mu.RUnlock()
		return state, true, nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok = s.tokens[token]
	if !ok {
		return nil, false, nil
	}
	if item.isExpired(now) {
		s.deleteTokenLocked(token)
		return nil, false, nil
	}
	return item.state.Clone(), true, nil
}

func (s *Store) DeleteTokenState(ctx context.Context, token core.TokenValue) error {
	if err := contextErr(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteTokenLocked(token)
	return nil
}

func (s *Store) FindTokenStates(ctx context.Context, subject core.LoginSubject) ([]*core.TokenState, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	key := subject.Normalize()

	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()

	tokens := s.indexes[key]
	result := make([]*core.TokenState, 0, len(tokens))
	for token := range tokens {
		item, ok := s.tokens[token]
		if !ok {
			delete(tokens, token)
			continue
		}
		if item.isExpired(now) {
			s.deleteTokenLocked(token)
			continue
		}
		result = append(result, item.state.Clone())
	}
	if len(tokens) == 0 {
		delete(s.indexes, key)
	}
	return result, nil
}

func (s *Store) DeleteTokenStates(ctx context.Context, subject core.LoginSubject) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	key := subject.Normalize()

	s.mu.Lock()
	defer s.mu.Unlock()

	for token := range s.indexes[key] {
		delete(s.tokens, token)
	}
	delete(s.indexes, key)
	return nil
}

func (s *Store) SaveSession(ctx context.Context, session *core.Session, ttl time.Duration) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if session == nil {
		return core.ErrEmptySessionID
	}
	key := session.Subject.Normalize()
	if key.IsZero() {
		return core.ErrEmptySessionID
	}

	var expiresAt *time.Time
	if ttl > 0 {
		exp := s.now().Add(ttl).UTC()
		expiresAt = &exp
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[key] = &sessionItem{
		session:   session.Clone(),
		expiresAt: expiresAt,
	}
	return nil
}

func (s *Store) GetSession(ctx context.Context, subject core.LoginSubject) (*core.Session, bool, error) {
	if err := contextErr(ctx); err != nil {
		return nil, false, err
	}
	key := subject.Normalize()

	now := s.now()
	s.mu.RLock()
	item, ok := s.sessions[key]
	if !ok {
		s.mu.RUnlock()
		return nil, false, nil
	}
	if !item.isExpired(now) {
		session := item.session.Clone()
		s.mu.RUnlock()
		return session, true, nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok = s.sessions[key]
	if !ok {
		return nil, false, nil
	}
	if item.isExpired(now) {
		delete(s.sessions, key)
		return nil, false, nil
	}
	return item.session.Clone(), true, nil
}

func (s *Store) DeleteSession(ctx context.Context, subject core.LoginSubject) error {
	if err := contextErr(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, subject.Normalize())
	return nil
}

func (s *Store) addIndexLocked(subject core.LoginSubject, token core.TokenValue) {
	key := subject.Normalize()
	if s.indexes[key] == nil {
		s.indexes[key] = make(map[core.TokenValue]struct{})
	}
	s.indexes[key][token] = struct{}{}
}

func (s *Store) removeIndexLocked(subject core.LoginSubject, token core.TokenValue) {
	key := subject.Normalize()
	tokens := s.indexes[key]
	if tokens == nil {
		return
	}
	delete(tokens, token)
	if len(tokens) == 0 {
		delete(s.indexes, key)
	}
}

func (s *Store) deleteTokenLocked(token core.TokenValue) {
	item, ok := s.tokens[token]
	if !ok {
		return
	}
	if item.state != nil {
		s.removeIndexLocked(item.state.Subject(), token)
	}
	delete(s.tokens, token)
}

func (i *tokenItem) isExpired(now time.Time) bool {
	if i == nil {
		return true
	}
	if i.expiresAt != nil && !now.Before(*i.expiresAt) {
		return true
	}
	return i.state != nil && i.state.IsExpired(now)
}

func (i *sessionItem) isExpired(now time.Time) bool {
	return i == nil || (i.expiresAt != nil && !now.Before(*i.expiresAt))
}

func expirationFromState(now time.Time, state *core.TokenState, ttl time.Duration) *time.Time {
	if state == nil {
		return nil
	}
	if ttl > 0 {
		exp := now.Add(ttl).UTC()
		return &exp
	}
	if state.ExpiresAt != nil {
		exp := state.ExpiresAt.UTC()
		return &exp
	}
	return nil
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

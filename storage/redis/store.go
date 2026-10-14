// Package redis 基于 github.com/apus-run/gala/components/rdb 实现 core.Store。
//
// key 布局（prefix 默认 "bt"）：
//
//	{prefix}:token:{token}                   -> TokenState JSON（带 TTL，含 kind/status）
//	{prefix}:v2:index:{encoded_type}:{encoded_id}   -> token Set（登录主体 -> 所有 kind 的 token 集合）
//	{prefix}:v2:session:{encoded_type}:{encoded_id} -> Session JSON（带 TTL）
//
// access / refresh / nonce 统一以 TokenState 表达，通过 kind 区分，不再有独立命名空间。
// 索引（Set）是本实现的内部细节：Manager 只通过 LoginSubject 表达语义，
// 索引的维护、过期成员的清理都由本包负责。
package redis

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"time"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/pkg/option"
	"github.com/apus-run/gala/components/rdb"
)

var (
	_ core.Store       = (*Store)(nil)
	_ core.AtomicStore = (*Store)(nil)
)

// DefaultKeyPrefix 是所有 key 的默认前缀，避免与共享同一 Redis 的其它应用冲突。
const DefaultKeyPrefix = "bt"

// Store 是 core.Store 的 Redis 实现，依赖 rdb.Provider 提供 redis 客户端。
type Store struct {
	rdb    rdb.Provider
	prefix string
	now    core.NowFunc
}

// Option 定制 Redis Store 的构造行为。
type Option = option.Option[Store]

// WithKeyPrefix 覆盖默认 key 前缀。空字符串将被忽略，保持默认前缀。
func WithKeyPrefix(prefix string) Option {
	return func(s *Store) {
		if prefix != "" {
			s.prefix = prefix
		}
	}
}

// WithRuntime 注入运行时（主要是自定义时钟），用于测试中冻结时间。
// runtime.Now 为 nil 时保持默认时钟不变。
func WithRuntime(runtime core.Runtime) Option {
	return func(s *Store) {
		if runtime.Now != nil {
			s.now = runtime.Now
		}
	}
}

// NewStore 基于 rdb.Provider 构造 Redis Store。provider 为 nil 时返回 nil。
func NewStore(provider rdb.Provider, opts ...Option) *Store {
	if provider == nil {
		return nil
	}
	s := &Store{
		rdb:    provider,
		prefix: DefaultKeyPrefix,
		now:    core.DefaultRuntime().Now,
	}
	option.Apply(s, opts...)
	return s
}

func (s *Store) SaveTokenState(ctx context.Context, state *core.TokenState, ttl time.Duration) error {
	for attempt := 0; attempt < 32; attempt++ {
		err := s.saveTokenState(ctx, nil, state, ttl)
		if err != core.ErrTokenInvalid {
			return err
		}
	}
	return core.ErrTokenInvalid
}

func (s *Store) CompareAndSwapTokenState(ctx context.Context, expected, next *core.TokenState, ttl time.Duration) (bool, error) {
	if expected == nil || next == nil || expected.Token != next.Token {
		return false, core.ErrTokenInvalid
	}
	err := s.saveTokenState(ctx, expected, next, ttl)
	if err == core.ErrTokenInvalid {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) saveTokenState(ctx context.Context, expected, state *core.TokenState, ttl time.Duration) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if state == nil || state.Token == "" {
		return core.ErrEmptyToken
	}
	clone := state.Clone()
	clone.Normalize()
	if clone.LoginID == "" && clone.Kind != core.TokenKindNonce {
		return core.ErrEmptyLoginID
	}
	data, err := json.Marshal(clone)
	if err != nil {
		return err
	}
	old, raw, marker, err := s.readToken(ctx, clone.Token)
	if err != nil {
		return err
	}
	if expected != nil {
		if old == nil || old.IsExpired(s.now()) || !reflect.DeepEqual(old, expected) {
			return core.ErrTokenInvalid
		}
	}
	oldSubject := clone.Subject()
	oldIndex := s.indexKey(oldSubject)
	if old != nil {
		oldSubject = old.Subject()
		oldIndex = s.indexKey(oldSubject)
	}
	expiration := redisTTL(s.now(), clone, ttl)
	millis := expiration.Milliseconds()
	if expiration > 0 && millis < 1 {
		millis = 1
	}
	result, err := s.rdb.DB(ctx).Eval(ctx, saveScript,
		[]string{s.tokenKey(clone.Token), s.consumedMarkerKey(clone.Token), oldIndex, s.indexKey(clone.Subject()), s.legacySubjectKey("index", oldSubject)},
		raw, marker, string(data), millis, string(clone.Token)).Int()
	if err != nil {
		return err
	}
	if result != 1 {
		return core.ErrTokenInvalid
	}
	return nil
}

// Read the state and legacy consumption marker in one Redis command. Go owns JSON
// encoding, preserving empty objects and integer precision during transitions.
func (s *Store) readToken(ctx context.Context, token core.TokenValue) (*core.TokenState, string, string, error) {
	values, err := s.rdb.DB(ctx).MGet(ctx, s.tokenKey(token), s.consumedMarkerKey(token)).Result()
	if err != nil {
		return nil, "", "", err
	}
	return decodeToken(values[0], values[1])
}

func decodeToken(value, consumed any) (*core.TokenState, string, string, error) {
	raw, _ := value.(string)
	marker, _ := consumed.(string)
	if raw == "" {
		return nil, raw, marker, nil
	}
	state := new(core.TokenState)
	if err := json.Unmarshal([]byte(raw), state); err != nil {
		return nil, raw, marker, err
	}
	state.Normalize()
	if marker != "" && state.Status == core.TokenStatusActive {
		state.Status = core.TokenStatusConsumed
	}
	return state, raw, marker, nil
}

func (s *Store) GetTokenState(ctx context.Context, token core.TokenValue) (*core.TokenState, bool, error) {
	if err := contextErr(ctx); err != nil {
		return nil, false, err
	}
	state, raw, marker, err := s.readToken(ctx, token)
	if err != nil || state == nil {
		return nil, false, err
	}
	if state.IsExpired(s.now()) {
		_, err = s.deleteSnapshot(ctx, token, state.Subject(), raw, marker)
		return nil, false, err
	}
	return state, true, nil
}

func (s *Store) ConsumeTokenState(ctx context.Context, token core.TokenValue) (*core.TokenState, bool, error) {
	return s.consumeTokenState(ctx, token, "")
}

func (s *Store) ConsumeTokenStateOfKind(ctx context.Context, token core.TokenValue, kind core.TokenKind) (*core.TokenState, bool, error) {
	if kind == "" {
		return nil, false, core.ErrUnsupportedKind
	}
	return s.consumeTokenState(ctx, token, kind)
}

func (s *Store) consumeTokenState(ctx context.Context, token core.TokenValue, kind core.TokenKind) (*core.TokenState, bool, error) {
	if err := contextErr(ctx); err != nil {
		return nil, false, err
	}
	for attempt := 0; attempt < 32; attempt++ {
		state, raw, marker, err := s.readToken(ctx, token)
		if err != nil || state == nil {
			return nil, false, err
		}
		if state.IsExpired(s.now()) {
			return nil, false, nil
		}
		if !state.IsActive(s.now()) || (kind != "" && state.Kind != kind) {
			return state, true, nil
		}
		consumed := state.Clone()
		consumed.MarkConsumed(s.now())
		data, err := json.Marshal(consumed)
		if err != nil {
			return nil, false, err
		}
		result, err := s.rdb.DB(ctx).Eval(ctx, consumeScript, []string{s.tokenKey(token), s.consumedMarkerKey(token)}, raw, marker, string(data)).Int()
		if err != nil {
			return nil, false, err
		}
		if result == 1 {
			return state, true, nil
		}
	}
	return nil, false, core.ErrTokenInvalid
}

func (s *Store) DeleteTokenState(ctx context.Context, token core.TokenValue) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	state, raw, marker, err := s.readToken(ctx, token)
	if err != nil || state == nil {
		return err
	}
	deleted, err := s.deleteSnapshot(ctx, token, state.Subject(), raw, marker)
	if err != nil {
		return err
	}
	if !deleted {
		return core.ErrTokenInvalid
	}
	return nil
}

func (s *Store) FindTokenStates(ctx context.Context, subject core.LoginSubject, kinds ...core.TokenKind) ([]*core.TokenState, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	subject = subject.Normalize()
	cli := s.rdb.DB(ctx)
	indexes := []string{s.indexKey(subject), s.legacySubjectKey("index", subject)}
	seen := make(map[string]bool)
	result := make([]*core.TokenState, 0)
	for _, index := range indexes {
		members, err := cli.SMembers(ctx, index).Result()
		if err != nil {
			return nil, err
		}
		if len(members) == 0 {
			continue
		}
		keys := make([]string, 0, len(members)*2)
		for _, member := range members {
			keys = append(keys, s.tokenKey(core.TokenValue(member)), s.consumedMarkerKey(core.TokenValue(member)))
		}
		values, err := cli.MGet(ctx, keys...).Result()
		if err != nil {
			return nil, err
		}
		for i, member := range members {
			if seen[member] {
				continue
			}
			seen[member] = true
			state, raw, marker, err := decodeToken(values[2*i], values[2*i+1])
			if err != nil {
				return nil, err
			}
			if state == nil {
				// Do not remove a member added again after this read.
				if err = cli.Eval(ctx, pruneIndexScript, []string{s.tokenKey(core.TokenValue(member)), index}, member).Err(); err != nil {
					return nil, err
				}
				continue
			}
			if state.IsExpired(s.now()) {
				if _, err = s.deleteSnapshot(ctx, core.TokenValue(member), state.Subject(), raw, marker); err != nil {
					return nil, err
				}
				continue
			}
			// Legacy delimiter indexes can contain several subjects. Membership is only
			// a lookup hint; ownership comes from the stored state.
			if state.Subject().Normalize() != subject || !core.MatchKind(state.Kind, kinds...) {
				continue
			}
			result = append(result, state)
		}
	}
	return result, nil
}

const pruneIndexScript = `
if not redis.call("GET",KEYS[1]) then return redis.call("SREM",KEYS[2],ARGV[1]) end
return 0
`

func (s *Store) DeleteTokenStates(ctx context.Context, subject core.LoginSubject, kinds ...core.TokenKind) error {
	states, err := s.FindTokenStates(ctx, subject, kinds...)
	if err != nil {
		return err
	}
	for _, state := range states {
		current, raw, marker, err := s.readToken(ctx, state.Token)
		if err != nil {
			return err
		}
		if current == nil || current.Subject().Normalize() != subject.Normalize() || !core.MatchKind(current.Kind, kinds...) {
			continue
		}
		deleted, err := s.deleteSnapshot(ctx, state.Token, current.Subject(), raw, marker)
		if err != nil {
			return err
		}
		if !deleted {
			return core.ErrTokenInvalid
		}
	}
	return nil
}

// Token mutations compare the raw stored snapshot within Redis. Go owns JSON
// encoding. Conditional updates cannot recreate a deleted key.
const saveScript = `
if (redis.call("GET",KEYS[1]) or "") ~= ARGV[1] or (redis.call("GET",KEYS[2]) or "") ~= ARGV[2] then return 0 end
if tonumber(ARGV[4]) > 0 then redis.call("SET",KEYS[1],ARGV[3],"PX",ARGV[4]) else redis.call("SET",KEYS[1],ARGV[3]) end
redis.call("DEL",KEYS[2])
redis.call("SREM",KEYS[3],ARGV[5])
redis.call("SREM",KEYS[5],ARGV[5])
redis.call("SADD",KEYS[4],ARGV[5])
return 1
`
const consumeScript = `
if redis.call("GET",KEYS[1]) ~= ARGV[1] or (redis.call("GET",KEYS[2]) or "") ~= ARGV[2] then return 0 end
redis.call("SET",KEYS[1],ARGV[3],"KEEPTTL")
redis.call("DEL",KEYS[2])
return 1
`
const deleteScript = `
if redis.call("GET",KEYS[1]) ~= ARGV[1] or (redis.call("GET",KEYS[2]) or "") ~= ARGV[2] then return 0 end
redis.call("DEL",KEYS[1],KEYS[2])
redis.call("SREM",KEYS[3],ARGV[3])
redis.call("SREM",KEYS[4],ARGV[3])
return 1
`

func (s *Store) deleteSnapshot(ctx context.Context, token core.TokenValue, subject core.LoginSubject, raw, marker string) (bool, error) {
	n, err := s.rdb.DB(ctx).Eval(ctx, deleteScript, []string{s.tokenKey(token), s.consumedMarkerKey(token), s.indexKey(subject), s.legacySubjectKey("index", subject)}, raw, marker, string(token)).Int()
	return n == 1, err
}
func encodeSubjectPart(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}
func (s *Store) legacySubjectKey(kind string, subject core.LoginSubject) string {
	n := subject.Normalize()
	return s.prefix + ":" + kind + ":" + n.LoginType + ":" + n.LoginID
}

func (s *Store) tokenKey(token core.TokenValue) string {
	return s.prefix + ":token:" + string(token)
}

func (s *Store) consumedMarkerKey(token core.TokenValue) string {
	return s.prefix + ":token:" + string(token) + ":consumed"
}

func (s *Store) indexKey(subject core.LoginSubject) string {
	n := subject.Normalize()
	return s.prefix + ":v2:index:" + encodeSubjectPart(n.LoginType) + ":" + encodeSubjectPart(n.LoginID)
}

func (s *Store) sessionKey(subject core.LoginSubject) string {
	n := subject.Normalize()
	return s.prefix + ":v2:session:" + encodeSubjectPart(n.LoginType) + ":" + encodeSubjectPart(n.LoginID)
}

// redisTTL 计算 token key 的物理过期时长，语义对齐 memory.Store：
// 取 ttl 派生过期时刻与 state.ExpiresAt 中较早者；二者皆无则返回 0（永不过期）。
func redisTTL(now time.Time, state *core.TokenState, ttl time.Duration) time.Duration {
	return redisExpiresTTL(now, state.EffectiveExpiresAt(now, ttl))
}

func redisExpiresTTL(now time.Time, expiresAt *time.Time) time.Duration {
	if expiresAt == nil {
		return 0
	}
	d := expiresAt.Sub(now)
	if d <= 0 {
		// 已过期：用极短 TTL 让其尽快物理消失，而非误持久化。
		return time.Millisecond
	}
	return d
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

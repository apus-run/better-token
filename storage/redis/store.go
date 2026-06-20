// Package redis 基于 github.com/apus-run/gala/components/rdb 实现 core.Store。
//
// key 布局（prefix 默认 "bt"）：
//
//	{prefix}:token:{token}                   -> TokenState JSON（带 TTL，含 kind/status）
//	{prefix}:index:{login_type}:{login_id}   -> token Set（登录主体 -> 所有 kind 的 token 集合）
//	{prefix}:session:{login_type}:{login_id} -> Session JSON（带 TTL）
//
// access / refresh / nonce 统一以 TokenState 表达，通过 kind 区分，不再有独立命名空间。
// 索引（Set）是本实现的内部细节：Manager 只通过 LoginSubject 表达语义，
// 索引的维护、过期成员的清理都由本包负责。
package redis

import (
	"context"
	"encoding/json"
	"time"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/pkg/option"
	"github.com/apus-run/gala/components/rdb"
)

var _ core.Store = (*Store)(nil)

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
	clone.Normalize()
	if clone.LoginID == "" && clone.Kind != core.TokenKindNonce {
		return core.ErrEmptyLoginID
	}

	data, err := json.Marshal(clone)
	if err != nil {
		return err
	}
	expiration := redisTTL(s.now(), clone, ttl)
	subject := clone.Subject()
	tokenKey := s.tokenKey(clone.Token)
	indexKey := s.indexKey(subject)

	cli := s.rdb.DB(ctx)

	// 处理主体重绑定：同一 token 改归属时，需从旧主体索引中移除。
	oldSubject, found, err := s.lookupSubject(ctx, cli, clone.Token)
	if err != nil {
		return err
	}

	pipe := cli.TxPipeline()
	if found && oldSubject != subject.Normalize() {
		pipe.SRem(ctx, s.indexKey(oldSubject), string(clone.Token))
	}
	pipe.Set(ctx, tokenKey, data, expiration)
	pipe.Del(ctx, s.consumedMarkerKey(clone.Token))
	pipe.SAdd(ctx, indexKey, string(clone.Token))
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}

	s.refreshIndexTTL(ctx, cli, indexKey, expiration)
	return nil
}

func (s *Store) GetTokenState(ctx context.Context, token core.TokenValue) (*core.TokenState, bool, error) {
	if err := contextErr(ctx); err != nil {
		return nil, false, err
	}
	cli := s.rdb.DB(ctx)
	data, err := cli.Get(ctx, s.tokenKey(token)).Bytes()
	if rdb.IsNilError(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	state := new(core.TokenState)
	if err := json.Unmarshal(data, state); err != nil {
		return nil, false, err
	}
	state.Normalize()

	if state.IsExpired(s.now()) {
		if err := s.deleteToken(ctx, cli, token, state.Subject()); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}
	return state, true, nil
}

func (s *Store) ConsumeTokenState(ctx context.Context, token core.TokenValue) (*core.TokenState, bool, error) {
	if err := contextErr(ctx); err != nil {
		return nil, false, err
	}
	result, err := s.rdb.DB(ctx).Eval(ctx, consumeScript, []string{s.tokenKey(token), s.consumedMarkerKey(token)}).Text()
	if rdb.IsNilError(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	status, raw, ok := cutNewline(result)
	if status == "missing" || raw == "" {
		return nil, false, nil
	}
	_ = ok
	state := new(core.TokenState)
	if err := json.Unmarshal([]byte(raw), state); err != nil {
		return nil, false, err
	}
	state.Normalize()
	// fresh：本次消费成功，返回消费前（active）快照；
	// consumed：此前已被消费，快照标记为 consumed 供上层判定重放。
	if status == "consumed" {
		state.MarkConsumed(s.now())
	}
	return state, true, nil
}

func (s *Store) DeleteTokenState(ctx context.Context, token core.TokenValue) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	cli := s.rdb.DB(ctx)
	subject, found, err := s.lookupSubject(ctx, cli, token)
	if err != nil {
		return err
	}
	if !found {
		return nil // 幂等：不存在直接返回
	}
	return s.deleteToken(ctx, cli, token, subject)
}

func (s *Store) FindTokenStates(ctx context.Context, subject core.LoginSubject, kinds ...core.TokenKind) ([]*core.TokenState, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	cli := s.rdb.DB(ctx)
	indexKey := s.indexKey(subject)

	members, err := cli.SMembers(ctx, indexKey).Result()
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return nil, nil
	}

	keys := make([]string, len(members))
	for i, m := range members {
		keys[i] = s.tokenKey(core.TokenValue(m))
	}
	values, err := cli.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}

	now := s.now()
	result := make([]*core.TokenState, 0, len(members))
	var staleMembers []any   // 索引中需移除的 token（物理已不存在）
	var expiredKeys []string // 仍物理存在但已逻辑过期的 token key，需物理删除
	for i, v := range values {
		raw, ok := v.(string)
		if !ok || raw == "" {
			staleMembers = append(staleMembers, members[i])
			continue
		}
		state := new(core.TokenState)
		if err := json.Unmarshal([]byte(raw), state); err != nil {
			return nil, err
		}
		state.Normalize()
		if state.IsExpired(now) {
			staleMembers = append(staleMembers, members[i])
			expiredKeys = append(expiredKeys, keys[i])
			continue
		}
		if !core.MatchKind(state.Kind, kinds...) {
			continue
		}
		result = append(result, state)
	}

	if len(staleMembers) > 0 || len(expiredKeys) > 0 {
		pipe := cli.TxPipeline()
		if len(staleMembers) > 0 {
			pipe.SRem(ctx, indexKey, staleMembers...)
		}
		if len(expiredKeys) > 0 {
			pipe.Del(ctx, expiredKeys...)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *Store) DeleteTokenStates(ctx context.Context, subject core.LoginSubject, kinds ...core.TokenKind) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	cli := s.rdb.DB(ctx)
	indexKey := s.indexKey(subject)

	members, err := cli.SMembers(ctx, indexKey).Result()
	if err != nil {
		return err
	}
	if len(members) == 0 {
		return nil
	}

	// 无 kind 过滤：整体删除该主体下全部 token 与索引。
	if len(kinds) == 0 {
		pipe := cli.TxPipeline()
		for _, m := range members {
			pipe.Del(ctx, s.tokenKey(core.TokenValue(m)))
		}
		pipe.Del(ctx, indexKey)
		_, err = pipe.Exec(ctx)
		return err
	}

	// 有 kind 过滤：读取每个 token 的 kind，仅删除匹配者。
	keys := make([]string, len(members))
	for i, m := range members {
		keys[i] = s.tokenKey(core.TokenValue(m))
	}
	values, err := cli.MGet(ctx, keys...).Result()
	if err != nil {
		return err
	}
	pipe := cli.TxPipeline()
	for i, v := range values {
		raw, ok := v.(string)
		if !ok || raw == "" {
			pipe.SRem(ctx, indexKey, members[i])
			continue
		}
		state := new(core.TokenState)
		if err := json.Unmarshal([]byte(raw), state); err != nil {
			return err
		}
		state.Normalize()
		if !core.MatchKind(state.Kind, kinds...) {
			continue
		}
		pipe.Del(ctx, keys[i])
		pipe.SRem(ctx, indexKey, members[i])
	}
	_, err = pipe.Exec(ctx)
	return err
}

func (s *Store) SaveSession(ctx context.Context, session *core.Session, ttl time.Duration) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if session == nil {
		return core.ErrEmptySessionID
	}
	subject := session.Subject.Normalize()
	if subject.IsZero() {
		return core.ErrEmptySessionID
	}

	data, err := json.Marshal(session.Clone())
	if err != nil {
		return err
	}
	var expiration time.Duration
	if ttl > 0 {
		expiration = ttl
	}
	return s.rdb.DB(ctx).Set(ctx, s.sessionKey(subject), data, expiration).Err()
}

func (s *Store) GetSession(ctx context.Context, subject core.LoginSubject) (*core.Session, bool, error) {
	if err := contextErr(ctx); err != nil {
		return nil, false, err
	}
	data, err := s.rdb.DB(ctx).Get(ctx, s.sessionKey(subject.Normalize())).Bytes()
	if rdb.IsNilError(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	session := new(core.Session)
	if err := json.Unmarshal(data, session); err != nil {
		return nil, false, err
	}
	return session, true, nil
}

func (s *Store) DeleteSession(ctx context.Context, subject core.LoginSubject) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	return s.rdb.DB(ctx).Del(ctx, s.sessionKey(subject.Normalize())).Err()
}

// lookupSubject 读取 token 当前归属的登录主体，用于索引维护。
func (s *Store) lookupSubject(ctx context.Context, cli rdb.Cmdable, token core.TokenValue) (core.LoginSubject, bool, error) {
	data, err := cli.Get(ctx, s.tokenKey(token)).Bytes()
	if rdb.IsNilError(err) {
		return core.LoginSubject{}, false, nil
	}
	if err != nil {
		return core.LoginSubject{}, false, err
	}
	state := new(core.TokenState)
	if err := json.Unmarshal(data, state); err != nil {
		return core.LoginSubject{}, false, err
	}
	return state.Subject().Normalize(), true, nil
}

// deleteToken 删除 token 并清理其在登录主体索引中的成员。
func (s *Store) deleteToken(ctx context.Context, cli rdb.Cmdable, token core.TokenValue, subject core.LoginSubject) error {
	pipe := cli.TxPipeline()
	pipe.Del(ctx, s.tokenKey(token))
	pipe.Del(ctx, s.consumedMarkerKey(token))
	pipe.SRem(ctx, s.indexKey(subject), string(token))
	_, err := pipe.Exec(ctx)
	return err
}

// refreshIndexTTL 将索引 Set 的 TTL 维持到不短于当前 token 的存活期。
// 仅作 GC 兜底：失败不影响正确性，故忽略错误。
func (s *Store) refreshIndexTTL(ctx context.Context, cli rdb.Cmdable, indexKey string, expiration time.Duration) {
	if expiration <= 0 {
		_ = cli.Persist(ctx, indexKey).Err()
		return
	}
	_ = cli.ExpireGT(ctx, indexKey, expiration).Err()
}

// consumeScript 用独立的 consumed 标记（SETNX）实现原子单次消费，避免对 token JSON
// 做 cjson 往返（空对象会被 cjson 误编码为数组）。KEYS[1]=token key，KEYS[2]=consumed 标记。
// 返回 "missing" / "fresh\n<json>" / "consumed\n<json>"。
const consumeScript = `
local value = redis.call("GET", KEYS[1])
if not value then
	return "missing"
end
if redis.call("SETNX", KEYS[2], "1") == 0 then
	return "consumed\n" .. value
end
local ttl = redis.call("PTTL", KEYS[1])
if ttl and ttl > 0 then
	redis.call("PEXPIRE", KEYS[2], ttl)
end
return "fresh\n" .. value
`

func cutNewline(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

func (s *Store) tokenKey(token core.TokenValue) string {
	return s.prefix + ":token:" + string(token)
}

func (s *Store) consumedMarkerKey(token core.TokenValue) string {
	return s.prefix + ":token:" + string(token) + ":consumed"
}

func (s *Store) indexKey(subject core.LoginSubject) string {
	n := subject.Normalize()
	return s.prefix + ":index:" + n.LoginType + ":" + n.LoginID
}

func (s *Store) sessionKey(subject core.LoginSubject) string {
	n := subject.Normalize()
	return s.prefix + ":session:" + n.LoginType + ":" + n.LoginID
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

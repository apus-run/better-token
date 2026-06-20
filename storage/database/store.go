// Package database 基于 github.com/apus-run/gala/components/db 实现 core.Store。
//
// 表结构（建议，由 Migrate 自动建表）：
//
//	token_states(token PK, login_id, login_type, device, state_json, expires_at, last_active_at, created_at)
//	  索引 idx_token_states_login(login_id, login_type)
//	sessions(login_id, login_type 复合主键, data_json, expires_at, created_at)
//
// 完整领域对象序列化进 state_json / data_json，其余列用于过滤与索引。
// 数据库无自动过期能力，过期由 expires_at 列过滤 + 读时惰性物理删除实现。
package database

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/pkg/option"
	"github.com/apus-run/gala/components/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var _ core.Store = (*Store)(nil)

// tokenStateRecord 是 token 状态的持久化行。完整 TokenState 存于 StateJSON，
// 其余列用于按主体索引与过期过滤。
type tokenStateRecord struct {
	Token        string     `gorm:"column:token;primaryKey;size:255"`
	LoginID      string     `gorm:"column:login_id;size:255;index:idx_token_states_login,priority:1"`
	LoginType    string     `gorm:"column:login_type;size:64;index:idx_token_states_login,priority:2"`
	Device       string     `gorm:"column:device;size:255"`
	StateJSON    []byte     `gorm:"column:state_json;type:text;not null"`
	ExpiresAt    *time.Time `gorm:"column:expires_at;index"`
	LastActiveAt time.Time  `gorm:"column:last_active_at"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
}

func (tokenStateRecord) TableName() string { return "token_states" }

// sessionRecord 是登录主体级 Session 的持久化行，按 (login_id, login_type) 唯一。
type sessionRecord struct {
	LoginID   string     `gorm:"column:login_id;primaryKey;size:255"`
	LoginType string     `gorm:"column:login_type;primaryKey;size:64"`
	DataJSON  []byte     `gorm:"column:data_json;type:text;not null"`
	ExpiresAt *time.Time `gorm:"column:expires_at;index"`
	CreatedAt time.Time  `gorm:"column:created_at"`
}

func (sessionRecord) TableName() string { return "sessions" }

// Store 是 core.Store 的数据库实现，依赖 db.Provider 提供 GORM 会话。
type Store struct {
	db  db.Provider
	now core.NowFunc
}

// Option 定制数据库 Store 的构造行为。
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

// NewStore 基于 db.Provider 构造数据库 Store。provider 为 nil 时返回 nil。
func NewStore(provider db.Provider, opts ...Option) *Store {
	if provider == nil {
		return nil
	}
	s := &Store{
		db:  provider,
		now: core.DefaultRuntime().Now,
	}
	option.Apply(s, opts...)
	return s
}

// Migrate 自动建表，应在使用 Store 前调用一次。
func (s *Store) Migrate(ctx context.Context) error {
	return s.db.DB(ctx).AutoMigrate(&tokenStateRecord{}, &sessionRecord{})
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

	data, err := json.Marshal(clone)
	if err != nil {
		return err
	}
	record := tokenStateRecord{
		Token:        string(clone.Token),
		LoginID:      clone.LoginID,
		LoginType:    clone.LoginType,
		Device:       clone.Device,
		StateJSON:    data,
		ExpiresAt:    effectiveExpiry(s.now(), clone, ttl),
		LastActiveAt: clone.LastActiveAt,
		CreatedAt:    clone.CreatedAt,
	}
	// token 为主键，重复保存（含主体变更）以全列覆盖更新。
	return s.db.DB(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "token"}},
		UpdateAll: true,
	}).Create(&record).Error
}

func (s *Store) GetTokenState(ctx context.Context, token core.TokenValue) (*core.TokenState, bool, error) {
	if err := contextErr(ctx); err != nil {
		return nil, false, err
	}

	var record tokenStateRecord
	err := s.db.DB(ctx).Where("token = ?", string(token)).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	state, err := decodeTokenState(record.StateJSON)
	if err != nil {
		return nil, false, err
	}

	// 过期判定依据落库的 expires_at 列（已编码 ttl 与 state.ExpiresAt 中较早者），
	// 而非 state JSON 内可能为空的 ExpiresAt 字段。
	if record.ExpiresAt != nil && !s.now().UTC().Before(record.ExpiresAt.UTC()) {
		if err := s.db.DB(ctx).Where("token = ?", string(token)).Delete(&tokenStateRecord{}).Error; err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}
	return state, true, nil
}

func (s *Store) DeleteTokenState(ctx context.Context, token core.TokenValue) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	return s.db.DB(ctx).Where("token = ?", string(token)).Delete(&tokenStateRecord{}).Error
}

func (s *Store) FindTokenStates(ctx context.Context, subject core.LoginSubject) ([]*core.TokenState, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	key := subject.Normalize()
	now := s.now()

	// 读时惰性清理：先物理删除该主体下已过期的记录，对齐 memory 的淘汰语义。
	if err := s.db.DB(ctx).
		Where("login_id = ? AND login_type = ? AND expires_at IS NOT NULL AND expires_at <= ?", key.LoginID, key.LoginType, now).
		Delete(&tokenStateRecord{}).Error; err != nil {
		return nil, err
	}

	var records []tokenStateRecord
	if err := s.db.DB(ctx).
		Where("login_id = ? AND login_type = ?", key.LoginID, key.LoginType).
		Find(&records).Error; err != nil {
		return nil, err
	}

	result := make([]*core.TokenState, 0, len(records))
	for i := range records {
		state, err := decodeTokenState(records[i].StateJSON)
		if err != nil {
			return nil, err
		}
		result = append(result, state)
	}
	return result, nil
}

func (s *Store) DeleteTokenStates(ctx context.Context, subject core.LoginSubject) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	key := subject.Normalize()
	return s.db.DB(ctx).
		Where("login_id = ? AND login_type = ?", key.LoginID, key.LoginType).
		Delete(&tokenStateRecord{}).Error
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

	data, err := json.Marshal(session.Clone())
	if err != nil {
		return err
	}
	var expiresAt *time.Time
	if ttl > 0 {
		exp := s.now().Add(ttl).UTC()
		expiresAt = &exp
	}
	record := sessionRecord{
		LoginID:   key.LoginID,
		LoginType: key.LoginType,
		DataJSON:  data,
		ExpiresAt: expiresAt,
		CreatedAt: s.now().UTC(),
	}
	return s.db.DB(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "login_id"}, {Name: "login_type"}},
		DoUpdates: clause.AssignmentColumns([]string{"data_json", "expires_at"}),
	}).Create(&record).Error
}

func (s *Store) GetSession(ctx context.Context, subject core.LoginSubject) (*core.Session, bool, error) {
	if err := contextErr(ctx); err != nil {
		return nil, false, err
	}
	key := subject.Normalize()

	var record sessionRecord
	err := s.db.DB(ctx).
		Where("login_id = ? AND login_type = ?", key.LoginID, key.LoginType).
		First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	if record.ExpiresAt != nil && !s.now().UTC().Before(record.ExpiresAt.UTC()) {
		if err := s.db.DB(ctx).
			Where("login_id = ? AND login_type = ?", key.LoginID, key.LoginType).
			Delete(&sessionRecord{}).Error; err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}

	session := new(core.Session)
	if err := json.Unmarshal(record.DataJSON, session); err != nil {
		return nil, false, err
	}
	return session, true, nil
}

func (s *Store) DeleteSession(ctx context.Context, subject core.LoginSubject) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	key := subject.Normalize()
	return s.db.DB(ctx).
		Where("login_id = ? AND login_type = ?", key.LoginID, key.LoginType).
		Delete(&sessionRecord{}).Error
}

func decodeTokenState(data []byte) (*core.TokenState, error) {
	state := new(core.TokenState)
	if err := json.Unmarshal(data, state); err != nil {
		return nil, err
	}
	return state, nil
}

// effectiveExpiry 计算落库的过期时刻，语义对齐 memory.Store：
// 取 ttl 派生过期时刻与 state.ExpiresAt 中较早者；二者皆无则返回 nil（永不过期）。
func effectiveExpiry(now time.Time, state *core.TokenState, ttl time.Duration) *time.Time {
	var expiresAt *time.Time
	if ttl > 0 {
		exp := now.Add(ttl).UTC()
		expiresAt = &exp
	}
	if state.ExpiresAt != nil {
		exp := state.ExpiresAt.UTC()
		if expiresAt == nil || exp.Before(*expiresAt) {
			expiresAt = &exp
		}
	}
	return expiresAt
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

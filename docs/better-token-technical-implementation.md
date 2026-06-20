# better-token 技术实现文档

> 版本：v1 技术实现规格  
> 目标语言：Go 1.24+  
> 项目定位：一个框架无关、存储可插拔、token/JWT 生成独立、以 `TokenState` 为统一状态模型的认证授权内核。

---

## 1. 总体定位

`better-token` 的核心目标不是复刻某个框架的工具类风格，而是提供一个 Go 风格的认证授权内核。

核心思想：

```text
Token/JWT 生成、签发、解析，是 token 包的职责。
登录态、会话、授权、事件，是 core 包的职责。
Web 框架 token 提取和上下文绑定，是 plugins 包的职责。
存储细节，是 storage 包的职责。
```

最终架构要满足：

- core 不依赖 Gin / Echo / Fiber / HTTP 框架。
- core 不依赖 Redis / SQL / Memory 具体实现。
- core 不生成 JWT，不解析 JWT，不知道 token 是 JWT、UUID、Hash 还是 Simple Token。
- core 只承认一个事实：某个 token 当前是否对应一个有效的 `TokenState`。
- `TokenState` 是统一 token 状态模型，承载 access / refresh / nonce / online 的公共生命周期字段。
- refresh / nonce / online 不再单独设计重复的 State 模型，而是通过 `TokenKind` 和可选 Info 结构表达差异。

---

## 2. 包结构

```text
better-token/
  token/
    jwt.go              // JwtManager[T], Claims[T]
    generator.go        // TokenGenerator[T], TokenStyle
    config.go           // TokenConfig, JwtConfig
    errors.go

  core/
    config.go           // Config
    manager.go          // Manager
    token_state.go      // TokenState, TokenKind, TokenStatus
    subject.go          // LoginSubject
    session.go          // Session
    store.go            // Store interface
    context.go          // AuthContext + context helper
    authorizer.go       // Authorizer, Authority
    event.go            // EventBus
    runtime.go          // Runtime, NowFunc
    option.go           // Option
    errors.go

  storage/
    memory/
      store.go
    redis/
      store.go
    database/
      store.go

  plugins/
    contract.go
    http/
      middleware.go
      extractor.go
      options.go
    gin/
      middleware.go
      extractor.go
      options.go
```

---

## 3. 模块边界

### 3.1 token 包

`token` 包负责：

- JWT 签发。
- JWT 解析。
- JWT 校验。
- 不同 token 风格生成，例如 JWT、UUID、Simple、Hash、Timestamp、Tiktok。

`token` 包不负责：

- 保存登录态。
- 判断 token 是否已登出。
- Session。
- 权限。
- 事件。
- HTTP / Gin 中间件。

### 3.2 core 包

`core` 包负责：

- `TokenState` 生命周期管理。
- 登录、登出、续期、校验。
- Session 管理。
- 统一授权判断。
- 事件发布。
- 当前认证上下文注入和读取。

`core` 包不负责：

- JWT 签名算法。
- JWT Secret。
- TokenStyle。
- Redis key 细节。
- SQL 表结构细节。
- Web 框架适配。

### 3.3 storage 包

`storage` 包负责实现 `core.Store`。

不同实现可以使用不同底层策略：

```text
memory   -> map + sync.RWMutex
redis    -> string key + set index
sql      -> token_states 表 + login subject 索引
```

但这些差异不能泄露给 `Manager`。

### 3.4 plugins 包

`plugins` 负责：

- 从 Header / Authorization / Cookie / Query 提取 token。
- 调用 `core.Manager.GetTokenState`。
- 创建 `core.AuthContext`。
- 写入 `context.Context`。
- 框架层未登录响应。

第一版只实现：

```text
plugins/http
plugins/gin
```

---

## 4. Manager 最终结构

```go
package core

type Manager struct {
	config     Config
	store      Store
	authorizer Authorizer
	eventBus   EventBus

	runtime Runtime
}
```

字段说明：

| 字段 | 职责 |
| --- | --- |
| `config` | 登录态策略配置 |
| `store` | `TokenState` / `Session` 存储端口 |
| `authorizer` | 统一授权判断 |
| `eventBus` | 经典事件总线 |
| `runtime` | 运行时辅助能力，目前主要是 `Now` |

`Manager` 不包含：

```text
jwtManager
tokenGenerator
issuer
TokenIndexStore
permissionChecker
roleChecker
clock Clock
router
extractor
middleware
```

设计原则：

```text
Manager 是登录态状态机。
Manager 不生成 token。
Manager 不解析 JWT。
Manager 不维护 Redis Set / SQL Index。
Manager 不感知 Web 框架。
```

---

## 5. Config 设计

```go
package core

import "time"

type Config struct {
	TokenName   string
	TokenPrefix string

	Timeout       time.Duration
	ActiveTimeout time.Duration
	AutoRenew     bool

	Concurrent bool
	ShareToken bool
}

func DefaultConfig() Config {
	return Config{
		TokenName:     "token",
		TokenPrefix:   "",
		Timeout:       30 * 24 * time.Hour,
		ActiveTimeout: 0,
		AutoRenew:     false,
		Concurrent:    true,
		ShareToken:    false,
	}
}
```

配置语义：

| 字段 | 含义 |
| --- | --- |
| `TokenName` | 请求中 token 的键名 |
| `TokenPrefix` | Header 前缀，如 `Bearer` |
| `Timeout` | token 状态有效期，`<=0` 表示永久 |
| `ActiveTimeout` | 自动续期时长 |
| `AutoRenew` | 读取 token 时是否自动续期 |
| `Concurrent` | 是否允许同一主体多 token 并发 |
| `ShareToken` | 是否复用已有有效 token |

不放入 `Config` 的内容：

```text
JWTSecretKey
JWTIssuer
JWTAudience
JWTAlgorithm
JWTExpiry
TokenStyle
SimpleTokenLength
```

这些属于 `token.TokenConfig` / `token.JwtConfig`。

---

## 6. Runtime 设计

`Runtime` 是公开类型，但它不是核心业务端口，而是运行时辅助配置。

```go
package core

import "time"

type Runtime struct {
	Now NowFunc
}

type NowFunc func() time.Time

func DefaultRuntime() Runtime {
	return Runtime{
		Now: func() time.Time {
			return time.Now().UTC()
		},
	}
}
```

`Manager` 内部统一取时间：

```go
func (m *Manager) now() time.Time {
	if m.runtime.Now == nil {
		return time.Now().UTC()
	}
	return m.runtime.Now().UTC()
}
```

Option：

```go
func WithRuntime(runtime Runtime) Option {
	return func(m *Manager) {
		m.runtime = runtime
		if m.runtime.Now == nil {
			m.runtime.Now = DefaultRuntime().Now
		}
	}
}

func WithNow(now NowFunc) Option {
	return func(m *Manager) {
		if now != nil {
			m.runtime.Now = now
		}
	}
}
```

设计目的：

- token 过期测试可控。
- 自动续期测试可控。
- `CreatedAt / LastActiveAt / ExpiresAt / Event.Time` 使用同一个 now。
- 普通用户无感。

---

## 7. LoginSubject 设计

`LoginSubject` 表示登录主体。

```go
package core

import "strings"

const DefaultLoginType = "default"

type LoginSubject struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

func NewLoginSubject(id string, loginType string) LoginSubject {
	id = strings.TrimSpace(id)
	loginType = strings.TrimSpace(loginType)
	if loginType == "" {
		loginType = DefaultLoginType
	}
	return LoginSubject{ID: id, Type: loginType}
}

func (s LoginSubject) Valid() bool {
	return strings.TrimSpace(s.ID) != ""
}

func (s LoginSubject) LoginID() string {
	return s.ID
}

func (s LoginSubject) LoginType() string {
	if strings.TrimSpace(s.Type) == "" {
		return DefaultLoginType
	}
	return s.Type
}
```

为什么需要 `LoginSubject`：

```text
不要让 loginID / loginType 两个字段在各个模型和 Store 方法里散落。
不要设计 TokenIndexStore。
用领域语言表达：查找某个登录主体的 token 状态。
```

---

## 8. TokenState 统一状态模型

### 8.1 设计原则

`TokenState` 是统一 token 状态模型，用于表达 access / refresh / nonce / online 的公共状态。

不是这样：

```text
TokenState
RefreshState
NonceState
OnlineState
```

而是这样：

```text
TokenState
  Kind = access / refresh / nonce
  RefreshInfo 可选
  NonceInfo 可选
  OnlineInfo 可选
```

公共字段放在 `TokenState`，差异字段放入可选 Info 结构。

### 8.2 TokenKind

```go
type TokenKind string

const (
	TokenKindAccess  TokenKind = "access"
	TokenKindRefresh TokenKind = "refresh"
	TokenKindNonce   TokenKind = "nonce"
)
```

说明：

```text
access  表示访问 token 登录态
refresh 表示刷新 token 状态
nonce   表示一次性 token / nonce 状态
```

`online` 不作为 `TokenKind`，因为在线状态是 access token 的在线投影，不是另一种 token。

### 8.3 TokenStatus

```go
type TokenStatus string

const (
	TokenStatusActive   TokenStatus = "active"
	TokenStatusRevoked  TokenStatus = "revoked"
	TokenStatusConsumed TokenStatus = "consumed"
)
```

说明：

- `active`：正常有效。
- `revoked`：已撤销。
- `consumed`：已消费，主要用于 nonce。
- `expired` 不必落库，可由 `ExpiresAt` 计算。

### 8.4 TokenState 结构

```go
package core

import (
	"encoding/json"
	"time"
)

type TokenValue string

type TokenState struct {
	Token   TokenValue   `json:"token"`
	Kind    TokenKind    `json:"kind"`
	Subject LoginSubject `json:"subject"`

	Device string `json:"device,omitempty"`

	CreatedAt    time.Time  `json:"created_at"`
	LastActiveAt time.Time  `json:"last_active_at"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`

	Status   TokenStatus     `json:"status"`
	Metadata json.RawMessage `json:"metadata,omitempty"`

	Refresh *RefreshInfo `json:"refresh,omitempty"`
	Nonce   *NonceInfo   `json:"nonce,omitempty"`
	Online  *OnlineInfo  `json:"online,omitempty"`
}
```

### 8.5 RefreshInfo

```go
type RefreshInfo struct {
	AccessToken TokenValue `json:"access_token,omitempty"`
	RotatedFrom TokenValue `json:"rotated_from,omitempty"`
	RotatedTo   TokenValue `json:"rotated_to,omitempty"`

	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}
```

使用条件：

```text
TokenState.Kind == TokenKindRefresh 时有效。
```

### 8.6 NonceInfo

```go
type NonceInfo struct {
	Purpose    string     `json:"purpose,omitempty"`
	ConsumedAt *time.Time `json:"consumed_at,omitempty"`
}
```

使用条件：

```text
TokenState.Kind == TokenKindNonce 时有效。
```

### 8.7 OnlineInfo

```go
type OnlineInfo struct {
	OnlineAt     *time.Time `json:"online_at,omitempty"`
	OfflineAt    *time.Time `json:"offline_at,omitempty"`
	ConnectionID string     `json:"connection_id,omitempty"`
	UserAgent    string     `json:"user_agent,omitempty"`
	IP           string     `json:"ip,omitempty"`
}
```

使用条件：

```text
通常用于 TokenKindAccess 的 TokenState。
```

### 8.8 TokenState 方法

```go
func (s TokenState) IsExpired(now time.Time) bool {
	return s.ExpiresAt != nil && now.After(*s.ExpiresAt)
}

func (s TokenState) IsRevoked() bool {
	return s.Status == TokenStatusRevoked
}

func (s TokenState) IsConsumed() bool {
	return s.Status == TokenStatusConsumed
}

func (s TokenState) IsActive(now time.Time) bool {
	return !s.IsExpired(now) &&
		s.Status != TokenStatusRevoked &&
		s.Status != TokenStatusConsumed
}

func (s *TokenState) Touch(now time.Time) {
	s.LastActiveAt = now
}

func (s *TokenState) MarkRevoked(now time.Time) {
	s.Status = TokenStatusRevoked
	if s.Refresh != nil {
		s.Refresh.RevokedAt = &now
	}
}

func (s *TokenState) MarkConsumed(now time.Time) {
	s.Status = TokenStatusConsumed
	if s.Nonce != nil {
		s.Nonce.ConsumedAt = &now
	}
}

func (s *TokenState) MarkOnline(now time.Time, info OnlineInfo) {
	info.OnlineAt = &now
	s.Online = &info
	s.Status = TokenStatusActive
	s.LastActiveAt = now
}

func (s *TokenState) MarkOffline(now time.Time) {
	if s.Online == nil {
		s.Online = &OnlineInfo{}
	}
	s.Online.OfflineAt = &now
	s.LastActiveAt = now
}
```

---

## 9. Store 设计

### 9.1 Store 接口

```go
package core

import (
	"context"
	"time"
)

type Store interface {
	SaveTokenState(ctx context.Context, state *TokenState, ttl time.Duration) error
	GetTokenState(ctx context.Context, token TokenValue) (*TokenState, bool, error)
	DeleteTokenState(ctx context.Context, token TokenValue) error

	FindTokenStates(ctx context.Context, subject LoginSubject, kinds ...TokenKind) ([]*TokenState, error)
	DeleteTokenStates(ctx context.Context, subject LoginSubject, kinds ...TokenKind) error

	SaveSession(ctx context.Context, session *Session, ttl time.Duration) error
	GetSession(ctx context.Context, subject LoginSubject) (*Session, bool, error)
	DeleteSession(ctx context.Context, subject LoginSubject) error
}
```

### 9.2 设计说明

不设计：

```text
TokenIndexStore
ListTokenStatesByLoginID
DeleteTokenStatesByLoginID
```

原因：

```text
Index 是实现细节。
LoginID + LoginType 是 LoginSubject。
Store 用领域方法表达：FindTokenStates(subject)。
```

### 9.3 ttl 语义

```text
ttl > 0   有过期时间
ttl == 0  永久保存
ttl < 0   Manager 层统一转换为 0 或拒绝
```

推荐：

```go
func normalizeTTL(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return 0
	}
	return timeout
}
```

### 9.4 SaveTokenState 语义

`SaveTokenState` 必须：

```text
1. 保存 token -> TokenState。
2. 维护 subject -> token states 的查询能力。
```

但 Manager 不关心底层如何维护。

### 9.5 DeleteTokenState 语义

`DeleteTokenState` 必须：

```text
1. 删除 token 主记录。
2. 清理 subject 相关索引或查询映射。
```

### 9.6 FindTokenStates 语义

```go
FindTokenStates(ctx, subject, TokenKindAccess)
FindTokenStates(ctx, subject, TokenKindRefresh)
FindTokenStates(ctx, subject, TokenKindNonce)
FindTokenStates(ctx, subject, TokenKindAccess, TokenKindRefresh)
FindTokenStates(ctx, subject) // 查询该 subject 下所有 kind
```

---

## 10. Session 设计

```go
package core

type Session struct {
	Subject LoginSubject  `json:"subject"`
	Data    map[string]any `json:"data"`
}

func NewSession(subject LoginSubject) *Session {
	return &Session{
		Subject: subject,
		Data:    make(map[string]any),
	}
}

func (s *Session) Set(key string, value any) {
	if s.Data == nil {
		s.Data = make(map[string]any)
	}
	s.Data[key] = value
}

func (s *Session) Get(key string) (any, bool) {
	if s.Data == nil {
		return nil, false
	}
	v, ok := s.Data[key]
	return v, ok
}

func (s *Session) Remove(key string) {
	if s.Data == nil {
		return
	}
	delete(s.Data, key)
}

func (s *Session) Clear() {
	s.Data = make(map[string]any)
}

func (s *Session) Has(key string) bool {
	_, ok := s.Get(key)
	return ok
}
```

`Session` 不等于 `TokenState`。

- `TokenState` 是 token 状态。
- `Session` 是登录主体的 KV 数据容器。

---

## 11. Authorizer 设计

### 11.1 Authority

```go
package core

type AuthorityType string

const (
	AuthorityPermission AuthorityType = "permission"
	AuthorityRole       AuthorityType = "role"
)

type Authority struct {
	Type  AuthorityType `json:"type"`
	Value string        `json:"value"`
}

func Permission(value string) Authority {
	return Authority{Type: AuthorityPermission, Value: value}
}

func Role(value string) Authority {
	return Authority{Type: AuthorityRole, Value: value}
}
```

### 11.2 Authorizer 接口

```go
type Authorizer interface {
	HasAuthority(ctx context.Context, subject LoginSubject, authority Authority) (bool, error)
	GetAuthorities(ctx context.Context, subject LoginSubject) ([]Authority, error)
}
```

设计说明：

```text
不再设计 PermissionChecker + RoleChecker。
统一成 Authorizer。
角色和权限只是不同类型的 Authority。
```

### 11.3 默认实现

```go
type NoopAuthorizer struct{}

func (NoopAuthorizer) HasAuthority(ctx context.Context, subject LoginSubject, authority Authority) (bool, error) {
	return false, nil
}

func (NoopAuthorizer) GetAuthorities(ctx context.Context, subject LoginSubject) ([]Authority, error) {
	return nil, nil
}
```

### 11.4 Manager 授权 API

```go
func (m *Manager) CheckAuthority(ctx context.Context, authority Authority) error
func (m *Manager) CheckPermission(ctx context.Context, permission string) error
func (m *Manager) CheckRole(ctx context.Context, role string) error
func (m *Manager) CheckAll(ctx context.Context, authorities ...Authority) error
func (m *Manager) CheckAny(ctx context.Context, authorities ...Authority) error
```

实现依赖 `AuthContext`：

```go
func (m *Manager) CheckAuthority(ctx context.Context, authority Authority) error {
	auth, err := RequireAuth(ctx)
	if err != nil {
		return err
	}

	allowed, err := m.authorizer.HasAuthority(ctx, auth.Subject, authority)
	if err != nil {
		return err
	}
	if !allowed {
		return AuthorityDeniedError{Authority: authority}
	}
	return nil
}
```

---

## 12. EventBus 设计

按经典事件总线设计，不拆 `EventPublisher`。

```go
type EventType string

const (
	EventLogin        EventType = "login"
	EventLogout       EventType = "logout"
	EventRenew        EventType = "renew"
	EventReplaced     EventType = "replaced"
	EventRefresh      EventType = "refresh"
	EventNonceConsume EventType = "nonce_consume"
	EventOnline       EventType = "online"
	EventOffline      EventType = "offline"
)

type Event struct {
	Type    EventType     `json:"type"`
	Subject LoginSubject  `json:"subject"`
	Token   TokenValue    `json:"token,omitempty"`
	Kind    TokenKind     `json:"kind,omitempty"`
	Time    time.Time     `json:"time"`
	Extra   map[string]any `json:"extra,omitempty"`
}

type Listener interface {
	Handle(ctx context.Context, event Event) error
}

type ListenerFunc func(ctx context.Context, event Event) error

func (f ListenerFunc) Handle(ctx context.Context, event Event) error {
	return f(ctx, event)
}

type EventBus interface {
	Register(listener Listener)
	Publish(ctx context.Context, event Event)
	Clear()
	ListenerCount() int
}
```

默认实现：

```text
NoopEventBus
SyncEventBus
```

第一版只做同步事件总线。

不做：

```text
异步队列
事件持久化
事件重试
分布式事件总线
```

---

## 13. context.go 设计

### 13.1 AuthContext

```go
package core

import (
	"context"
	"encoding/json"
	"time"
)

type AuthContext struct {
	Token   TokenValue   `json:"token"`
	Kind    TokenKind    `json:"kind"`
	Subject LoginSubject `json:"subject"`

	Device    string          `json:"device,omitempty"`
	ExpiresAt *time.Time      `json:"expires_at,omitempty"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
}

func NewAuthContext(state *TokenState) *AuthContext {
	if state == nil {
		return nil
	}
	return &AuthContext{
		Token:     state.Token,
		Kind:      state.Kind,
		Subject:   state.Subject,
		Device:    state.Device,
		ExpiresAt: state.ExpiresAt,
		Metadata:  cloneRawMessage(state.Metadata),
	}
}
```

### 13.2 context helper

```go
type authContextKey struct{}

func WithAuth(ctx context.Context, auth *AuthContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if auth == nil {
		return ctx
	}
	return context.WithValue(ctx, authContextKey{}, auth)
}

func AuthFromContext(ctx context.Context) (*AuthContext, bool) {
	if ctx == nil {
		return nil, false
	}
	auth, ok := ctx.Value(authContextKey{}).(*AuthContext)
	return auth, ok && auth != nil
}

func RequireAuth(ctx context.Context) (*AuthContext, error) {
	auth, ok := AuthFromContext(ctx)
	if !ok || !auth.Subject.Valid() {
		return nil, ErrNotLogin
	}
	return auth, nil
}

func SubjectFromContext(ctx context.Context) (LoginSubject, bool) {
	auth, ok := AuthFromContext(ctx)
	if !ok || !auth.Subject.Valid() {
		return LoginSubject{}, false
	}
	return auth.Subject, true
}

func RequireSubject(ctx context.Context) (LoginSubject, error) {
	subject, ok := SubjectFromContext(ctx)
	if !ok {
		return LoginSubject{}, ErrNotLogin
	}
	return subject, nil
}

func LoginIDFromContext(ctx context.Context) (string, bool) {
	subject, ok := SubjectFromContext(ctx)
	if !ok {
		return "", false
	}
	return subject.ID, true
}

func RequireLoginID(ctx context.Context) (string, error) {
	loginID, ok := LoginIDFromContext(ctx)
	if !ok {
		return "", ErrNotLogin
	}
	return loginID, nil
}

func TokenFromContext(ctx context.Context) (TokenValue, bool) {
	auth, ok := AuthFromContext(ctx)
	if !ok || auth.Token == "" {
		return "", false
	}
	return auth.Token, true
}

func IsAuthenticated(ctx context.Context) bool {
	_, ok := SubjectFromContext(ctx)
	return ok
}

func cloneRawMessage(v json.RawMessage) json.RawMessage {
	if len(v) == 0 {
		return nil
	}
	cp := make(json.RawMessage, len(v))
	copy(cp, v)
	return cp
}
```

`context.go` 不做：

```text
token 提取
JWT 解析
TokenState 查询
权限判断
Session 查询
Gin / HTTP 适配
全局当前用户
goroutine-local
```

---

## 14. Manager API

```go
type TokenManager interface {
	Login(ctx context.Context, subject LoginSubject, token TokenValue, opts ...LoginOption) (*TokenState, error)

	Logout(ctx context.Context, token TokenValue) error
	LogoutSubject(ctx context.Context, subject LoginSubject, kinds ...TokenKind) error

	GetTokenState(ctx context.Context, token TokenValue) (*TokenState, error)
	IsValid(ctx context.Context, token TokenValue) bool
	Renew(ctx context.Context, token TokenValue, ttl time.Duration) error

	SaveRefreshToken(ctx context.Context, subject LoginSubject, refreshToken TokenValue, accessToken TokenValue, opts ...TokenOption) (*TokenState, error)
	ConsumeNonce(ctx context.Context, nonce TokenValue) (*TokenState, error)
	MarkOnline(ctx context.Context, token TokenValue, info OnlineInfo) error
	MarkOffline(ctx context.Context, token TokenValue) error

	GetSession(ctx context.Context, subject LoginSubject) (*Session, error)
	SaveSession(ctx context.Context, session *Session) error
	DeleteSession(ctx context.Context, subject LoginSubject) error

	CheckAuthority(ctx context.Context, authority Authority) error
	CheckPermission(ctx context.Context, permission string) error
	CheckRole(ctx context.Context, role string) error
	CheckAll(ctx context.Context, authorities ...Authority) error
	CheckAny(ctx context.Context, authorities ...Authority) error
}
```

第一版可以先实现核心 access token 方法：

```text
Login
Logout
LogoutSubject
GetTokenState
IsValid
Renew
Session
Authorizer
```

refresh / nonce / online 可以在第二阶段补齐，但模型已经预留。

---

## 15. Login 流程

```text
Login(ctx, subject, token):
  1. 校验 subject / token
  2. 归一化 subject.Type
  3. now := runtime.Now()
  4. 如果 ShareToken=true，查找 subject 下 access token，存在有效状态则复用
  5. 如果 Concurrent=false，删除 subject 下 access token
  6. 构造 TokenState{Kind: access, Status: active}
  7. store.SaveTokenState
  8. eventBus.Publish(EventLogin)
  9. 返回 TokenState
```

示意：

```go
func (m *Manager) Login(ctx context.Context, subject LoginSubject, token TokenValue, opts ...LoginOption) (*TokenState, error) {
	if !subject.Valid() {
		return nil, ErrEmptyLoginID
	}
	if strings.TrimSpace(string(token)) == "" {
		return nil, ErrEmptyToken
	}

	o := defaultLoginOptions()
	for _, opt := range opts {
		opt(&o)
	}

	subject = NewLoginSubject(subject.ID, subject.Type)
	now := m.now()

	if m.config.ShareToken {
		states, err := m.store.FindTokenStates(ctx, subject, TokenKindAccess)
		if err != nil {
			return nil, err
		}
		for _, state := range states {
			if state != nil && state.IsActive(now) {
				return state, nil
			}
		}
	}

	if !m.config.Concurrent {
		if err := m.store.DeleteTokenStates(ctx, subject, TokenKindAccess); err != nil {
			return nil, err
		}
	}

	state := &TokenState{
		Token:        token,
		Kind:         TokenKindAccess,
		Subject:      subject,
		Device:       o.Device,
		CreatedAt:    now,
		LastActiveAt: now,
		ExpiresAt:    expiresAt(now, m.config.Timeout),
		Status:       TokenStatusActive,
		Metadata:     cloneRawMessage(o.Metadata),
	}

	if err := m.store.SaveTokenState(ctx, state, normalizeTTL(m.config.Timeout)); err != nil {
		return nil, err
	}

	m.eventBus.Publish(ctx, Event{
		Type:    EventLogin,
		Subject: subject,
		Token:   token,
		Kind:    TokenKindAccess,
		Time:    now,
	})

	return state, nil
}
```

---

## 16. GetTokenState 流程

```text
GetTokenState(ctx, token):
  1. store.GetTokenState
  2. 不存在 -> ErrTokenNotFound
  3. revoked / consumed -> ErrTokenInvalid
  4. expired -> 删除状态 -> ErrTokenExpired
  5. AutoRenew=true -> 更新 LastActiveAt / ExpiresAt
  6. 返回 TokenState
```

---

## 17. Refresh 实现思路

`refresh` 不再单独使用 `RefreshState`，而是使用：

```text
TokenState.Kind = refresh
TokenState.Refresh != nil
```

保存 refresh token：

```go
func (m *Manager) SaveRefreshToken(
	ctx context.Context,
	subject LoginSubject,
	refreshToken TokenValue,
	accessToken TokenValue,
	opts ...TokenOption,
) (*TokenState, error) {
	// 构造 TokenState{Kind: refresh, Refresh: &RefreshInfo{AccessToken: accessToken}}
}
```

刷新 access token：

```text
1. GetTokenState(refreshToken)
2. 确认 Kind=refresh
3. 确认 IsActive(now)
4. 外部生成 newAccessToken
5. 调用 Login(ctx, subject, newAccessToken)
6. 更新 refreshState.Refresh.AccessToken / LastUsedAt / RotatedTo
7. SaveTokenState(refreshState)
```

注意：new access token 仍由 `token` 包生成，core 不生成。

---

## 18. Nonce 实现思路

`nonce` 使用：

```text
TokenState.Kind = nonce
TokenState.Nonce != nil
```

生成 nonce：

```text
1. 外部或 token 包生成 nonce token
2. 构造 TokenState{Kind: nonce, Status: active, Nonce: &NonceInfo{Purpose: ...}}
3. store.SaveTokenState
```

消费 nonce：

```text
1. store.GetTokenState(nonce)
2. 确认 Kind=nonce
3. 确认 active
4. MarkConsumed(now)
5. store.SaveTokenState 或 DeleteTokenState
6. Publish(EventNonceConsume)
```

并发要求：

```text
Nonce 的消费必须具备原子性。
```

Redis 实现可以用 Lua 或 SET NX 消费标记。  
SQL 实现可以用 `UPDATE ... WHERE status='active'` 并检查影响行数。

---

## 19. Online 实现思路

online 使用：

```text
TokenState.Kind = access
TokenState.Online != nil
```

上线：

```text
Login 成功后，可选择 MarkOnline。
plugins/http 或 plugins/gin 认证通过后，可 Touch LastActiveAt。
```

下线：

```text
Logout 时 MarkOffline 或 DeleteTokenState。
```

Online 不是单独 token 类型，而是 access token 状态上的在线投影。

---

## 20. storage/memory 实现

```go
type Store struct {
	mu sync.RWMutex

	tokens  map[core.TokenValue]tokenEntry
	bySubj  map[string]map[core.TokenValue]struct{}
	sessions map[string]sessionEntry
}

type tokenEntry struct {
	state    *core.TokenState
	expiresAt *time.Time
}

type sessionEntry struct {
	session  *core.Session
	expiresAt *time.Time
}
```

subject key：

```go
func subjectKey(subject core.LoginSubject) string {
	return subject.LoginType() + ":" + subject.ID
}
```

kind 过滤在 `FindTokenStates` 内完成。

---

## 21. storage/redis 实现

Key 设计：

```text
bt:token:{token}                     -> TokenState JSON
bt:subject:tokens:{type}:{id}        -> Set<TokenValue>
bt:session:{type}:{id}               -> Session JSON
```

保存：

```text
SET bt:token:{token} json EX ttl
SADD bt:subject:tokens:{type}:{id} token
EXPIRE bt:subject:tokens:{type}:{id} ttl
```

查询 subject token states：

```text
SMEMBERS bt:subject:tokens:{type}:{id}
MGET bt:token:{token...}
过滤 kind
过滤不存在 token
清理脏索引
```

不使用：

```text
KEYS bt:token:*
```

---

## 22. storage/database 实现

建议表：

```sql
CREATE TABLE token_states (
  token          VARCHAR(1024) PRIMARY KEY,
  kind           VARCHAR(32) NOT NULL,
  subject_id     VARCHAR(255) NOT NULL,
  subject_type   VARCHAR(64) NOT NULL,
  status         VARCHAR(32) NOT NULL,
  device         VARCHAR(128),
  state_json     TEXT NOT NULL,
  expires_at     TIMESTAMP NULL,
  last_active_at TIMESTAMP NOT NULL,
  created_at     TIMESTAMP NOT NULL
);

CREATE INDEX idx_token_states_subject
ON token_states(subject_type, subject_id, kind);

CREATE INDEX idx_token_states_expires_at
ON token_states(expires_at);
```

Session 表：

```sql
CREATE TABLE sessions (
  subject_id   VARCHAR(255) NOT NULL,
  subject_type VARCHAR(64) NOT NULL,
  data_json    TEXT NOT NULL,
  expires_at   TIMESTAMP NULL,
  created_at   TIMESTAMP NOT NULL,
  updated_at   TIMESTAMP NOT NULL,
  PRIMARY KEY (subject_type, subject_id)
);
```

---

## 23. plugins/http 实现

认证流程：

```text
1. ExtractToken
2. manager.GetTokenState
3. 要求 state.Kind == access
4. NewAuthContext(state)
5. core.WithAuth(r.Context(), auth)
6. next.ServeHTTP
```

Token 提取顺序：

```text
Header[TokenName]
Authorization
Cookie[TokenName]
Query[TokenName]
```

---

## 24. plugins/gin 实现

Gin 插件同时写入：

```text
c.Request.Context()
c.Set("auth", auth)
```

推荐业务读取：

```go
subject, err := core.RequireSubject(c.Request.Context())
```

Gin 风格可选：

```go
v, ok := c.Get("auth")
auth, ok := v.(*core.AuthContext)
```

---

## 25. 错误模型

```go
var (
	ErrEmptyToken       = errors.New("empty token")
	ErrEmptyLoginID     = errors.New("empty login id")
	ErrTokenNotFound    = errors.New("token not found")
	ErrTokenExpired     = errors.New("token expired")
	ErrTokenInvalid     = errors.New("token invalid")
	ErrNotLogin         = errors.New("not login")
	ErrAuthorityDenied  = errors.New("authority denied")
	ErrUnsupportedKind  = errors.New("unsupported token kind")
)
```

HTTP 映射：

```text
ErrNotLogin / ErrTokenNotFound / ErrTokenExpired / ErrTokenInvalid -> 401
ErrAuthorityDenied -> 403
其他错误 -> 500
```

---

## 26. 第一版实现范围

第一版必须实现：

```text
core.Config
core.Runtime
core.LoginSubject
core.TokenState
core.Store
core.Session
core.AuthContext
core.Authorizer
core.EventBus
core.Manager access token 登录态流程
storage/memory.Store
plugins/http
plugins/gin
```

第一版可以预留但不完整实现：

```text
RefreshInfo
NonceInfo
OnlineInfo
SaveRefreshToken
ConsumeNonce
MarkOnline
MarkOffline
```

第二阶段实现：

```text
refresh 完整流程
nonce 原子消费
online touch / offline / list online
storage/redis.Store
```

第三阶段实现：

```text
storage/database.Store
gRPC / Fiber / Echo 插件
高级审计事件
```

---

## 27. 最终结论

better-token 最终架构是：

```text
TokenState 是统一 token 状态模型。
TokenKind 区分 access / refresh / nonce。
Online 是 access token 的状态投影。
Store 是领域存储端口，不暴露 TokenIndex。
Manager 是登录态状态机，不生成 token，不解析 JWT。
Authorizer 统一 role / permission。
EventBus 采用经典事件总线。
Runtime 公开但只作为运行时辅助配置。
Context 只传递 AuthContext。
Plugins 负责框架适配。
```

一句话：

```text
better-token core 只承认和管理 TokenState；token 包负责生成 token，storage 包负责保存状态，plugins 包负责接入框架，业务通过 context 获取 AuthContext。
```

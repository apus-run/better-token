# better-token 最终架构与技术实现文档

> 版本：v0.1 架构定稿  
> 定位：Go 版认证授权核心库，借鉴 sa-token-core 的登录态思想，但按 Go 生态重新收敛。  
> 核心目标：架构整洁、边界清晰、不过度设计、使用心智简单。

---

## 1. 一句话定位

`better-token` 是一个与具体 Web 框架解耦的认证授权核心库。

它不负责业务用户模型，不负责 RBAC 数据库建模，不负责 OAuth2 / SSO，也不把 JWT 解析、token 生成强塞进核心状态机。

最终边界是：

```text
 token 包：生成 token、签发 JWT、解析 JWT、校验 JWT
 core 包：管理服务端登录态 TokenState、Session、授权判断、事件发布、认证上下文
 storage 包：实现 TokenState / Session 的持久化
 plugins 包：实现 net/http、gin 等框架适配
```

核心原则：

```text
token 是客户端凭证
JWT 是 token 的一种表达形式
TokenState 是服务端承认的登录态
Manager 是登录态状态机
Store 是状态存储端口
Authorizer 是统一授权端口
EventBus 是经典事件总线
Runtime 是运行时辅助配置，目前只负责 Now
```

---

## 2. 总体架构

```text
应用代码 / Web 框架
  |
  |  plugins/http 或 plugins/gin
  |  - 从 Header / Cookie / Query 提取 token
  |  - 调用 core.Manager 校验 TokenState
  |  - 写入 core.AuthContext 到 context.Context
  v
core.Manager
  |
  |  协调 Config / Store / Authorizer / EventBus / Runtime
  v
core.Store
  |
  |  storage/memory / storage/redis / storage/database
  v
持久化介质
```

---

## 3. 最终目录结构

```text
better-token/
  token/
    jwt.go              // JwtManager[T], Claims[T], JwtConfig
    generator.go        // TokenGenerator[T], TokenStyle, TokenConfig
    errors.go

  core/
    config.go           // Config
    manager.go          // Manager 登录态状态机
    token_state.go      // TokenState
    session.go          // Session
    store.go            // Store 领域存储端口
    context.go          // AuthContext + context.Context helpers
    authorizer.go       // Authority / Authorizer
    event.go            // Event / EventBus / Listener
    runtime.go          // Runtime / NowFunc
    option.go           // Option
    errors.go           // 核心错误

  storage/
    memory/
      store.go
    redis/
      store.go
    database/
      store.go          // 第二阶段

  plugins/
    contract.go         // 插件通用契约，可选
    http/
      middleware.go
      extractor.go
      options.go
    gin/
      middleware.go
      extractor.go
      options.go
```

第一版只落地：

```text
core
token 已有能力整理
storage/memory
plugins/http
plugins/gin
```

第二阶段再做：

```text
storage/redis
storage/database
plugins/echo
plugins/fiber
plugins/chi
plugins/grpc
```

暂不进入第一版：

```text
RefreshToken
Nonce
OAuth2
SSO
OnlineManager
DistributedSession
异步 EventBus
RBAC 数据库实现
纯无状态 JWT 模式
```

---

## 4. token 包边界

`token` 包已经具备独立能力，应继续保持独立。

### 4.1 JwtManager[T]

职责：

```text
JWT 签发
JWT 解析
JWT 校验
JWT Claims 泛型载荷
JWT 配置
```

核心模型：

```go
type Claims[T any] struct {
    Data T `json:"data"`
    jwt.RegisteredClaims
}

type JwtConfig struct {
    SecretKey string
    Issuer    string
    Audience  []string
    Algorithm string
    Expiry    time.Duration
}

type JwtManager[T any] struct {
    conf *JwtConfig
}
```

核心 API：

```go
func (jm *JwtManager[T]) GenerateToken(userID string, data T) (string, error)
func (jm *JwtManager[T]) ParseToken(tokenStr string) (*Claims[T], error)
func (jm *JwtManager[T]) VerifyToken(tokenStr string) (bool, error)
```

### 4.2 TokenGenerator[T]

职责：

```text
根据 TokenStyle 生成 token 字符串
```

支持风格：

```go
type TokenStyle string

const (
    TokenStyleSimple    TokenStyle = "simple"
    TokenStyleTimestamp TokenStyle = "timestamp"
    TokenStyleUUID      TokenStyle = "uuid"
    TokenStyleHash      TokenStyle = "hash"
    TokenStyleJWT       TokenStyle = "jwt"
    TokenStyleTiktok    TokenStyle = "tiktok"
)
```

核心接口：

```go
type Generator[T any] interface {
    GenerateToken(userID string, data T) (string, error)
}
```

### 4.3 token 包与 core 包的关系

`core.Manager` 不依赖 `JwtManager[T]`，不依赖 `TokenGenerator[T]`，不关心 token 是 JWT、UUID、Hash 还是 Simple。

正确用法：

```go
jwtToken, err := jwtManager.GenerateToken(userID, data)
if err != nil {
    return err
}

state, err := manager.Login(ctx, userID, core.TokenValue(jwtToken))
```

或者：

```go
tokenStr, err := tokenGenerator.GenerateToken(userID, data)
if err != nil {
    return err
}

state, err := manager.Login(ctx, userID, core.TokenValue(tokenStr))
```

这能彻底解耦：

```text
token 生成 / JWT 签发
和
服务端登录态保存
```

---

## 5. core.Manager 最终定稿

```go
type Manager struct {
    config     Config
    store      Store
    authorizer Authorizer
    eventBus   EventBus

    runtime Runtime
}
```

### 5.1 字段语义

| 字段 | 定位 | 说明 |
|---|---|---|
| `config Config` | 登录态策略 | timeout、auto renew、多端策略、token 提取名 |
| `store Store` | 状态存储端口 | 保存 TokenState / Session |
| `authorizer Authorizer` | 授权端口 | 统一角色、权限、scope、policy 等授权判断 |
| `eventBus EventBus` | 事件总线 | 登录、登出、续期、替换等事件 |
| `runtime Runtime` | 运行时辅助配置 | 第一版只放 `Now`，解决时间一致性和可测试性 |

### 5.2 已经明确不放入 Manager 的内容

```text
TokenIssuer
TokenIndexStore
JwtManager
TokenGenerator
PermissionChecker + RoleChecker
HTTP extractor
Gin context
Router
PathMatcher
RefreshManager
NonceManager
OnlineManager
```

设计理由：

```text
issuer 不放入 Manager：token 生成已经属于 token 包
index 不放入 Manager：索引是 Store 的内部实现细节
permissionChecker / roleChecker 不放入 Manager：统一成 Authorizer
Web 相关能力不放入 Manager：属于 plugins
Refresh / Nonce / OAuth2 / SSO 不放入 Manager：属于后续独立模块
```

---

## 6. Config

`core.Config` 只负责登录态管理配置，不包含 JWT 配置，也不包含 token 生成配置。

```go
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

字段说明：

| 字段 | 含义 |
|---|---|
| `TokenName` | 请求中 token 的键名，例如 Header / Cookie / Query 中的名称 |
| `TokenPrefix` | token 前缀，例如 `Bearer` |
| `Timeout` | TokenState 有效期，`<=0` 表示不过期 |
| `ActiveTimeout` | 自动续期使用的活跃有效期 |
| `AutoRenew` | 读取 TokenState 时是否自动续期 |
| `Concurrent` | 是否允许同账号多 token 并发 |
| `ShareToken` | 同账号登录时是否复用已有有效 token |

不放入 `core.Config` 的字段：

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

## 7. Runtime

`Runtime` 公开，但它不是核心业务端口，而是运行时辅助配置。

第一版只保留时间源。

```go
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

Manager 内部统一取时间：

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

设计目标：

```text
默认用户无感
测试可固定时间
Login / Renew / LastActiveAt / Event.Time 使用同一个 now
避免 clock Clock 误导为核心业务端口
```

---

## 8. TokenState

`TokenState` 是服务端 token 登录态，不是 JWT Claims，不是用户资料，不是 RefreshToken 状态。

```go
type TokenValue string

type TokenState struct {
    Token        TokenValue      `json:"token"`
    LoginID      string          `json:"login_id"`
    LoginType    string          `json:"login_type"`
    Device       string          `json:"device,omitempty"`

    CreatedAt    time.Time       `json:"created_at"`
    LastActiveAt time.Time       `json:"last_active_at"`
    ExpiresAt    *time.Time      `json:"expires_at,omitempty"`

    Metadata     json.RawMessage `json:"metadata,omitempty"`
}
```

### 8.1 Metadata 语义

`Metadata` 表示登录态附加元数据。

适合放：

```text
ip
user_agent
platform
client_id
tenant_id
app_id
login_channel
```

不建议放：

```text
user profile
roles
permissions
refresh token
nonce
jwt claims
```

### 8.2 内部状态方法

建议作为内部方法，不暴露给普通使用者：

```go
func (s TokenState) isExpired(now time.Time) bool {
    return s.ExpiresAt != nil && now.After(*s.ExpiresAt)
}

func (s *TokenState) touch(now time.Time) {
    s.LastActiveAt = now
}
```

过期判断和续期由 `Manager` 统一负责。

---

## 9. Session

Session 是用户会话 KV 数据，不等于登录态。

```go
type Session struct {
    ID   string         `json:"id"`
    Data map[string]any `json:"data"`
}
```

方法：

```go
func NewSession(id string) *Session
func (s *Session) Set(key string, value any)
func (s *Session) Get(key string) (any, bool)
func (s *Session) Remove(key string)
func (s *Session) Clear()
func (s *Session) Has(key string) bool
```

设计原则：

```text
Session 是用户级 KV 容器
TokenState 是 token 登录态
删除 token 不一定删除 session
按 login_id 踢下线可以删除 session
```

---

## 10. Store

`Store` 是领域语义存储接口，不再暴露底层 KV，不再单独暴露 TokenIndexStore。

```go
type Store interface {
    SaveTokenState(ctx context.Context, state *TokenState, ttl time.Duration) error
    GetTokenState(ctx context.Context, token TokenValue) (*TokenState, bool, error)
    DeleteTokenState(ctx context.Context, token TokenValue) error

    ListTokenStates(ctx context.Context, loginID string, loginType string) ([]*TokenState, error)
    DeleteTokenStates(ctx context.Context, loginID string, loginType string) error

    SaveSession(ctx context.Context, session *Session, ttl time.Duration) error
    GetSession(ctx context.Context, loginID string) (*Session, bool, error)
    DeleteSession(ctx context.Context, loginID string) error
}
```

### 10.1 为什么不用 Storage + TokenIndexStore

```text
Manager 不应该知道索引如何维护
Memory 可以用 map
Redis 可以用 Set
SQL 可以用 login_id + login_type 索引
```

因此索引属于 Store 实现细节。

### 10.2 存储 key 建议

Redis / KV 实现建议：

```text
bt:token:{token}                        -> TokenState JSON
bt:login:tokens:{login_id}              -> token set/list
bt:login:tokens:{login_type}:{login_id} -> token set/list，可选
bt:session:{login_id}                   -> Session JSON
```

SQL 实现建议：

```sql
CREATE TABLE token_states (
    token TEXT PRIMARY KEY,
    login_id TEXT NOT NULL,
    login_type TEXT NOT NULL,
    device TEXT,
    state_json TEXT NOT NULL,
    expires_at TIMESTAMP NULL,
    last_active_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL
);

CREATE INDEX idx_token_states_login
ON token_states(login_id, login_type);
```

---

## 11. Authorizer

`PermissionChecker` 和 `RoleChecker` 统一成 `Authorizer`。

角色、权限、scope、policy 本质都是授权项。

```go
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

统一授权接口：

```go
type Authorizer interface {
    HasAuthority(ctx context.Context, loginID string, authority Authority) (bool, error)
    GetAuthorities(ctx context.Context, loginID string) ([]Authority, error)
}
```

默认实现：

```text
NoopAuthorizer：默认拒绝所有授权检查
MemoryAuthorizer：测试 / Demo / 小项目使用
```

Manager 对外保留便捷方法：

```go
func (m *Manager) CheckAuthority(ctx context.Context, authority Authority) error
func (m *Manager) CheckPermission(ctx context.Context, permission string) error
func (m *Manager) CheckRole(ctx context.Context, role string) error
func (m *Manager) CheckAll(ctx context.Context, authorities ...Authority) error
func (m *Manager) CheckAny(ctx context.Context, authorities ...Authority) error
```

匹配规则：

```text
同 AuthorityType 才能匹配
精确匹配：user:create == user:create
后缀通配：user:* 匹配 user:create / user:update / user:delete
```

---

## 12. EventBus

EventBus 第一版按经典设计实现，不拆成 EventPublisher。

```go
type EventType string

const (
    EventLogin        EventType = "login"
    EventLogout       EventType = "logout"
    EventKickOut      EventType = "kick_out"
    EventRenewTimeout EventType = "renew_timeout"
    EventReplaced     EventType = "replaced"
)

type Event struct {
    Type      EventType      `json:"type"`
    LoginID   string         `json:"login_id,omitempty"`
    LoginType string         `json:"login_type,omitempty"`
    Token     TokenValue     `json:"token,omitempty"`
    Time      time.Time      `json:"time"`
    Metadata  map[string]any `json:"metadata,omitempty"`
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

第一版实现：

```text
NoopEventBus
SyncEventBus
```

规则：

```text
状态变更成功后再发布事件
监听器按注册顺序执行
监听器错误不破坏主流程
不要在持有 Manager 锁时发布事件
第一版不做异步队列、不做事件持久化、不做重试
```

---

## 13. context.go

`context.go` 只做认证上下文的注入和读取。

不要做：

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

### 13.1 AuthContext

```go
type AuthContext struct {
    Token     TokenValue      `json:"token"`
    LoginID   string          `json:"login_id"`
    LoginType string          `json:"login_type"`
    Device    string          `json:"device,omitempty"`
    ExpiresAt *time.Time      `json:"expires_at,omitempty"`

    Metadata  json.RawMessage `json:"metadata,omitempty"`
}
```

`AuthContext` 是当前请求生命周期内的认证快照。

`TokenState` 是服务端持久化状态模型。

二者不要混为一个东西。

### 13.2 从 TokenState 创建 AuthContext

```go
func NewAuthContext(state *TokenState) *AuthContext {
    if state == nil {
        return nil
    }

    return &AuthContext{
        Token:     state.Token,
        LoginID:   state.LoginID,
        LoginType: state.LoginType,
        Device:    state.Device,
        ExpiresAt: state.ExpiresAt,
        Metadata:  cloneRawMessage(state.Metadata),
    }
}
```

### 13.3 context key

不要用 string key。

```go
type authContextKey struct{}
```

核心方法：

```go
func WithAuth(ctx context.Context, auth *AuthContext) context.Context
func AuthFromContext(ctx context.Context) (*AuthContext, bool)
func RequireAuth(ctx context.Context) (*AuthContext, error)
func LoginIDFromContext(ctx context.Context) (string, bool)
func RequireLoginID(ctx context.Context) (string, error)
func TokenFromContext(ctx context.Context) (TokenValue, bool)
func IsAuthenticated(ctx context.Context) bool
```

完整草图：

```go
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
    if !ok || auth.LoginID == "" {
        return nil, ErrNotLogin
    }
    return auth, nil
}

func LoginIDFromContext(ctx context.Context) (string, bool) {
    auth, ok := AuthFromContext(ctx)
    if !ok || auth.LoginID == "" {
        return "", false
    }
    return auth.LoginID, true
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
    _, ok := LoginIDFromContext(ctx)
    return ok
}
```

---

## 14. Manager API

最终接口建议：

```go
type TokenManager interface {
    Login(ctx context.Context, loginID string, token TokenValue, opts ...LoginOption) (*TokenState, error)

    Logout(ctx context.Context, token TokenValue) error
    LogoutByLoginID(ctx context.Context, loginID string, opts ...LogoutOption) error

    GetTokenState(ctx context.Context, token TokenValue) (*TokenState, error)
    IsValid(ctx context.Context, token TokenValue) bool
    Renew(ctx context.Context, token TokenValue, ttl time.Duration) error

    GetSession(ctx context.Context, loginID string) (*Session, error)
    SaveSession(ctx context.Context, session *Session) error
    DeleteSession(ctx context.Context, loginID string) error

    CheckAuthority(ctx context.Context, authority Authority) error
    CheckPermission(ctx context.Context, permission string) error
    CheckRole(ctx context.Context, role string) error
    CheckAll(ctx context.Context, authorities ...Authority) error
    CheckAny(ctx context.Context, authorities ...Authority) error
}
```

### 14.1 Login 流程

```text
Login(ctx, loginID, token, opts...)
  1. 校验 loginID / token
  2. 解析 LoginOption
  3. now := m.now()
  4. 如果 ShareToken=true，查找已有有效 TokenState 并复用
  5. 如果 Concurrent=false，删除该 loginID/loginType 下旧 TokenState
  6. 构建 TokenState
  7. store.SaveTokenState
  8. eventBus.Publish(EventLogin)
  9. 返回 TokenState
```

### 14.2 GetTokenState 流程

```text
GetTokenState(ctx, token)
  1. store.GetTokenState
  2. 不存在：ErrTokenNotFound
  3. now := m.now()
  4. 已过期：删除 TokenState，返回 ErrTokenNotFound
  5. AutoRenew=true：更新 LastActiveAt / ExpiresAt，保存
  6. 返回 TokenState
```

### 14.3 Logout 流程

```text
Logout(ctx, token)
  1. 查询 TokenState
  2. 删除 TokenState
  3. 发布 EventLogout
```

### 14.4 LogoutByLoginID 流程

```text
LogoutByLoginID(ctx, loginID, opts...)
  1. 解析 loginType 等选项
  2. store.DeleteTokenStates(ctx, loginID, loginType)
  3. 可选删除 Session
  4. 发布 EventKickOut 或 EventLogout
```

---

## 15. plugins 设计

第一版只实现：

```text
plugins/http
plugins/gin
```

后续 `fiber / echo / chi / grpc` 只新增适配实现，不影响 core。

### 15.1 插件职责

```text
ExtractToken：从请求中提取 token
调用 Manager.GetTokenState
生成 AuthContext
把 AuthContext 写入 context.Context
未登录时中断请求
```

插件不负责：

```text
JWT 签发
JWT 解析
权限模型
Session 存储
业务用户查询
```

### 15.2 token 提取顺序

```text
1. Header[TokenName]
2. Header Authorization
3. Cookie[TokenName]
4. Query[TokenName]
```

如果配置了 `TokenPrefix = "Bearer"`，则支持：

```text
Authorization: Bearer xxx
```

### 15.3 net/http 插件

```go
func Middleware(manager *core.Manager, opts ...Option) func(http.Handler) http.Handler
```

流程：

```go
token, ok, err := extractor.ExtractToken(r, manager.Config())
state, err := manager.GetTokenState(r.Context(), token)
auth := core.NewAuthContext(state)
ctx := core.WithAuth(r.Context(), auth)
next.ServeHTTP(w, r.WithContext(ctx))
```

业务读取：

```go
loginID, err := core.RequireLoginID(r.Context())
```

### 15.4 gin 插件

```go
func Middleware(manager *core.Manager, opts ...Option) gin.HandlerFunc
```

Gin 同时写入：

```text
c.Request.Context()
c.Set("auth", auth)
```

推荐业务读取：

```go
loginID, err := core.RequireLoginID(c.Request.Context())
```

Gin 风格可选：

```go
v, ok := c.Get("auth")
auth, ok := v.(*core.AuthContext)
```

---

## 16. Option 设计

```go
type Option func(*Manager)

func WithConfig(config Config) Option
func WithAuthorizer(authorizer Authorizer) Option
func WithEventBus(eventBus EventBus) Option
func WithRuntime(runtime Runtime) Option
func WithNow(now NowFunc) Option
```

构造函数：

```go
func NewManager(store Store, opts ...Option) *Manager {
    m := &Manager{
        config:     DefaultConfig(),
        store:      store,
        authorizer: NoopAuthorizer{},
        eventBus:   NewEventBus(),
        runtime:    DefaultRuntime(),
    }

    for _, opt := range opts {
        opt(m)
    }

    return m
}
```

`store` 是必需依赖，放构造函数参数里；其他作为 Option。

---

## 17. 错误模型

基础错误：

```go
var (
    ErrEmptyLoginID     = errors.New("empty login id")
    ErrEmptyToken       = errors.New("empty token")
    ErrTokenNotFound    = errors.New("token not found")
    ErrNotLogin         = errors.New("not login")
    ErrAuthorityDenied  = errors.New("authority denied")
)
```

结构化授权错误：

```go
type AuthorityDeniedError struct {
    Authority Authority
}

func (e AuthorityDeniedError) Error() string {
    return "authority denied: " + string(e.Authority.Type) + ":" + e.Authority.Value
}

func (e AuthorityDeniedError) Unwrap() error {
    return ErrAuthorityDenied
}
```

HTTP 映射：

```text
ErrNotLogin / ErrTokenNotFound -> 401
ErrAuthorityDenied -> 403
其他错误 -> 500
```

---

## 18. 第一版验收清单

### 18.1 core

```text
Config 默认值正确
Runtime.Now 默认使用 UTC
Login 保存 TokenState
Login 支持 Concurrent=false
Login 支持 ShareToken=true
GetTokenState 能识别过期
AutoRenew 能更新 LastActiveAt / ExpiresAt
Logout 删除 TokenState
LogoutByLoginID 删除指定用户 TokenState
Session set/get/remove/clear 正常
Authorizer 支持 role / permission
CheckAll / CheckAny 正常
EventBus 能收到 login/logout/renew/replaced 事件
context.go 能写入和读取 AuthContext
```

### 18.2 storage/memory

```text
SaveTokenState / GetTokenState / DeleteTokenState 正常
ListTokenStates 按 loginID/loginType 返回
DeleteTokenStates 正常
Session 存取正常
过期数据不会被当作有效数据返回
并发测试 go test -race 无数据竞争
```

### 18.3 plugins/http

```text
Header[TokenName] 提取正常
Authorization Bearer 提取正常
Cookie 提取正常
Query 提取正常
未登录返回 401
登录后 context 中可以读取 LoginID
```

### 18.4 plugins/gin

```text
未登录 Abort 401
登录后 c.Request.Context() 可读取 AuthContext
登录后 c.Get("auth") 可读取 AuthContext
```

---

## 19. 关键设计取舍总结

### 19.1 TokenInfo 改为 TokenState

`TokenInfo` 容易让人误以为包含 JWT Claims、token 生成配置、refresh token、nonce 等信息。

`TokenState` 更准确：

```text
服务端承认的 token 登录态
```

### 19.2 Extra 改为 Metadata

`Extra` 语义太泛，`Metadata` 更正式，表达登录态附加元数据。

### 19.3 PermissionChecker / RoleChecker 统一为 Authorizer

角色、权限、scope、policy 都是 Authority。

Manager 只依赖：

```go
authorizer Authorizer
```

### 19.4 Storage + TokenIndexStore 合并为 Store

索引属于 Store 实现细节。

Manager 不关心 Memory map、Redis Set、SQL index。

### 19.5 issuer 不进入 Manager

token 生成已经独立。

Manager 的 Login 直接接收外部生成好的 token。

### 19.6 Clock 不作为核心字段

时间源作为 `Runtime.Now`，公开但低心智。

它解决：

```text
token 过期
自动续期
LastActiveAt
事件时间
测试固定时间
```

但不会让 `Clock` 成为和 Store / Authorizer / EventBus 同级的核心端口。

### 19.7 context.go 使用 AuthContext

`AuthContext` 是请求认证快照。  
`TokenState` 是服务端持久化状态。  
二者分离，避免业务代码直接修改状态模型。

### 19.8 plugins 第一版只做标准库和 gin

标准库优先，gin 作为最常见 Web 框架适配。  
Fiber / Echo / Chi 后续按接口扩展。

---

## 20. 最终核心心智

```text
better-token core = 登录态状态机 + 授权门面 + 事件发布 + 请求认证上下文
```

最终核心对象：

```go
type Manager struct {
    config     Config
    store      Store
    authorizer Authorizer
    eventBus   EventBus

    runtime Runtime
}
```

最终核心模型：

```text
TokenValue   token 字符串值
TokenState   服务端 token 登录态
Session      用户 KV 会话
Authority    授权项
AuthContext  当前请求认证快照
Runtime      运行时辅助配置
```

最终边界：

```text
token 包负责造 token / 读 JWT
core 包负责承认 token 是否还登录
storage 包负责保存状态
plugins 包负责接入 Web 框架
业务系统负责用户、角色、权限数据来源
```

---

## 21. 第一版不做什么

```text
不做全局 StpUtil 风格 API
不做 goroutine-local
不把 Manager 放进 context
不把 TokenState 直接暴露为唯一业务上下文
不做 OAuth2
不做 SSO
不做 RefreshToken
不做 Nonce
不做 OnlineManager
不做 DistributedSession
不做异步 EventBus
不做 RBAC 数据库表
不做纯无状态 JWT 模式
```

---

## 22. 推荐落地顺序

```text
1. core/errors.go
2. core/config.go
3. core/runtime.go
4. core/token_state.go
5. core/session.go
6. core/store.go
7. core/event.go
8. core/authorizer.go
9. core/context.go
10. core/option.go
11. core/manager.go
12. storage/memory/store.go
13. plugins/http
14. plugins/gin
15. 单元测试与 race 测试
```

---

## 23. 最终结论

这个版本不是极简主义，也不是大而全。

它保留了认证授权核心库真正需要的能力：

```text
登录态
会话
授权
事件
上下文
插件适配
运行时辅助
```

同时排除了会破坏心智的内容：

```text
JWT 生成细节
token style
索引结构
Web 框架对象
RBAC 数据模型
Refresh / Nonce / OAuth2 / SSO
```

最终架构可以作为 better-token 第一版实现基线。

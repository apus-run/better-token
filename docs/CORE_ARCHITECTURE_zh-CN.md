# sa-token-core 核心架构提炼

> 面向多语言复刻的核心功能说明。基于 `sa-token-core` 当前实现反向整理，重点描述语言无关的模块边界、数据模型、存储契约和关键流程。

## 1. 一句话定位

`sa-token-core` 是一个与 Web 框架无关的认证授权内核：它把「登录态」建模为可持久化的 `TokenInfo`，通过统一存储接口保存状态，通过 Manager 提供登录、校验、登出、会话、续期、踢下线等状态机操作，再由各框架适配器负责从请求中提取 token、写入请求上下文。

复刻时先做最小内核：

- `Config`：认证配置与 token 生成策略。
- `Storage`：带 TTL 的 key-value 存储抽象。
- `TokenInfo`：登录态数据模型。
- `TokenManager`：登录态状态机。
- `Session`：用户会话键值数据。
- `RequestContext`：请求生命周期内的当前 token / login_id。
- `Router/AuthFlow`：框架无关的 token 提取、路径鉴权和上下文创建。

其他能力（JWT、事件、权限、Nonce、Refresh Token、OAuth2、SSO、WebSocket、在线用户、分布式 Session）应作为可选模块挂在内核周边。

## 2. 分层架构

```text
应用代码 / Web 框架
  |
  |  框架适配器：从 Header / Cookie / Query / Body 提取 token，绑定请求上下文
  v
AuthFlow / Router
  |
  |  调用 TokenManager 校验 token，生成 RequestContext
  v
TokenManager 认证内核
  |
  |  TokenInfo / Session / Event / Permission / Optional Modules
  v
Storage 抽象
  |
  |  Memory / Redis / Database / 其他语言自己的存储实现
  v
持久化介质
```

### 2.1 关键设计原则

- 内核不依赖具体 Web 框架，框架层只负责请求适配。
- 内核不依赖具体存储，所有状态通过 `Storage` 抽象读写。
- token 值和 token 状态分离：token 是客户端凭证，`TokenInfo` 是服务端状态。
- JWT 是一种 token 生成风格，不取代服务端 `TokenInfo` 存储模型。
- 权限检查和角色检查是可替换策略，不应强绑定数据库或业务模型。
- 事件是旁路扩展，不应影响主流程成功与否。

## 3. 最小核心模块

### 3.1 Config

配置决定 token 名称、过期策略、并发登录策略、token 风格和安全扩展开关。

核心字段：

| 字段 | 含义 | 默认思路 |
| --- | --- | --- |
| `token_name` | 请求中 token 的键名 | `sa-token` |
| `timeout` | access token 有效期，负数表示永久 | 30 天 |
| `active_timeout` | 自动续期时长 | 不启用 |
| `auto_renew` | 读取/校验时是否自动续期 | false |
| `is_concurrent` | 是否允许同账号多端并发 | true |
| `is_share` | 多端登录是否共享 token | true，当前核心主要保留配置位 |
| `token_style` | token 生成风格 | UUID |
| `token_prefix` | 请求头前缀，如 `Bearer` | 无 |
| `jwt_secret_key` / `jwt_algorithm` | JWT 配置 | HS256 |
| `enable_nonce` / `nonce_timeout` | 防重放配置 | 关闭 |
| `enable_refresh_token` / `refresh_token_timeout` | 刷新令牌配置 | 关闭 / 7 天 |

### 3.2 Storage

所有语言都应先实现这个接口。最小可用版本需要 `get/set/delete/exists/expire/ttl/keys`。

```text
Storage:
  get(key) -> Option<String>
  set(key, value, ttl: Option<Duration>)
  delete(key)
  exists(key) -> bool
  expire(key, ttl)
  ttl(key) -> Option<Duration>
  keys(pattern) -> List<String>
```

实现注意：

- `set(..., ttl=None)` 表示永久保存。
- `keys(pattern)` 用于按 login_id 登出、扫描 token 等功能；如果底层存储不支持扫描，需要额外维护索引。
- 对 Redis 这类存储，`validate_and_consume(nonce)` 最好用原子 `SET NX EX` 语义实现。
- 所有跨语言实现建议统一 JSON 序列化格式，便于不同语言共享存储。

### 3.3 TokenInfo

`TokenInfo` 是认证内核的核心状态。

```text
TokenInfo:
  token: string
  login_id: string
  login_type: string = "default"
  create_time: datetime
  last_active_time: datetime
  expire_time: Option<datetime>
  device: Option<string>
  extra_data: Option<json>
  nonce: Option<string>
  refresh_token: Option<string>
  refresh_token_expire_time: Option<datetime>
```

核心方法：

- `is_expired()`：如果 `expire_time` 存在且当前时间超过它，则过期。
- `update_active_time()`：刷新最后活跃时间。

### 3.4 TokenValue

`TokenValue` 只是 token 字符串的强类型包装。其他语言可直接用字符串，但建议保留专门类型或 value object，避免把 login_id、refresh token、access token 混用。

## 4. 存储 key-space

复刻时建议保持 key 格式一致，这样不同语言实现可以共享 Redis / 数据库。

| Key | Value | TTL | 用途 |
| --- | --- | --- | --- |
| `sa:token:{token}` | JSON `TokenInfo` | token timeout | token 到登录态的主索引 |
| `sa:login:token:{login_id}` | token string | token timeout | login_id 到 token 的快捷索引 |
| `sa:login:token:{login_id}:{login_type}` | token string | token timeout | 区分登录类型的快捷索引 |
| `sa:session:{login_id}` | JSON `Session` | 通常永久 | 用户 Session |
| `sa:nonce:{nonce}` | JSON `{login_id, created_at}` | nonce timeout | 标记 nonce 已消费 |
| `sa:refresh:{refresh_token}` | JSON refresh 数据 | refresh timeout | refresh token 状态 |
| `sa:oauth2:client:{client_id}` | JSON `OAuth2Client` | 永久 | OAuth2 客户端 |
| `sa:oauth2:code:{code}` | JSON `AuthorizationCode` | code TTL | OAuth2 授权码 |
| `sa:oauth2:token:{access_token}` | JSON `OAuth2TokenInfo` | token TTL | OAuth2 access token |

当前工具层还暴露 `sa:login:tokens:{login_id}` 读取多 token 列表的 API，但核心登录流程主要写单 token 快捷索引。若目标语言要完整支持多端 token 枚举，建议显式维护这个列表或使用可扫描索引。

## 5. 核心流程

### 5.1 登录

```text
login(login_id):
  token = TokenGenerator.generate(config, login_id)
  info = TokenInfo(token, login_id)
  info.login_type = input.login_type or "default"
  info.device = input.device
  info.extra_data = input.extra_data
  info.nonce = input.nonce
  info.last_active_time = now

  if info.expire_time is empty and config.timeout >= 0:
    info.expire_time = now + config.timeout

  storage.set("sa:token:{token}", json(info), config.timeout)
  storage.set(login_token_key(login_id, login_type), token, config.timeout)

  if config.is_concurrent == false:
    logout other tokens of login_id

  event_bus.publish(Login)
  return token
```

复刻建议：

- 如果实现 `is_concurrent=false`，应先清理旧 token 或清理时排除当前 token，避免误删刚创建的新 token。
- 如果实现 `is_share=true`，可在登录前查 `sa:login:token:{login_id}`，存在且有效则复用旧 token。
- 如果启用 nonce，登录前或登录时应调用 `NonceManager.validate_and_consume(nonce, login_id)`。

### 5.2 校验 token

```text
get_token_info(token):
  value = storage.get("sa:token:{token}")
  if value is None:
    error TokenNotFound

  info = json_decode(value)
  if info.is_expired():
    logout(token)
    error TokenExpired

  if config.auto_renew:
    renew_timeout(token, config.active_timeout or config.timeout)

  return info

is_valid(token):
  return get_token_info(token) succeeds
```

注意：当前实现对 JWT token 仍会查服务端存储；JWT 签名验证由 `JwtManager` 提供，但 `SaTokenManager` 的登录态判断以存储中的 `TokenInfo` 为准。

### 5.3 登出

```text
logout(token):
  info = storage.get("sa:token:{token}") and decode if exists
  storage.delete("sa:token:{token}")
  if info exists:
    event_bus.publish(Logout)
    online_manager.mark_offline(login_id, token) if enabled
```

复刻建议同步删除 login_id 反向索引，否则 `get_token_by_login_id` 可能读到已失效 token。若支持多端列表，也要同步移除列表项。

### 5.4 按 login_id 登出 / 踢下线

```text
logout_by_login_id(login_id):
  for key in storage.keys("sa:token:*"):
    info = decode(storage.get(key))
    if info.login_id == login_id:
      logout(token_from_key(key))

kick_out(login_id):
  notify online manager if enabled
  logout_by_login_id(login_id)
  delete_session(login_id)
  event_bus.publish(KickOut)
```

如果目标存储不适合全量扫描，应维护：

- `sa:login:tokens:{login_id}`：该用户所有 token 列表。
- 或集合类型：`SADD sa:login:tokens:{login_id} token`。

### 5.5 Session

```text
get_session(login_id):
  value = storage.get("sa:session:{login_id}")
  if value exists: return decode(value)
  return new Session(login_id)

save_session(session):
  storage.set("sa:session:{session.id}", json(session), ttl=None)

delete_session(login_id):
  storage.delete("sa:session:{login_id}")
```

`Session` 是 JSON 对象：

```text
Session:
  id: string
  create_time: datetime
  data: map<string, json>
```

### 5.6 请求鉴权流水线

```text
run_auth_flow(request, manager, path_config):
  token_name = manager.config.token_name
  token = extract_token(request, token_name)
  path = request.path

  if path_config exists:
    need_auth = path matches include and not exclude
    if token exists: info = manager.get_token_info(token)
    is_valid = token exists and info exists and login_id_validator(info.login_id)
    should_reject = need_auth and not is_valid
  else:
    is_valid = token exists and manager.is_valid(token)
    should_reject = false

  context = RequestContext(token, info, login_id)
  return AuthFlowResult(auth, context)
```

token 提取顺序：

1. Header `[token_name]`，支持 `Bearer xxx` 或裸 token。
2. Header `Authorization`，如果 `token_name` 不是 Authorization。
3. Cookie `[token_name]`。
4. Query 参数 `[token_name]`。

路径匹配规则：

- `/**` 匹配全部。
- `/api/**` 匹配指定前缀下全部路径。
- `/api/*` 匹配指定前缀下一层路径。
- `*.html` 匹配后缀。
- 其他为精确匹配。

## 6. Token 生成策略

| 风格 | 生成方式 | 适用场景 |
| --- | --- | --- |
| UUID | UUID v4 标准字符串 | 默认通用 |
| Simple UUID | UUID v4 去横杠 | 短一些的随机 token |
| Random32/64/128 | UUID bytes 做 SHA-512 后截断 hex | 指定长度随机 token |
| JWT | JWT claims 签名 | 需要自包含 claims |
| Hash | SHA-256(login_id + timestamp + uuid) | 不想暴露 UUID 格式 |
| Timestamp | `{timestamp_ms}_{random_suffix}` | 需要可读创建时间 |
| Tik | 8 位 base62 | 短 token，碰撞风险更高 |

JWT claims：

```text
JwtClaims:
  sub: login_id
  iss?: issuer
  aud?: audience
  exp?: unix_timestamp
  nbf?: unix_timestamp
  iat?: unix_timestamp
  jti?: string
  login_type?: string
  device?: string
  extra: map<string, json>
```

支持算法：HS256、HS384、HS512、RS256、RS384、RS512、ES256、ES384。跨语言复刻时应使用当地成熟 JWT 库，不要手写签名算法。

## 7. 权限和角色

核心有两种设计：

1. 策略接口：
   - `PermissionChecker.has_permission(login_id, permission)`
   - `PermissionChecker.get_permissions(login_id)`
   - `RoleChecker.has_role(login_id, role)`
   - `RoleChecker.get_roles(login_id)`

2. 便捷内存映射：
   - `login_id -> permissions[]`
   - `login_id -> roles[]`

权限匹配规则：

- 精确匹配：`user:read` 匹配 `user:read`。
- 后缀通配：`admin:*` 匹配以 `admin:` 开头的权限。
- AND：所有权限/角色都满足。
- OR：任一权限/角色满足。

跨语言实现建议把内存映射视为演示或测试能力，生产实现走策略接口，从数据库、IAM、配置中心或业务服务查询。

## 8. 事件系统

事件类型：

- `Login`
- `Logout`
- `KickOut`
- `RenewTimeout`
- `Replaced`
- `Banned`

事件数据：

```text
Event:
  event_type
  login_id
  token
  login_type
  timestamp
  extra?
```

事件总线：

```text
EventBus:
  register(listener)
  clear()
  listener_count()
  publish(event)
```

复刻建议：

- 发布事件前先完成主状态变更。
- 监听器按注册顺序执行。
- 监听器异常不要破坏主流程；需要记录日志或隔离错误。
- 对高吞吐系统，可选择异步队列发布，但要明确事件最终一致性。

## 9. 安全扩展

### 9.1 Nonce 防重放

```text
generate():
  return "nonce_{timestamp_ms}_{uuid_simple}"

validate(nonce):
  return storage.get("sa:nonce:{nonce}") is None

validate_and_consume(nonce, login_id):
  if nonce already exists: error NonceAlreadyUsed
  storage.set("sa:nonce:{nonce}", {login_id, created_at}, nonce_timeout)
```

建议：

- 服务端生成 nonce。
- 敏感操作一个 nonce 只能使用一次。
- 分布式存储必须保证消费操作原子性。

### 9.2 Refresh Token

```text
RefreshData:
  access_token
  login_id
  created_at
  expire_time?
  extra_data?
  refreshed_at?
```

流程：

1. `generate(login_id)` 生成 `refresh_{timestamp}_{login_id}_{uuid}`。
2. `store(refresh_token, access_token, login_id, extra_data)` 保存到 `sa:refresh:{refresh_token}`。
3. `validate(refresh_token)` 返回 login_id，并检查 refresh token 是否过期。
4. `refresh_access_token(refresh_token)` 生成新的 access token，更新 refresh 记录里的 `access_token` 和 `refreshed_at`。

注意：如果要支持「撤销用户所有 refresh token」，需要维护 login_id 到 refresh token 列表的索引。

## 10. 可选大模块

| 模块 | 核心职责 | 是否属于最小内核 |
| --- | --- | --- |
| OAuth2 | 客户端注册、授权码、access token、refresh token、scope 校验 | 否 |
| SSO | ticket、中心会话、客户端列表、统一登出 | 否 |
| WebSocket | 从连接参数中提取 token，验证并绑定 WS 会话 | 否 |
| OnlineManager | 在线用户表、会话活动时间、消息推送、踢下线通知 | 否 |
| DistributedSession | 跨服务 session、服务凭证、session attribute | 否 |

这些模块都依赖 `Storage`、`TokenManager` 或自己的存储接口，适合在多语言版本中逐步实现。

## 11. 跨语言复刻顺序

推荐分 6 个里程碑：

1. **认证内核**
   - `Config`
   - `Storage`
   - `TokenValue`
   - `TokenInfo`
   - `TokenGenerator`
   - `TokenManager.login/get_token_info/is_valid/logout`

2. **Session 与上下文**
   - `Session`
   - `get_session/save_session/delete_session`
   - `RequestContext`
   - 当前请求上下文读取 API

3. **Web 框架适配**
   - `Request` 抽象：header/cookie/query/path
   - `extract_token`
   - `PathAuthConfig`
   - `run_auth_flow`
   - 各框架 middleware / guard / decorator

4. **权限与事件**
   - `PermissionChecker`
   - `RoleChecker`
   - 内存权限表或业务权限适配
   - `EventBus`

5. **安全能力**
   - JWT
   - Nonce
   - Refresh Token

6. **高级协议**
   - OAuth2
   - SSO
   - WebSocket auth
   - Online users
   - Distributed session

## 12. 语言映射建议

| Rust 概念 | Go | Java / Kotlin | TypeScript | Python |
| --- | --- | --- | --- | --- |
| `trait SaStorage` | interface | interface | interface/type | Protocol/ABC |
| `Arc<dyn Storage>` | interface 指针 | bean/interface 注入 | object dependency | injected object |
| `async fn` | `context.Context` + blocking/async lib | CompletableFuture/Reactor/普通同步 | Promise | async def |
| `DateTime<Utc>` | `time.Time` | `Instant`/`OffsetDateTime` | `Date`/number | `datetime` |
| `serde_json::Value` | `map[string]any` | Jackson `JsonNode`/Map | `unknown`/object | dict/list/scalar |
| `OnceCell` 全局工具 | package global | singleton/static holder | module singleton | module singleton |
| task-local context | `context.Context` | ThreadLocal/Reactor Context | AsyncLocalStorage | contextvars |

请求上下文要特别注意：

- Node.js 建议用 `AsyncLocalStorage`。
- Python 建议用 `contextvars`。
- Go 建议显式传递 `context.Context`，不要用 goroutine-local。
- Java Servlet 可用 request attribute / ThreadLocal；响应式栈用 Reactor Context。

## 13. Go 生态实现蓝图

Go 版本建议做成“标准库优先、适配器分离”的结构：核心包只依赖 `context`、`time`、`sync`、`encoding/json`、`crypto/*`、`net/http` 等标准库；Gin、Echo、Fiber、Chi、gRPC、Redis、SQL 等都放到独立 adapter/storage 包里。这样核心包可被任何 Go Web 栈复用。

### 13.1 推荐模块拆分

```text
satoken/
  config.go              // Config, TokenStyle, Option
  errors.go              // 统一错误变量与错误类型
  manager.go             // Manager 核心状态机
  token.go               // TokenValue, TokenInfo, TokenGenerator
  session.go             // Session
  storage.go             // Storage interface
  context.go             // RequestContext + context.Context helper
  router.go              // PathAuthConfig, ExtractToken, RunAuthFlow
  permission.go          // PermissionChecker, RoleChecker
  event.go               // EventBus, Listener
  nonce.go               // NonceManager
  refresh.go             // RefreshTokenManager
  jwt.go                 // JWT optional, 可拆到 satokenjwt

satoken-storage-memory/
  memory.go

satoken-storage-redis/
  redis.go

satokenhttp/
  middleware.go          // net/http middleware

satoken-gin/
  middleware.go

satoken-echo/
  middleware.go

satoken-fiber/
  middleware.go

satokengrpc/
  interceptor.go
```

如果希望发布为一个仓库，可用 Go module 子包组织；如果希望依赖更干净，可以拆成多个 module：

- `github.com/your-org/satoken-go`
- `github.com/your-org/satoken-go/storage/redis`
- `github.com/your-org/satoken-go/adapters/gin`

### 13.2 Go 核心接口草图

Go 里所有可能阻塞的操作都应接收 `context.Context`。不要把当前登录态藏进全局变量或 goroutine-local；请求态放进 `context.Context`，应用层显式传递。

```go
type Storage interface {
    Get(ctx context.Context, key string) (string, bool, error)
    Set(ctx context.Context, key string, value string, ttl time.Duration) error
    SetForever(ctx context.Context, key string, value string) error
    Delete(ctx context.Context, key string) error
    Exists(ctx context.Context, key string) (bool, error)
    Expire(ctx context.Context, key string, ttl time.Duration) error
    TTL(ctx context.Context, key string) (time.Duration, bool, error)
    Keys(ctx context.Context, pattern string) ([]string, error)
}

type Manager struct {
    storage Storage
    config  Config
    events  *EventBus

    permissions map[string][]string
    roles       map[string][]string
    mu          sync.RWMutex
}
```

TTL 表达建议：

- `ttl > 0`：设置过期。
- `ttl == 0`：永久保存。
- 配置里的 `Timeout < 0`：业务语义上的永久 token，写存储时使用永久保存。

### 13.3 Go 数据模型

```go
type TokenValue string

type TokenInfo struct {
    Token                  TokenValue      `json:"token"`
    LoginID                string          `json:"login_id"`
    LoginType              string          `json:"login_type"`
    CreateTime             time.Time       `json:"create_time"`
    LastActiveTime         time.Time       `json:"last_active_time"`
    ExpireTime             *time.Time      `json:"expire_time,omitempty"`
    Device                 *string         `json:"device,omitempty"`
    ExtraData              json.RawMessage `json:"extra_data,omitempty"`
    Nonce                  *string         `json:"nonce,omitempty"`
    RefreshToken           *string         `json:"refresh_token,omitempty"`
    RefreshTokenExpireTime *time.Time      `json:"refresh_token_expire_time,omitempty"`
}

func (t TokenInfo) IsExpired(now time.Time) bool {
    return t.ExpireTime != nil && now.After(*t.ExpireTime)
}

type Session struct {
    ID         string         `json:"id"`
    CreateTime time.Time      `json:"create_time"`
    Data       map[string]any `json:"data"`
}
```

如果要和 Rust 当前 `SaSession` 的 `serde(flatten)` 完全兼容，Go 的 Session 序列化需要自定义 `MarshalJSON` / `UnmarshalJSON`，把 `Data` 展平到顶层。若不追求跨语言共用同一 session JSON，保留 `data` 字段更清晰。

### 13.4 Go 请求上下文

推荐提供两层 API：

```go
type RequestContext struct {
    Token     TokenValue
    TokenInfo *TokenInfo
    LoginID   string
}

func ContextWithAuth(ctx context.Context, auth *RequestContext) context.Context
func AuthFromContext(ctx context.Context) (*RequestContext, bool)
func LoginIDFromContext(ctx context.Context) (string, bool)
```

`StpUtil` 风格的全局工具在 Go 里可以保留，但应作为便捷层，而不是核心依赖：

```go
var defaultManager atomic.Value // stores *Manager

func InitDefault(m *Manager)
func Login(ctx context.Context, loginID string) (TokenValue, error)
func CheckLogin(ctx context.Context) error
```

更 Go 的用法是让业务持有 `*Manager`：

```go
token, err := authManager.Login(ctx, "user_123")
info, err := authManager.GetTokenInfo(ctx, token)
```

### 13.5 net/http 作为基础适配器

先实现 `net/http` middleware，再给 Gin/Echo/Fiber/Chi 包一层适配，维护成本最低。

```text
HTTP middleware:
  1. ExtractToken(r, config.TokenName)
  2. RunAuthFlow(r.Context(), requestAdapter, manager, pathConfig)
  3. if result.ShouldReject: write 401
  4. ctx = ContextWithAuth(r.Context(), result.Context)
  5. next.ServeHTTP(w, r.WithContext(ctx))
```

框架适配器职责：

- Gin：把 `RequestContext` 同时写入 `c.Request.Context()` 和 `c.Set(...)`，方便两种风格读取。
- Echo：写入 `request.Context()`，必要时写入 `echo.Context.Set(...)`。
- Fiber：由于不是标准 `net/http` 上下文模型，需要自己适配 `Ctx` 的 locals，并注意生命周期。
- Chi：基本可以直接复用 `net/http` middleware。
- gRPC：做 unary/stream interceptor，从 metadata 提取 token，把 auth context 注入 `context.Context`。

### 13.6 Go 存储实现建议

内存存储：

- 用 `map[string]entry` + `sync.RWMutex`。
- `entry` 包含 `value string` 和 `expireAt *time.Time`。
- `Get/Exists/TTL/Keys` 时懒清理过期 key。
- 可选后台清理 ticker，但测试中要能关闭。

Redis 存储：

- `Set` 使用 Redis 原生 TTL。
- `Keys` 不建议生产使用 `KEYS`；实现上优先 `SCAN`。
- 多端 token 索引用 set：`sa:login:tokens:{login_id}`。
- nonce 消费用原子语义：`SET key value NX EX seconds`。

SQL 存储：

- 表结构至少包含 `key`, `value`, `expire_at`。
- `key` 是主键。
- `Get` 查询时带上 `expire_at IS NULL OR expire_at > now`，过期数据可后台清理。
- `Keys(pattern)` 可用 `LIKE`，但生产中更建议显式索引 login_id/token。

### 13.7 Go 错误模型

Go 里建议用可比较 sentinel error 加包装信息：

```go
var (
    ErrTokenNotFound       = errors.New("token not found")
    ErrTokenExpired        = errors.New("token expired")
    ErrNotLogin            = errors.New("not login")
    ErrPermissionDenied    = errors.New("permission denied")
    ErrRoleDenied          = errors.New("role denied")
    ErrNonceAlreadyUsed    = errors.New("nonce already used")
    ErrRefreshTokenInvalid = errors.New("refresh token invalid")
)

type PermissionDeniedError struct {
    Permission string
}
```

业务层可以用 `errors.Is(err, ErrNotLogin)` 判断 401，用 `errors.Is(err, ErrPermissionDenied)` 判断 403。

### 13.8 Go 并发与一致性取舍

- `Manager` 本身应可并发使用。
- 内存权限表、事件监听器列表用 `sync.RWMutex` 保护。
- 登录、登出、续期涉及多 key 写入，Redis/SQL 版本可用 pipeline/transaction 改善一致性。
- 事件发布不应持有 Manager 锁。
- 监听器如果较慢，建议用异步队列；默认同步发布更容易测试和推理。
- `logout_by_login_id` 不建议依赖全量扫描，Go 版本应优先维护用户 token 集合索引。

### 13.9 Go 生态优先级

第一阶段建议只做：

- `satoken` 核心。
- `storage/memory`。
- `net/http` middleware。
- `jwt` 可选能力。
- 基础测试。

第二阶段再做：

- Redis storage。
- Gin / Echo / Fiber / Chi adapters。
- Nonce / Refresh Token。
- PermissionChecker / RoleChecker 外部策略接口。

第三阶段做：

- gRPC interceptor。
- OAuth2 / SSO。
- WebSocket auth。
- Online users。
- Distributed session。

### 13.10 Go 版本最小验收

Go 生态版本除了通用验收用例，还应额外覆盖：

- `context.Context` 中能取到当前登录态。
- `net/http` middleware 在未登录时返回 401。
- Gin/Echo/Fiber 等 adapter 不污染核心包依赖。
- 并发登录/登出在 `go test -race` 下无数据竞争。
- Redis nonce 消费具备原子性。
- 内存存储过期 key 不会被 `Get/Exists/Keys` 当作有效数据返回。

## 14. 必须保持的不变量

- token 不存在于 `sa:token:{token}` 时视为未登录。
- `TokenInfo.expire_time` 超时后必须删除或视为无效。
- `logout` 必须删除 token 主索引，并尽量清理 login_id 反向索引。
- `Session` 不等于登录态；删除 token 不一定删除 session，踢下线会删除 session。
- 事件在状态变更之后发布。
- 权限不足和未登录应区分错误类型。
- nonce 一旦消费，在 TTL 内不可复用。
- refresh token 的有效期独立于 access token。

## 15. 最小 API 清单

一个可用的跨语言核心库至少暴露：

```text
TokenManager:
  login(login_id) -> token
  login_with_options(login_id, login_type?, device?, extra_data?, nonce?, expire_time?) -> token
  logout(token)
  logout_by_login_id(login_id)
  kick_out(login_id)
  get_token_info(token) -> TokenInfo
  is_valid(token) -> bool
  renew_timeout(token, seconds)
  get_session(login_id) -> Session
  save_session(session)
  delete_session(login_id)

AuthFlow:
  extract_token(request, token_name) -> Option<string>
  match_path(path, pattern) -> bool
  need_auth(path, include, exclude) -> bool
  run_auth_flow(request, manager, path_config?) -> AuthFlowResult

Session:
  set(key, value)
  get(key)
  remove(key)
  clear()
  has(key)

Permission:
  has_permission(login_id, permission)
  has_role(login_id, role)
  check_permission(login_id, permission)
  check_role(login_id, role)
```

## 16. 已知实现差异与复刻取舍

- 当前 Rust 内核的 `SaTokenManager` 使用存储态判断登录，JWT 只负责生成/解析/签名校验；如果某语言版本想做纯无状态 JWT，需要单独定义黑名单、续期和登出语义。
- `is_share` 在配置中存在，但核心登录路径没有完整实现 token 复用逻辑；复刻时可以补齐。
- 多 token 列表读取 API 存在，但核心登录路径没有系统维护 `sa:login:tokens:{login_id}`；复刻时建议维护集合索引。
- `logout_by_login_id` 依赖 `keys("sa:token:*")` 扫描；生产级 Redis / SQL 实现建议改为 login_id 索引。
- `NonceManager.validate_and_consume` 在通用 KV 抽象上是先查再写；分布式复刻应改成原子写入。

## 17. 复刻验收用例

建议每种语言实现至少覆盖：

- 登录后能用 token 取回 login_id。
- token 过期后 `is_valid=false`，并清理主索引。
- 登出后 token 不再有效。
- 按 login_id 踢下线会清理 token 和 session。
- session set/get/remove/clear 正常。
- Header、Authorization、Cookie、Query 四种提取路径正常。
- `/api/**`、`/api/*`、`*.html`、精确路径匹配正常。
- 权限精确匹配和 `prefix:*` 通配匹配正常。
- 事件监听器能收到 login/logout/kick_out。
- nonce 二次消费失败。
- refresh token 能换发新 access token，过期后失败。
- JWT claims 中 `sub/exp/extra` 正常生成和校验。

# better-token 第二阶段功能路线图 Issues

> Source PRD: `tasks/prd-phase-2-roadmap.md`
> Source SPEC: `tasks/spec-phase-2-roadmap.md`
> Creation mode: Local
> Generated: 2026-06-20

## Issue #1: 新增 RefreshToken 核心契约

### Description

定义 RefreshTokenState、RefreshStore capability、RefreshConfig、刷新相关错误与事件，保持 `core.Store` 方法签名不变。该 Issue 只建立核心契约，不实现具体 store 和刷新流程。

### Acceptance Criteria

- [x] 定义 `core.RefreshTokenState`，包含 refresh token、loginID、loginType、device、createdAt、expiresAt、revokedAt。
- [x] 定义 `core.RefreshStore` capability，支持保存、读取、删除、按登录主体查找、按登录主体删除。
- [x] 定义 `core.StoreWithRefresh` 组合接口。
- [x] 定义 `core.RefreshConfig` 与默认配置。
- [x] 新增 refresh 相关错误：空 refresh token、不存在、过期、撤销、缺失 next refresh token。
- [x] 新增 refresh 相关事件类型。
- [x] 现有 `core.Store` 方法签名保持兼容。
- [x] Go tests pass.

### Dependencies

None

### Type

backend

### Priority

high

### SPEC Reference

Sections 2.2, 3.2, 4.1, 6.1

---

## Issue #2: 新增 Nonce 核心契约

### Description

定义 NonceState、NonceStore capability、NonceConfig、NonceConsumer、登录 nonce 选项与相关错误。该 Issue 只建立防重放能力的核心契约，不实现具体 store 和 manager。

### Acceptance Criteria

- [x] 定义 `core.NonceState`，包含 nonce、loginID、loginType、purpose、createdAt、expiresAt、consumedAt、metadata。
- [x] 定义 `core.NonceStore` capability，支持保存 nonce 和原子消费 nonce。
- [x] 定义 `core.StoreWithNonce` 组合接口。
- [x] 定义 `core.NonceConfig` 与默认配置。
- [x] 定义 `core.NonceConsumer`。
- [x] `core.Config` 新增 `RequireNonce`，默认值为 `false`。
- [x] 新增 `WithNonce` 登录选项。
- [x] 新增 `WithNonceConsumer` Manager 选项。
- [x] 新增 nonce 相关错误：空 nonce、不存在、过期、重复消费、consumer 未配置。
- [x] Go tests pass.

### Dependencies

None

### Type

backend

### Priority

high

### SPEC Reference

Sections 2.2, 3.2, 4.1, 6.1

---

## Issue #3: 实现 memory refresh/nonce store

### Description

扩展 `storage/memory.Store`，实现 RefreshStore 与 NonceStore，保证 copy 语义、TTL 语义与并发安全。

### Acceptance Criteria

- [x] `storage/memory.Store` 实现 `core.StoreWithRefresh`。
- [x] `storage/memory.Store` 实现 `core.StoreWithNonce`。
- [x] 支持 refresh token 保存、读取、删除、按登录主体查找、按登录主体删除。
- [x] 支持 nonce 保存和原子消费。
- [x] 过期 refresh token 不返回，并清理索引。
- [x] 过期 nonce 不可消费。
- [x] 同一个 nonce 并发消费时仅一次成功。
- [x] 返回对象具备 copy 语义。
- [x] 并发读写无 data race。
- [x] Go tests pass.

### Dependencies

Issue #1, Issue #2

### Type

backend

### Priority

high

### SPEC Reference

Sections 3.1, 9.2

---

## Issue #4: 实现 Redis refresh/nonce store

### Description

扩展 `storage/redis.Store`，增加 refresh/nonce key-space、索引维护与原子 nonce 消费。

### Acceptance Criteria

- [x] `storage/redis.Store` 实现 `core.StoreWithRefresh`。
- [x] `storage/redis.Store` 实现 `core.StoreWithNonce`。
- [x] 新增 refresh key：`bt:refresh:{refresh_token}`。
- [x] 新增 refresh subject index：`bt:refresh-index:{login_type}:{login_id}`。
- [x] 新增 nonce key：`bt:nonce:{nonce}`。
- [x] refresh token 保存、读取、删除、按登录主体查找、按登录主体删除行为正确。
- [x] refresh subject index 支持过期/陈旧成员读时清理。
- [x] nonce 消费使用 Lua、GETDEL 或等价原子语义。
- [x] 同一个 nonce 并发消费时仅一次成功。
- [x] miniredis 测试覆盖 refresh 与 nonce 行为。
- [x] Go tests pass.

### Dependencies

Issue #1, Issue #2

### Type

backend

### Priority

high

### SPEC Reference

Sections 3.1, 8.2, 9.2

---

## Issue #5: 实现 database refresh/nonce store

### Description

扩展 `storage/database.Store` 和 `Migrate(ctx)`，新增 refresh/nonce 表与事务化 nonce 消费。

### Acceptance Criteria

- [x] `storage/database.Store` 实现 `core.StoreWithRefresh`。
- [x] `storage/database.Store` 实现 `core.StoreWithNonce`。
- [x] `Migrate(ctx)` 创建 `refresh_token_states` 表。
- [x] `Migrate(ctx)` 创建 `nonce_states` 表。
- [x] refresh 表包含 token、login_id、login_type、access_token、device、state_json、expires_at、revoked_at、last_used_at、created_at。
- [x] nonce 表包含 nonce、state_json、expires_at、consumed_at、created_at。
- [x] refresh 相关索引覆盖 login、access_token、expires_at、revoked_at。
- [x] nonce 相关索引覆盖 expires_at、consumed_at。
- [x] 支持 refresh token 保存、读取、删除、按登录主体查找、按登录主体删除。
- [x] nonce 消费在事务中完成。
- [x] 同一个 nonce 并发消费时仅一次成功。
- [x] sqlite 测试覆盖 migration、refresh 与 nonce 行为。
- [x] Go tests pass.

### Dependencies

Issue #1, Issue #2

### Type

backend

### Priority

high

### SPEC Reference

Sections 3.1, 3.4, 8.3, 9.2

---

## Issue #6: 实现 RefreshManager

### Description

实现登录签发 refresh、刷新 access token、轮换 refresh token、撤销 refresh token 的核心流程。RefreshManager 接收调用方生成的 access/refresh token 字符串，不在 core 内生成 token。

### Acceptance Criteria

- [x] 实现 `core.NewRefreshManager(manager, store, opts...)`。
- [x] 实现 `RefreshManager.Login(ctx, loginID, accessToken, refreshToken, opts...)`。
- [x] 实现 `RefreshManager.Refresh(ctx, refreshToken, nextAccessToken, opts...)`。
- [x] 实现 `RefreshManager.Revoke(ctx, refreshToken)`。
- [x] 实现 `RefreshManager.RevokeByLoginID(ctx, loginID, opts...)`。
- [x] 默认 `core.Manager.Login` 行为不签发 RefreshToken。
- [x] RefreshToken TTL 与 access token TTL 可分别配置。
- [x] 有效 refresh token 可换新 access token。
- [x] 过期 refresh token 返回明确错误。
- [x] 已撤销 refresh token 返回明确错误。
- [x] 开启 refresh token 轮换时，缺失 next refresh token 返回明确错误。
- [x] 保存 refresh state 失败时回滚新建 access token。
- [x] 换新后的 access token 可被现有中间件识别。
- [x] Go tests pass.

### Dependencies

Issue #1, Issue #3

### Type

backend

### Priority

high

### SPEC Reference

Sections 2.3, 4.1, 5.1

---

## Issue #7: 实现 NonceManager 与登录 nonce 校验

### Description

实现 nonce 生成/消费，并在 `core.Manager.Login` 中支持可选 nonce 防重放。默认登录流程不要求 nonce。

### Acceptance Criteria

- [x] 实现 `core.NewNonceManager(store, opts...)`。
- [x] 实现 `NonceManager.Generate(ctx, opts...)`。
- [x] 实现 `NonceManager.Consume(ctx, nonce)`。
- [x] nonce 生成结果带 TTL。
- [x] nonce 第一次消费返回成功。
- [x] nonce 第二次消费返回 replay 错误。
- [x] `Config.RequireNonce=false` 时登录流程不要求 nonce。
- [x] `Config.RequireNonce=true` 且缺失 nonce 时返回明确错误。
- [x] `Config.RequireNonce=true` 且 nonce 重复消费时返回明确错误。
- [x] `Config.RequireNonce=true` 且未配置 NonceConsumer 时返回明确错误。
- [x] Go tests pass.

### Dependencies

Issue #2, Issue #3

### Type

backend

### Priority

high

### SPEC Reference

Sections 2.3, 4.1, 5.1

---

## Issue #8: 新增 OnlineManager 方法与分布式 Session 测试

### Description

在 `core.Manager` 增加在线 token 查询和按设备踢下线能力，并补齐跨 Manager Session 共享测试。DistributedSession 不新增独立 public type，而是强化现有 Session 的跨实例契约。

### Acceptance Criteria

- [x] 实现 `Manager.ListTokenStates(ctx, loginID, opts...)`。
- [x] 实现 `Manager.LogoutByDevice(ctx, loginID, device, opts...)`。
- [x] `ListTokenStates` 支持按 loginID 和 loginType 查询在线 TokenState。
- [x] `ListTokenStates` 返回结果不包含已过期 token。
- [x] `ListTokenStates` 返回结果包含 device、createdAt、lastActiveAt、expiresAt。
- [x] `LogoutByDevice` 只删除匹配 device 的 token。
- [x] 被踢下线 token 后续通过中间件访问返回 401。
- [x] 其他 device 的 token 不受影响。
- [x] 明确 DistributedSession 与现有 Session 的关系和命名。
- [x] Redis/database store 下 Session TTL 行为有测试覆盖。
- [x] 多 Manager 实例共享同一 store 时能读取同一 Session。
- [x] Go tests pass.

### Dependencies

None

### Type

backend

### Priority

medium

### SPEC Reference

Sections 2.2, 4.1, 5.1, 9.2

---

## Issue #9: 新增 AsyncEventBus

### Description

实现异步事件总线，兼容现有 `EventBus` 接口，并提供错误处理与关闭/flush 能力。

### Acceptance Criteria

- [x] 实现 `core.AsyncEventBus`。
- [x] `AsyncEventBus` 实现现有 `core.EventBus` 接口。
- [x] 提供 `NewAsyncEventBus(opts...)`。
- [x] 异步队列容量可配置。
- [x] worker 数量可配置。
- [x] listener panic 不会导致主流程 panic。
- [x] listener error 可通过错误处理器观察。
- [x] 提供 `Flush(ctx)`。
- [x] 提供 `Close(ctx)`。
- [x] `Flush` 和 `Close` 行为有测试覆盖。
- [x] Go tests pass.

### Dependencies

None

### Type

backend

### Priority

medium

### SPEC Reference

Sections 2.2, 4.1, 6.3, 9.1

---

## Issue #10: 新增 RBAC helper 包

### Description

新增可选 `rbac` 包，实现角色-权限图并适配 `core.Authorizer`。该包不替代业务系统自己的 Authorizer，也不强制引入数据库模型。

### Acceptance Criteria

- [x] 新增 `rbac` 包。
- [x] 实现 `rbac.Authorizer`。
- [x] `rbac.Authorizer` 满足 `core.Authorizer` 接口。
- [x] 支持给 loginID 绑定 role。
- [x] 支持给 loginID 撤销 role。
- [x] 支持给 role 绑定 permission。
- [x] 支持给 role 撤销 permission。
- [x] 支持给 loginID 设置 direct permissions。
- [x] `CheckRole` 能通过 RBAC helper 返回结果。
- [x] `CheckPermission` 能通过 RBAC helper 返回结果。
- [x] 支持与现有 wildcard permission 语义一致的匹配行为。
- [x] Go tests pass.

### Dependencies

None

### Type

backend

### Priority

medium

### SPEC Reference

Sections 2.2, 4.1, 7.1, 9.1

---

## Issue #11: 补齐第二阶段 README 与 examples

### Description

更新 README 功能索引，并新增 refresh token、nonce、online manager 示例，满足新用户 10 分钟内完成第二阶段能力最小接入的目标。

### Acceptance Criteria

- [x] README 增加第二阶段能力索引。
- [x] README 说明 access token、refresh token、server-side TokenState 三者关系。
- [x] README 说明 RefreshToken 默认不启用，需要显式使用 `RefreshManager`。
- [x] README 说明 `Config.RequireNonce` 默认值为 `false`。
- [x] README 说明 database store 需要运行 `Migrate(ctx)`。
- [x] 新增 `examples/refresh-token/main.go`。
- [x] 新增 `examples/nonce/main.go`。
- [x] 新增 `examples/online-manager/main.go`。
- [x] 示例代码可通过 `go test ./...` 或对应运行检查。
- [x] 第二阶段新增文档不破坏第一版 README 中已有示例。

### Dependencies

Issue #6, Issue #7, Issue #8

### Type

backend

### Priority

high

### SPEC Reference

Sections 4.2, 9.4, 10.3

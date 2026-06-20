# better-token 三阶段补齐 · Issues

> 来源：`tasks/prd-three-phase-completion.md` + `tasks/spec-three-phase-completion.md`
> 共 16 个 Issue，按 SPEC §10.1 阶段排序。建议阶段1+2（#1~#9）作为破坏式重构里程碑集中完成，再叠加 #10~#16。
> 用 `/goal` 逐个实现。

---

## 阶段1 · 统一数据模型（破坏式重构基础）

### Issue #1: 统一 TokenState 状态模型

**Description:** 把 `TokenState` 改造为统一状态模型，用 `TokenKind` + `TokenStatus` + 可选 `RefreshInfo`/`NonceInfo`/`OnlineInfo` 表达 access/refresh/nonce/online，移除独立的 `RefreshTokenState`/`NonceState`。这是整个重构的基础。

**Acceptance Criteria:**
- [ ] `TokenState` 增加 `Kind TokenKind`（access/refresh/nonce）、`Status TokenStatus`（active/revoked/consumed）。
- [ ] `TokenState` 增加可选 `Refresh *RefreshInfo`、`Nonce *NonceInfo`、`Online *OnlineInfo`。
- [ ] 提供 `IsExpired/IsRevoked/IsConsumed/IsActive/Touch/MarkRevoked/MarkConsumed/MarkOnline/MarkOffline` 方法。
- [ ] 移除独立的 `RefreshTokenState` / `NonceState` 类型。
- [ ] `Clone()` 深拷贝新增的指针字段（Metadata、Refresh、Nonce、Online、时间指针）。
- [ ] `go build ./...` 通过；`go test ./core/...` 通过。

**Dependencies:** None
**Type:** backend
**Priority:** high
**SPEC Reference:** §3.1

---

### Issue #2: 统一 Store 接口

**Description:** 将 `core.Store` 收敛为统一的 TokenState + Session 端口：`FindTokenStates`/`DeleteTokenStates` 增加 `kinds ...TokenKind` 过滤，新增原子 `ConsumeTokenState`，移除独立的 refresh/nonce 存储接口。

**Acceptance Criteria:**
- [ ] `FindTokenStates` / `DeleteTokenStates` 增加 `kinds ...TokenKind` 参数（空表示不过滤）。
- [ ] 新增 `ConsumeTokenState(ctx, token) (*TokenState, bool, error)`（原子地将 active 置 consumed 并返回消费前状态）。
- [ ] 移除 `RefreshStore` / `NonceStore` / `StoreWithRefresh` / `StoreWithNonce` / `NonceConsumer`。
- [ ] `go build ./...` 通过。

**Dependencies:** Issue #1
**Type:** backend
**Priority:** high
**SPEC Reference:** §3.2, §5.1

---

### Issue #3: 重写 memory store 为统一 Store

**Description:** 让 `storage/memory` 实现统一 Store 接口，按 kind 过滤，`ConsumeTokenState` 在锁内原子完成。

**Acceptance Criteria:**
- [ ] memory store 实现统一 Store 全部方法。
- [ ] `FindTokenStates`/`DeleteTokenStates` 按 `kinds` 过滤。
- [ ] `ConsumeTokenState` 在 `mu.Lock()` 内完成 active→consumed 转换并返回消费前快照。
- [ ] 原有 TokenState/Session 行为不回退。
- [ ] `go test ./storage/memory/...` 通过。

**Dependencies:** Issue #2
**Type:** backend
**Priority:** high
**SPEC Reference:** §3.4, §5.1

---

### Issue #4: 重写 redis store 为统一 Store

**Description:** 让 `storage/redis` 实现统一 Store，`ConsumeTokenState` 使用原子语义（Lua 或 SETNX 消费标记）。

**Acceptance Criteria:**
- [ ] redis store 实现统一 Store 全部方法（token JSON 含 kind/status）。
- [ ] subject 索引按 kind 在取出后过滤；不使用 `KEYS`。
- [ ] `ConsumeTokenState` 用 Lua/SETNX 保证并发下仅一个赢家。
- [ ] 原有 TokenState/Session 行为不回退。
- [ ] `go test ./storage/redis/...` 通过。

**Dependencies:** Issue #2
**Type:** backend
**Priority:** high
**SPEC Reference:** §3.4, §5.1

---

### Issue #5: 重写 database store 为统一 Store

**Description:** 让 `storage/database` 实现统一 Store，`token_states` 表含 kind/status 列，`ConsumeTokenState` 用 `UPDATE ... WHERE status='active'` 并校验影响行数；`Migrate` 兼容旧表。

**Acceptance Criteria:**
- [ ] `token_states` 表含 `kind`/`status`/`state_json` 列与 `(subject_type,subject_id,kind)` 索引。
- [ ] `FindTokenStates`/`DeleteTokenStates` 按 `kinds` 过滤（走索引）。
- [ ] `ConsumeTokenState` 用 `UPDATE...WHERE status='active'`，`RowsAffected` 判定消费成功。
- [ ] `Migrate` 对旧表 `ALTER TABLE ADD COLUMN` 并回填 `'access'`/`'active'`。
- [ ] `go test ./storage/database/...` 通过。

**Dependencies:** Issue #2
**Type:** backend
**Priority:** high
**SPEC Reference:** §3.4, §5.1

---

## 阶段2 · 统一 Manager

### Issue #6: 定义统一 TokenManager 接口

**Description:** 在 `core` 定义统一 `TokenManager` 接口（覆盖 access + refresh + nonce + online + session + `Check*`），并精简 `Manager` 字段（删除 `nonceConsumer`/`refreshRevoker`，新增 `refreshConfig`/`nonceConfig`）。

**Acceptance Criteria:**
- [ ] 新建 `core/token_manager.go` 定义 `TokenManager` 接口（文档第14节方法集 + `Check*`）。
- [ ] `var _ TokenManager = (*Manager)(nil)` 编译期断言通过。
- [ ] `Manager` 删除 `nonceConsumer`/`refreshRevoker` 字段，新增 `refreshConfig`/`nonceConfig`。
- [ ] 新增 `WithRefreshConfig`/`WithNonceConfig` 选项；删除 `WithNonceConsumer`。
- [ ] 新增错误 `ErrTokenInvalid`/`ErrUnsupportedKind`；删除 `ErrNonceConsumerNotConfigured`。
- [ ] v1 access token 方法签名保持兼容；`GetTokenState` 命中 revoked/consumed 返回 `ErrTokenInvalid`。
- [ ] `go build ./...` 通过。

**Dependencies:** Issue #1, Issue #2
**Type:** backend
**Priority:** high
**SPEC Reference:** §4.1, §4.3, §5.2, §6.1

---

### Issue #7: refresh 能力并入 Manager

**Description:** 把 refresh 流程从独立 `RefreshManager` 迁移为 `*Manager` 方法，refresh token 以 `Kind=refresh` 的 `TokenState`（含 `RefreshInfo`）承载；`ConsumeTokenState` 复用于轮换；登出时内联撤销关联 refresh。

**Acceptance Criteria:**
- [ ] `Manager` 提供 `LoginWithRefresh` / `Refresh` / `RevokeRefreshToken` / `RevokeRefreshByLoginID`。
- [ ] refresh 轮换通过 `ConsumeTokenState` 原子消费旧 refresh。
- [ ] `Logout`/`LogoutByLoginID`/`LogoutByDevice` 按 `RefreshConfig.RevokeRefreshOnLogout` 撤销关联 refresh。
- [ ] 移除 `RefreshManager` / `NewRefreshManager`。
- [ ] 有效/过期/已撤销 refresh 的换新行为与原实现一致，有测试覆盖。
- [ ] 更新 `examples/refresh-token`；`go test ./core/... ./examples/...` 通过。

**Dependencies:** Issue #6, Issue #3
**Type:** backend
**Priority:** high
**SPEC Reference:** §4.2, §4.5

---

### Issue #8: nonce 能力并入 Manager

**Description:** 把 nonce 流程从独立 `NonceManager` 迁移为 `*Manager` 方法，nonce 以 `Kind=nonce` 的 `TokenState`（含 `NonceInfo`）承载，`ConsumeNonce` 返回 `*TokenState`，消费基于 `ConsumeTokenState` 原子完成。

**Acceptance Criteria:**
- [ ] `Manager` 提供 `GenerateNonce` / `ConsumeNonce`（返回 `*TokenState`）。
- [ ] nonce 消费基于 `ConsumeTokenState`，重放→`ErrNonceReplayed`、过期→`ErrNonceExpired`、不存在→`ErrNonceNotFound`、kind 不符→`ErrUnsupportedKind`。
- [ ] `Login` 的 `RequireNonce` 路径调用 `ConsumeNonce`（缺失→`ErrEmptyNonce`）。
- [ ] 移除 `NonceManager` / `NewNonceManager`。
- [ ] 更新 `examples/nonce`；`go test ./core/... ./examples/...` 通过。

**Dependencies:** Issue #6, Issue #3
**Type:** backend
**Priority:** high
**SPEC Reference:** §4.2, §5.1

---

### Issue #9: online MarkOnline/MarkOffline 并入 Manager

**Description:** 在 `Manager` 提供 `MarkOnline`/`MarkOffline`，作用于 `Kind=access` 的 `TokenState.Online` 投影；`ListTokenStates`/`LogoutByDevice` 按 kind 过滤 access。

**Acceptance Criteria:**
- [ ] `Manager` 提供 `MarkOnline(ctx, token, OnlineInfo)` / `MarkOffline(ctx, token)`。
- [ ] 目标非 access → `ErrUnsupportedKind`；过期/无效 → 对应错误。
- [ ] 发布 `EventOnline`/`EventOffline`；TTL 取剩余寿命。
- [ ] `ListTokenStates` 仅返回未过期的 access 状态。
- [ ] 更新 `examples/online-manager`；`go test ./core/... ./examples/...` 通过。

**Dependencies:** Issue #6
**Type:** backend
**Priority:** medium
**SPEC Reference:** §3.1, §5.3

---

## 阶段3 · 插件契约与 gRPC

### Issue #10: 抽出 plugins/contract.go 并改造 http/gin

**Description:** 新增 `plugins/contract.go` 定义框架无关的 token 提取（`TokenLookup`/`Getters`）与认证（`Authenticate`，含 `Kind==access` 校验），http/gin 复用，去除重复逻辑。

**Acceptance Criteria:**
- [ ] 新增 `plugins/contract.go`：`TokenLookup`/`Source`/`Getters`/`Resolve`/`Authenticate`。
- [ ] `plugins/http` 的 `Extractor` 基于 `TokenLookup.Resolve` 实现；gin 复用 http。
- [ ] `Authenticate` 要求 `Kind==access`，否则视为未授权。
- [ ] http/gin 现有接入方式与行为保持兼容。
- [ ] `go test ./plugins/...` 通过。

**Dependencies:** Issue #1, Issue #6
**Type:** backend
**Priority:** medium
**SPEC Reference:** §2.2

---

### Issue #11: gRPC server + client 拦截器

**Description:** 新增 `plugins/grpc`（独立 go module），提供 server 端认证拦截器（校验 metadata token）与 client 端拦截器（注入 token 到 outgoing metadata），复用 `plugins/contract.go`。

**Acceptance Criteria:**
- [ ] 提供 `UnaryServerInterceptor`（及可选 `StreamServerInterceptor`）：从 metadata 按可配置键（默认 `authorization`）提取 token。
- [ ] server 端认证通过后注入 `core.AuthContext` 到 handler context；要求 `Kind==access`；失败返回 `codes.Unauthenticated`。
- [ ] 提供 `UnaryClientInterceptor`（及可选 `StreamClientInterceptor`）：从 `TokenSource`（默认 `core.TokenFromContext`）取 token 注入 outgoing metadata；无 token 时透传不报错。
- [ ] `plugins/grpc` 为独立 go module（`replace` 指向本地 core）。
- [ ] bufconn 集成测试覆盖有效/无效/缺失 token；`go test ./plugins/grpc/...` 通过。

**Dependencies:** Issue #10
**Type:** infra
**Priority:** medium
**SPEC Reference:** §4.4

---

## 阶段4 · 审计

### Issue #12: 审计事件模型 + slog 监听器

**Description:** 新增 `audit/` 包：独立 `AuditEventType` + `AuditEvent` 结构，`Listener` 实现 `core.Listener` 从 `core.Event` 映射，默认提供 slog Sink。

**Acceptance Criteria:**
- [ ] 定义独立 `AuditEventType` 与 `AuditEvent`（类型、登录主体、token、device、IP、时间、结果、Detail）。
- [ ] `Listener` 实现 `core.Listener`，把 `core.Event` 映射为 `AuditEvent`；可注册到 `EventBus`/`AsyncEventBus`。
- [ ] 默认提供 `NewSlogSink`，并支持注入自定义 Sink。
- [ ] 登录/登出/刷新/踢下线/nonce 消费/上线/下线事件均可被捕获。
- [ ] 监听器 panic/Sink error 不影响主认证流程。
- [ ] `go test ./audit/...` 通过。

**Dependencies:** Issue #6
**Type:** backend
**Priority:** medium
**SPEC Reference:** §3.3, §5.4, §6.2

---

## 阶段5 · 第二阶段测试

### Issue #13: DistributedSession 语义与跨实例测试

**Description:** 明确 Session 在多实例下的共享语义并补测试：两个 Manager 共享同一 store 时 Session 读写一致、TTL 到期行为。

**Acceptance Criteria:**
- [ ] 文档明确 DistributedSession 与现有 `Session` 的关系（强化 store contract，不引入破坏性新 type）。
- [ ] 测试：两个独立 `Manager` 共享同一 store 时，一端写入的 Session 可被另一端读取。
- [ ] 测试：Session TTL 在 redis/database store 下到期后不可读（redis 用 miniredis，database 用 sqlite 内存）。
- [ ] 现有 `Session` API 行为保持不变。
- [ ] `go test ./...` 通过。

**Dependencies:** Issue #3, Issue #4, Issue #5
**Type:** backend
**Priority:** medium
**SPEC Reference:** §9.2

---

### Issue #14: redis/database 一致性测试

**Description:** 补齐 redis 与 database store 的 TTL、撤销、nonce 原子消费的一致性测试。

**Acceptance Criteria:**
- [ ] redis：access/refresh kind 的 TTL 到期后查询返回不存在。
- [ ] redis：nonce 二次消费返回失败（原子性）。
- [ ] database：按登录主体 + kind 撤销后 `FindTokenStates` 不再返回。
- [ ] database：nonce 二次消费返回失败。
- [ ] `go test ./storage/...` 通过。

**Dependencies:** Issue #4, Issue #5
**Type:** backend
**Priority:** medium
**SPEC Reference:** §9.2

---

## 阶段6 · 文档与示例

### Issue #15: 文档对齐代码 + token/config.go 归位

**Description:** 更新技术实现文档使其以代码为准（LoginSubject 入参、登出命名、统一模型），并把 token 配置归入 `token/config.go`（可选清理）。

**Acceptance Criteria:**
- [ ] 技术文档更新：`Login` 入参以 `loginID string` 为准、登出命名以 `LogoutByLoginID`/`LogoutByDevice` 为准，标注"已随实现演进"。
- [ ] 技术文档第 8/9/14 节与统一模型、统一 Store、统一 Manager 一致。
- [ ] （可选）`TokenConfig`/`JwtConfig` 及其 Option 迁移到 `token/config.go`，导出签名不变。
- [ ] `go build ./...` 与 `go test ./token/...` 通过。

**Dependencies:** Issue #6
**Type:** docs
**Priority:** low
**SPEC Reference:** §2.4

---

### Issue #16: 三阶段 README 与示例整合

**Description:** README 增加三阶段能力索引；新增 gRPC、审计示例；refresh/nonce/online 示例迁移到统一 Manager API。

**Acceptance Criteria:**
- [ ] README 增加三阶段能力索引（v1 核心 / 第二阶段扩展 / 第三阶段适配与审计）。
- [ ] 新增 `examples/grpc`、`examples/audit` 最小接入示例。
- [ ] README 中 refresh/nonce/online 示例使用合并后的统一 Manager API。
- [ ] 示例代码可通过 `go build ./...` 或 `go test ./...` 验证。

**Dependencies:** Issue #7, Issue #8, Issue #9, Issue #11, Issue #12
**Type:** docs
**Priority:** low
**SPEC Reference:** §2.4

---

## 依赖关系总览

```
#1 ─┬─> #2 ─┬─> #3 ─┐
    │       ├─> #4 ─┤
    │       └─> #5 ─┤
    └───────────────┼─> #6 ─┬─> #7 ──┐
                    │       ├─> #8 ──┤
                    │       ├─> #9 ──┤
                    │       ├─> #12 ─┤
                    │       └─> #15  │
                    │  #1+#6 ─> #10 ─> #11 ─┤
              #3+#4+#5 ─> #13          │
                 #4+#5 ─> #14          │
   #7+#8+#9+#11+#12 ──────────────────> #16
```

# PRD: better-token 三阶段补齐路线图

> 来源：`docs/better-token-technical-implementation.md`（v1 技术实现规格）
> 定位：基于现状补齐。本 PRD 不重写已实现能力，而是盘点三个阶段的现状，把剩余缺口拆成可独立实现的功能流程。

## Introduction

`better-token` 是一个框架无关、存储可插拔、以 `TokenState` 为统一状态模型的 Go 认证授权内核。技术实现文档第 26 节将能力划分为三个阶段（v1 必须 / 第二阶段 / 第三阶段）。

经过对当前代码库的盘点，v1 核心与第二阶段能力已基本落地：

- `token` 包：JWT 签发/解析/校验、多种 TokenStyle（simple/uuid/timestamp/hash/tiktok）、`TokenConfig`/`JwtConfig`。
- `core` 包：`TokenState`/`TokenKind`/`TokenStatus`、`Manager` 登录态流程、`RefreshManager`、`NonceManager`、在线查询/踢下线、`AsyncEventBus`、`Store` 接口、`Authorizer`、`Session`、`AuthContext`、`Runtime` 等。
- `storage`：memory / redis / database 三种实现，均覆盖 TokenState + Session + Refresh + Nonce。
- `plugins`：http、gin 中间件，含 Header/Authorization/Cookie/Query 提取顺序。
- `rbac`：内置 role/permission Authorizer。
- `examples`：basic / nonce / online-manager / refresh-token。

本 PRD 聚焦尚未完成、需补齐、需重构对齐的部分。它是路线图版规划，不要求一次迭代内全部完成。

### 关键设计决策（已确认）

- **统一状态模型**：采用技术文档第 8、9 节的**统一 `TokenState` 模型**——用 `TokenKind`（access/refresh/nonce）+ `TokenStatus`（active/revoked/consumed）+ 可选 `RefreshInfo`/`NonceInfo`/`OnlineInfo` 表达差异，**移除独立的 `RefreshTokenState` / `NonceState` 类型**。`Store` 随之收敛为统一的 TokenState + Session 方法（带 `kinds ...TokenKind` 过滤 + 原子消费），**移除独立的 `RefreshStore` / `NonceStore`**，并重写 memory/redis/database 三个后端。
- **统一 Manager**：将当前独立的 `RefreshManager` / `NonceManager` 能力**合并回核心 `Manager`**，按技术文档第 14 节的统一 `TokenManager` 接口实现（含 RBAC `Check*`），**直接移除**旧 Manager 类型。
- **LoginSubject / 命名分歧**：`Manager` 对外仍以 `loginID string` 为入参、`LogoutByLoginID` 等命名，仅更新文档使其与代码一致。
- **审计**：使用独立的 `AuditEventType`，并默认提供 slog 实现。
- **第三阶段框架适配**：本轮只实现 **gRPC**，不做 Echo / Fiber。

## Goals

- 保持 v1 access token 登录态的核心行为与 `plugins/http`、`plugins/gin` 接入方式兼容。
- 补齐 v1 阶段缺失的 `plugins/contract.go` 共享插件契约。
- 采用统一 `TokenState` 模型与统一 `Store` 接口，移除独立的 `RefreshTokenState`/`NonceState`/`RefreshStore`/`NonceStore`，并重写三个存储后端。
- 把 refresh / nonce / online 能力收敛到统一的 `Manager` API（统一 `TokenManager` 接口），移除独立 Manager 的分裂结构。
- 更新技术实现文档，使其作为与代码一致的单一可信来源。
- 补齐第二阶段 DistributedSession 语义与跨实例一致性测试。
- 确认并补齐 redis / database 在 TTL、撤销、Nonce 原子消费上的测试覆盖。
- 实现第三阶段的 gRPC 框架适配。
- 提供第三阶段的高级审计事件能力。
- 整合三个阶段的文档与示例，使新用户能快速理解整体架构与接入路径。

## User Stories

<!-- ===== 第一阶段（v1）补齐 ===== -->

### US-001: 提取 plugins 共享契约
**Description:** As a plugin author, I want 一个 `plugins/contract.go` 定义各框架插件复用的提取与认证契约 so that http、gin、gRPC 及后续插件不重复实现相同逻辑。

**Acceptance Criteria:**
- [ ] 新增 `plugins/contract.go`，定义跨框架共享的 token 提取契约（如 `TokenExtractor` 接口与认证流程辅助类型）。
- [ ] `plugins/http` 与 `plugins/gin` 复用该契约，去除重复定义。
- [ ] 现有 `plugins/http`、`plugins/gin` public API 保持兼容。
- [ ] `go build ./...` 通过。
- [ ] `go test ./plugins/...` 通过。

### US-002: 文档对齐代码（LoginSubject 与命名）
**Description:** As a library maintainer, I want 把代码与技术文档在 LoginSubject 入参、登出命名上的分歧记录为以代码为准 so that 文档不再误导接入者。

**Acceptance Criteria:**
- [ ] 技术实现文档更新：`Manager.Login` 入参以 `loginID string` 为准（保留内部 `LoginSubject` 模型说明）。
- [ ] 技术实现文档更新：登出方法命名以 `LogoutByLoginID` / `LogoutByDevice` 为准。
- [ ] 文档中标注这些点为"已随实现演进"。
- [ ] 本故事不改动任何已实现 public API（仅文档变更）。

### US-003: token 配置文件归位（可选清理）
**Description:** As a library maintainer, I want token 配置按文档结构归入 `token/config.go` so that 包结构与技术文档一致、可读性更好。

**Acceptance Criteria:**
- [ ] 将 `TokenConfig`/`JwtConfig` 及其 Option 迁移到 `token/config.go`。
- [ ] `token` 包对外导出的类型与函数签名保持不变。
- [ ] `go build ./...` 与 `go test ./token/...` 通过。

<!-- ===== 统一状态模型与 Manager 重构 ===== -->

### US-004: 统一 TokenState 状态模型
**Description:** As a library maintainer, I want `TokenState` 成为统一状态模型 so that access/refresh/nonce/online 用同一模型表达，不再有分裂的 state 类型。

**Acceptance Criteria:**
- [ ] `TokenState` 增加 `Kind TokenKind`（access/refresh/nonce）、`Status TokenStatus`（active/revoked/consumed）。
- [ ] `TokenState` 增加可选 `Refresh *RefreshInfo`、`Nonce *NonceInfo`、`Online *OnlineInfo`。
- [ ] 提供 `IsExpired/IsRevoked/IsConsumed/IsActive/Touch/MarkRevoked/MarkConsumed/MarkOnline/MarkOffline` 方法。
- [ ] 移除独立的 `RefreshTokenState` / `NonceState` 类型，调用方改用 `TokenState`。
- [ ] `Clone()` 深拷贝新增的指针字段。
- [ ] `go build ./...` 通过。

### US-005: 统一 Store 接口
**Description:** As a library maintainer, I want `Store` 收敛为统一的 TokenState + Session 端口 so that 不再为 refresh/nonce 单列存储接口。

**Acceptance Criteria:**
- [ ] `Store` 的 `FindTokenStates` / `DeleteTokenStates` 增加 `kinds ...TokenKind` 过滤参数。
- [ ] `Store` 增加原子消费方法 `ConsumeTokenState`（供 nonce/refresh 轮换使用）。
- [ ] 移除 `RefreshStore` / `NonceStore` / `StoreWithRefresh` / `StoreWithNonce` / `NonceConsumer`。
- [ ] `go build ./...` 通过。

### US-006: 重写三个存储后端为统一 Store
**Description:** As a library maintainer, I want memory/redis/database 实现统一 Store so that 三种后端在统一模型下一致工作。

**Acceptance Criteria:**
- [ ] memory store 实现统一 Store，按 kind 过滤、`ConsumeTokenState` 在锁内原子完成。
- [ ] redis store 实现统一 Store，`ConsumeTokenState` 使用原子语义（Lua 或 SETNX 消费标记）。
- [ ] database store 实现统一 Store，`token_states` 表含 kind/status 列，`ConsumeTokenState` 用 `UPDATE ... WHERE status='active'` 并校验影响行数。
- [ ] 三个后端原有 TokenState/Session 行为不回退。
- [ ] `go test ./storage/...` 通过。

### US-007: 定义统一 TokenManager 接口
**Description:** As a library maintainer, I want 在 `core` 中定义统一的 `TokenManager` 接口 so that refresh / nonce / online / 授权能力作为 Manager 的方法暴露。

**Acceptance Criteria:**
- [ ] 在 `core` 定义 `TokenManager` 接口，覆盖技术文档第 14 节的方法集（access + refresh + nonce + online + session + `Check*`）。
- [ ] `*Manager` 声明实现该接口（编译期断言 `var _ TokenManager = (*Manager)(nil)`）。
- [ ] v1 access token 方法签名保持兼容。
- [ ] `go build ./...` 通过。

### US-008: 将 RefreshToken 能力合并进 Manager
**Description:** As an application developer, I want refresh 能力直接通过 `Manager` 调用 so that 我无需额外构造 `RefreshManager`。

**Acceptance Criteria:**
- [ ] `Manager` 提供 refresh 方法，refresh token 以 `Kind=refresh` 的 `TokenState`（含 `RefreshInfo`）承载。
- [ ] 移除 `RefreshManager` / `NewRefreshManager`，`core` 内不再要求独立构造。
- [ ] 有效/过期/已撤销 refresh token 的换新行为与原实现一致，且有测试覆盖。
- [ ] 更新 `examples/refresh-token` 使用合并后的 Manager API。
- [ ] `go test ./core/... ./examples/...` 通过。

### US-009: 将 Nonce 能力合并进 Manager
**Description:** As an application developer, I want nonce 能力直接通过 `Manager` 调用 so that 我无需额外构造 `NonceManager`。

**Acceptance Criteria:**
- [ ] `Manager` 提供 nonce 方法，nonce 以 `Kind=nonce` 的 `TokenState`（含 `NonceInfo`）承载。
- [ ] 移除 `NonceManager` / `NewNonceManager`，`core` 内不再要求独立构造。
- [ ] nonce 一次性消费基于 `ConsumeTokenState` 原子完成，重放/过期返回明确错误，且有测试覆盖。
- [ ] 更新 `examples/nonce` 使用合并后的 Manager API。
- [ ] `go test ./core/... ./examples/...` 通过。

### US-010: 将 Online 标记纳入统一 Manager
**Description:** As an application developer, I want 通过 `Manager` 标记 token 上线/下线 so that 在线投影能力与统一接口一致。

**Acceptance Criteria:**
- [ ] `Manager` 提供 `MarkOnline` / `MarkOffline` 方法，作用于 `Kind=access` 的 `TokenState.Online` 投影。
- [ ] 现有 `ListTokenStates` / `LogoutByDevice` 行为保持不变（在统一模型下按 kind 过滤 access）。
- [ ] `MarkOnline` / `MarkOffline` 有测试覆盖。
- [ ] 更新 `examples/online-manager` 反映统一 API。
- [ ] `go test ./core/... ./examples/...` 通过。

<!-- ===== 第二阶段补齐 ===== -->

### US-011: 明确 DistributedSession 语义
**Description:** As a distributed application developer, I want 明确 Session 在多实例下的共享语义与命名 so that 我清楚跨服务读写 Session 的一致性边界。

**Acceptance Criteria:**
- [ ] 在文档中明确 DistributedSession 与现有 `Session` 的关系（是否引入独立 public type，或仅强化 store contract）。
- [ ] 明确多 `Manager` 实例共享同一 store 时的 Session 读写一致性约定。
- [ ] 现有 `Session` API 行为保持不变。
- [ ] `go build ./...` 通过。

### US-012: DistributedSession 跨实例测试
**Description:** As a library maintainer, I want 跨实例 Session 共享有测试覆盖 so that 多实例部署行为可被验证。

**Acceptance Criteria:**
- [ ] 新增测试：两个独立 `Manager` 实例共享同一 store 时，一端写入的 Session 可被另一端读取。
- [ ] 新增测试：Session TTL 在 redis / database store 下到期后不可读。
- [ ] `go test ./...` 通过。

### US-013: redis / database 一致性测试补齐
**Description:** As a library maintainer, I want redis 与 database store 的 TTL、撤销、Nonce 原子消费有明确测试 so that 生产存储行为可信。

**Acceptance Criteria:**
- [ ] redis store：TokenState（access/refresh kind）TTL 到期后查询返回不存在的测试。
- [ ] redis store：nonce 二次消费返回失败（原子性）的测试。
- [ ] database store：按登录主体 + kind 撤销后 `FindTokenStates` 不再返回的测试。
- [ ] database store：nonce 二次消费返回失败的测试。
- [ ] `go test ./storage/...` 通过。

<!-- ===== 第三阶段补齐 ===== -->

### US-014: gRPC 拦截器插件
**Description:** As a gRPC service developer, I want 一套 gRPC 认证拦截器（server + client）so that 我能在 gRPC 服务中校验 token，并在客户端自动注入 token。

**Acceptance Criteria:**
- [ ] 新增 `plugins/grpc`，提供 `UnaryServerInterceptor`（及可选 `StreamServerInterceptor`）。
- [ ] 提供 `UnaryClientInterceptor`（及可选 `StreamClientInterceptor`），把 token 注入 outgoing metadata。
- [ ] server 端从 metadata 按配置键提取 token（默认 `authorization`，键名可配置）。
- [ ] server 端认证通过后将 `core.AuthContext` 注入 handler context，业务可用 `core.RequireSubject` 读取。
- [ ] server 端要求 token 的 `Kind == access`；未登录/无效返回 `codes.Unauthenticated`。
- [ ] client 端 token 来源可配置（默认从 `core.TokenFromContext` 取），无 token 时透传不报错。
- [ ] 复用 `plugins/contract.go` 的提取契约（适配 metadata 来源）。
- [ ] `go test ./plugins/grpc/...` 通过。

### US-015: 审计事件模型
**Description:** As a security-conscious developer, I want 一个结构化审计事件模型 so that 登录态关键操作可被记录与追溯。

**Acceptance Criteria:**
- [ ] 定义独立的 `AuditEventType` 与审计事件结构，至少包含事件类型、登录主体、token、device、时间、来源 IP（可选）、结果。
- [ ] 审计事件从现有 `core.Event` 映射而来，不破坏现有 `EventBus`。
- [ ] `go build ./...` 通过。

### US-016: 审计事件监听器
**Description:** As an operator, I want 一个可插拔的审计监听器 so that 我能把审计事件落到日志或外部系统。

**Acceptance Criteria:**
- [ ] 提供一个审计 `Listener` 实现，可注册到 `EventBus`/`AsyncEventBus`。
- [ ] 默认提供 slog Sink 实现，并支持注入自定义 Sink。
- [ ] 登录、登出、刷新、踢下线、Nonce 消费事件均可被审计监听器捕获。
- [ ] 监听器 panic 不影响主认证流程（与 AsyncEventBus 行为一致）。
- [ ] `go test ./audit/...` 通过。

<!-- ===== 文档整合 ===== -->

### US-017: 三阶段文档与示例整合
**Description:** As a new user, I want 一份覆盖三个阶段能力的整合文档与可运行示例 so that 我能快速理解整体架构并完成接入。

**Acceptance Criteria:**
- [ ] README 增加三阶段能力索引（v1 核心 / 第二阶段扩展 / 第三阶段适配与审计）。
- [ ] 新增 gRPC 的最小接入示例。
- [ ] 新增审计监听器接入示例。
- [ ] README 中的 refresh / nonce / online 示例使用合并后的统一 Manager API。
- [ ] 示例代码可通过 `go build ./...` 或 `go test ./...` 验证。

## Functional Requirements

<!-- 第一阶段 -->
- FR-1: 系统必须提供 `plugins/contract.go`，定义跨框架共享的 token 提取契约。
- FR-2: 系统必须保持现有 `plugins/http`、`plugins/gin` 的接入方式兼容。
- FR-3: 系统必须更新技术实现文档，使 LoginSubject 入参与登出命名以代码为准。

<!-- 统一状态模型与 Manager 重构 -->
- FR-4: 系统必须以统一 `TokenState`（`TokenKind` + `TokenStatus` + 可选 `RefreshInfo`/`NonceInfo`/`OnlineInfo`）表达 access/refresh/nonce/online。
- FR-5: 系统必须移除独立的 `RefreshTokenState` / `NonceState` 类型。
- FR-6: 系统必须将 `Store` 收敛为统一的 TokenState + Session 接口（`FindTokenStates`/`DeleteTokenStates` 支持 `kinds` 过滤）。
- FR-7: 系统必须提供原子 `ConsumeTokenState` 能力，并移除 `RefreshStore`/`NonceStore`。
- FR-8: 系统必须重写 memory/redis/database 三个后端以实现统一 Store。
- FR-9: 系统必须定义统一的 `TokenManager` 接口并由 `*Manager` 实现（含 `Check*`）。
- FR-10: 系统必须把 RefreshToken 能力作为 `Manager` 方法提供（refresh 为 `Kind=refresh` 的 TokenState）。
- FR-11: 系统必须把 Nonce 能力作为 `Manager` 方法提供（nonce 为 `Kind=nonce` 的 TokenState，原子消费）。
- FR-12: 系统必须把 Online 上线/下线标记作为 `Manager` 方法提供（作用于 `Kind=access` 的 `Online` 投影）。
- FR-13: 系统必须更新 refresh / nonce / online 示例以使用合并后的 API。

<!-- 第二阶段 -->
- FR-14: 系统必须明确 DistributedSession 的跨实例一致性语义。
- FR-15: 系统必须为多实例共享 store 的 Session 读写提供测试覆盖。
- FR-16: 系统必须为 redis store 的 TTL 到期与 Nonce 原子消费提供测试覆盖。
- FR-17: 系统必须为 database store 的撤销与 Nonce 消费提供测试覆盖。

<!-- 第三阶段 -->
- FR-18: 系统必须提供 gRPC server 端认证拦截器。
- FR-19: gRPC server 拦截器必须从 metadata 按可配置键提取 token。
- FR-20: gRPC server 拦截器认证通过后必须注入 `core.AuthContext` 到 handler context。
- FR-21: gRPC server 拦截器在未登录/无效 token 时必须返回 `codes.Unauthenticated`。
- FR-22: 系统必须提供 gRPC client 端拦截器，把 token 注入 outgoing metadata。
- FR-23: 系统必须提供独立 `AuditEventType` 的结构化审计事件模型。
- FR-24: 系统必须提供可插拔的审计事件监听器，并默认提供 slog 实现。
- FR-25: 审计监听器异常不得中断主认证流程。

<!-- 文档 -->
- FR-26: 系统必须提供覆盖三个阶段能力的 README 索引。
- FR-27: 系统必须为 gRPC 插件与审计监听器提供最小接入示例。

## Non-Goals

- 不改动 v1 核心 access token 登录态的 `Manager` 方法签名（`Login`/`Logout`/`GetTokenState`/`Renew`/`Session`/`Check*` 入参出参保持兼容）；但 `TokenState`、`Store` 的数据模型按统一模型有意重塑，属预期破坏。
- 本轮不实现 Echo、Fiber 框架适配。
- 不实现 OAuth2 / SSO / 纯无状态 JWT 模式。
- 不强制引入业务 RBAC 数据库表。
- 不提供前端 UI。
- 不要求一次迭代内完成全部三阶段路线图。
- 不为审计事件实现分布式队列、事件持久化或重试（沿用现有 EventBus 能力边界）。

## Technical Considerations

- 统一 `TokenState` 后，`RefreshManager` / `NonceManager` / `RefreshTokenState` / `NonceState` / `RefreshStore` / `NonceStore` 全部移除；调用方与示例需同步迁移，文档给出迁移示例。
- 统一 `Store` 需提供原子 `ConsumeTokenState`：redis 用 Lua/SETNX，database 用 `UPDATE ... WHERE status='active'` 校验影响行数，memory 在锁内完成。
- database 的 `token_states` 表需含 `kind`/`status` 列（技术文档第 22 节已如此设计），便于按 kind 过滤与原子消费。
- `plugins/contract.go` 应只依赖 `core`，不依赖任何具体框架，作为各插件（含 gRPC）的共享底座。
- gRPC 插件建议独立 go module 依赖，避免给核心库引入强制的 gRPC 依赖。
- 审计事件应从现有 `core.Event` 映射，以独立 `AuditEventType` + 监听器形式落地，避免引入并行的事件机制。
- DistributedSession 优先强化现有 `Session` 的 store contract，而非引入破坏性的新 public type。
- 一致性测试在 redis 上可借助内存版 redis（如 miniredis）或集成测试标签；database 可用 sqlite 内存库或测试容器。

## Success Metrics

- `go build ./...` 与 `go test ./...` 全绿。
- refresh / nonce / online 能力可仅通过 `Manager` 调用完成，示例不再构造独立 Manager。
- gRPC 插件有可运行的最小接入示例。
- redis / database 的 TTL、撤销、Nonce 原子性均有明确测试用例。
- 新用户能依据 README 在 10 分钟内完成 http / gin / gRPC 任一接入。
- 默认配置下 v1 行为保持一致，已有示例不被破坏。

## Open Questions

全部关键决策已确认：旧 `RefreshManager`/`NonceManager` 直接移除；统一 `TokenManager` 纳入 `Check*`；审计用独立 `AuditEventType` + 默认 slog 实现；采用文档第 8+9 节的完整统一模型并重写三个存储后端；`GetTokenState` 命中 `revoked`/`consumed` 返回 `ErrTokenInvalid`；refresh 登录入口命名 `LoginWithRefresh`；`ConsumeTokenState` 复用于 refresh 轮换；gRPC 同时提供 server + client 端拦截器。

无剩余阻塞性开放项，可进入 Issue 拆解。

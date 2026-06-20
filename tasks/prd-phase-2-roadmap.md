# PRD: better-token 第二阶段功能路线图

## Introduction

第二阶段目标是在保持当前 public API 兼容的前提下，把 `better-token` 从“第一版核心登录态库”推进到“更适合生产接入的认证授权内核”。本阶段重点补齐 RefreshToken、Nonce 防重放、在线 token 管理、分布式 Session、异步事件与 RBAC 辅助能力，同时通过 README 和 examples 降低接入成本。

本 PRD 是路线图版规划：它定义第二阶段完整能力边界，不要求所有功能必须在一个迭代内一次性实现。

## Goals

- 保持现有 `core.Manager`、`core.Store`、`plugins/http`、`plugins/gin` 的 public API 兼容。
- 增量提供 RefreshToken 流程，支持 access token 过期后的安全换新。
- 增量提供 Nonce 能力，支持一次性 nonce 校验和防重放。
- 提供 OnlineManager 能力，支持查询在线 token、按设备踢下线。
- 提供 DistributedSession 能力，支持跨服务共享 Session 语义。
- 提供 AsyncEventBus 能力，支持异步事件发布与可观测错误处理。
- 提供 RBAC 辅助模块，但不强绑定业务数据库模型。
- 补齐 README、examples 和测试，使新用户能在 10 分钟内接入第二阶段能力。

## User Stories

### US-001: RefreshToken 数据模型与端口

**Description:** As a library maintainer, I want RefreshToken 有独立数据模型和存储端口 so that refresh 流程不污染现有 TokenState 状态机。

**Acceptance Criteria:**
- [ ] 定义 RefreshToken 状态模型，包含 refresh token、loginID、loginType、device、createdAt、expiresAt、revokedAt。
- [ ] 定义 RefreshToken 存储端口，支持保存、读取、删除、按登录主体删除。
- [ ] 现有 `core.Store` 方法签名保持兼容。
- [ ] Go tests pass.

### US-002: 签发 RefreshToken

**Description:** As an application developer, I want 登录时可选签发 RefreshToken so that 客户端能在 access token 过期后换新。

**Acceptance Criteria:**
- [ ] 默认登录流程不签发 RefreshToken。
- [ ] 启用 RefreshToken 配置后，登录结果可返回 refresh token。
- [ ] RefreshToken TTL 与 access token TTL 可分别配置。
- [ ] Go tests pass.

### US-003: 使用 RefreshToken 换新 Access Token

**Description:** As an API user, I want 用合法 refresh token 换新 access token so that 用户无需重新登录。

**Acceptance Criteria:**
- [ ] 有效 refresh token 可换新 access token。
- [ ] 过期 refresh token 返回明确错误。
- [ ] 已撤销 refresh token 返回明确错误。
- [ ] 换新后的 access token 可被现有中间件识别。
- [ ] Go tests pass.

### US-004: RefreshToken 撤销

**Description:** As an application developer, I want 登出时撤销相关 refresh token so that 退出登录后不能继续换新 access token。

**Acceptance Criteria:**
- [ ] 单 token 登出可撤销关联 refresh token。
- [ ] 按 loginID 登出可撤销该主体下所有 refresh token。
- [ ] 撤销后再次刷新返回明确错误。
- [ ] Go tests pass.

### US-005: Nonce 生成与消费

**Description:** As an application developer, I want 生成并消费一次性 nonce so that 敏感认证请求可以防止重放。

**Acceptance Criteria:**
- [ ] 提供 nonce 生成 API。
- [ ] nonce 可配置 TTL。
- [ ] 第一次消费合法 nonce 返回成功。
- [ ] 第二次消费同一 nonce 返回重放错误。
- [ ] Go tests pass.

### US-006: 登录流程接入 Nonce 校验

**Description:** As an application developer, I want 登录时可选校验 nonce so that 重放的登录请求无法复用。

**Acceptance Criteria:**
- [ ] 默认登录流程不要求 nonce。
- [ ] 启用 nonce 配置后，缺失 nonce 返回明确错误。
- [ ] 启用 nonce 配置后，重复 nonce 返回明确错误。
- [ ] Go tests pass.

### US-007: 在线 Token 查询

**Description:** As an application developer, I want 查询某个 loginID 的在线 token so that 管理后台或业务服务能展示当前登录设备。

**Acceptance Criteria:**
- [ ] 提供按 loginID 和 loginType 查询在线 TokenState 的 API。
- [ ] 返回结果不包含已过期 token。
- [ ] 返回结果包含 device、createdAt、lastActiveAt、expiresAt。
- [ ] Go tests pass.

### US-008: 按设备踢下线

**Description:** As an application developer, I want 按 device 踢下线 so that 用户可以只退出某台设备。

**Acceptance Criteria:**
- [ ] 提供按 loginID、loginType、device 删除 token 的 API。
- [ ] 被踢下线 token 后续通过中间件访问返回 401。
- [ ] 其他 device 的 token 不受影响。
- [ ] Go tests pass.

### US-009: DistributedSession 语义

**Description:** As a distributed application developer, I want Session 支持跨服务共享 so that 多实例部署下 Session 读写行为一致。

**Acceptance Criteria:**
- [ ] 明确 DistributedSession 与现有 Session 的关系和命名。
- [ ] Redis/database store 下 Session TTL 行为有测试覆盖。
- [ ] 多 Manager 实例共享同一 store 时能读取同一 Session。
- [ ] Go tests pass.

### US-010: 异步事件总线

**Description:** As an application developer, I want 异步处理登录、登出、刷新、踢下线事件 so that 事件监听不阻塞主认证流程。

**Acceptance Criteria:**
- [ ] 提供 AsyncEventBus 实现。
- [ ] 异步队列容量可配置。
- [ ] listener panic 不会导致主流程 panic。
- [ ] listener error 可通过错误处理器观察。
- [ ] Go tests pass.

### US-011: RBAC 辅助模块

**Description:** As an application developer, I want 使用内置 RBAC helper so that 小项目可以快速接入角色和权限判断。

**Acceptance Criteria:**
- [ ] 提供可选 RBAC 包，不强制替换现有 Authorizer。
- [ ] 支持给 loginID 绑定 role。
- [ ] 支持给 role 绑定 permission。
- [ ] `CheckRole` 和 `CheckPermission` 能通过 RBAC helper 返回结果。
- [ ] Go tests pass.

### US-012: 第二阶段文档与示例

**Description:** As a new user, I want 通过 README 和 examples 快速理解第二阶段能力 so that 我能在 10 分钟内完成最小接入。

**Acceptance Criteria:**
- [ ] README 增加第二阶段能力索引。
- [ ] examples 增加 refresh token 示例。
- [ ] examples 增加 nonce 示例。
- [ ] examples 增加 online manager 或踢下线示例。
- [ ] 示例代码可通过 `go test ./...` 或对应运行检查。

## Functional Requirements

- FR-1: 系统必须保持现有 `core.Manager` 构造方式兼容。
- FR-2: 系统必须保持现有 `core.Store` 接口兼容。
- FR-3: 系统必须将 RefreshToken 能力设计为增量可选能力。
- FR-4: 系统必须支持配置 RefreshToken TTL。
- FR-5: 系统必须支持撤销单个 RefreshToken。
- FR-6: 系统必须支持按登录主体撤销 RefreshToken。
- FR-7: 系统必须支持使用 RefreshToken 换新 access token。
- FR-8: 系统必须拒绝已过期 RefreshToken。
- FR-9: 系统必须拒绝已撤销 RefreshToken。
- FR-10: 系统必须提供 Nonce 生成能力。
- FR-11: 系统必须提供 Nonce 一次性消费能力。
- FR-12: 系统必须拒绝重复消费的 Nonce。
- FR-13: 系统必须提供按登录主体查询在线 TokenState 的能力。
- FR-14: 系统必须过滤在线查询中的过期 TokenState。
- FR-15: 系统必须提供按 device 踢下线能力。
- FR-16: 系统必须保留现有 Session API 行为。
- FR-17: 系统必须定义 DistributedSession 的跨实例一致性要求。
- FR-18: 系统必须提供异步 EventBus 实现。
- FR-19: 系统必须让异步 EventBus 的监听器错误可观测。
- FR-20: 系统必须提供可选 RBAC helper。
- FR-21: 系统必须让 RBAC helper 适配现有 `core.Authorizer`。
- FR-22: 系统必须提供第二阶段 README 文档。
- FR-23: 系统必须提供第二阶段 examples。
- FR-24: 系统必须为 memory、redis、database 存储相关能力补充测试。

## Non-Goals

- 第二阶段不实现 OAuth2。
- 第二阶段不实现 SSO。
- 第二阶段不实现纯无状态 JWT 模式。
- 第二阶段不强制引入业务 RBAC 数据库表。
- 第二阶段不移除或重命名第一版 public API。
- 第二阶段不提供前端 UI。
- 第二阶段不要求一次性完成所有路线图功能。

## Technical Considerations

- RefreshToken、Nonce、OnlineManager、DistributedSession 优先设计为可选扩展，避免扩大第一版核心接口的破坏面。
- 若必须扩展存储能力，优先通过新接口或 capability detection 实现，避免修改现有 `core.Store`。
- Redis 版 Nonce 消费应使用原子语义，避免并发重放通过。
- database store 需要明确 refresh token、nonce、online index 的表结构或 provider 责任边界。
- AsyncEventBus 需要提供关闭/flush 能力，方便测试与服务优雅退出。
- RBAC helper 应作为默认可用的小型实现，不替代业务系统自己的 Authorizer。
- 文档应明确 access token、refresh token、server-side TokenState 三者关系。

## Success Metrics

- 新用户能在 10 分钟内根据 README 和 examples 完成 refresh token 最小接入。
- `go test ./...` 覆盖第二阶段核心能力并通过。
- Redis/database 下 TTL、撤销、索引一致性有明确测试用例。
- 第二阶段新增能力不破坏第一版 README 中已有示例。
- 默认配置下行为与第一版保持一致。

## Open Questions

- RefreshToken 是否需要默认启用轮换策略，还是先提供可配置选项？
- RefreshToken 是否应与 access token 一一绑定，还是与 loginID/device 绑定？
- Nonce 是否只用于登录流程，还是提供通用防重放 API？
- DistributedSession 是否需要单独 public type，还是只强化现有 Session 的 store contract？
- OnlineManager 应放在 `core` 包内，还是作为 `core/online` 可选包？
- RBAC helper 是否需要持久化 store，还是第二阶段先提供 memory/helper 版本？

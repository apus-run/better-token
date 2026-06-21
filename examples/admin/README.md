# better-token Admin Example

这是一个可直接运行的后台业务示例，不是孤立的 API 展示。SQLite 保存后台用户，better-token 负责完整认证状态；页面使用 Gin 服务端模板和 HTMX 渐进增强。

## 运行

```bash
# SQLite 用户表 + 内存登录态（默认）
go run ./examples/admin

# SQLite 用户表 + SQLite 持久化登录态
go run ./examples/admin -token-store sqlite -db ./admin.db -addr :8080
```

打开 <http://localhost:8080>，默认账号为 `admin / admin123` 和 `operator / operator123`。生产环境必须修改初始密码，并在 TLS 下把 Cookie 的 `Secure` 属性设为 `true`。

参数：

- `-addr`：监听地址，默认 `:8080`。
- `-db`：SQLite 文件，默认 `admin.db`。
- `-token-store`：`memory` 或 `sqlite`，默认 `memory`。

## 登录形态（登录框选项卡）

登录框用选项卡演示同一套核心库能支撑的五种真实业务登录形态。它们底层是几个共享同一 store 的 `core.Manager`，只在 `Config` 上有差异：

| 选项卡 | mode | 核心机制 | 真实场景 |
| --- | --- | --- | --- |
| 记住我（默认） | `refresh` | `LoginWithRefresh`，签发 access + 长效 refresh，支持轮转 | 移动端 App、Web「记住我」长会话 |
| 普通登录 | `basic` | `Login`，仅单个 access token，多端并发 | 最常见的后台 / Web 登录 |
| 安全登录 | `nonce` | 登录页预发一次性 nonce（`GenerateNonce`），提交时由 `RequireNonce` 的 Manager 消费 | 后台、支付确认等高安全入口，登录请求防重放 |
| 单设备登录 | `single` | `Concurrent=false`，新登录挤掉该账号其它设备 | 银行、敏感系统的单会话策略（异地登录挤下线） |
| 共享会话 | `shared` | `ShareToken=true`，同账号重复登录复用同一 token | 单点会话复用 |

> 安全登录可这样验证防重放：刷新登录页拿到新 nonce 后提交一次会成功，再用同一 nonce 提交会被拒绝（提示一次性 nonce 已被使用）。登录后仪表盘「当前会话」会显示本次使用的登录方式。

## 串联的能力

1. 登录框按选项卡切换五种登录形态（记住我 / 普通 / 安全 / 单设备 / 共享会话），见上表。
2. 登录按所选形态签发 access（及可选 refresh）token；HttpOnly Cookie 由 Gin 插件提取。
3. 受保护路由通过 Gin middleware 写入 `core.AuthContext`。
4. RBAC 中 admin 使用通配权限，operator 仅有 dashboard/session 权限；页面同时执行 `CheckPermission`、`CheckRole`、`CheckAll` 和 `CheckAny`。
5. Session 保存用户主题和运维备注。
6. 页面可续期 token、维护在线/离线投影、按设备踢下线、全端退出。
7. refresh 操作同时旋转 access/refresh，旧 token 被消费；也可单独调用 `RevokeRefreshToken`。
8. 登录、刷新、续期、上线、下线和登出事件经 `AsyncEventBus` 写入审计面板。
9. `memory` 与 `storage/database` 两种 better-token Store 使用相同业务流程。
10. 场景实验室页面可对比 admin/operator 权限矩阵，并模拟同一用户多设备登录、按设备踢下线和全端清理。
11. 场景实验室覆盖非 JWT token 创建与使用：`simple`、`timestamp`、`uuid`、`hash`、`tiktok` 都可作为 access/refresh TokenValue 发放到模拟设备。

HTMX 从 CDN 加载；即使 CDN 不可用，所有表单仍可通过普通 HTTP 提交完成操作。

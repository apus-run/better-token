package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/apus-run/better-token/audit"
	"github.com/apus-run/better-token/core"
	btgin "github.com/apus-run/better-token/plugins/gin"
	"github.com/apus-run/better-token/rbac"
	dbstore "github.com/apus-run/better-token/storage/database"
	"github.com/apus-run/better-token/storage/memory"
	"github.com/apus-run/better-token/token"
	"github.com/apus-run/gala/components/db"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const (
	accessCookie  = "better_token"
	refreshCookie = "better_refresh"
	loginType     = "admin"
	dashboardPath = "/admin/dashboard.html"
	scenarioPath  = "/admin/scenarios.html"
)

type config struct {
	addr       string
	database   string
	tokenStore string
}

type user struct {
	ID           uint   `gorm:"primaryKey"`
	Username     string `gorm:"size:64;uniqueIndex;not null"`
	PasswordHash string `gorm:"size:255;not null"`
	DisplayName  string `gorm:"size:100;not null"`
	Role         string `gorm:"size:32;not null"`
	CreatedAt    time.Time
}

type memoryAuditSink struct {
	mu     sync.RWMutex
	events []audit.AuditEvent
}

type seededUser struct {
	Username    string
	Password    string
	DisplayName string
	Role        string
}

type authorityScenario struct {
	Label     string
	Expected  string
	Authority core.Authority
	Allowed   bool
}

type userScenario struct {
	User        user
	LoginID     string
	Authorities []core.Authority
	Checks      []authorityScenario
	States      []*core.TokenState
	Session     *core.Session
}

type tokenStyleScenario struct {
	Style   token.TokenStyle
	Label   string
	Usage   string
	Sample  string
	Length  int
	Pattern string
}

type jwtCustomDataScenario struct {
	Token        string
	TokenPreview string
	UserID       string
	TrackID      string
	Role         string
	Device       string
	Version      int
	ExpiresAt    string
	IssuedAt     string
}

func (s *memoryAuditSink) Write(_ context.Context, event audit.AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append([]audit.AuditEvent{event}, s.events...)
	if len(s.events) > 50 {
		s.events = s.events[:50]
	}
	return nil
}

func (s *memoryAuditSink) List() []audit.AuditEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]audit.AuditEvent(nil), s.events...)
}

func (s *memoryAuditSink) ListPaginated(page, perPage int) ([]audit.AuditEvent, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := len(s.events)
	if perPage <= 0 {
		perPage = 10
	}
	if page <= 0 {
		page = 1
	}
	offset := (page - 1) * perPage
	if offset >= total {
		return nil, total
	}
	end := offset + perPage
	if end > total {
		end = total
	}
	return append([]audit.AuditEvent(nil), s.events[offset:end]...), total
}

type app struct {
	db         *gorm.DB
	manager    *core.Manager // base：多端并发、不强制 nonce，承担校验与所有 /admin 操作
	nonceMgr   *core.Manager // RequireNonce=true，演示一次性 nonce 防重放
	singleMgr  *core.Manager // Concurrent=false，演示单设备登录（异地挤下线）
	sharedMgr  *core.Manager // ShareToken=true，演示共享会话（单点会话复用）
	authorizer *rbac.Authorizer
	generator  *token.TokenGenerator[map[string]string]
	audits     *memoryAuditSink
	eventBus   *core.AsyncEventBus
	templates  *template.Template
	storeName  string
}

// loginScenario 描述登录框里一个选项卡对应的真实业务登录形态。
type loginScenario struct {
	Key    string // 表单提交的 mode 值
	Tab    string // 选项卡标题
	Button string // 提交按钮文案
	Desc   string // 场景说明
}

// loginScenarios 返回登录框选项卡，第一个为默认登录方式。
func loginScenarios() []loginScenario {
	return []loginScenario{
		{Key: "refresh", Tab: "记住我", Button: "登录并保持 7 天", Desc: "短期 access + 长期 refresh，access 失效用 refresh 续签。移动端 App、Web「记住我」常用。"},
		{Key: "basic", Tab: "普通登录", Button: "账号密码登录", Desc: "只签发单个 access token，多端可同时在线。最常见的后台 / Web 登录。"},
		{Key: "nonce", Tab: "安全登录", Button: "Nonce 防重放登录", Desc: "登录页预发一次性 nonce，提交时由服务端消费；重放或过期会被拒绝。后台、支付确认等高安全入口。"},
		{Key: "single", Tab: "单设备登录", Button: "登录并踢掉其它设备", Desc: "Concurrent=false：登录成功后该账号其它设备立即下线。银行、敏感系统的单会话策略。"},
		{Key: "shared", Tab: "共享会话", Button: "复用已有会话登录", Desc: "ShareToken=true：同账号重复登录复用同一 token，实现单点会话。"},
	}
}

// scenarioTab 返回某登录模式的中文标题，用于展示。
func scenarioTab(mode string) string {
	for _, s := range loginScenarios() {
		if s.Key == mode {
			return s.Tab
		}
	}
	return loginScenarios()[0].Tab
}

// normalizeMode 兜底未知 / 空模式为默认登录方式。
func normalizeMode(mode string) string {
	mode = strings.TrimSpace(mode)
	for _, s := range loginScenarios() {
		if s.Key == mode {
			return mode
		}
	}
	return loginScenarios()[0].Key
}

func main() {
	cfg := config{}
	flag.StringVar(&cfg.addr, "addr", ":8080", "HTTP listen address")
	flag.StringVar(&cfg.database, "db", "admin.db", "SQLite database file")
	flag.StringVar(&cfg.tokenStore, "token-store", "memory", "token state store: memory or sqlite")
	flag.Parse()

	a, err := newApp(cfg)
	if err != nil {
		log.Fatal(err)
	}

	srv := &http.Server{Addr: cfg.addr, Handler: a.routes(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		log.Printf("better-token admin listening on %s (admin / admin123), token-store=%s", cfg.addr, cfg.tokenStore)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serve: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	_ = a.eventBus.Close(ctx)
}

func newApp(cfg config) (*app, error) {
	if cfg.database == "" {
		cfg.database = filepath.Join(os.TempDir(), "better-token-admin.db")
	}
	provider, err := db.NewDB(sqlite.Open(cfg.database))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	gdb := provider.DB(context.Background())
	if err := gdb.AutoMigrate(&user{}); err != nil {
		return nil, fmt.Errorf("migrate users: %w", err)
	}

	var store core.Store
	switch cfg.tokenStore {
	case "", "memory":
		cfg.tokenStore = "memory"
		store = memory.NewStore()
	case "sqlite":
		s := dbstore.NewStore(provider)
		if err := s.Migrate(context.Background()); err != nil {
			return nil, fmt.Errorf("migrate token store: %w", err)
		}
		store = s
	default:
		return nil, fmt.Errorf("unsupported token store %q (want memory or sqlite)", cfg.tokenStore)
	}

	authorizer := rbac.NewAuthorizer()
	// admin 的 "*" 演示 rbac 通配权限；operator 仅拥有明确列出的最小权限。
	authorizer.GrantPermission("admin", "*")
	authorizer.GrantPermission("operator", "dashboard:read")
	authorizer.GrantPermission("operator", "session:write")
	audits := &memoryAuditSink{}
	bus := core.NewAsyncEventBus(core.WithEventWorkerCount(2), core.WithEventQueueSize(128))
	bus.Register(audit.New(audits))

	manager := buildManager(store, authorizer, bus, nil)
	a := &app{
		db: gdb, manager: manager, authorizer: authorizer,
		nonceMgr:  buildManager(store, authorizer, bus, func(c *core.Config) { c.RequireNonce = true }),
		singleMgr: buildManager(store, authorizer, bus, func(c *core.Config) { c.Concurrent = false }),
		sharedMgr: buildManager(store, authorizer, bus, func(c *core.Config) { c.ShareToken = true }),
		generator: token.NewTokenGenerator[map[string]string](token.WithTokenStyle(token.TokenStyleUUID)),
		audits:    audits, eventBus: bus, storeName: cfg.tokenStore,
	}
	if err := a.seed(); err != nil {
		return nil, err
	}
	if err := a.loadRoles(); err != nil {
		return nil, err
	}
	a.templates, err = parseTemplates()
	if err != nil {
		return nil, err
	}
	return a, nil
}

// buildManager 基于一套共用配置创建 Manager，mutate 用于按登录场景微调 Config。
// 所有 Manager 共享同一 store/authorizer/eventBus，因此任意一个签发的登录态都能被其它 Manager 校验。
func buildManager(store core.Store, authorizer core.Authorizer, bus core.EventBus, mutate func(*core.Config)) *core.Manager {
	cfg := core.Config{
		TokenName: accessCookie, Timeout: 30 * time.Minute, ActiveTimeout: 30 * time.Minute,
		AutoRenew: true, Concurrent: true,
		Refresh: core.RefreshConfig{Timeout: 7 * 24 * time.Hour, RotateRefreshToken: true, RevokeAccessTokenOnRefresh: true, RevokeRefreshOnLogout: true},
		Nonce:   core.NonceConfig{Timeout: 2 * time.Minute, Length: 32},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return core.NewManager(store,
		core.WithAuthorizer(authorizer),
		core.WithEventBus(bus),
		core.WithConfig(cfg),
	)
}

func (a *app) seed() error {
	defaults := []seededUser{
		{Username: "admin", Password: "admin123", DisplayName: "系统管理员", Role: "admin"},
		{Username: "operator", Password: "operator123", DisplayName: "值班操作员", Role: "operator"},
	}
	for _, u := range defaults {
		if err := a.seedUser(u); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) seedUser(seed seededUser) error {
	var count int64
	if err := a.db.Model(&user{}).Where("username = ?", seed.Username).Count(&count).Error; err != nil || count > 0 {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(seed.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return a.db.Create(&user{Username: seed.Username, PasswordHash: string(hash), DisplayName: seed.DisplayName, Role: seed.Role}).Error
}

func (a *app) loadRoles() error {
	var users []user
	if err := a.db.Find(&users).Error; err != nil {
		return err
	}
	for _, u := range users {
		a.authorizer.AssignRole(strconv.FormatUint(uint64(u.ID), 10), u.Role)
	}
	return nil
}

func (a *app) routes() *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())
	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok", "token_store": a.storeName}) })
	r.GET("/", a.loginPage)
	r.POST("/login", a.login)
	r.POST("/refresh", a.refresh)

	protected := r.Group("/admin")
	protected.Use(btgin.Middleware(a.manager, btgin.WithUnauthorized(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Redirect", "/")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})))
	protected.Use(a.requirePermission("dashboard:read"))
	protected.GET("", func(c *gin.Context) { c.Redirect(http.StatusSeeOther, dashboardPath) })
	protected.GET("/dashboard.html", a.dashboard)
	protected.GET("/scenarios.html", a.scenarios)
	protected.POST("/session", a.requirePermission("session:write"), a.saveSession)
	protected.POST("/renew", a.renew)
	protected.POST("/online", a.markOnline)
	protected.POST("/offline", a.markOffline)
	protected.POST("/kick-device", a.kickDevice)
	protected.POST("/kick-devices", a.kickDevices)
	protected.POST("/scenarios/device", a.createScenarioDevice)
	protected.POST("/scenarios/kick-device", a.kickScenarioDevice)
	protected.POST("/scenarios/logout-user", a.logoutScenarioUser)
	protected.POST("/logout-all", a.logoutAll)
	protected.POST("/logout", a.logout)
	protected.POST("/revoke-refresh", a.revokeRefresh)
	protected.POST("/users", a.requirePermission("admin:users:write"), a.createUser)
	protected.POST("/users/:id", a.requirePermission("admin:users:write"), a.updateUser)
	protected.POST("/users/:id/delete", a.requirePermission("admin:users:write"), a.deleteUser)
	return r
}

func (a *app) requirePermission(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := a.manager.CheckPermission(c.Request.Context(), permission); err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
		c.Next()
	}
}

func (a *app) loginPage(c *gin.Context) {
	if cookie, err := c.Cookie(accessCookie); err == nil && a.manager.IsValid(c.Request.Context(), core.TokenValue(cookie)) {
		c.Redirect(http.StatusSeeOther, dashboardPath)
		return
	}
	// 为「安全登录」选项卡预发一次性 nonce；提交时由 nonceMgr 消费，重放/过期会被拒绝。
	nonce, err := a.nonceMgr.GenerateNonce(c.Request.Context(), core.WithNoncePurpose("admin-login"))
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	a.render(c, "login", gin.H{
		"Error":       c.Query("error"),
		"Nonce":       nonce,
		"Scenarios":   loginScenarios(),
		"DefaultMode": loginScenarios()[0].Key,
	})
}

func (a *app) login(c *gin.Context) {
	var form struct {
		Username string `form:"username" binding:"required"`
		Password string `form:"password" binding:"required"`
		Device   string `form:"device" binding:"required"`
		Mode     string `form:"mode"`
		Nonce    string `form:"nonce"`
	}
	if err := c.ShouldBind(&form); err != nil {
		a.redirectError(c, "/", "请填写用户名、密码和设备名")
		return
	}
	var u user
	if err := a.db.Where("username = ?", strings.TrimSpace(form.Username)).First(&u).Error; err != nil || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(form.Password)) != nil {
		a.redirectError(c, "/", "用户名或密码错误")
		return
	}
	loginID := strconv.FormatUint(uint64(u.ID), 10)
	metadata := []byte(fmt.Sprintf(`{"username":%q,"role":%q}`, u.Username, u.Role))
	mode := normalizeMode(form.Mode)

	token, refresh, err := a.performLogin(c.Request.Context(), mode, loginID, form.Device, form.Nonce, metadata)
	if err != nil {
		a.redirectError(c, "/", err.Error())
		return
	}

	_ = a.manager.MarkOnline(c.Request.Context(), token, core.OnlineInfo{IP: c.ClientIP(), UserAgent: c.Request.UserAgent(), ConnectionID: form.Device})
	session := core.NewSessionForSubject(core.LoginSubject{LoginID: loginID, LoginType: loginType})
	session.Set("display_name", u.DisplayName)
	session.Set("theme", "system")
	session.Set("login_mode", scenarioTab(mode))
	_ = a.manager.SaveSession(c.Request.Context(), session)
	a.setAuthCookies(c, token, refresh)
	redirectAfterPost(c, dashboardPath)
}

// performLogin 按所选登录形态走对应的 Manager / 流程，返回 access token 与（可选的）refresh token。
func (a *app) performLogin(ctx context.Context, mode, loginID, device, nonce string, metadata []byte) (core.TokenValue, core.TokenValue, error) {
	opts := []core.LoginOption{core.WithLoginType(loginType), core.WithDevice(device), core.WithMetadata(metadata)}
	switch mode {
	case "basic": // 普通登录：单 access token，多端并发。
		access, err := a.generate(loginID, "access")
		if err != nil {
			return "", "", err
		}
		state, err := a.manager.Login(ctx, loginID, access, opts...)
		if err != nil {
			return "", "", err
		}
		return state.Token, "", nil

	case "nonce": // 安全登录：消费登录页预发的一次性 nonce，重放/过期被拒绝。
		if strings.TrimSpace(nonce) == "" {
			return "", "", errors.New("一次性 nonce 缺失，请刷新登录页后重试")
		}
		access, err := a.generate(loginID, "access")
		if err != nil {
			return "", "", err
		}
		state, err := a.nonceMgr.Login(ctx, loginID, access, append(opts, core.WithNonce(core.TokenValue(nonce)))...)
		if err != nil {
			return "", "", errors.New(nonceErrorMessage(err))
		}
		return state.Token, "", nil

	case "single": // 单设备登录：Concurrent=false，登录成功后挤掉其它设备。
		access, refresh, err := a.generatePair(loginID)
		if err != nil {
			return "", "", err
		}
		result, err := a.singleMgr.LoginWithRefresh(ctx, loginID, access, refresh, opts...)
		if err != nil {
			return "", "", err
		}
		return result.TokenState.Token, result.RefreshState.Token, nil

	case "shared": // 共享会话：ShareToken=true，同账号复用已有有效 token。
		access, err := a.generate(loginID, "access")
		if err != nil {
			return "", "", err
		}
		state, err := a.sharedMgr.Login(ctx, loginID, access, opts...)
		if err != nil {
			return "", "", err
		}
		return state.Token, "", nil

	default: // refresh：记住我，签发 access + refresh。
		access, refresh, err := a.generatePair(loginID)
		if err != nil {
			return "", "", err
		}
		result, err := a.manager.LoginWithRefresh(ctx, loginID, access, refresh, opts...)
		if err != nil {
			return "", "", err
		}
		return result.TokenState.Token, result.RefreshState.Token, nil
	}
}

func (a *app) generatePair(loginID string) (access core.TokenValue, refresh core.TokenValue, err error) {
	if access, err = a.generate(loginID, "access"); err != nil {
		return "", "", err
	}
	refresh, err = a.generate(loginID, "refresh")
	return access, refresh, err
}

func nonceErrorMessage(err error) string {
	switch {
	case errors.Is(err, core.ErrNonceReplayed):
		return "一次性 nonce 已被使用（重放被拒绝），请刷新登录页重试"
	case errors.Is(err, core.ErrNonceExpired):
		return "一次性 nonce 已过期，请刷新登录页重试"
	case errors.Is(err, core.ErrNonceNotFound):
		return "一次性 nonce 无效，请刷新登录页重试"
	default:
		return err.Error()
	}
}

func (a *app) refresh(c *gin.Context) {
	old, err := c.Cookie(refreshCookie)
	if err != nil {
		a.redirectError(c, "/", "缺少 refresh token")
		return
	}
	access, err := a.generate("refresh", "access")
	if err != nil {
		a.redirectError(c, "/", err.Error())
		return
	}
	refresh, err := a.generate("refresh", "refresh")
	if err != nil {
		a.redirectError(c, "/", err.Error())
		return
	}
	result, err := a.manager.Refresh(c.Request.Context(), core.TokenValue(old), access, core.WithNextRefreshToken(refresh))
	if err != nil {
		a.clearAuthCookies(c)
		a.redirectError(c, "/", err.Error())
		return
	}
	a.setAuthCookies(c, result.TokenState.Token, result.RefreshState.Token)
	redirectAfterPost(c, dashboardPath)
}

func (a *app) dashboard(c *gin.Context) {
	auth, _ := core.RequireAuth(c.Request.Context())
	states, err := a.manager.ListTokenStates(c.Request.Context(), auth.LoginID, core.WithListLoginType(loginType))
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	session, _ := a.manager.GetSession(c.Request.Context(), auth.LoginID, core.WithSessionLoginType(loginType))
	current, _ := a.manager.GetTokenState(c.Request.Context(), auth.Token)
	currentUser, err := a.userByLoginID(auth.LoginID)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	var users []user
	_ = a.db.Order("id").Find(&users).Error
	authorities, _ := a.authorizer.GetAuthorities(c.Request.Context(), auth.LoginID)
	checks := map[string]bool{
		"role_admin":               a.manager.CheckRole(c.Request.Context(), "admin") == nil,
		"all_operator_permissions": a.manager.CheckAll(c.Request.Context(), core.Permission("dashboard:read"), core.Permission("session:write")) == nil,
		"any_privileged_authority": a.manager.CheckAny(c.Request.Context(), core.Role("admin"), core.Permission("admin:users:write")) == nil,
	}
	_ = a.eventBus.Flush(c.Request.Context())

	auditPerPage := 10
	auditPage, _ := strconv.Atoi(c.DefaultQuery("ap", "1"))
	if auditPage < 1 {
		auditPage = 1
	}
	auditEvents, auditTotal := a.audits.ListPaginated(auditPage, auditPerPage)
	auditTotalPages := (auditTotal + auditPerPage - 1) / auditPerPage

	a.render(c, "dashboard", gin.H{
		"Auth": auth, "Current": current, "CurrentUser": currentUser, "States": states, "Session": session, "Users": users,
		"Authorities": authorities, "Checks": checks, "Audits": auditEvents, "AuditTotal": auditTotal,
		"AuditPage": auditPage, "AuditPerPage": auditPerPage, "AuditTotalPages": auditTotalPages,
		"Store": a.storeName, "CanManageUsers": a.canManageUsers(c),
		"CanWriteSession": a.manager.CheckPermission(c.Request.Context(), "session:write") == nil,
		"Message": c.Query("message"), "Error": c.Query("error"),
	})
}

func (a *app) scenarios(c *gin.Context) {
	auth, _ := core.RequireAuth(c.Request.Context())
	scenarios, err := a.userScenarios(c.Request.Context())
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	_ = a.eventBus.Flush(c.Request.Context())
	a.render(c, "scenarios", gin.H{
		"Auth":           auth,
		"Users":          scenarios,
		"TokenStyles":    a.tokenStyleScenarios(auth.LoginID),
	"JwtCustomData":  a.jwtCustomDataScenarios(auth.LoginID),
		"CanManageUsers": a.canManageUsers(c),
		"Message":        c.Query("message"),
		"Error":          c.Query("error"),
	})
}

func (a *app) userScenarios(ctx context.Context) ([]userScenario, error) {
	var users []user
	if err := a.db.Order("id").Find(&users).Error; err != nil {
		return nil, err
	}
	result := make([]userScenario, 0, len(users))
	for _, u := range users {
		loginID := strconv.FormatUint(uint64(u.ID), 10)
		authorities, err := a.authorizer.GetAuthorities(ctx, loginID)
		if err != nil {
			return nil, err
		}
		checks, err := a.authorityScenarios(ctx, loginID)
		if err != nil {
			return nil, err
		}
		states, err := a.manager.ListTokenStates(ctx, loginID, core.WithListLoginType(loginType))
		if err != nil {
			return nil, err
		}
		session, err := a.manager.GetSession(ctx, loginID, core.WithSessionLoginType(loginType))
		if errors.Is(err, core.ErrSessionNotFound) {
			session = nil
		} else if err != nil {
			return nil, err
		}
		result = append(result, userScenario{
			User:        u,
			LoginID:     loginID,
			Authorities: authorities,
			Checks:      checks,
			States:      states,
			Session:     session,
		})
	}
	return result, nil
}

func (a *app) authorityScenarios(ctx context.Context, loginID string) ([]authorityScenario, error) {
	checks := []authorityScenario{
		{Label: "管理员角色", Expected: "admin 可用", Authority: core.Role("admin")},
		{Label: "值班角色", Expected: "operator 可用", Authority: core.Role("operator")},
		{Label: "查看控制台", Expected: "两种角色可用", Authority: core.Permission("dashboard:read")},
		{Label: "保存 Session", Expected: "两种角色可用", Authority: core.Permission("session:write")},
		{Label: "创建用户", Expected: "仅 admin 可用", Authority: core.Permission("admin:users:write")},
	}
	for i := range checks {
		allowed, err := a.authorizer.HasAuthority(ctx, loginID, checks[i].Authority)
		if err != nil {
			return nil, err
		}
		checks[i].Allowed = allowed
	}
	return checks, nil
}

func (a *app) createScenarioDevice(c *gin.Context) {
	auth, _ := core.RequireAuth(c.Request.Context())
	loginID := strings.TrimSpace(c.PostForm("login_id"))
	if loginID == "" {
		loginID = auth.LoginID
	}
	if err := a.requireScenarioUserAccess(c, loginID); err != nil {
		a.redirectError(c, scenarioPath, err.Error())
		return
	}
	device := strings.TrimSpace(c.PostForm("device"))
	if device == "" {
		a.redirectError(c, scenarioPath, "请填写模拟设备名")
		return
	}
	style := parseTokenStyle(c.PostForm("style"))
	u, err := a.userByLoginID(loginID)
	if err != nil {
		a.redirectError(c, scenarioPath, err.Error())
		return
	}
	if err := a.issueScenarioDevice(c.Request.Context(), u, device, style, c.ClientIP(), c.Request.UserAgent()); err != nil {
		a.redirectError(c, scenarioPath, err.Error())
		return
	}
	a.actionOKTo(c, scenarioPath, fmt.Sprintf("已为 %s 创建 %s 设备 token：%s", u.Username, style, device))
}

func (a *app) kickScenarioDevice(c *gin.Context) {
	auth, _ := core.RequireAuth(c.Request.Context())
	loginID := strings.TrimSpace(c.PostForm("login_id"))
	if loginID == "" {
		loginID = auth.LoginID
	}
	if err := a.requireScenarioUserAccess(c, loginID); err != nil {
		a.redirectError(c, scenarioPath, err.Error())
		return
	}
	device := strings.TrimSpace(c.PostForm("device"))
	if device == "" {
		a.redirectError(c, scenarioPath, "请填写要踢下线的设备名")
		return
	}
	if err := a.manager.LogoutByDevice(c.Request.Context(), loginID, device, core.WithLogoutLoginType(loginType)); err != nil {
		a.redirectError(c, scenarioPath, err.Error())
		return
	}
	a.actionOKTo(c, scenarioPath, "已踢下线设备 "+device)
}

func (a *app) logoutScenarioUser(c *gin.Context) {
	if !a.canManageUsers(c) {
		a.redirectError(c, scenarioPath, "只有 admin 可以清空其他用户的所有设备")
		return
	}
	loginID := strings.TrimSpace(c.PostForm("login_id"))
	if loginID == "" {
		a.redirectError(c, scenarioPath, "请选择用户")
		return
	}
	u, err := a.userByLoginID(loginID)
	if err != nil {
		a.redirectError(c, scenarioPath, err.Error())
		return
	}
	if err := a.manager.LogoutByLoginID(c.Request.Context(), loginID, core.WithLogoutLoginType(loginType), core.WithDeleteSession(true)); err != nil {
		a.redirectError(c, scenarioPath, err.Error())
		return
	}
	a.actionOKTo(c, scenarioPath, "已清空 "+u.Username+" 的所有设备与 Session")
}

func (a *app) issueScenarioDevice(ctx context.Context, u user, device string, style token.TokenStyle, ip, userAgent string) error {
	loginID := strconv.FormatUint(uint64(u.ID), 10)
	access, err := a.generateWithStyle(loginID, "scenario-access", style)
	if err != nil {
		return err
	}
	refresh, err := a.generateWithStyle(loginID, "scenario-refresh", style)
	if err != nil {
		return err
	}
	metadata := []byte(fmt.Sprintf(`{"username":%q,"role":%q,"scenario":true,"token_style":%q}`, u.Username, u.Role, style))
	result, err := a.manager.LoginWithRefresh(ctx, loginID, access, refresh,
		core.WithLoginType(loginType), core.WithDevice(device), core.WithMetadata(metadata))
	if err != nil {
		return err
	}
	return a.manager.MarkOnline(ctx, result.TokenState.Token, core.OnlineInfo{IP: ip, UserAgent: userAgent, ConnectionID: device})
}

func (a *app) requireScenarioUserAccess(c *gin.Context, loginID string) error {
	auth, _ := core.RequireAuth(c.Request.Context())
	if loginID == auth.LoginID || a.canManageUsers(c) {
		return nil
	}
	return errors.New("operator 只能操作自己的模拟设备")
}

func (a *app) canManageUsers(c *gin.Context) bool {
	return a.manager.CheckPermission(c.Request.Context(), "admin:users:write") == nil
}

func (a *app) userByLoginID(loginID string) (user, error) {
	var u user
	if err := a.db.Where("id = ?", loginID).First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return user{}, fmt.Errorf("用户 %s 不存在", loginID)
		}
		return user{}, err
	}
	return u, nil
}

func (a *app) saveSession(c *gin.Context) {
	auth, _ := core.RequireAuth(c.Request.Context())
	session, err := a.manager.GetSession(c.Request.Context(), auth.LoginID, core.WithSessionLoginType(loginType))
	if errors.Is(err, core.ErrSessionNotFound) {
		session = core.NewSessionForSubject(core.LoginSubject{LoginID: auth.LoginID, LoginType: loginType})
	} else if err != nil {
		a.actionError(c, err)
		return
	}
	session.Set("theme", strings.TrimSpace(c.PostForm("theme")))
	session.Set("note", strings.TrimSpace(c.PostForm("note")))
	if err := a.manager.SaveSession(c.Request.Context(), session); err != nil {
		a.actionError(c, err)
		return
	}
	a.actionOK(c, "Session 已保存")
}

func (a *app) renew(c *gin.Context) {
	auth, _ := core.RequireAuth(c.Request.Context())
	if err := a.manager.Renew(c.Request.Context(), auth.Token, 30*time.Minute); err != nil {
		a.actionError(c, err)
		return
	}
	a.actionOK(c, "当前 access token 已续期 30 分钟")
}

func (a *app) markOnline(c *gin.Context) {
	auth, _ := core.RequireAuth(c.Request.Context())
	err := a.manager.MarkOnline(c.Request.Context(), auth.Token, core.OnlineInfo{IP: c.ClientIP(), UserAgent: c.Request.UserAgent(), ConnectionID: c.PostForm("device")})
	if err != nil {
		a.actionError(c, err)
		return
	}
	a.actionOK(c, "当前设备已标记在线")
}

func (a *app) markOffline(c *gin.Context) {
	auth, _ := core.RequireAuth(c.Request.Context())
	if err := a.manager.MarkOffline(c.Request.Context(), auth.Token); err != nil {
		a.actionError(c, err)
		return
	}
	a.actionOK(c, "当前设备已标记离线（token 仍有效）")
}

func (a *app) kickDevice(c *gin.Context) {
	auth, _ := core.RequireAuth(c.Request.Context())
	if err := a.manager.LogoutByDevice(c.Request.Context(), auth.LoginID, c.PostForm("device"), core.WithLogoutLoginType(loginType)); err != nil {
		a.actionError(c, err)
		return
	}
	a.actionOK(c, "指定设备及关联 refresh token 已撤销")
}

func (a *app) kickDevices(c *gin.Context) {
	auth, _ := core.RequireAuth(c.Request.Context())
	devices := uniqueNonEmpty(c.PostFormArray("devices"))
	if len(devices) == 0 {
		a.actionError(c, errors.New("请先选择要踢下线的设备"))
		return
	}
	for _, device := range devices {
		if device == auth.Device {
			a.actionError(c, errors.New("当前设备请使用“退出当前设备”操作"))
			return
		}
		if err := a.manager.LogoutByDevice(c.Request.Context(), auth.LoginID, device, core.WithLogoutLoginType(loginType)); err != nil {
			a.actionError(c, err)
			return
		}
	}
	a.actionOK(c, fmt.Sprintf("已踢下线 %d 个设备，关联 refresh token 已撤销", len(devices)))
}

func (a *app) logoutAll(c *gin.Context) {
	auth, _ := core.RequireAuth(c.Request.Context())
	if err := a.manager.LogoutByLoginID(c.Request.Context(), auth.LoginID, core.WithLogoutLoginType(loginType), core.WithDeleteSession(true)); err != nil {
		a.actionError(c, err)
		return
	}
	a.clearAuthCookies(c)
	redirectAfterPost(c, "/")
}

func (a *app) logout(c *gin.Context) {
	auth, _ := core.RequireAuth(c.Request.Context())
	_ = a.manager.MarkOffline(c.Request.Context(), auth.Token)
	if err := a.manager.Logout(c.Request.Context(), auth.Token); err != nil && !errors.Is(err, core.ErrTokenNotFound) {
		a.actionError(c, err)
		return
	}
	a.clearAuthCookies(c)
	redirectAfterPost(c, "/")
}

func (a *app) revokeRefresh(c *gin.Context) {
	value, err := c.Cookie(refreshCookie)
	if err != nil {
		a.actionError(c, errors.New("当前设备没有 refresh token"))
		return
	}
	if err := a.manager.RevokeRefreshToken(c.Request.Context(), core.TokenValue(value)); err != nil {
		a.actionError(c, err)
		return
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(refreshCookie, "", -1, "/", "", false, true)
	a.actionOK(c, "Refresh token 已撤销，当前 access token 仍可使用")
}

func (a *app) createUser(c *gin.Context) {
	var form struct {
		Username    string `form:"username" binding:"required"`
		DisplayName string `form:"display_name" binding:"required"`
		Password    string `form:"password" binding:"required,min=8"`
		Role        string `form:"role" binding:"required,oneof=admin operator"`
	}
	if err := c.ShouldBind(&form); err != nil {
		a.actionError(c, fmt.Errorf("用户字段无效: %w", err))
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(form.Password), bcrypt.DefaultCost)
	if err != nil {
		a.actionError(c, err)
		return
	}
	u := user{Username: strings.TrimSpace(form.Username), DisplayName: strings.TrimSpace(form.DisplayName), PasswordHash: string(hash), Role: form.Role}
	if err := a.db.Create(&u).Error; err != nil {
		a.actionError(c, err)
		return
	}
	a.authorizer.AssignRole(strconv.FormatUint(uint64(u.ID), 10), u.Role)
	a.actionOK(c, "用户已创建并同步 RBAC 角色")
}

func (a *app) updateUser(c *gin.Context) {
	target, err := a.userByLoginID(c.Param("id"))
	if err != nil {
		a.actionError(c, err)
		return
	}
	var form struct {
		DisplayName string `form:"display_name" binding:"required"`
		Role        string `form:"role" binding:"required,oneof=admin operator"`
		Password    string `form:"password"`
	}
	if err := c.ShouldBind(&form); err != nil {
		a.actionError(c, fmt.Errorf("用户字段无效: %w", err))
		return
	}
	password := strings.TrimSpace(form.Password)
	if password != "" && len(password) < 8 {
		a.actionError(c, errors.New("重置密码至少 8 位"))
		return
	}
	updates := map[string]any{"display_name": strings.TrimSpace(form.DisplayName), "role": form.Role}
	if password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			a.actionError(c, err)
			return
		}
		updates["password_hash"] = string(hash)
	}
	if err := a.db.Model(&user{}).Where("id = ?", target.ID).Updates(updates).Error; err != nil {
		a.actionError(c, err)
		return
	}
	if target.Role != form.Role {
		loginID := strconv.FormatUint(uint64(target.ID), 10)
		a.authorizer.RevokeRole(loginID, target.Role)
		a.authorizer.AssignRole(loginID, form.Role)
	}
	a.actionOK(c, "用户已更新")
}

func (a *app) deleteUser(c *gin.Context) {
	auth, _ := core.RequireAuth(c.Request.Context())
	if c.Param("id") == auth.LoginID {
		a.actionError(c, errors.New("不能删除当前登录用户"))
		return
	}
	target, err := a.userByLoginID(c.Param("id"))
	if err != nil {
		a.actionError(c, err)
		return
	}
	if err := a.db.Delete(&user{}, target.ID).Error; err != nil {
		a.actionError(c, err)
		return
	}
	loginID := strconv.FormatUint(uint64(target.ID), 10)
	a.authorizer.RevokeRole(loginID, target.Role)
	_ = a.manager.LogoutByLoginID(c.Request.Context(), loginID, core.WithLogoutLoginType(loginType), core.WithDeleteSession(true))
	a.actionOK(c, "用户已删除并强制下线")
}

func (a *app) generate(loginID, kind string) (core.TokenValue, error) {
	return a.generateWithStyle(loginID, kind, token.TokenStyleUUID)
}

func (a *app) generateWithStyle(loginID, kind string, style token.TokenStyle) (core.TokenValue, error) {
	style = parseTokenStyle(string(style))
	generator := a.generator
	if style != token.TokenStyleUUID {
		generator = token.NewTokenGenerator[map[string]string](
			token.WithTokenStyle(style),
			token.WithSimpleTokenLength(24),
		)
	}
	value, err := generator.GenerateToken(loginID, map[string]string{"kind": kind, "style": string(style)})
	return core.TokenValue(value), err
}

func (a *app) tokenStyleScenarios(loginID string) []tokenStyleScenario {
	cases := []tokenStyleScenario{
		{Style: token.TokenStyleSimple, Label: "Simple 随机串", Usage: "Opaque access / refresh token，服务端只按 TokenState 承认登录态。", Pattern: "随机字符"},
		{Style: token.TokenStyleTimestamp, Label: "Timestamp", Usage: "把生成时间和主体拼入 token，便于人工排查一次性调试场景。", Pattern: "毫秒时间_loginID_随机后缀"},
		{Style: token.TokenStyleUUID, Label: "UUID", Usage: "后台会话默认用法，适合作为不可预测的服务端状态 key。", Pattern: "RFC 4122 UUID"},
		{Style: token.TokenStyleHash, Label: "Hash", Usage: "固定长度 SHA-256 十六进制 token，适合需要稳定长度的存储或日志脱敏。", Pattern: "64 位十六进制"},
		{Style: token.TokenStyleTiktok, Label: "Tiktok 短码", Usage: "短 token 示例，适合低风险兑换码或演示，不建议承载高价值长期登录态。", Pattern: "11 位大小写字母数字"},
	}
	for i := range cases {
		value, err := a.generateWithStyle(loginID, "sample", cases[i].Style)
		if err != nil {
			cases[i].Sample = "生成失败: " + err.Error()
			continue
		}
		cases[i].Sample = string(value)
		cases[i].Length = len(value)
	}
	return cases
}

func (a *app) jwtCustomDataScenarios(loginID string) jwtCustomDataScenario {
	type demoClaims struct {
		UserID  string `json:"user_id"`
		TrackID string `json:"track_id"`
		Role    string `json:"role"`
		Device  string `json:"device"`
		Version int    `json:"ver"`
	}

	jwtManager := token.NewJwtManager[demoClaims](
		token.WithSecretKey("demo-secret-key-for-jwt-scenario"),
		token.WithIssuer("better-token-demo"),
	)

	claims := demoClaims{
		UserID:  loginID,
		TrackID: "track_" + loginID + "_" + time.Now().Format("150405"),
		Role:    "admin",
		Device:  "web-chrome",
		Version: 1,
	}

	jwtToken, _ := jwtManager.GenerateToken(loginID, claims)
	parsed, _ := jwtManager.ParseToken(jwtToken)

	preview := jwtToken
	if len(preview) > 60 {
		preview = preview[:60] + "..."
	}

	return jwtCustomDataScenario{
		Token:        jwtToken,
		TokenPreview: preview,
		UserID:       parsed.Data.UserID,
		TrackID:      parsed.Data.TrackID,
		Role:         parsed.Data.Role,
		Device:       parsed.Data.Device,
		Version:      parsed.Data.Version,
		ExpiresAt:    parsed.ExpiresAt.Local().Format("2006-01-02 15:04:05"),
		IssuedAt:     parsed.IssuedAt.Local().Format("2006-01-02 15:04:05"),
	}
}

func parseTokenStyle(value string) token.TokenStyle {
	switch token.TokenStyle(strings.TrimSpace(value)) {
	case token.TokenStyleSimple:
		return token.TokenStyleSimple
	case token.TokenStyleTimestamp:
		return token.TokenStyleTimestamp
	case token.TokenStyleHash:
		return token.TokenStyleHash
	case token.TokenStyleTiktok:
		return token.TokenStyleTiktok
	case token.TokenStyleUUID:
		return token.TokenStyleUUID
	default:
		return token.TokenStyleUUID
	}
}

func (a *app) setAuthCookies(c *gin.Context, access, refresh core.TokenValue) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(accessCookie, string(access), 30*60, "/", "", false, true)
	if refresh == "" {
		// 普通 / Nonce / 共享会话登录不签发 refresh token，清掉可能残留的旧 cookie。
		c.SetCookie(refreshCookie, "", -1, "/", "", false, true)
		return
	}
	c.SetCookie(refreshCookie, string(refresh), 7*24*60*60, "/", "", false, true)
}

func (a *app) clearAuthCookies(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(accessCookie, "", -1, "/", "", false, true)
	c.SetCookie(refreshCookie, "", -1, "/", "", false, true)
}

func (a *app) render(c *gin.Context, name string, data gin.H) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	if err := a.templates.ExecuteTemplate(c.Writer, name, data); err != nil {
		c.Error(err)
	}
}

func (a *app) actionOK(c *gin.Context, message string) {
	a.actionOKTo(c, dashboardPath, message)
}

func (a *app) actionOKTo(c *gin.Context, base, message string) {
	url := base + "?message=" + queryEscape(message)
	redirectAfterPost(c, url)
}

func (a *app) actionError(c *gin.Context, err error) { a.redirectError(c, dashboardPath, err.Error()) }

func (a *app) redirectError(c *gin.Context, base, message string) {
	url := base + "?error=" + queryEscape(message)
	redirectAfterPost(c, url)
}

func redirectAfterPost(c *gin.Context, target string) {
	if c.GetHeader("HX-Request") == "true" {
		c.Header("HX-Redirect", target)
		c.Status(http.StatusNoContent)
		return
	}
	c.Redirect(http.StatusSeeOther, target)
}

func queryEscape(value string) string {
	return url.QueryEscape(value)
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

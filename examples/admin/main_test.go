package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/apus-run/better-token/core"
)

func TestAdminFlowWithMemoryStore(t *testing.T) {
	exerciseAdminFlow(t, "memory")
}

func TestAdminFlowWithSQLiteStore(t *testing.T) {
	exerciseAdminFlow(t, "sqlite")
}

func exerciseAdminFlow(t *testing.T, storeName string) {
	t.Helper()
	a, err := newApp(config{database: filepath.Join(t.TempDir(), "admin.db"), tokenStore: storeName})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.eventBus.Close(t.Context()) })
	router := a.routes()

	form := url.Values{"username": {"admin"}, "password": {"admin123"}, "device": {"test-browser"}}
	w := request(t, router, http.MethodPost, "/login", form, nil)
	if w.Code != http.StatusNoContent || w.Header().Get("HX-Redirect") != dashboardPath {
		t.Fatalf("login status=%d hx-redirect=%q body=%s", w.Code, w.Header().Get("HX-Redirect"), w.Body.String())
	}
	cookies := authCookies(w.Result().Cookies())
	if len(cookies) != 2 {
		t.Fatalf("auth cookies=%d", len(cookies))
	}
	oldAccess := cookieValue(cookies, accessCookie)

	w = request(t, router, http.MethodGet, dashboardPath, nil, cookies)
	if w.Code != http.StatusOK {
		t.Fatalf("dashboard status=%d body=%s", w.Code, w.Body.String())
	}
	for _, expected := range []string{"当前会话", "风险概览", "踢下线所选设备", "复制 Token", "异步审计事件", storeName + " token store"} {
		if !strings.Contains(w.Body.String(), expected) {
			t.Fatalf("dashboard missing %q", expected)
		}
	}

	w = request(t, router, http.MethodPost, "/admin/session", url.Values{"theme": {"dark"}, "note": {"verified"}}, cookies)
	if w.Code != http.StatusNoContent || !strings.Contains(w.Header().Get("HX-Redirect"), "message=") {
		t.Fatalf("session status=%d headers=%v", w.Code, w.Header())
	}
	session, err := a.manager.GetSession(t.Context(), "1", core.WithSessionLoginType(loginType))
	if err != nil {
		t.Fatal(err)
	}
	if theme, _ := session.Get("theme"); theme != "dark" {
		t.Fatalf("session theme=%v", theme)
	}

	w = request(t, router, http.MethodPost, "/refresh", nil, cookies)
	if w.Code != http.StatusNoContent || w.Header().Get("HX-Redirect") != dashboardPath {
		t.Fatalf("refresh status=%d body=%s", w.Code, w.Body.String())
	}
	if _, err := a.manager.GetTokenState(t.Context(), core.TokenValue(oldAccess)); !errors.Is(err, core.ErrTokenNotFound) {
		t.Fatalf("old access token should be revoked, err=%v", err)
	}
	rotated := authCookies(w.Result().Cookies())
	if len(rotated) != 2 || cookieValue(rotated, accessCookie) == oldAccess {
		t.Fatalf("tokens were not rotated: %v", rotated)
	}

	w = request(t, router, http.MethodPost, "/admin/revoke-refresh", nil, rotated)
	if w.Code != http.StatusNoContent {
		t.Fatalf("revoke refresh status=%d body=%s", w.Code, w.Body.String())
	}
	if !a.manager.IsValid(t.Context(), core.TokenValue(cookieValue(rotated, accessCookie))) {
		t.Fatal("revoking refresh token must not revoke current access token")
	}
}

func TestAdminLoginRedirectsPlainPostToDashboardHTML(t *testing.T) {
	a, err := newApp(config{database: filepath.Join(t.TempDir(), "admin.db"), tokenStore: "memory"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.eventBus.Close(t.Context()) })
	router := a.routes()

	form := url.Values{"username": {"admin"}, "password": {"admin123"}, "device": {"plain-browser"}}
	w := requestPlain(t, router, http.MethodPost, "/login", form, nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != dashboardPath {
		t.Fatalf("login status=%d location=%q body=%s", w.Code, w.Header().Get("Location"), w.Body.String())
	}
}

func TestLoginModeBasicIssuesOnlyAccess(t *testing.T) {
	a, router := newTestApp(t)

	form := url.Values{"username": {"admin"}, "password": {"admin123"}, "device": {"web"}, "mode": {"basic"}}
	w := request(t, router, http.MethodPost, "/login", form, nil)
	if w.Code != http.StatusNoContent || w.Header().Get("HX-Redirect") != dashboardPath {
		t.Fatalf("basic login status=%d redirect=%q", w.Code, w.Header().Get("HX-Redirect"))
	}
	cookies := authCookies(w.Result().Cookies())
	if cookieValue(cookies, accessCookie) == "" {
		t.Fatal("basic login missing access cookie")
	}
	if cookieValue(cookies, refreshCookie) != "" {
		t.Fatal("basic login should not issue a refresh token")
	}
	w = request(t, router, http.MethodGet, dashboardPath, nil, cookies)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "普通登录") {
		t.Fatalf("dashboard should show login mode 普通登录: status=%d", w.Code)
	}
	states, err := a.manager.ListTokenStates(t.Context(), "1", core.WithListLoginType(loginType))
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 {
		t.Fatalf("basic login states=%d, want 1", len(states))
	}
}

func TestLoginModeSharedReusesToken(t *testing.T) {
	a, router := newTestApp(t)

	first := loginWithMode(t, router, "shared", "web")
	second := loginWithMode(t, router, "shared", "phone")
	if cookieValue(first, accessCookie) != cookieValue(second, accessCookie) {
		t.Fatal("shared login should reuse the same access token across logins")
	}
	states, err := a.manager.ListTokenStates(t.Context(), "1", core.WithListLoginType(loginType))
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 {
		t.Fatalf("shared login states=%d, want 1 (single shared session)", len(states))
	}
}

func TestLoginModeSingleKicksOtherDevices(t *testing.T) {
	a, router := newTestApp(t)

	first := loginWithMode(t, router, "single", "laptop")
	loginWithMode(t, router, "single", "phone")

	if a.manager.IsValid(t.Context(), core.TokenValue(cookieValue(first, accessCookie))) {
		t.Fatal("single-device login must revoke the previous device's token")
	}
	states, err := a.manager.ListTokenStates(t.Context(), "1", core.WithListLoginType(loginType))
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 {
		t.Fatalf("single login states=%d, want 1", len(states))
	}
}

func TestLoginModeNonceRejectsReplay(t *testing.T) {
	_, router := newTestApp(t)

	nonce := fetchLoginNonce(t, router)
	form := func() url.Values {
		return url.Values{"username": {"admin"}, "password": {"admin123"}, "device": {"web"}, "mode": {"nonce"}, "nonce": {nonce}}
	}
	w := request(t, router, http.MethodPost, "/login", form(), nil)
	if w.Code != http.StatusNoContent || w.Header().Get("HX-Redirect") != dashboardPath {
		t.Fatalf("first nonce login status=%d redirect=%q", w.Code, w.Header().Get("HX-Redirect"))
	}

	w = request(t, router, http.MethodPost, "/login", form(), nil)
	if w.Code != http.StatusNoContent || !strings.Contains(w.Header().Get("HX-Redirect"), "error=") {
		t.Fatalf("replayed nonce should be rejected: status=%d redirect=%q", w.Code, w.Header().Get("HX-Redirect"))
	}
}

func newTestApp(t *testing.T) (*app, http.Handler) {
	t.Helper()
	a, err := newApp(config{database: filepath.Join(t.TempDir(), "admin.db"), tokenStore: "memory"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.eventBus.Close(t.Context()) })
	return a, a.routes()
}

func loginWithMode(t *testing.T, handler http.Handler, mode, device string) []*http.Cookie {
	t.Helper()
	form := url.Values{"username": {"admin"}, "password": {"admin123"}, "device": {device}, "mode": {mode}}
	w := request(t, handler, http.MethodPost, "/login", form, nil)
	if w.Code != http.StatusNoContent || w.Header().Get("HX-Redirect") != dashboardPath {
		t.Fatalf("%s login status=%d redirect=%q body=%s", mode, w.Code, w.Header().Get("HX-Redirect"), w.Body.String())
	}
	return authCookies(w.Result().Cookies())
}

func fetchLoginNonce(t *testing.T, handler http.Handler) string {
	t.Helper()
	w := request(t, handler, http.MethodGet, "/", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("login page status=%d", w.Code)
	}
	m := regexp.MustCompile(`name="nonce" value="([^"]+)"`).FindStringSubmatch(w.Body.String())
	if len(m) != 2 || m[1] == "" {
		t.Fatalf("login page missing nonce hidden input: %s", w.Body.String())
	}
	return m[1]
}

func TestOperatorRoleAndScenarioRestrictions(t *testing.T) {
	a, err := newApp(config{database: filepath.Join(t.TempDir(), "admin.db"), tokenStore: "memory"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.eventBus.Close(t.Context()) })
	router := a.routes()
	operator := findUser(t, a, "operator")

	cookies := loginAs(t, router, "operator", "operator123", "ops-laptop")
	w := request(t, router, http.MethodGet, scenarioPath, nil, cookies)
	if w.Code != http.StatusOK {
		t.Fatalf("scenario status=%d body=%s", w.Code, w.Body.String())
	}
	for _, expected := range []string{"场景实验室", "operator", "admin:users:write", "拒绝"} {
		if !strings.Contains(w.Body.String(), expected) {
			t.Fatalf("scenario missing %q", expected)
		}
	}

	w = request(t, router, http.MethodPost, "/admin/users", url.Values{
		"username":     {"blocked"},
		"display_name": {"Blocked"},
		"password":     {"blocked123"},
		"role":         {"operator"},
	}, cookies)
	if w.Code != http.StatusForbidden {
		t.Fatalf("operator create user status=%d body=%s", w.Code, w.Body.String())
	}

	w = request(t, router, http.MethodPost, "/admin/scenarios/device", url.Values{
		"login_id": {"1"},
		"device":   {"other-user-phone"},
	}, cookies)
	if w.Code != http.StatusNoContent || !strings.Contains(w.Header().Get("HX-Redirect"), "operator+%E5%8F%AA%E8%83%BD%E6%93%8D%E4%BD%9C%E8%87%AA%E5%B7%B1%E7%9A%84%E6%A8%A1%E6%8B%9F%E8%AE%BE%E5%A4%87") {
		t.Fatalf("operator cross-user scenario status=%d redirect=%q", w.Code, w.Header().Get("HX-Redirect"))
	}

	w = request(t, router, http.MethodPost, "/admin/scenarios/device", url.Values{
		"login_id": {strconvID(operator.ID)},
		"device":   {"ops-phone"},
		"style":    {"simple"},
	}, cookies)
	if w.Code != http.StatusNoContent || !strings.Contains(w.Header().Get("HX-Redirect"), scenarioPath) {
		t.Fatalf("operator own scenario status=%d redirect=%q", w.Code, w.Header().Get("HX-Redirect"))
	}
	states, err := a.manager.ListTokenStates(t.Context(), strconvID(operator.ID), core.WithListLoginType(loginType))
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 2 {
		t.Fatalf("operator states=%d, want 2", len(states))
	}
	if state := stateByDevice(states, "ops-phone"); state == nil || len(state.Token) != 24 {
		t.Fatalf("operator simple token = %v, want length 24", state)
	}
}

func TestAdminScenarioMultiDeviceControls(t *testing.T) {
	a, err := newApp(config{database: filepath.Join(t.TempDir(), "admin.db"), tokenStore: "memory"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.eventBus.Close(t.Context()) })
	router := a.routes()
	admin := findUser(t, a, "admin")
	operator := findUser(t, a, "operator")

	cookies := loginAs(t, router, "admin", "admin123", "admin-laptop")
	w := request(t, router, http.MethodPost, "/admin/scenarios/device", url.Values{
		"login_id": {strconvID(admin.ID)},
		"device":   {"admin-phone"},
		"style":    {"hash"},
	}, cookies)
	if w.Code != http.StatusNoContent || !strings.Contains(w.Header().Get("HX-Redirect"), scenarioPath) {
		t.Fatalf("admin create scenario status=%d redirect=%q", w.Code, w.Header().Get("HX-Redirect"))
	}
	states, err := a.manager.ListTokenStates(t.Context(), strconvID(admin.ID), core.WithListLoginType(loginType))
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 2 {
		t.Fatalf("admin states=%d, want 2", len(states))
	}
	if state := stateByDevice(states, "admin-phone"); state == nil || len(state.Token) != 64 {
		t.Fatalf("admin hash token = %v, want length 64", state)
	}

	w = request(t, router, http.MethodGet, scenarioPath, nil, cookies)
	for _, expected := range []string{"admin-phone", "Token 创建与使用场景", "复制 Token", "simple", "timestamp", "hash", "tiktok"} {
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), expected) {
			t.Fatalf("scenario page missing %q: status=%d body=%s", expected, w.Code, w.Body.String())
		}
	}

	w = request(t, router, http.MethodPost, "/admin/kick-devices", url.Values{"devices": {"admin-phone"}}, cookies)
	if w.Code != http.StatusNoContent {
		t.Fatalf("bulk kick devices status=%d body=%s", w.Code, w.Body.String())
	}
	states, err = a.manager.ListTokenStates(t.Context(), strconvID(admin.ID), core.WithListLoginType(loginType))
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 {
		t.Fatalf("admin states after kick=%d, want 1", len(states))
	}

	w = request(t, router, http.MethodPost, "/admin/scenarios/device", url.Values{
		"login_id": {strconvID(operator.ID)},
		"device":   {"ops-tablet"},
		"style":    {"tiktok"},
	}, cookies)
	if w.Code != http.StatusNoContent {
		t.Fatalf("admin create operator device status=%d body=%s", w.Code, w.Body.String())
	}
	states, err = a.manager.ListTokenStates(t.Context(), strconvID(operator.ID), core.WithListLoginType(loginType))
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 {
		t.Fatalf("operator scenario states=%d, want 1", len(states))
	}
	if state := stateByDevice(states, "ops-tablet"); state == nil || len(state.Token) != 11 {
		t.Fatalf("operator tiktok token = %v, want length 11", state)
	}

	w = request(t, router, http.MethodPost, "/admin/scenarios/logout-user", url.Values{"login_id": {strconvID(operator.ID)}}, cookies)
	if w.Code != http.StatusNoContent {
		t.Fatalf("logout scenario user status=%d body=%s", w.Code, w.Body.String())
	}
	states, err = a.manager.ListTokenStates(t.Context(), strconvID(operator.ID), core.WithListLoginType(loginType))
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 0 {
		t.Fatalf("operator states after logout=%d, want 0", len(states))
	}
}

func TestAdminUserCRUD(t *testing.T) {
	a, err := newApp(config{database: filepath.Join(t.TempDir(), "admin.db"), tokenStore: "memory"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.eventBus.Close(t.Context()) })
	router := a.routes()

	cookies := loginAs(t, router, "admin", "admin123", "admin-laptop")

	// 仪表盘应渲染新增按钮、操作列和编辑/删除入口。
	w := request(t, router, http.MethodGet, dashboardPath, nil, cookies)
	for _, expected := range []string{"新增用户", "data-modal-open=\"userCreate\"", "操作", "data-modal-open=\"userEdit-2\"", "/admin/users/2/delete"} {
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), expected) {
			t.Fatalf("dashboard missing %q: status=%d", expected, w.Code)
		}
	}

	// 创建一个新用户。
	w = request(t, router, http.MethodPost, "/admin/users", url.Values{
		"username":     {"viewer"},
		"display_name": {"只读员"},
		"password":     {"viewer123"},
		"role":         {"operator"},
	}, cookies)
	if w.Code != http.StatusNoContent {
		t.Fatalf("create user status=%d body=%s", w.Code, w.Body.String())
	}
	created := findUser(t, a, "viewer")

	// 编辑：改显示名、升级角色并重置密码。
	w = request(t, router, http.MethodPost, "/admin/users/"+strconvID(created.ID), url.Values{
		"display_name": {"超级管理员"},
		"role":         {"admin"},
		"password":     {"viewer456"},
	}, cookies)
	if w.Code != http.StatusNoContent {
		t.Fatalf("update user status=%d body=%s", w.Code, w.Body.String())
	}
	updated := findUser(t, a, "viewer")
	if updated.DisplayName != "超级管理员" || updated.Role != "admin" {
		t.Fatalf("update user = %+v", updated)
	}
	if isAdmin, _ := a.authorizer.HasAuthority(t.Context(), strconvID(created.ID), core.Role("admin")); !isAdmin {
		t.Fatal("rbac role not re-synced to admin after update")
	}
	if isOperator, _ := a.authorizer.HasAuthority(t.Context(), strconvID(created.ID), core.Role("operator")); isOperator {
		t.Fatal("old operator role not revoked after update")
	}

	// 删除：用户应被移除并撤销角色。
	w = request(t, router, http.MethodPost, "/admin/users/"+strconvID(created.ID)+"/delete", nil, cookies)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete user status=%d body=%s", w.Code, w.Body.String())
	}
	if err := a.db.Where("username = ?", "viewer").First(&user{}).Error; err == nil {
		t.Fatal("deleted user still exists")
	}
	if isAdmin, _ := a.authorizer.HasAuthority(t.Context(), strconvID(created.ID), core.Role("admin")); isAdmin {
		t.Fatal("deleted user still has rbac role")
	}

	// 不能删除当前登录用户。
	admin := findUser(t, a, "admin")
	w = request(t, router, http.MethodPost, "/admin/users/"+strconvID(admin.ID)+"/delete", nil, cookies)
	if w.Code != http.StatusNoContent || !strings.Contains(w.Header().Get("HX-Redirect"), "error=") {
		t.Fatalf("self-delete should be rejected: status=%d redirect=%q", w.Code, w.Header().Get("HX-Redirect"))
	}
}

func loginAs(t *testing.T, handler http.Handler, username, password, device string) []*http.Cookie {
	t.Helper()
	w := request(t, handler, http.MethodPost, "/login", url.Values{
		"username": {username},
		"password": {password},
		"device":   {device},
	}, nil)
	if w.Code != http.StatusNoContent || w.Header().Get("HX-Redirect") != dashboardPath {
		t.Fatalf("login %s status=%d hx-redirect=%q body=%s", username, w.Code, w.Header().Get("HX-Redirect"), w.Body.String())
	}
	cookies := authCookies(w.Result().Cookies())
	if len(cookies) != 2 {
		t.Fatalf("login %s auth cookies=%d", username, len(cookies))
	}
	return cookies
}

func findUser(t *testing.T, a *app, username string) user {
	t.Helper()
	var u user
	if err := a.db.Where("username = ?", username).First(&u).Error; err != nil {
		t.Fatal(err)
	}
	return u
}

func strconvID(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}

func stateByDevice(states []*core.TokenState, device string) *core.TokenState {
	for _, state := range states {
		if state != nil && state.Device == device {
			return state
		}
	}
	return nil
}

func request(t *testing.T, handler http.Handler, method, target string, form url.Values, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	return requestWithHTMX(t, handler, method, target, form, cookies, true)
}

func requestPlain(t *testing.T, handler http.Handler, method, target string, form url.Values, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	return requestWithHTMX(t, handler, method, target, form, cookies, false)
}

func requestWithHTMX(t *testing.T, handler http.Handler, method, target string, form url.Values, cookies []*http.Cookie, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	var body *strings.Reader
	if form == nil {
		body = strings.NewReader("")
	} else {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, target, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func authCookies(cookies []*http.Cookie) []*http.Cookie {
	result := make([]*http.Cookie, 0, 2)
	for _, cookie := range cookies {
		if cookie.Name == accessCookie || cookie.Name == refreshCookie {
			result = append(result, cookie)
		}
	}
	return result
}

func cookieValue(cookies []*http.Cookie, name string) string {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

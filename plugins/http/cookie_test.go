package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/storage/memory"
)

func TestTokenCookieRoundtripAndDeletion(t *testing.T) {
	config := DefaultCookieConfig()
	config.Name = "access"
	config.Path = "/app"
	config.Domain = "example.com"
	config.Secure = true
	w := httptest.NewRecorder()
	if err := config.SetTokenCookie(w, "raw-token", 1500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	cookie := w.Result().Cookies()[0]
	if cookie.Name != "access" || cookie.Value != "raw-token" || cookie.Path != "/app" || cookie.Domain != "example.com" || cookie.MaxAge != 2 || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie=%+v", cookie)
	}
	request := httptest.NewRequest(http.MethodGet, "https://example.com/app", nil)
	jar, _ := cookiejar.New(nil)
	jar.SetCookies(request.URL, w.Result().Cookies())
	request.AddCookie(cookie)
	extractor := NewExtractor(WithTokenName("Authorization"), WithTokenPrefix("Bearer"), WithCookieConfig(config))
	if token, ok := extractor.ExtractToken(request); !ok || token != "raw-token" {
		t.Fatalf("token=%q,%v", token, ok)
	}
	request.Header.Set("Authorization", "Bearer header-token")
	if token, _ := extractor.ExtractToken(request); token != "header-token" {
		t.Fatal("header priority changed")
	}
	deleted := httptest.NewRecorder()
	if err := config.ClearTokenCookie(deleted); err != nil {
		t.Fatal(err)
	}
	clear := deleted.Result().Cookies()[0]
	jar.SetCookies(request.URL, deleted.Result().Cookies())
	if len(jar.Cookies(request.URL)) != 0 {
		t.Fatal("cookie jar retained credential after clear")
	}
	if clear.Name != cookie.Name || clear.Path != cookie.Path || clear.Domain != cookie.Domain || clear.MaxAge != -1 || !clear.Expires.Before(time.Now()) || !clear.Secure || !clear.HttpOnly {
		t.Fatalf("clear=%+v", clear)
	}
}
func TestCookieReadCanBeDisabled(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: "token", Value: "cookie-token"})
	if _, ok := NewExtractor(WithCookieRead(false)).ExtractToken(request); ok {
		t.Fatal("disabled cookie accepted")
	}
	request.Header.Set("token", "header-token")
	if token, ok := NewExtractor(WithCookieRead(false)).ExtractToken(request); !ok || token != "header-token" {
		t.Fatal("disabling cookie disabled header")
	}
}
func TestCookieValidationRejectsInvalidValuesBeforeWrite(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config CookieConfig
		token  core.TokenValue
	}{
		{"invalid-name", CookieConfig{Name: "bad;name"}, "token"},
		{"invalid-value", DefaultCookieConfig(), "good\nbad"},
		{"insecure-none", CookieConfig{SameSite: http.SameSiteNoneMode}, "token"},
		{"insecure-prefix", CookieConfig{Name: "__Secure-token"}, "token"},
		{"host-domain", CookieConfig{Name: "__Host-token", Secure: true, Domain: "example.com"}, "token"},
		{"host-path", CookieConfig{Name: "__Host-token", Secure: true, Path: "/app"}, "token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			err := tc.config.SetTokenCookie(w, tc.token, time.Hour)
			if !errors.Is(err, ErrInvalidCookie) || len(w.Header().Values("Set-Cookie")) != 0 {
				t.Fatalf("err=%v,headers=%v", err, w.Header())
			}
		})
	}
}
func TestCookieSessionLifetimeAndEmptyToken(t *testing.T) {
	config := DefaultCookieConfig()
	w := httptest.NewRecorder()
	if err := config.SetTokenCookie(w, "token", 0); err != nil {
		t.Fatal(err)
	}
	cookie := w.Result().Cookies()[0]
	if cookie.MaxAge != 0 || !cookie.Expires.IsZero() {
		t.Fatal("zero TTL must produce browser-session cookie")
	}
	w = httptest.NewRecorder()
	if err := config.SetTokenCookie(w, " ", time.Hour); !errors.Is(err, core.ErrEmptyToken) || len(w.Header().Values("Set-Cookie")) != 0 {
		t.Fatal("empty token was written")
	}
}
func TestCookieLoginAuthenticationLogoutFlow(t *testing.T) {
	manager := core.NewManager(memory.NewStore())
	state, err := manager.Login(context.Background(), "alice", "access-token")
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultCookieConfig()
	config.Name = "access"
	login := httptest.NewRecorder()
	if err = config.SetTokenCookie(login, state.Token, time.Hour); err != nil {
		t.Fatal(err)
	}
	handler := Middleware(manager, WithCookieConfig(config))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, err := core.RequireLoginID(r.Context()); err != nil || id != "alice" {
			t.Fatal("cookie auth missing")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(login.Result().Cookies()[0])
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatal("cookie login failed")
	}
	if err = manager.Logout(request.Context(), state.Token); err != nil {
		t.Fatal(err)
	}
	logout := httptest.NewRecorder()
	if err = config.ClearTokenCookie(logout); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatal("clearing cookie did not accompany server-side revocation")
	}
	if !strings.Contains(logout.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatal("browser deletion missing")
	}
}

func TestCookieEmptyNameUsesSameReadWriteDefault(t *testing.T) {
	config := CookieConfig{Path: "/", HttpOnly: true}
	for _, opts := range [][]Option{
		{WithTokenName("access"), WithCookieConfig(config)},
		{WithCookieConfig(config), WithTokenName("access")},
	} {
		written := httptest.NewRecorder()
		if err := config.SetTokenCookie(written, "raw-token", time.Hour); err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
		request.AddCookie(written.Result().Cookies()[0])
		if token, found := NewExtractor(opts...).ExtractToken(request); !found || token != "raw-token" {
			t.Fatalf("roundtrip token=%q found=%v", token, found)
		}
		removed := httptest.NewRecorder()
		if err := config.ClearTokenCookie(removed); err != nil {
			t.Fatal(err)
		}
		if removed.Result().Cookies()[0].Name != written.Result().Cookies()[0].Name {
			t.Fatal("clear name differs from write")
		}
	}
	// Legacy callers which only customize TokenName continue using that cookie name.
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: "access", Value: "legacy"})
	if token, found := NewExtractor(WithTokenName("access")).ExtractToken(request); !found || token != "legacy" {
		t.Fatal("legacy cookie name changed")
	}
}
func TestCookiePathRejectsRelativeAndClearsAcrossRoutes(t *testing.T) {
	config := DefaultCookieConfig()
	config.Path = "relative"
	for _, clear := range []bool{false, true} {
		response := httptest.NewRecorder()
		var err error
		if clear {
			err = config.ClearTokenCookie(response)
		} else {
			err = config.SetTokenCookie(response, "token", time.Hour)
		}
		if !errors.Is(err, ErrInvalidCookie) || response.Header().Get("Set-Cookie") != "" {
			t.Fatalf("relative path accepted: %v", err)
		}
	}
	config.Path = "/auth"
	login, _ := url.Parse("https://example.com/auth/login")
	logout, _ := url.Parse("https://example.com/logout")
	resource, _ := url.Parse("https://example.com/auth/me")
	jar, _ := cookiejar.New(nil)
	written := httptest.NewRecorder()
	if err := config.SetTokenCookie(written, "token", time.Hour); err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(login, written.Result().Cookies())
	if len(jar.Cookies(resource)) != 1 {
		t.Fatal("cookie was not stored")
	}
	cleared := httptest.NewRecorder()
	if err := config.ClearTokenCookie(cleared); err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(logout, cleared.Result().Cookies())
	if len(jar.Cookies(resource)) != 0 {
		t.Fatal("credential remains after cross-route deletion")
	}
}

package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/storage/memory"
	"github.com/apus-run/better-token/token"
)

func TestExtractorOrderAndBearerPrefix(t *testing.T) {
	extractor := NewExtractor(WithTokenName("bt"), WithTokenPrefix("Bearer"))

	req := httptest.NewRequest(http.MethodGet, "/?bt=query-token", nil)
	req.AddCookie(&http.Cookie{Name: "bt", Value: "cookie-token"})
	req.Header.Set("Authorization", "Bearer auth-token")
	req.Header.Set("bt", "header-token")
	got, ok := extractor.ExtractToken(req)
	if !ok || got != "header-token" {
		t.Fatalf("Header[TokenName] should win, got %q ok=%v", got, ok)
	}

	req.Header.Del("bt")
	got, ok = extractor.ExtractToken(req)
	if !ok || got != "auth-token" {
		t.Fatalf("Authorization should be second, got %q ok=%v", got, ok)
	}

	req.Header.Del("Authorization")
	got, ok = extractor.ExtractToken(req)
	if !ok || got != "cookie-token" {
		t.Fatalf("Cookie should be third, got %q ok=%v", got, ok)
	}

	req = httptest.NewRequest(http.MethodGet, "/?bt=query-token", nil)
	got, ok = extractor.ExtractToken(req)
	if !ok || got != "query-token" {
		t.Fatalf("Query should be fourth, got %q ok=%v", got, ok)
	}
}

func TestMiddlewareInjectsAuthContext(t *testing.T) {
	manager := core.NewManager(memory.NewStore(), core.WithConfig(core.Config{
		TokenName:   "Authorization",
		TokenPrefix: "Bearer",
		Timeout:     time.Hour,
		Concurrent:  true,
		ShareToken:  false,
		AutoRenew:   false,
	}))
	if _, err := manager.Login(context.Background(), "1001", "http-token"); err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	handler := Middleware(manager)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		loginID, err := core.RequireLoginID(r.Context())
		if err != nil {
			t.Fatalf("RequireLoginID failed: %v", err)
		}
		if loginID != "1001" {
			t.Fatalf("loginID = %q", loginID)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer http-token")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d", res.Code)
	}

	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/", nil))
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d", missing.Code)
	}
}

func TestTokenGeneratorIntegrationWithHTTPMiddleware(t *testing.T) {
	manager := core.NewManager(memory.NewStore())
	generator := token.NewTokenGenerator[any](token.WithTokenStyle(token.TokenStyleSimple))
	tokenStr, err := generator.GenerateToken("1001", nil)
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}
	if _, err := manager.Login(context.Background(), "1001", core.TokenValue(tokenStr)); err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	handler := Middleware(manager)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := core.RequireLoginID(r.Context()); err != nil {
			t.Fatalf("RequireLoginID failed: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/?token="+tokenStr, nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d", res.Code)
	}
}

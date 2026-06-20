package gin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	gonic "github.com/gin-gonic/gin"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/storage/memory"
	"github.com/apus-run/better-token/token"
)

func TestGinMiddlewareInjectsAuthContext(t *testing.T) {
	gonic.SetMode(gonic.TestMode)

	manager := core.NewManager(memory.NewStore(), core.WithConfig(core.Config{
		TokenName:   "Authorization",
		TokenPrefix: "Bearer",
		Timeout:     0,
		Concurrent:  true,
	}))

	jwtManager := token.NewJwtManager[map[string]string](token.WithSecretKey("test-secret"))
	jwtToken, err := jwtManager.GenerateToken("1001", map[string]string{"kind": "access"})
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}
	if _, err := manager.Login(context.Background(), "1001", core.TokenValue(jwtToken)); err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	router := gonic.New()
	router.Use(Middleware(manager))
	router.GET("/me", func(c *gonic.Context) {
		loginID, err := core.RequireLoginID(c.Request.Context())
		if err != nil {
			t.Fatalf("RequireLoginID failed: %v", err)
		}
		value, ok := c.Get("auth")
		if !ok {
			t.Fatal("expected auth in gin context")
		}
		auth, ok := value.(*core.AuthContext)
		if !ok || auth.LoginID != loginID {
			t.Fatalf("unexpected auth: %#v", value)
		}
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d", res.Code)
	}

	missing := httptest.NewRecorder()
	router.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/me", nil))
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d", missing.Code)
	}
}

func TestGinMiddlewareUsesCustomUnauthorizedHandler(t *testing.T) {
	gonic.SetMode(gonic.TestMode)

	manager := core.NewManager(memory.NewStore())
	router := gonic.New()
	router.Use(Middleware(manager, WithUnauthorized(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})))
	router.GET("/me", func(c *gonic.Context) {
		c.Status(http.StatusNoContent)
	})

	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/me", nil))
	if res.Code != http.StatusTeapot {
		t.Fatalf("missing token status = %d", res.Code)
	}
}

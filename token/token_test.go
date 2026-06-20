package token

import (
	"errors"
	"testing"
	"time"
)

func TestJwtManagerGenerateParseVerify(t *testing.T) {
	manager := NewJwtManager[map[string]string](
		WithSecretKey("test-secret"),
		WithIssuer("better-token-test"),
		WithAudience("app"),
		WithExpiry(time.Hour),
	)

	tokenStr, err := manager.GenerateToken("1001", map[string]string{"tenant": "acme"})
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}
	claims, err := manager.ParseToken(tokenStr)
	if err != nil {
		t.Fatalf("ParseToken failed: %v", err)
	}
	if claims.Subject != "1001" || claims.Data["tenant"] != "acme" {
		t.Fatalf("Unexpected claims: %#v", claims)
	}
	ok, err := manager.VerifyToken(tokenStr)
	if err != nil || !ok {
		t.Fatalf("VerifyToken ok=%v err=%v", ok, err)
	}
}

func TestJwtManagerRejectsIssuerAndAudienceMismatch(t *testing.T) {
	signer := NewJwtManager[string](
		WithSecretKey("test-secret"),
		WithIssuer("issuer-a"),
		WithAudience("app-a"),
		WithExpiry(time.Hour),
	)
	tokenStr, err := signer.GenerateToken("1001", "payload")
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	wrongIssuer := NewJwtManager[string](
		WithSecretKey("test-secret"),
		WithIssuer("issuer-b"),
		WithAudience("app-a"),
		WithExpiry(time.Hour),
	)
	if _, err := wrongIssuer.ParseToken(tokenStr); !errors.Is(err, ErrInvalidIssuer) {
		t.Fatalf("ParseToken with wrong issuer error = %v", err)
	}

	wrongAudience := NewJwtManager[string](
		WithSecretKey("test-secret"),
		WithIssuer("issuer-a"),
		WithAudience("app-b"),
		WithExpiry(time.Hour),
	)
	if _, err := wrongAudience.ParseToken(tokenStr); !errors.Is(err, ErrInvalidAudience) {
		t.Fatalf("ParseToken with wrong audience error = %v", err)
	}
}

func TestJwtManagerRequiresExplicitSecret(t *testing.T) {
	manager := NewJwtManager[string]()
	if _, err := manager.GenerateToken("1001", "payload"); !errors.Is(err, ErrEmptyJWTSecret) {
		t.Fatalf("GenerateToken without secret error = %v", err)
	}

	generator := NewTokenGenerator[string]()
	tokenStr, err := generator.GenerateToken("1001", "payload")
	if err != nil {
		t.Fatalf("Default TokenGenerator should use non-JWT token style: %v", err)
	}
	if tokenStr == "" {
		t.Fatal("Generated token should not be empty")
	}

	generator = NewTokenGenerator[string](WithTokenStyle(""))
	tokenStr, err = generator.GenerateToken("1001", "payload")
	if err != nil {
		t.Fatalf("Empty TokenStyle option should use non-JWT default: %v", err)
	}
	if tokenStr == "" {
		t.Fatal("Generated token should not be empty")
	}
}

func TestJwtManagerRejectsUnsupportedAlgorithm(t *testing.T) {
	manager := NewJwtManager[string](
		WithSecretKey("test-secret"),
		WithAlgorithm("RS256"),
	)
	if _, err := manager.GenerateToken("1001", "payload"); !errors.Is(err, ErrUnsupportedJWTAlgorithm) {
		t.Fatalf("GenerateToken with unsupported algorithm error = %v", err)
	}
}

func TestNilOptionsAreIgnored(t *testing.T) {
	var jwtOpt func(*JwtConfig)
	var tokenOpt func(*TokenConfig)

	manager := NewJwtManager[string](jwtOpt, WithSecretKey("test-secret"))
	if _, err := manager.GenerateToken("1001", "payload"); err != nil {
		t.Fatalf("GenerateToken with nil option failed: %v", err)
	}

	generator := NewTokenGenerator[string](tokenOpt)
	tokenStr, err := generator.GenerateToken("1001", "payload")
	if err != nil {
		t.Fatalf("GenerateToken with nil option failed: %v", err)
	}
	if tokenStr == "" {
		t.Fatal("Generated token should not be empty")
	}
}

func TestTokenGeneratorStyles(t *testing.T) {
	styles := []TokenStyle{
		TokenStyleSimple,
		TokenStyleTimestamp,
		TokenStyleUUID,
		TokenStyleHash,
		TokenStyleJWT,
		TokenStyleTiktok,
	}

	for _, style := range styles {
		t.Run(string(style), func(t *testing.T) {
			generator := NewTokenGenerator[string](
				WithTokenStyle(style),
				WithJWTSecretKey("test-secret"),
				WithJWTIssuer("better-token-test"),
			)
			tokenStr, err := generator.GenerateToken("1001", "payload")
			if err != nil {
				t.Fatalf("GenerateToken failed: %v", err)
			}
			if tokenStr == "" {
				t.Fatal("Generated token should not be empty")
			}
		})
	}
}

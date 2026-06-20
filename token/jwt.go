package token

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/apus-run/better-token/pkg/option"
	"github.com/apus-run/gala/pkg/jwtx"

	"github.com/google/uuid"
)

const (
	DefaultJWTSecret    = ""
	DefaultJWTAlgorithm = "HS256"
	DefaultIssuer       = "hurry-app"
	DefaultJWTExpiry    = 7 * 24 * time.Hour
	JWTSecretLength     = 32
)

type Claims[T any] struct {
	Data T `json:"data"`

	jwt.RegisteredClaims
}

type JwtConfig struct {
	SecretKey string
	Issuer    string
	Audience  []string
	Algorithm string
	Expiry    time.Duration
}

func NewJwtConfig(opts ...option.Option[JwtConfig]) *JwtConfig {
	config := DefaultJwtConfig()

	option.Apply(config, opts...)

	return config
}

func DefaultJwtConfig() *JwtConfig {
	return &JwtConfig{
		SecretKey: DefaultJWTSecret,
		Issuer:    DefaultIssuer,
		Algorithm: DefaultJWTAlgorithm,
		Expiry:    DefaultJWTExpiry,
	}
}

func WithSecretKey(secretKey string) option.Option[JwtConfig] {
	return func(c *JwtConfig) {
		c.SecretKey = secretKey
	}
}

func WithIssuer(issuer string) option.Option[JwtConfig] {
	return func(c *JwtConfig) {
		if strings.TrimSpace(issuer) == "" {
			issuer = DefaultIssuer
		}
		c.Issuer = issuer
	}
}

func WithAudience(audience ...string) option.Option[JwtConfig] {
	return func(c *JwtConfig) {
		c.Audience = append([]string(nil), audience...)
	}
}

func WithAlgorithm(algorithm string) option.Option[JwtConfig] {
	return func(c *JwtConfig) {
		if strings.TrimSpace(algorithm) == "" {
			algorithm = DefaultJWTAlgorithm
		}
		c.Algorithm = algorithm
	}
}

func WithExpiry(expiry time.Duration) option.Option[JwtConfig] {
	return func(c *JwtConfig) {
		if expiry == 0 {
			expiry = DefaultJWTExpiry
		}
		c.Expiry = expiry
	}
}

func WithJwtConfig(config JwtConfig) option.Option[JwtConfig] {
	return func(c *JwtConfig) {
		WithSecretKey(config.SecretKey)(c)
		WithIssuer(config.Issuer)(c)
		WithAudience(config.Audience...)(c)
		WithAlgorithm(config.Algorithm)(c)
		WithExpiry(config.Expiry)(c)
	}
}

func (c TokenConfig) jwtConfig() option.Option[JwtConfig] {
	return func(jwt *JwtConfig) {
		WithSecretKey(c.JWTSecretKey)(jwt)
		WithIssuer(c.JWTIssuer)(jwt)
		WithAudience(c.JWTAudience...)(jwt)
		WithAlgorithm(c.JWTAlgorithm)(jwt)
		WithExpiry(c.JWTExpiry)(jwt)
	}
}

type JwtManager[T any] struct {
	conf *JwtConfig
}

func NewJwtManager[T any](opts ...option.Option[JwtConfig]) *JwtManager[T] {
	conf := NewJwtConfig(opts...)

	jm := &JwtManager[T]{
		conf: conf,
	}

	return jm
}

func (jm *JwtManager[T]) GenerateToken(userID string, data T) (string, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "", ErrEmptyUserID
	}

	secretKey := strings.TrimSpace(jm.conf.SecretKey)
	if secretKey == "" {
		return "", ErrEmptyJWTSecret
	}
	signingMethod, err := jwtSigningMethod(jm.conf.Algorithm)
	if err != nil {
		return "", err
	}

	now := time.Now()
	claims := &Claims[T]{
		Data: data,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(jm.conf.Expiry)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    jm.conf.Issuer,
			Audience:  jwt.ClaimStrings(jm.conf.Audience),
			Subject:   userID,
			ID:        uuid.NewString(),
		},
	}

	tokenStr, err := jwtx.GenerateToken(
		func(*jwt.Token) (any, error) {
			return []byte(secretKey), nil
		},
		jwtx.WithClaims(func() jwt.Claims {
			return claims
		}),
		jwtx.WithSigningMethod(signingMethod),
	)
	if err != nil {
		return "", err
	}
	return tokenStr, nil
}

func (jm *JwtManager[T]) ParseToken(tokenStr string) (*Claims[T], error) {
	if strings.TrimSpace(tokenStr) == "" {
		return nil, ErrEmptyToken
	}

	secretKey := strings.TrimSpace(jm.conf.SecretKey)
	if secretKey == "" {
		return nil, ErrEmptyJWTSecret
	}
	signingMethod, err := jwtSigningMethod(jm.conf.Algorithm)
	if err != nil {
		return nil, err
	}

	token, err := jwtx.ParseToken(
		tokenStr,
		func(*jwt.Token) (any, error) {
			return []byte(secretKey), nil
		},
		jwtx.WithClaims(func() jwt.Claims {
			return &Claims[T]{}
		}),
		jwtx.WithSigningMethod(signingMethod),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to parse jwt token: %w", err)
	}

	if claims, ok := token.Claims.(*Claims[T]); ok && token.Valid {
		if err := jm.validateRegisteredClaims(claims); err != nil {
			return nil, err
		}
		return claims, nil
	}

	return nil, ErrInvalidToken
}

func (jm *JwtManager[T]) validateRegisteredClaims(claims *Claims[T]) error {
	if claims == nil {
		return ErrInvalidToken
	}

	if issuer := strings.TrimSpace(jm.conf.Issuer); issuer != "" && claims.Issuer != issuer {
		return fmt.Errorf("%w: %s", ErrInvalidIssuer, claims.Issuer)
	}
	if len(jm.conf.Audience) > 0 && !audienceMatches(claims.Audience, jm.conf.Audience) {
		return fmt.Errorf("%w: %v", ErrInvalidAudience, claims.Audience)
	}
	return nil
}

func audienceMatches(actual jwt.ClaimStrings, expected []string) bool {
	for _, want := range expected {
		want = strings.TrimSpace(want)
		if want == "" {
			continue
		}
		for _, got := range actual {
			if got == want {
				return true
			}
		}
	}
	return false
}

func (jm *JwtManager[T]) VerifyToken(tokenStr string) (bool, error) {
	if _, err := jm.ParseToken(tokenStr); err != nil {
		return false, fmt.Errorf("failed to verify token: %w", err)
	}
	return true, nil
}

func GenerateJWTSecret(length int) string {
	if length <= 0 {
		length = JWTSecretLength
	}
	return hex.EncodeToString(jwtx.GenerateJWTSecret(length))
}

func jwtSigningMethod(algorithm string) (jwt.SigningMethod, error) {
	switch strings.ToUpper(strings.TrimSpace(algorithm)) {
	case "HS256":
		return jwt.SigningMethodHS256, nil
	case "HS384":
		return jwt.SigningMethodHS384, nil
	case "HS512":
		return jwt.SigningMethodHS512, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedJWTAlgorithm, algorithm)
	}
}

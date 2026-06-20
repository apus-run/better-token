package token

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/apus-run/better-token/pkg/option"
	random "github.com/apus-run/better-token/pkg/rand"
)

const (
	DefaultSimpleLength = 16
	TiktokTokenLength   = 11
	TiktokCharset       = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	HashRandomBytesLen  = 16
	TimestampRandomLen  = 8

	DefaultTokenName = "token"
)

type TokenStyle string

const (
	TokenStyleSimple    TokenStyle = "simple"
	TokenStyleTimestamp TokenStyle = "timestamp"
	TokenStyleUUID      TokenStyle = "uuid"
	TokenStyleHash      TokenStyle = "hash"
	TokenStyleJWT       TokenStyle = "jwt"
	TokenStyleTiktok    TokenStyle = "tiktok"
)

type TokenConfig struct {
	JWTIssuer    string        `json:"jwt_issuer"`
	JWTSecretKey string        `json:"jwt_secret_key"`
	JWTAudience  []string      `json:"jwt_audience" `
	JWTAlgorithm string        `json:"jwt_algorithm"`
	JWTExpiry    time.Duration `json:"jwt_expiry" `

	TokenPrefix string     `json:"token_prefix" `
	TokenStyle  TokenStyle `json:"token_style"`
	TokenName   string     `json:"token_name"`

	SimpleTokenLength int `json:"simple_token_length"`
}

func DefaultTokenConfig() TokenConfig {
	return TokenConfig{
		JWTIssuer:    DefaultIssuer,
		JWTSecretKey: DefaultJWTSecret,
		JWTAlgorithm: DefaultJWTAlgorithm,
		JWTExpiry:    DefaultJWTExpiry,
		TokenStyle:   TokenStyleSimple,
		TokenName:    DefaultTokenName,

		SimpleTokenLength: DefaultSimpleLength,
	}
}

func NewTokenConfig(opts ...option.Option[TokenConfig]) *TokenConfig {
	config := DefaultTokenConfig()

	option.Apply(&config, opts...)

	return &config
}

func WithTokenConfig(config TokenConfig) option.Option[TokenConfig] {
	return func(c *TokenConfig) {
		WithJWTIssuer(config.JWTIssuer)(c)
		WithJWTSecretKey(config.JWTSecretKey)(c)
		WithJWTAudience(config.JWTAudience...)(c)
		WithJWTAlgorithm(config.JWTAlgorithm)(c)
		WithJWTExpiry(config.JWTExpiry)(c)
		WithTokenPrefix(config.TokenPrefix)(c)
		WithTokenStyle(config.TokenStyle)(c)
		WithTokenName(config.TokenName)(c)
		WithSimpleTokenLength(config.SimpleTokenLength)(c)
	}
}

func WithJWTIssuer(issuer string) option.Option[TokenConfig] {
	return func(c *TokenConfig) {
		if issuer == "" {
			issuer = DefaultIssuer
		}
		c.JWTIssuer = issuer
	}
}

func WithJWTSecretKey(secretKey string) option.Option[TokenConfig] {
	return func(c *TokenConfig) {
		c.JWTSecretKey = secretKey
	}
}

func WithJWTAudience(audience ...string) option.Option[TokenConfig] {
	return func(c *TokenConfig) {
		c.JWTAudience = append([]string(nil), audience...)
	}
}

func WithJWTAlgorithm(algorithm string) option.Option[TokenConfig] {
	return func(c *TokenConfig) {
		if algorithm == "" {
			algorithm = DefaultJWTAlgorithm
		}
		c.JWTAlgorithm = algorithm
	}
}

func WithJWTExpiry(expiry time.Duration) option.Option[TokenConfig] {
	return func(c *TokenConfig) {
		if expiry == 0 {
			expiry = DefaultJWTExpiry
		}
		c.JWTExpiry = expiry
	}
}

func WithTokenPrefix(prefix string) option.Option[TokenConfig] {
	return func(c *TokenConfig) {
		c.TokenPrefix = prefix
	}
}

func WithTokenStyle(style TokenStyle) option.Option[TokenConfig] {
	return func(c *TokenConfig) {
		if style == "" {
			style = TokenStyleSimple
		}
		c.TokenStyle = style
	}
}

func WithTokenName(name string) option.Option[TokenConfig] {
	return func(c *TokenConfig) {
		if name == "" {
			name = DefaultTokenName
		}
		c.TokenName = name
	}
}

func WithSimpleTokenLength(length int) option.Option[TokenConfig] {
	return func(c *TokenConfig) {
		if length <= 0 {
			length = DefaultSimpleLength
		}
		c.SimpleTokenLength = length
	}
}

type Generator[T any] interface {
	GenerateToken(userID string, data T) (string, error)
}

type TokenGenerator[T any] struct {
	config *TokenConfig
}

func NewTokenGenerator[T any](opts ...option.Option[TokenConfig]) *TokenGenerator[T] {
	jwtConfig := NewTokenConfig(opts...)

	return &TokenGenerator[T]{config: jwtConfig}
}

func (tg *TokenGenerator[T]) GenerateToken(userID string, data T) (string, error) {
	switch tg.config.TokenStyle {
	case TokenStyleJWT:
		return NewJwtManager[T](tg.config.jwtConfig()).GenerateToken(userID, data)
	case TokenStyleSimple:
		return generateSimpleToken(tg.config.SimpleTokenLength)
	case TokenStyleUUID:
		return generateUUIDToken()
	case TokenStyleTimestamp:
		return generateTimestampToken(userID)
	case TokenStyleHash:
		return generateHashToken(userID)
	case TokenStyleTiktok:
		return generateTiktokToken()
	default:
		return "", fmt.Errorf("%w: %s", ErrUnsupportedTokenStyle, tg.config.TokenStyle)
	}
}

func generateSimpleToken(length int) (string, error) {
	if length <= 0 {
		length = DefaultSimpleLength
	}

	tokenStr, err := random.RandomString(length)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrGenerateToken, err)
	}
	if tokenStr == "" {
		return "", fmt.Errorf("%w: random string is empty", ErrGenerateToken)
	}
	return tokenStr, nil
}

func generateUUIDToken() (string, error) {
	return uuid.NewString(), nil
}

func generateTimestampToken(userID string) (string, error) {
	randomBytes := make([]byte, TimestampRandomLen)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("%w: %w", ErrGenerateToken, err)
	}

	timestamp := time.Now().UnixMilli()
	randomPart := hex.EncodeToString(randomBytes)
	return fmt.Sprintf("%d_%s_%s", timestamp, strings.TrimSpace(userID), randomPart), nil
}

func generateHashToken(userID string) (string, error) {
	randomBytes := make([]byte, HashRandomBytesLen)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("%w: %w", ErrGenerateToken, err)
	}

	data := fmt.Sprintf("%s:%d:%s",
		strings.TrimSpace(userID),
		time.Now().UnixNano(),
		hex.EncodeToString(randomBytes))

	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:]), nil
}

func generateTiktokToken() (string, error) {
	result := make([]byte, TiktokTokenLength)
	charsetLen := int64(len(TiktokCharset))

	for i := range result {
		num, err := rand.Int(rand.Reader, big.NewInt(charsetLen))
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrGenerateToken, err)
		}
		result[i] = TiktokCharset[num.Int64()]
	}

	return string(result), nil
}

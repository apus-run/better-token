package core

import "time"

const (
	DefaultTokenName = "token"
	DefaultLoginType = "login"
)

type Config struct {
	TokenName   string
	TokenPrefix string

	Timeout       time.Duration
	ActiveTimeout time.Duration
	AutoRenew     bool

	Concurrent bool
	ShareToken bool

	RequireNonce bool

	// Refresh / Nonce 是各自能力的配置组，随 Config 一同承载。
	// 零值会在 NewManager 中由 withDefaults 兜底。
	Refresh RefreshConfig
	Nonce   NonceConfig
}

func DefaultConfig() Config {
	return Config{
		TokenName:     DefaultTokenName,
		TokenPrefix:   "",
		Timeout:       30 * 24 * time.Hour,
		ActiveTimeout: 0,
		AutoRenew:     false,
		Concurrent:    true,
		ShareToken:    false,
		RequireNonce:  false,
		Refresh:       DefaultRefreshConfig(),
		Nonce:         DefaultNonceConfig(),
	}
}

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
	}
}

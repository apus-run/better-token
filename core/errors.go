package core

import (
	"errors"
	"strings"
)

var (
	ErrEmptyLoginID    = errors.New("empty login id")
	ErrEmptyToken      = errors.New("empty token")
	ErrTokenNotFound   = errors.New("token not found")
	ErrTokenInvalid    = errors.New("token invalid")
	ErrUnsupportedKind = errors.New("unsupported token kind")
	ErrNotLogin        = errors.New("not login")
	ErrAuthorityDenied = errors.New("authority denied")
	ErrEmptySessionID  = errors.New("empty session id")
	ErrSessionNotFound = errors.New("session not found")
	ErrEmptyAuthority  = errors.New("empty authority")
	ErrEmptyDevice     = errors.New("empty device")

	ErrEmptyRefreshToken        = errors.New("empty refresh token")
	ErrRefreshTokenNotFound     = errors.New("refresh token not found")
	ErrRefreshTokenExpired      = errors.New("refresh token expired")
	ErrRefreshTokenRevoked      = errors.New("refresh token revoked")
	ErrNextRefreshTokenRequired = errors.New("next refresh token required")
	ErrNextRefreshTokenReuse    = errors.New("next refresh token must differ")
	ErrNextAccessTokenReuse     = errors.New("next access token must differ")

	ErrEmptyNonce    = errors.New("empty nonce")
	ErrNonceNotFound = errors.New("nonce not found")
	ErrNonceExpired  = errors.New("nonce expired")
	ErrNonceReplayed = errors.New("nonce replayed")

	ErrEventListenerPanic = errors.New("event listener panic")
)

type AuthorityDeniedError struct {
	Authority Authority
}

func (e AuthorityDeniedError) Error() string {
	kind := strings.TrimSpace(string(e.Authority.Type))
	value := strings.TrimSpace(e.Authority.Value)
	if kind == "" && value == "" {
		return ErrAuthorityDenied.Error()
	}
	if kind == "" {
		return ErrAuthorityDenied.Error() + ": " + value
	}
	if value == "" {
		return ErrAuthorityDenied.Error() + ": " + kind
	}
	return ErrAuthorityDenied.Error() + ": " + kind + ":" + value
}

func (e AuthorityDeniedError) Unwrap() error {
	return ErrAuthorityDenied
}

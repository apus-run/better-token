package core

import (
	"errors"
	"strings"
)

var (
	ErrEmptyLoginID    = errors.New("empty login id")
	ErrEmptyToken      = errors.New("empty token")
	ErrTokenNotFound   = errors.New("token not found")
	ErrNotLogin        = errors.New("not login")
	ErrAuthorityDenied = errors.New("authority denied")
	ErrEmptySessionID  = errors.New("empty session id")
	ErrSessionNotFound = errors.New("session not found")
	ErrEmptyAuthority  = errors.New("empty authority")
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

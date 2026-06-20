package token

import "errors"

var (
	ErrEmptyUserID             = errors.New("user id is empty")
	ErrEmptyToken              = errors.New("token is empty")
	ErrEmptyJWTSecret          = errors.New("jwt secret key is empty")
	ErrInvalidToken            = errors.New("invalid token")
	ErrInvalidIssuer           = errors.New("invalid token issuer")
	ErrInvalidAudience         = errors.New("invalid token audience")
	ErrUnsupportedTokenStyle   = errors.New("unsupported token style")
	ErrUnsupportedJWTAlgorithm = errors.New("unsupported jwt algorithm")
	ErrGenerateToken           = errors.New("failed to generate token")
)

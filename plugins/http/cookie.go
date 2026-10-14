package http

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/apus-run/better-token/core"
)

var ErrInvalidCookie = errors.New("invalid token cookie")

// CookieConfig is shared by cookie extraction, writing and deletion. Start with
// DefaultCookieConfig when customizing flags; a literal's booleans are explicit.
type CookieConfig struct {
	Name     string
	Path     string
	Domain   string
	Secure   bool
	HttpOnly bool
	SameSite http.SameSite
}

func DefaultCookieConfig() CookieConfig {
	return CookieConfig{Name: core.DefaultTokenName, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode}
}

// SetTokenCookie writes the raw credential, without a header token prefix.
// ttl <= 0 creates a browser-session cookie; positive ttl sets its lifetime.
func (c CookieConfig) SetTokenCookie(w http.ResponseWriter, token core.TokenValue, ttl time.Duration) error {
	if w == nil {
		return fmt.Errorf("%w: nil response writer", ErrInvalidCookie)
	}
	token = core.TokenValue(strings.TrimSpace(string(token)))
	if token == "" {
		return core.ErrEmptyToken
	}
	cookie, err := c.cookie(string(token))
	if err != nil {
		return err
	}
	if ttl > 0 {
		seconds := int64((ttl-1)/time.Second) + 1
		if int64(int(seconds)) != seconds {
			return fmt.Errorf("%w: lifetime exceeds MaxAge range", ErrInvalidCookie)
		}
		cookie.MaxAge = int(seconds)
		cookie.Expires = time.Now().Add(ttl).UTC()
	}
	http.SetCookie(w, cookie)
	return nil
}

// ClearTokenCookie deletes the same name/path/domain cookie used for writing.
func (c CookieConfig) ClearTokenCookie(w http.ResponseWriter) error {
	if w == nil {
		return fmt.Errorf("%w: nil response writer", ErrInvalidCookie)
	}
	cookie, err := c.cookie("")
	if err != nil {
		return err
	}
	cookie.MaxAge = -1
	cookie.Expires = time.Unix(1, 0).UTC()
	http.SetCookie(w, cookie)
	return nil
}

func (c CookieConfig) cookie(value string) (*http.Cookie, error) {
	if c.Name == "" {
		c.Name = core.DefaultTokenName
	}
	if c.Path == "" {
		c.Path = "/"
	}
	if !strings.HasPrefix(c.Path, "/") {
		return nil, fmt.Errorf("%w: Path must be absolute", ErrInvalidCookie)
	}
	if c.SameSite == 0 {
		c.SameSite = http.SameSiteLaxMode
	}
	if c.SameSite < http.SameSiteDefaultMode || c.SameSite > http.SameSiteNoneMode {
		return nil, fmt.Errorf("%w: unsupported SameSite", ErrInvalidCookie)
	}
	if c.SameSite == http.SameSiteNoneMode && !c.Secure {
		return nil, fmt.Errorf("%w: SameSite=None requires Secure", ErrInvalidCookie)
	}
	if strings.HasPrefix(c.Name, "__Secure-") && !c.Secure {
		return nil, fmt.Errorf("%w: __Secure- requires Secure", ErrInvalidCookie)
	}
	if strings.HasPrefix(c.Name, "__Host-") && (!c.Secure || c.Domain != "" || c.Path != "/") {
		return nil, fmt.Errorf("%w: __Host- requires Secure, Path=/ and no Domain", ErrInvalidCookie)
	}
	cookie := &http.Cookie{Name: c.Name, Value: value, Path: c.Path, Domain: c.Domain, Secure: c.Secure, HttpOnly: c.HttpOnly, SameSite: c.SameSite}
	if err := cookie.Valid(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCookie, err)
	}
	return cookie, nil
}

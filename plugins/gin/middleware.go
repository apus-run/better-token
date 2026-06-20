package gin

import (
	"github.com/gin-gonic/gin"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/plugins"
	httpplugin "github.com/apus-run/better-token/plugins/http"
)

type Option = httpplugin.Option

var (
	WithTokenName    = httpplugin.WithTokenName
	WithTokenPrefix  = httpplugin.WithTokenPrefix
	WithUnauthorized = httpplugin.WithUnauthorized
)

func Middleware(manager *core.Manager, opts ...Option) gin.HandlerFunc {
	config := core.DefaultConfig()
	if manager != nil {
		config = manager.Config()
	}
	extractor := httpplugin.NewExtractor(httpplugin.OptionsFromConfig(config, opts...)...)
	unauthorized := extractor.Unauthorized()

	return func(c *gin.Context) {
		if manager == nil {
			unauthorized(c.Writer, c.Request)
			c.Abort()
			return
		}
		token, ok := extractor.ExtractToken(c.Request)
		if !ok {
			unauthorized(c.Writer, c.Request)
			c.Abort()
			return
		}
		auth, err := plugins.Authenticate(c.Request.Context(), manager, token)
		if err != nil {
			unauthorized(c.Writer, c.Request)
			c.Abort()
			return
		}
		c.Request = c.Request.WithContext(core.WithAuth(c.Request.Context(), auth))
		c.Set("auth", auth)
		c.Next()
	}
}

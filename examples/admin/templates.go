package main

import (
	"embed"
	"html/template"
	"strings"
	"time"

	"github.com/apus-run/better-token/core"
)

//go:embed templates/*.html
var templateFS embed.FS

func parseTemplates() (*template.Template, error) {
	return template.New("").Funcs(template.FuncMap{
		"short": func(token core.TokenValue) string {
			s := string(token)
			if len(s) <= 12 {
				return s
			}
			return s[:8] + "…" + s[len(s)-4:]
		},
		"timefmt": func(t time.Time) string {
			if t.IsZero() {
				return "-"
			}
			return t.Local().Format("01-02 15:04:05")
		},
		"ptrtime": func(t *time.Time) string {
			if t == nil {
				return "永不过期"
			}
			return t.Local().Format("01-02 15:04:05")
		},
		"session": func(s *core.Session, key string) string {
			if s == nil {
				return ""
			}
			v, _ := s.Get(key)
			if v == nil {
				return ""
			}
			return strings.TrimSpace(v.(string))
		},
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
	}).ParseFS(templateFS, "templates/*.html")
}

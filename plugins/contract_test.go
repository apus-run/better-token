package plugins_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/plugins"
)

func TestLegacyContractKeepsTypeIdentityAndMutableDefault(t *testing.T) {
	const path = "github.com/apus-run/better-token/plugins"
	for _, v := range []any{plugins.SourceHeader, plugins.Getters{}, plugins.TokenLookup{}} {
		if reflect.TypeOf(v).PkgPath() != path {
			t.Fatalf("type moved: %T", v)
		}
	}
	saved := plugins.DefaultOrder
	defer func() { plugins.DefaultOrder = saved }()
	plugins.DefaultOrder = []plugins.Source{plugins.SourceQuery}
	token, ok := (plugins.TokenLookup{}).Resolve(plugins.Getters{Query: func(string) string { return "legacy-query" }})
	if !ok || token != "legacy-query" {
		t.Fatal("mutable default ignored")
	}
	var m *core.Manager
	if _, err := plugins.Authenticate(context.Background(), m, "token"); !errors.Is(err, core.ErrNotLogin) {
		t.Fatalf("typed nil = %v", err)
	}
}

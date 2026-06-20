package rbac_test

import (
	"context"
	"testing"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/rbac"
)

func TestAuthorizerRolesPermissionsAndDirectPermissions(t *testing.T) {
	authorizer := rbac.NewAuthorizer()
	authorizer.AssignRole("1001", "admin")
	authorizer.GrantPermission("admin", "user:*")
	authorizer.SetDirectPermissions("1001", []string{"report:read"})

	ok, err := authorizer.HasAuthority(context.Background(), "1001", core.Role("admin"))
	if err != nil || !ok {
		t.Fatalf("role check ok=%v err=%v", ok, err)
	}
	ok, err = authorizer.HasAuthority(context.Background(), "1001", core.Permission("user:create"))
	if err != nil || !ok {
		t.Fatalf("role permission wildcard check ok=%v err=%v", ok, err)
	}
	ok, err = authorizer.HasAuthority(context.Background(), "1001", core.Permission("report:read"))
	if err != nil || !ok {
		t.Fatalf("direct permission check ok=%v err=%v", ok, err)
	}

	authorities, err := authorizer.GetAuthorities(context.Background(), "1001")
	if err != nil {
		t.Fatalf("GetAuthorities failed: %v", err)
	}
	if len(authorities) != 3 {
		t.Fatalf("expected 3 authorities, got %#v", authorities)
	}

	authorizer.RevokePermission("admin", "user:*")
	ok, err = authorizer.HasAuthority(context.Background(), "1001", core.Permission("user:create"))
	if err != nil {
		t.Fatalf("permission check after revoke failed: %v", err)
	}
	if ok {
		t.Fatal("revoked role permission should not match")
	}

	authorizer.RevokeRole("1001", "admin")
	ok, err = authorizer.HasAuthority(context.Background(), "1001", core.Role("admin"))
	if err != nil {
		t.Fatalf("role check after revoke failed: %v", err)
	}
	if ok {
		t.Fatal("revoked role should not match")
	}
}

package rbac

import (
	"context"
	"strings"
	"sync"

	"github.com/apus-run/better-token/core"
)

var _ core.Authorizer = (*Authorizer)(nil)

type Authorizer struct {
	mu                sync.RWMutex
	userRoles         map[string]map[string]struct{}
	rolePermissions   map[string]map[string]struct{}
	directPermissions map[string]map[string]struct{}
}

type Option func(*Authorizer)

func NewAuthorizer(opts ...Option) *Authorizer {
	a := &Authorizer{
		userRoles:         make(map[string]map[string]struct{}),
		rolePermissions:   make(map[string]map[string]struct{}),
		directPermissions: make(map[string]map[string]struct{}),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(a)
		}
	}
	return a
}

func (a *Authorizer) AssignRole(loginID, role string) {
	loginID = strings.TrimSpace(loginID)
	role = strings.TrimSpace(role)
	if loginID == "" || role == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.userRoles[loginID] == nil {
		a.userRoles[loginID] = make(map[string]struct{})
	}
	a.userRoles[loginID][role] = struct{}{}
}

func (a *Authorizer) RevokeRole(loginID, role string) {
	loginID = strings.TrimSpace(loginID)
	role = strings.TrimSpace(role)
	if loginID == "" || role == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.userRoles[loginID], role)
	if len(a.userRoles[loginID]) == 0 {
		delete(a.userRoles, loginID)
	}
}

func (a *Authorizer) GrantPermission(role, permission string) {
	role = strings.TrimSpace(role)
	permission = strings.TrimSpace(permission)
	if role == "" || permission == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.rolePermissions[role] == nil {
		a.rolePermissions[role] = make(map[string]struct{})
	}
	a.rolePermissions[role][permission] = struct{}{}
}

func (a *Authorizer) RevokePermission(role, permission string) {
	role = strings.TrimSpace(role)
	permission = strings.TrimSpace(permission)
	if role == "" || permission == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.rolePermissions[role], permission)
	if len(a.rolePermissions[role]) == 0 {
		delete(a.rolePermissions, role)
	}
}

func (a *Authorizer) SetDirectPermissions(loginID string, permissions []string) {
	loginID = strings.TrimSpace(loginID)
	if loginID == "" {
		return
	}
	values := make(map[string]struct{})
	for _, permission := range permissions {
		permission = strings.TrimSpace(permission)
		if permission == "" {
			continue
		}
		values[permission] = struct{}{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.directPermissions[loginID] = values
}

func (a *Authorizer) HasAuthority(ctx context.Context, loginID string, authority core.Authority) (bool, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return false, err
		}
	}
	loginID = strings.TrimSpace(loginID)
	authority.Value = strings.TrimSpace(authority.Value)
	if loginID == "" || authority.Value == "" {
		return false, nil
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	switch authority.Type {
	case core.AuthorityRole:
		_, ok := a.userRoles[loginID][authority.Value]
		return ok, nil
	case core.AuthorityPermission:
		if permissionSetMatches(a.directPermissions[loginID], authority.Value) {
			return true, nil
		}
		for role := range a.userRoles[loginID] {
			if permissionSetMatches(a.rolePermissions[role], authority.Value) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (a *Authorizer) GetAuthorities(ctx context.Context, loginID string) ([]core.Authority, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	loginID = strings.TrimSpace(loginID)
	if loginID == "" {
		return nil, nil
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	seen := make(map[core.Authority]struct{})
	result := make([]core.Authority, 0)
	add := func(authority core.Authority) {
		if authority.Value == "" {
			return
		}
		if _, ok := seen[authority]; ok {
			return
		}
		seen[authority] = struct{}{}
		result = append(result, authority)
	}
	for role := range a.userRoles[loginID] {
		add(core.Role(role))
		for permission := range a.rolePermissions[role] {
			add(core.Permission(permission))
		}
	}
	for permission := range a.directPermissions[loginID] {
		add(core.Permission(permission))
	}
	return result, nil
}

func permissionSetMatches(values map[string]struct{}, requested string) bool {
	for candidate := range values {
		if authorityValueMatches(candidate, requested) {
			return true
		}
	}
	return false
}

func authorityValueMatches(candidate, requested string) bool {
	candidate = strings.TrimSpace(candidate)
	requested = strings.TrimSpace(requested)
	if candidate == requested {
		return true
	}
	if strings.HasSuffix(candidate, "*") {
		prefix := strings.TrimSuffix(candidate, "*")
		return strings.HasPrefix(requested, prefix)
	}
	return false
}

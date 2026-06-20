package core

import (
	"context"
	"strings"
	"sync"
)

type AuthorityType string

const (
	AuthorityPermission AuthorityType = "permission"
	AuthorityRole       AuthorityType = "role"
)

type Authority struct {
	Type  AuthorityType `json:"type"`
	Value string        `json:"value"`
}

func Permission(value string) Authority {
	return Authority{Type: AuthorityPermission, Value: strings.TrimSpace(value)}
}

func Role(value string) Authority {
	return Authority{Type: AuthorityRole, Value: strings.TrimSpace(value)}
}

type Authorizer interface {
	HasAuthority(ctx context.Context, loginID string, authority Authority) (bool, error)
	GetAuthorities(ctx context.Context, loginID string) ([]Authority, error)
}

type NoopAuthorizer struct{}

func (NoopAuthorizer) HasAuthority(context.Context, string, Authority) (bool, error) {
	return false, nil
}

func (NoopAuthorizer) GetAuthorities(context.Context, string) ([]Authority, error) {
	return nil, nil
}

type MemoryAuthorizer struct {
	mu          sync.RWMutex
	authorities map[string][]Authority
}

func NewMemoryAuthorizer() *MemoryAuthorizer {
	return &MemoryAuthorizer{authorities: make(map[string][]Authority)}
}

func (a *MemoryAuthorizer) SetAuthorities(loginID string, authorities []Authority) {
	a.mu.Lock()
	defer a.mu.Unlock()

	copied := make([]Authority, 0, len(authorities))
	for _, authority := range authorities {
		authority.Value = strings.TrimSpace(authority.Value)
		if authority.Value == "" {
			continue
		}
		copied = append(copied, authority)
	}
	a.authorities[strings.TrimSpace(loginID)] = copied
}

func (a *MemoryAuthorizer) SetRoles(loginID string, roles []string) {
	authorities := make([]Authority, 0, len(roles))
	for _, role := range roles {
		authorities = append(authorities, Role(role))
	}
	a.mergeAuthorities(loginID, AuthorityRole, authorities)
}

func (a *MemoryAuthorizer) SetPermissions(loginID string, permissions []string) {
	authorities := make([]Authority, 0, len(permissions))
	for _, permission := range permissions {
		authorities = append(authorities, Permission(permission))
	}
	a.mergeAuthorities(loginID, AuthorityPermission, authorities)
}

func (a *MemoryAuthorizer) mergeAuthorities(loginID string, authorityType AuthorityType, authorities []Authority) {
	loginID = strings.TrimSpace(loginID)
	a.mu.Lock()
	defer a.mu.Unlock()

	next := make([]Authority, 0, len(a.authorities[loginID])+len(authorities))
	for _, authority := range a.authorities[loginID] {
		if authority.Type != authorityType {
			next = append(next, authority)
		}
	}
	next = append(next, authorities...)
	a.authorities[loginID] = next
}

func (a *MemoryAuthorizer) HasAuthority(ctx context.Context, loginID string, authority Authority) (bool, error) {
	if err := ctxErr(ctx); err != nil {
		return false, err
	}

	authority.Value = strings.TrimSpace(authority.Value)
	if authority.Value == "" {
		return false, nil
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	for _, candidate := range a.authorities[strings.TrimSpace(loginID)] {
		if authorityMatches(candidate, authority) {
			return true, nil
		}
	}
	return false, nil
}

func (a *MemoryAuthorizer) GetAuthorities(ctx context.Context, loginID string) ([]Authority, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	values := a.authorities[strings.TrimSpace(loginID)]
	result := make([]Authority, len(values))
	copy(result, values)
	return result, nil
}

func authorityMatches(candidate, requested Authority) bool {
	if candidate.Type != requested.Type {
		return false
	}
	candidate.Value = strings.TrimSpace(candidate.Value)
	requested.Value = strings.TrimSpace(requested.Value)
	if candidate.Value == requested.Value {
		return true
	}
	if strings.HasSuffix(candidate.Value, "*") {
		prefix := strings.TrimSuffix(candidate.Value, "*")
		return strings.HasPrefix(requested.Value, prefix)
	}
	return false
}

package storage_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/apus-run/better-token/core"
	"github.com/apus-run/better-token/plugins"
	dbstore "github.com/apus-run/better-token/storage/database"
	"github.com/apus-run/better-token/storage/memory"
	redisstore "github.com/apus-run/better-token/storage/redis"
	"github.com/apus-run/gala/components/db"
	"github.com/apus-run/gala/components/rdb"
	"gorm.io/driver/sqlite"
)

var ctx = context.Background()

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func stores(t *testing.T, f func(*testing.T, core.Store)) {
	for _, name := range []string{"memory", "redis", "database"} {
		t.Run(name, func(t *testing.T) {
			var s core.Store
			switch name {
			case "memory":
				s = memory.NewStore()
			case "redis":
				p, e := rdb.NewRDB("redis://" + miniredis.RunT(t).Addr())
				must(t, e)
				s = redisstore.NewStore(p)
			case "database":
				p, e := db.NewDB(sqlite.Open(filepath.Join(t.TempDir(), "test.db")))
				must(t, e)
				d := dbstore.NewStore(p)
				must(t, d.Migrate(ctx))
				s = d
			}
			f(t, s)
		})
	}
}
func state() *core.TokenState {
	return &core.TokenState{Token: "key", LoginID: "alice", LoginType: "login", Kind: core.TokenKindAccess, Status: core.TokenStatusActive}
}
func TestCASDoesNotCreate(t *testing.T) {
	stores(t, func(t *testing.T, s core.Store) {
		before := state()
		must(t, s.SaveTokenState(ctx, before, time.Hour))
		before, _, err := s.GetTokenState(ctx, before.Token)
		must(t, err)
		must(t, s.DeleteTokenState(ctx, before.Token))
		after := before.Clone()
		after.Touch(time.Now())
		updated, err := s.(core.AtomicStore).CompareAndSwapTokenState(ctx, before, after, time.Hour)
		must(t, err)
		if updated {
			t.Fatal("CAS recreated deleted token")
		}
	})
}
func TestConsumeChecksKind(t *testing.T) {
	stores(t, func(t *testing.T, s core.Store) {
		before := state()
		must(t, s.SaveTokenState(ctx, before, time.Hour))
		_, _, err := s.(core.AtomicStore).ConsumeTokenStateOfKind(ctx, before.Token, core.TokenKindNonce)
		must(t, err)
		got, _, err := s.ConsumeTokenState(ctx, before.Token)
		must(t, err)
		if got.IsConsumed() {
			t.Fatal("wrong-kind operation consumed access")
		}
	})
}
func TestRejectedAuthDoesNotResetNonce(t *testing.T) {
	stores(t, func(t *testing.T, s core.Store) {
		c := core.DefaultConfig()
		c.AutoRenew = true
		m := core.NewManager(s, core.WithConfig(c))
		n, err := m.GenerateNonce(ctx)
		must(t, err)
		_, err = m.ConsumeNonce(ctx, n)
		must(t, err)
		if _, err = plugins.Authenticate(ctx, m, n); err == nil {
			t.Fatal("nonce authenticated")
		}
		if _, err = m.ConsumeNonce(ctx, n); err == nil {
			t.Fatal("nonce replayed after rejected authentication")
		}
	})
}
func TestSubjectIsolation(t *testing.T) {
	stores(t, func(t *testing.T, s core.Store) {
		a := core.NewSessionForSubject(core.LoginSubject{LoginType: "a:b", LoginID: "c"})
		a.Set("owner", "a")
		must(t, s.SaveSession(ctx, a, time.Hour))
		b := core.NewSessionForSubject(core.LoginSubject{LoginType: "a", LoginID: "b:c"})
		b.Set("owner", "b")
		must(t, s.SaveSession(ctx, b, time.Hour))
		got, _, err := s.GetSession(ctx, a.Subject)
		must(t, err)
		if got.Data["owner"] != "a" {
			t.Fatal("subject collision")
		}
	})
}
func TestRefreshKindsStayDistinct(t *testing.T) {
	stores(t, func(t *testing.T, s core.Store) {
		m := core.NewManager(s)
		result, err := m.LoginWithRefresh(ctx, "alice", "same", "same")
		if err == nil {
			if _, err = plugins.Authenticate(ctx, m, result.TokenState.Token); err != nil {
				t.Fatal("login succeeded with unusable access")
			}
		}
	})
}
func TestAuthenticationKind(t *testing.T) {
	m := core.NewManager(memory.NewStore())
	_, err := m.LoginWithRefresh(ctx, "alice", "a", "r")
	must(t, err)
	if _, err = plugins.Authenticate(ctx, m, "r"); err == nil {
		t.Fatal("refresh authenticated")
	}
}
func TestQueryIsOptIn(t *testing.T) {
	if _, ok := (plugins.TokenLookup{}).Resolve(plugins.Getters{Query: func(string) string { return "secret" }}); ok {
		t.Fatal("query accepted by default")
	}
}
func TestDefaultOrderCompatibility(t *testing.T) {
	old := plugins.DefaultOrder
	defer func() { plugins.DefaultOrder = old }()
	plugins.DefaultOrder = []plugins.Source{plugins.SourceQuery}
	if _, ok := (plugins.TokenLookup{}).Resolve(plugins.Getters{Query: func(string) string { return "secret" }}); !ok {
		t.Fatal("legacy DefaultOrder ignored")
	}
}

// Wrappers that expose only Store hide optional capabilities.
type legacyWrapper struct{ core.Store }

func TestWrapperCapabilityLoss(t *testing.T) {
	s := memory.NewStore()
	var wrapped core.Store = &legacyWrapper{s}
	if _, ok := wrapped.(core.AtomicStore); ok {
		t.Fatal("unexpected capability promotion")
	}
	t.Log("Store-only wrapper hides AtomicStore; atomic wrappers must embed or forward the complete extension")
}

type sessionHook struct {
	core.Store
	core.AtomicStore
	afterRead func()
}

func (s *sessionHook) GetSession(c context.Context, subject core.LoginSubject) (*core.Session, bool, error) {
	value, ok, err := s.Store.GetSession(c, subject)
	if s.afterRead != nil {
		f := s.afterRead
		s.afterRead = nil
		f()
	}
	return value, ok, err
}
func TestSessionTTLDoesNotOverwrite(t *testing.T) {
	stores(t, func(t *testing.T, s core.Store) {
		session := core.NewSession("alice")
		session.Set("value", "old")
		must(t, s.SaveSession(ctx, session, time.Hour))
		wrapped := &sessionHook{Store: s, AtomicStore: s.(core.AtomicStore), afterRead: func() {
			next := core.NewSession("alice")
			next.Set("value", "new")
			must(t, s.SaveSession(ctx, next, time.Hour))
		}}
		m := core.NewManager(wrapped)
		_, err := m.Login(ctx, "alice", "access")
		must(t, err)
		got, _, err := s.GetSession(ctx, session.Subject)
		must(t, err)
		if got.Data["value"] != "new" {
			t.Fatal("TTL synchronization overwrote concurrent Session data")
		}
	})
}

// Preserve AtomicStore while injecting a concurrent Session content update.
func (s *sessionHook) EnsureSession(c context.Context, subject core.LoginSubject, ttl time.Duration) error {
	if s.afterRead != nil {
		f := s.afterRead
		s.afterRead = nil
		f()
	}
	return s.AtomicStore.EnsureSession(c, subject, ttl)
}

func TestRevokedNonceRejected(t *testing.T) {
	stores(t, func(t *testing.T, s core.Store) {
		token := core.TokenValue("nonce-secret")
		n := state()
		n.Token = core.TokenValue(fmt.Sprintf("%x", sha256.Sum256([]byte(token))))
		n.Kind = core.TokenKindNonce
		n.Status = core.TokenStatusRevoked
		n.Nonce = &core.NonceInfo{}
		must(t, s.SaveTokenState(ctx, n, time.Hour))
		if _, err := core.NewManager(s).ConsumeNonce(ctx, token); err == nil {
			t.Fatal("revoked nonce accepted")
		}
	})
}
func TestNonRotatingRefreshSingleSession(t *testing.T) {
	stores(t, func(t *testing.T, s core.Store) {
		c := core.DefaultConfig()
		c.Concurrent = false
		c.Refresh.RotateRefreshToken = false
		m := core.NewManager(s, core.WithConfig(c))
		_, err := m.LoginWithRefresh(ctx, "alice", "a", "r")
		must(t, err)
		_, err = m.Refresh(ctx, "r", "next-a")
		if err != nil {
			t.Fatalf("non-rotating single-session refresh failed: %v", err)
		}
	})
}

func TestCASRejectsChangedSnapshot(t *testing.T) {
	stores(t, func(t *testing.T, s core.Store) {
		original := state()
		must(t, s.SaveTokenState(ctx, original, time.Hour))
		expected, _, err := s.GetTokenState(ctx, original.Token)
		must(t, err)
		changed := expected.Clone()
		changed.Device = "new-device"
		must(t, s.SaveTokenState(ctx, changed, time.Hour))
		next := expected.Clone()
		next.Touch(time.Now())
		updated, err := s.(core.AtomicStore).CompareAndSwapTokenState(ctx, expected, next, time.Hour)
		must(t, err)
		if updated {
			t.Fatal("stale snapshot overwrote a newer state")
		}
		got, found, err := s.GetTokenState(ctx, original.Token)
		must(t, err)
		if !found || got.Device != "new-device" {
			t.Fatalf("newer state lost: %+v", got)
		}
	})
}

// Forward the explicit optional operation, injecting a second Manager's logout
// immediately before CAS. This also verifies that core actually selects CAS.
type updateHook struct {
	core.Store
	core.AtomicStore
	beforeUpdate func()
}

func (s *updateHook) CompareAndSwapTokenState(c context.Context, before, after *core.TokenState, ttl time.Duration) (bool, error) {
	if s.beforeUpdate != nil {
		f := s.beforeUpdate
		s.beforeUpdate = nil
		f()
	}
	return s.AtomicStore.CompareAndSwapTokenState(c, before, after, ttl)
}
func TestUpdatesCannotUndoLogout(t *testing.T) {
	for _, operation := range []string{"renew", "auto-renew", "online", "offline"} {
		t.Run(operation, func(t *testing.T) {
			stores(t, func(t *testing.T, s core.Store) {
				config := core.DefaultConfig()
				config.AutoRenew = operation == "auto-renew"
				wrapped := &updateHook{Store: s, AtomicStore: s.(core.AtomicStore)}
				m := core.NewManager(wrapped, core.WithConfig(config))
				other := core.NewManager(s)
				_, err := m.Login(ctx, "alice", "access")
				must(t, err)
				wrapped.beforeUpdate = func() { must(t, other.Logout(ctx, "access")) }
				switch operation {
				case "renew":
					err = m.Renew(ctx, "access", time.Hour)
				case "auto-renew":
					_, err = m.GetTokenState(ctx, "access")
				case "online":
					err = m.MarkOnline(ctx, "access", core.OnlineInfo{})
				case "offline":
					err = m.MarkOffline(ctx, "access")
				}
				if !errors.Is(err, core.ErrTokenInvalid) {
					t.Fatalf("stale update returned %v", err)
				}
				if other.IsValid(ctx, "access") {
					t.Fatal("stale update resurrected logged-out access")
				}
			})
		})
	}
}
func TestNonceWrongKindLeavesAccessUsable(t *testing.T) {
	stores(t, func(t *testing.T, s core.Store) {
		m := core.NewManager(s)
		_, err := m.Login(ctx, "alice", "access")
		must(t, err)
		if _, err = m.ConsumeNonce(ctx, "access"); !errors.Is(err, core.ErrUnsupportedKind) {
			t.Fatalf("wrong error: %v", err)
		}
		_, err = plugins.Authenticate(ctx, m, "access")
		must(t, err)
	})
}
func TestConsumedStateVisibleEverywhere(t *testing.T) {
	stores(t, func(t *testing.T, s core.Store) {
		n := state()
		n.Kind = core.TokenKindNonce
		n.Nonce = &core.NonceInfo{}
		must(t, s.SaveTokenState(ctx, n, time.Hour))
		snapshot, _, err := s.ConsumeTokenState(ctx, n.Token)
		must(t, err)
		if snapshot.IsConsumed() {
			t.Fatal("must return pre-consume snapshot")
		}
		got, _, err := s.GetTokenState(ctx, n.Token)
		must(t, err)
		if !got.IsConsumed() {
			t.Fatal("Get did not observe consumption")
		}
		listed, err := s.FindTokenStates(ctx, n.Subject(), core.TokenKindNonce)
		must(t, err)
		if len(listed) != 1 || !listed[0].IsConsumed() {
			t.Fatal("Find did not observe consumption")
		}
	})
}
func TestExactlyOneConcurrentConsumer(t *testing.T) {
	stores(t, func(t *testing.T, s core.Store) {
		m := core.NewManager(s)
		n, err := m.GenerateNonce(ctx)
		must(t, err)
		var wg sync.WaitGroup
		results := make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); _, err := m.ConsumeNonce(ctx, n); results <- err }()
		}
		wg.Wait()
		close(results)
		successes := 0
		for err := range results {
			if err == nil {
				successes++
			}
		}
		if successes != 1 {
			t.Fatalf("successful consumers = %d, want 1", successes)
		}
	})
}
func TestRefreshAliasValidationHasNoSideEffects(t *testing.T) {
	for _, kind := range []string{"next-access-is-refresh", "next-pair-equal", "next-refresh-is-old-access"} {
		t.Run(kind, func(t *testing.T) {
			stores(t, func(t *testing.T, s core.Store) {
				m := core.NewManager(s)
				_, err := m.LoginWithRefresh(ctx, "alice", "a", "r")
				must(t, err)
				nextAccess, nextRefresh := core.TokenValue("next-a"), core.TokenValue("next-r")
				switch kind {
				case "next-access-is-refresh":
					nextAccess = "r"
				case "next-pair-equal":
					nextRefresh = nextAccess
				case "next-refresh-is-old-access":
					nextRefresh = "a"
				}
				if _, err = m.Refresh(ctx, "r", nextAccess, core.WithNextRefreshToken(nextRefresh)); err == nil {
					t.Fatal("credential alias accepted")
				}
				if !m.IsValid(ctx, "a") {
					t.Fatal("validation revoked existing access")
				}
				_, err = m.Refresh(ctx, "r", "valid-a", core.WithNextRefreshToken("valid-r"))
				must(t, err)
			})
		})
	}
}

type splitConsumer struct {
	core.Store
	core.AtomicStore
	readers sync.WaitGroup
	writes  sync.Mutex
}

func (s *splitConsumer) ConsumeTokenState(c context.Context, k core.TokenValue) (*core.TokenState, bool, error) {
	snapshot, found, err := s.Store.GetTokenState(c, k)
	s.readers.Done()
	s.readers.Wait()
	if err != nil || !found {
		return snapshot, found, err
	}
	next := snapshot.Clone()
	next.MarkConsumed(time.Now())
	s.writes.Lock()
	err = s.Store.SaveTokenState(c, next, time.Minute)
	s.writes.Unlock()
	return snapshot, found, err
}
func (s *splitConsumer) ConsumeTokenStateOfKind(c context.Context, k core.TokenValue, kind core.TokenKind) (*core.TokenState, bool, error) {
	return s.ConsumeTokenState(c, k)
}
func TestAtomicityAblation(t *testing.T) {
	for _, ablated := range []bool{false, true} {
		t.Run(fmt.Sprint("ablated=", ablated), func(t *testing.T) {
			var s core.Store = memory.NewStore()
			if ablated {
				split := &splitConsumer{Store: s, AtomicStore: s.(core.AtomicStore)}
				split.readers.Add(8)
				s = split
			}
			m := core.NewManager(s)
			n, err := m.GenerateNonce(ctx)
			must(t, err)
			results := make(chan error, 8)
			for i := 0; i < 8; i++ {
				go func() { _, err := m.ConsumeNonce(ctx, n); results <- err }()
			}
			successes := 0
			for i := 0; i < 8; i++ {
				if <-results == nil {
					successes++
				}
			}
			want := 1
			if ablated {
				want = 8
			}
			t.Logf("successful consumers=%d", successes)
			if successes != want {
				t.Fatalf("want %d successes", want)
			}
		})
	}
}

// Decorators expose storage and atomic capabilities independently.
var _ core.Store = (*sessionHook)(nil)
var _ core.AtomicStore = (*sessionHook)(nil)
var _ core.Store = (*updateHook)(nil)
var _ core.AtomicStore = (*updateHook)(nil)
var _ core.Store = (*splitConsumer)(nil)
var _ core.AtomicStore = (*splitConsumer)(nil)

func TestSessionSnapshotsDetachNestedData(t *testing.T) {
	stores(t, func(t *testing.T, s core.Store) {
		session := core.NewSession("alice")
		profile := map[string]any{"roles": []any{"user"}}
		session.Set("profile", profile)
		must(t, s.SaveSession(ctx, session, time.Hour))
		profile["roles"].([]any)[0] = "changed-after-save"
		first, found, err := s.GetSession(ctx, session.Subject)
		must(t, err)
		if !found {
			t.Fatal("session missing")
		}
		roles := first.Data["profile"].(map[string]any)["roles"].([]any)
		if roles[0] != "user" {
			t.Fatal("save retained nested references")
		}
		roles[0] = "changed-after-read"
		second, _, err := s.GetSession(ctx, session.Subject)
		must(t, err)
		if second.Data["profile"].(map[string]any)["roles"].([]any)[0] != "user" {
			t.Fatal("read leaked nested references")
		}
	})
}
func TestSessionTypedNumbersAcrossStores(t *testing.T) {
	stores(t, func(t *testing.T, s core.Store) {
		session := core.NewSession("alice")
		session.SetMulti(map[string]any{"count": int(42), "id": int64(9007199254740993), "enabled": true})
		must(t, s.SaveSession(ctx, session, time.Hour))
		got, found, err := s.GetSession(ctx, session.Subject)
		must(t, err)
		if !found || got.GetInt("count") != 42 || got.GetInt64("id") != 9007199254740993 || !got.GetBool("enabled") {
			t.Fatal("typed values or large integer lost in storage")
		}
	})
}

// Pause each request immediately before finalizing the reusable refresh. On the
// old implementation both requests reached this point after issuing an access.
type refreshCommitHook struct {
	core.Store
	core.AtomicStore
	beforeCommit func()
	commitOnce   sync.Once
}

func (s *refreshCommitHook) CompareAndSwapTokenState(c context.Context, before, after *core.TokenState, ttl time.Duration) (bool, error) {
	if s.beforeCommit != nil {
		s.commitOnce.Do(s.beforeCommit)
	}
	return s.AtomicStore.CompareAndSwapTokenState(c, before, after, ttl)
}
func TestNonRotatingRefreshLoserCannotInvalidateWinner(t *testing.T) {
	stores(t, func(t *testing.T, s core.Store) {
		config := core.DefaultConfig()
		config.Concurrent = false
		config.Refresh.RotateRefreshToken = false
		setup := core.NewManager(s, core.WithConfig(config))
		_, err := setup.LoginWithRefresh(ctx, "alice", "a", "r")
		must(t, err)
		atFirst, atSecond := make(chan struct{}), make(chan struct{})
		resumeFirst, resumeSecond := make(chan struct{}), make(chan struct{})
		var onceFirst, onceSecond sync.Once
		releaseFirst := func() { onceFirst.Do(func() { close(resumeFirst) }) }
		defer releaseFirst()
		releaseSecond := func() { onceSecond.Do(func() { close(resumeSecond) }) }
		defer releaseSecond()
		first := core.NewManager(&refreshCommitHook{Store: s, AtomicStore: s.(core.AtomicStore), beforeCommit: func() { close(atFirst); <-resumeFirst }}, core.WithConfig(config))
		second := core.NewManager(&refreshCommitHook{Store: s, AtomicStore: s.(core.AtomicStore), beforeCommit: func() { close(atSecond); <-resumeSecond }}, core.WithConfig(config))
		type answer struct {
			result *core.LoginResult
			err    error
		}
		firstDone, secondDone := make(chan answer, 1), make(chan answer, 1)
		go func() { result, err := first.Refresh(ctx, "r", "b"); firstDone <- answer{result, err} }()
		select {
		case <-atFirst:
		case <-time.After(5 * time.Second):
			t.Fatal("first refresh did not reach commit")
		}
		go func() { result, err := second.Refresh(ctx, "r", "c"); secondDone <- answer{result, err} }()
		var loser answer
		reachedCommit := false
		select {
		case <-atSecond:
			reachedCommit = true
		case loser = <-secondDone:
		case <-time.After(5 * time.Second):
			t.Fatal("competing refresh did not return")
		}
		releaseFirst()
		winner := <-firstDone
		releaseSecond()
		if reachedCommit {
			loser = <-secondDone
		}
		must(t, winner.err)
		if loser.err == nil {
			t.Fatal("competing request succeeded while refresh was in flight")
		}
		if !setup.IsValid(ctx, winner.result.TokenState.Token) {
			t.Fatal("successful refresh returned an invalid access token")
		}
		if setup.IsValid(ctx, "c") {
			t.Fatal("losing request left an access token")
		}
		// A reusable token remains usable after successful finalization.
		_, err = setup.Refresh(ctx, "r", "d")
		must(t, err)
	})
}

// Fail immediately after acquiring the claim, before any access state is deleted.
type refreshDeleteFailure struct {
	core.Store
	core.AtomicStore
	fail func() error
}

func (s *refreshDeleteFailure) DeleteTokenState(c context.Context, k core.TokenValue) error {
	if s.fail != nil {
		f := s.fail
		s.fail = nil
		return f()
	}
	return s.Store.DeleteTokenState(c, k)
}
func TestNonRotatingRefreshReleasesFailedClaim(t *testing.T) {
	for _, mode := range []string{"storage-error", "cancellation", "revocation"} {
		t.Run(mode, func(t *testing.T) {
			stores(t, func(t *testing.T, s core.Store) {
				config := core.DefaultConfig()
				config.Refresh.RotateRefreshToken = false
				base := core.NewManager(s, core.WithConfig(config))
				_, err := base.LoginWithRefresh(ctx, "alice", "a", "r")
				must(t, err)
				failure := errors.New("injected deletion failure")
				request, cancel := context.WithCancel(ctx)
				defer cancel()
				wrapped := &refreshDeleteFailure{Store: s, AtomicStore: s.(core.AtomicStore), fail: func() error {
					if mode == "cancellation" {
						cancel()
						return context.Canceled
					}
					if mode == "revocation" {
						must(t, base.RevokeRefreshToken(ctx, "r"))
					}
					return failure
				}}
				m := core.NewManager(wrapped, core.WithConfig(config))
				_, err = m.Refresh(request, "r", "b")
				if mode == "cancellation" {
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("error=%v", err)
					}
				} else if !errors.Is(err, failure) {
					t.Fatalf("error=%v", err)
				}
				if !base.IsValid(ctx, "a") {
					t.Fatal("failed request changed old access")
				}
				_, err = base.Refresh(ctx, "r", "c")
				if mode == "revocation" {
					if !errors.Is(err, core.ErrRefreshTokenRevoked) {
						t.Fatalf("revocation overwritten: %v", err)
					}
				} else {
					must(t, err)
				}
			})
		})
	}
}

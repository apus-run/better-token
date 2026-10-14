package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"os"
	"os/exec"
	"reflect"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apus-run/better-token/core"
)

func TestSessionDataOperations(t *testing.T) {
	s := core.NewSession("alice")
	s.SetMulti(map[string]any{"name": "Alice", "enabled": true, "count": int32(7)})
	if s.GetString("name") != "Alice" || !s.GetBool("enabled") || s.GetInt("count") != 7 || s.Size() != 3 {
		t.Fatal("typed reads or batch update failed")
	}
	keys := s.Keys()
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"count", "enabled", "name"}) {
		t.Fatalf("keys=%v", keys)
	}
	s.Remove("name")
	if s.Has("name") {
		t.Fatal("remove failed")
	}
	s.Clear()
	if !s.IsEmpty() {
		t.Fatal("clear failed")
	}
	var nilSession *core.Session
	nilSession.Set("key", 1)
	nilSession.SetMulti(map[string]any{"key": 1})
	if nilSession.Size() != 0 || !nilSession.IsEmpty() || nilSession.GetInt("key") != 0 || nilSession.GetBool("key") {
		t.Fatal("nil behavior changed")
	}
}
func TestSessionNumericReadsRejectLossyConversions(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  int64
	}{
		{"int", int(42), 42}, {"unsigned", uint32(42), 42}, {"float-integral", float64(42), 42},
		{"fraction", float64(1.5), 0}, {"nan", math.NaN(), 0}, {"infinity", math.Inf(1), 0},
		{"float-overflow", float64(0x1p63), 0}, {"unsigned-overflow", uint64(math.MaxInt64) + 1, 0},
		{"json-large", json.Number("9007199254740993"), 9007199254740993}, {"json-max", json.Number("9223372036854775807"), math.MaxInt64},
		{"json-overflow", json.Number("9223372036854775808"), 0}, {"string", "42", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := core.NewSession("alice")
			s.Set("value", tc.value)
			if got := s.GetInt64("value"); got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
}
func TestSessionCloneDetachesContainersAndPreservesCycles(t *testing.T) {
	s := core.NewSession("alice")
	s.Set("profile", map[string]any{"roles": []string{"user"}})
	s.Set("bytes", []byte{1, 2})
	s.Set("array", [1][]int{{3}})
	s.Set("nil-map", map[string]int(nil))
	s.Set("self", s.Data)
	clone := s.Clone()
	profile, _ := clone.Get("profile")
	profile.(map[string]any)["roles"].([]string)[0] = "admin"
	buffer, _ := clone.Get("bytes")
	buffer.([]byte)[0] = 9
	array, _ := clone.Get("array")
	array.([1][]int)[0][0] = 9
	original, _ := s.Get("profile")
	if original.(map[string]any)["roles"].([]string)[0] != "user" {
		t.Fatal("nested roles aliased")
	}
	original, _ = s.Get("bytes")
	if original.([]byte)[0] != 1 {
		t.Fatal("byte buffer aliased")
	}
	original, _ = s.Get("array")
	if original.([1][]int)[0][0] != 3 {
		t.Fatal("nested array aliased")
	}
	nilMap, _ := clone.Get("nil-map")
	if !reflect.ValueOf(nilMap).IsNil() {
		t.Fatal("typed nil lost")
	}
	self, _ := clone.Get("self")
	self.(map[string]any)["clone-only"] = true
	if !clone.Has("clone-only") || s.Has("clone-only") {
		t.Fatal("cycle does not target cloned root")
	}
}
func TestSessionJSONPreservesPrecisionAndPartialUpdates(t *testing.T) {
	s := core.NewSession("alice")
	s.Set("old", true)
	if err := json.Unmarshal([]byte(`{"data":{"id":9007199254740993}}`), s); err != nil {
		t.Fatal(err)
	}
	if s.Subject.LoginID != "alice" || !s.GetBool("old") || s.GetInt64("id") != 9007199254740993 {
		t.Fatal("partial update or number precision lost")
	}
	encoded, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var copy core.Session
	if err = json.Unmarshal(encoded, &copy); err != nil {
		t.Fatal(err)
	}
	if copy.GetInt64("id") != 9007199254740993 {
		t.Fatal("roundtrip rounded number")
	}
	if err = json.Unmarshal([]byte("null"), s); err != nil || s.Subject.LoginID != "alice" {
		t.Fatal("null changed existing receiver")
	}
	var zero core.Session
	encoded, err = json.Marshal(&zero)
	if err != nil || string(encoded) != `{"subject":{"login_id":"","login_type":""},"data":null}` {
		t.Fatalf("zero JSON=%s,%v", encoded, err)
	}
}
func TestSessionMethodsConcurrentWithSerialization(t *testing.T) {
	s := core.NewSession("alice")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				s.Set("counter", i+j)
				s.SetMulti(map[string]any{"enabled": true})
				s.Get("counter")
				s.Keys()
				s.Size()
				s.Clone()
				if _, err := json.Marshal(s); err != nil {
					t.Error(err)
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestSessionJSONIntegralRepresentations(t *testing.T) {
	for _, tc := range []struct {
		text string
		want int64
	}{
		{"1000", 1000}, {"1000.0", 1000}, {"1e3", 1000}, {"1E+3", 1000}, {"100e-2", 1}, {"0.001e3", 1},
		{"-1.00e3", -1000}, {"9223372036854775807.0", math.MaxInt64}, {"-9.223372036854775808e18", math.MinInt64},
		{"9007199254740993.0", 9007199254740993}, {"92233720368547758070e-1", math.MaxInt64},
		{"9223372036854775808.0", 0}, {"-9223372036854775809.0", 0}, {"1.00000000000000000001", 0},
		{"1e99999999999999999999999999", 0}, {"1e-999999999", 0}, {"0e999999999", 0},
	} {
		t.Run(tc.text, func(t *testing.T) {
			s := core.NewSession("alice")
			if err := json.Unmarshal([]byte(`{"data":{"count":`+tc.text+`}}`), s); err != nil {
				t.Fatal(err)
			}
			if got := s.GetInt64("count"); got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
}

type ignoredSessionReference struct {
	Self *core.Session `json:"-"`
}
type marshaledSessionReference struct{ Self *core.Session }

func (m marshaledSessionReference) MarshalJSON() ([]byte, error) { return []byte(`"opaque"`), nil }
func TestSessionJSONAllowsSharedAcyclicValues(t *testing.T) {
	parent, child := core.NewSession("parent"), core.NewSession("child")
	child.Set("value", true)
	parent.SetMulti(map[string]any{"left": child, "right": child, "ignored": ignoredSessionReference{parent}, "custom": marshaledSessionReference{parent}})
	if _, err := json.Marshal(parent); err != nil {
		t.Fatal(err)
	}
}

// A regression must fail this subprocess test, not crash the whole test runner.
func TestSessionJSONRejectsCycles(t *testing.T) {
	if kind := os.Getenv("BETTER_TOKEN_SESSION_CYCLE_CASE"); kind != "" {
		debug.SetMaxStack(8 * 1024 * 1024)
		session := core.NewSession("alice")
		switch kind {
		case "direct":
			session.Set("self", session)
		case "pair":
			other := core.NewSession("other")
			session.Set("other", other)
			other.Set("parent", session)
		case "struct":
			session.Set("holder", &struct{ Session *core.Session }{session})
		case "container":
			session.Set("self", session.Data)
		}
		_, err := json.Marshal(session)
		var unsupported *json.UnsupportedValueError
		if !errors.As(err, &unsupported) {
			t.Fatalf("expected cycle error, got %v", err)
		}
		return
	}
	for _, kind := range []string{"direct", "pair", "struct", "container"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSessionJSONRejectsCycles$")
			command.Env = append(os.Environ(), "BETTER_TOKEN_SESSION_CYCLE_CASE="+kind)
			output, err := command.CombinedOutput()
			if err != nil {
				if len(output) > 4096 {
					output = output[:4096]
				}
				t.Fatalf("cycle subprocess: %v\n%s", err, output)
			}
		})
	}
}

// Bounded exact arithmetic is an independent oracle for the decimal parser.
func FuzzSessionJSONInteger(f *testing.F) {
	for _, seed := range []string{"1e3", "1000.0", "-9.223372036854775808e18", "9007199254740993.0", "1.00000000000000000001", "100e-2", "0", "-0.0"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) == 0 || len(text) > 80 || (text[0] != '-' && (text[0] < '0' || text[0] > '9')) || !json.Valid([]byte(text)) {
			return
		}
		if index := strings.IndexAny(text, "eE"); index >= 0 {
			exponent, err := strconv.ParseInt(text[index+1:], 10, 64)
			if err != nil || exponent < -1000 || exponent > 1000 {
				return
			}
		}
		exact, ok := new(big.Rat).SetString(text)
		if !ok {
			return
		}
		want := int64(0)
		if exact.IsInt() && exact.Num().IsInt64() {
			want = exact.Num().Int64()
		}
		session := core.NewSession("alice")
		session.Set("value", json.Number(text))
		if got := session.GetInt64("value"); got != want {
			t.Fatalf("%s: got %d want %d", text, got, want)
		}
	})
}

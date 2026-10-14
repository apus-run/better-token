package core

import (
	"bytes"
	"encoding"
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

// Session 是登录主体级别的服务端 KV 容器，按 LoginSubject 存取。
// 数据方法可并发调用；使用后不要复制 Session 值，也不要并发访问公开字段
// 或修改 Get 返回的可变容器。复制快照请使用 Clone。
type Session struct {
	Subject LoginSubject   `json:"subject"`
	Data    map[string]any `json:"data"`
	mu      sync.RWMutex
}

// NewSession 基于 loginID 创建一个使用默认登录类型的 Session。
// 如需指定登录类型，使用 NewSessionForSubject 或直接设置 Subject 字段。
func NewSession(loginID string) *Session {
	return NewSessionForSubject(LoginSubject{LoginID: loginID})
}

// NewSessionForSubject 基于完整登录主体创建 Session。
func NewSessionForSubject(subject LoginSubject) *Session {
	return &Session{
		Subject: subject.Normalize(),
		Data:    make(map[string]any),
	}
}

func (s *Session) Set(key string, value any) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Data == nil {
		s.Data = make(map[string]any)
	}
	s.Data[key] = value
}

func (s *Session) Get(key string) (any, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.Data[key]
	return value, ok
}

func (s *Session) Remove(key string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Data, key)
}

func (s *Session) Clear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Data = make(map[string]any)
}

func (s *Session) Has(key string) bool {
	_, ok := s.Get(key)
	return ok
}

// Clone detaches maps, slices and arrays recursively, preserving their Go types
// and cycles. Opaque pointers and structs remain caller-owned; store mutable
// session data as containers. Direct field access must not race with methods.
func (s *Session) Clone() *Session {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	clone := &Session{Subject: s.Subject, Data: make(map[string]any)}
	if s.Data != nil {
		clone.Data = cloneSessionValue(reflect.ValueOf(s.Data), make(map[sessionCloneKey]reflect.Value)).Interface().(map[string]any)
	}
	return clone
}

type sessionCloneKey struct {
	typ     reflect.Type
	pointer uintptr
	length  int
}

func cloneSessionValue(value reflect.Value, seen map[sessionCloneKey]reflect.Value) reflect.Value {
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		clone := reflect.New(value.Type()).Elem()
		clone.Set(cloneSessionValue(value.Elem(), seen))
		return clone
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		key := sessionCloneKey{typ: value.Type(), pointer: value.Pointer()}
		if clone, ok := seen[key]; ok {
			return clone
		}
		clone := reflect.MakeMapWithSize(value.Type(), value.Len())
		seen[key] = clone
		entries := value.MapRange()
		for entries.Next() {
			clone.SetMapIndex(entries.Key(), cloneSessionValue(entries.Value(), seen))
		}
		return clone
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		key := sessionCloneKey{typ: value.Type(), pointer: value.Pointer(), length: value.Len()}
		if clone, ok := seen[key]; ok {
			return clone
		}
		clone := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		seen[key] = clone
		for i := 0; i < value.Len(); i++ {
			clone.Index(i).Set(cloneSessionValue(value.Index(i), seen))
		}
		return clone
	case reflect.Array:
		clone := reflect.New(value.Type()).Elem()
		for i := 0; i < value.Len(); i++ {
			clone.Index(i).Set(cloneSessionValue(value.Index(i), seen))
		}
		return clone
	default:
		return value
	}
}

// SetMulti updates several values in one critical section. Persistence remains
// explicit through Manager.SaveSession, so storage errors reach the caller.
func (s *Session) SetMulti(values map[string]any) {
	if s == nil || len(values) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Data == nil {
		s.Data = make(map[string]any)
	}
	for key, value := range values {
		s.Data[key] = value
	}
}
func (s *Session) GetString(key string) string {
	value, _ := s.Get(key)
	text, _ := value.(string)
	return text
}
func (s *Session) GetBool(key string) bool {
	value, _ := s.Get(key)
	result, _ := value.(bool)
	return result
}

// GetInt64 accepts integer values and exact, in-range integral floats. JSON
// numbers are parsed without float64 rounding; invalid conversions return zero.
func (s *Session) GetInt64(key string) int64 {
	value, _ := s.Get(key)
	if number, ok := value.(json.Number); ok {
		if result, err := number.Int64(); err == nil {
			return result
		}
		// Normalize decimal/exponent notation using digits, never float64 or
		// unbounded big integers. The final coefficient has at most 19 digits.
		text := string(number)
		if len(text) == 0 || !json.Valid([]byte(text)) {
			return 0
		}
		negative := strings.HasPrefix(text, "-")
		if negative {
			text = text[1:]
		}
		exponent := int64(0)
		if index := strings.IndexAny(text, "eE"); index >= 0 {
			parsed, err := strconv.ParseInt(text[index+1:], 10, 64)
			if err != nil {
				return 0
			}
			exponent = parsed
			text = text[:index]
		}
		// Larger exponents cannot produce a nonzero int64 from this mantissa.
		if exponent > int64(len(text))+19 || exponent < -int64(len(text)) {
			return 0
		}
		if dot := strings.IndexByte(text, '.'); dot >= 0 {
			exponent -= int64(len(text) - dot - 1)
			text = text[:dot] + text[dot+1:]
		}
		text = strings.TrimLeft(text, "0")
		if text == "" {
			return 0
		}
		if exponent < 0 {
			remove := -exponent
			if remove > int64(len(text)) {
				return 0
			}
			split := len(text) - int(remove)
			if strings.Trim(text[split:], "0") != "" {
				return 0
			}
			text = text[:split]
			exponent = 0
		}
		if int64(len(text))+exponent > 19 {
			return 0
		}
		text += strings.Repeat("0", int(exponent))
		if negative {
			text = "-" + text
		}
		result, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return 0
		}
		return result
	}
	number := reflect.ValueOf(value)
	switch number.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return number.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		result := number.Uint()
		if result > math.MaxInt64 {
			return 0
		}
		return int64(result)
	case reflect.Float32, reflect.Float64:
		result := number.Float()
		if math.Trunc(result) != result || result < -0x1p63 || result >= 0x1p63 {
			return 0
		}
		return int64(result)
	default:
		return 0
	}
}
func (s *Session) GetInt(key string) int {
	value := s.GetInt64(key)
	result := int(value)
	if int64(result) != value {
		return 0
	}
	return result
}
func (s *Session) Keys() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.Data))
	for key := range s.Data {
		keys = append(keys, key)
	}
	return keys
}
func (s *Session) Size() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.Data)
}
func (s *Session) IsEmpty() bool { return s.Size() == 0 }

// MarshalJSON snapshots the map under the same lock as data methods.
func (s *Session) MarshalJSON() ([]byte, error) {
	if s == nil {
		return []byte("null"), nil
	}
	s.mu.RLock()
	snapshot := struct {
		Subject LoginSubject   `json:"subject"`
		Data    map[string]any `json:"data"`
	}{Subject: s.Subject}
	if s.Data != nil {
		snapshot.Data = cloneSessionValue(reflect.ValueOf(s.Data), make(map[sessionCloneKey]reflect.Value)).Interface().(map[string]any)
	}
	s.mu.RUnlock()
	path := map[sessionCloneKey]bool{{typ: reflect.TypeFor[*Session](), pointer: reflect.ValueOf(s).Pointer()}: true}
	if err := checkSessionJSONCycles(reflect.ValueOf(snapshot.Data), path); err != nil {
		return nil, err
	}
	return json.Marshal(snapshot)
}

// UnmarshalJSON preserves integer precision across JSON-backed stores.
func (s *Session) UnmarshalJSON(data []byte) error {
	if s == nil {
		return ErrEmptySessionID
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}
	var wire struct {
		Subject json.RawMessage `json:"subject"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	subject := s.Subject
	if len(wire.Subject) > 0 {
		if err := json.Unmarshal(wire.Subject, &subject); err != nil {
			return err
		}
	}
	values := s.Data
	if len(wire.Data) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(wire.Data))
		decoder.UseNumber()
		if err := decoder.Decode(&values); err != nil {
			return err
		}
	}
	s.Subject = subject
	s.Data = values
	return nil
}

// Nested *Session values invoke MarshalJSON with a fresh encoder, so encoding/json
// cannot see a cycle spanning those calls. Check the visible graph before encoding.
func checkSessionJSONCycles(value reflect.Value, path map[sessionCloneKey]bool) error {
	if !value.IsValid() {
		return nil
	}
	for value.Kind() == reflect.Interface {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice:
		if value.IsNil() {
			return nil
		}
		key := sessionCloneKey{typ: value.Type(), pointer: value.Pointer()}
		if value.Kind() == reflect.Slice {
			key.length = value.Len()
		}
		if path[key] {
			return &json.UnsupportedValueError{Value: value, Str: "encountered a cycle in Session data"}
		}
		path[key] = true
		defer delete(path, key)
	}
	if value.Type() == reflect.TypeFor[*Session]() && value.CanInterface() {
		snapshot := value.Interface().(*Session).Clone()
		return checkSessionJSONCycles(reflect.ValueOf(snapshot.Data), path)
	}
	if value.CanInterface() {
		if _, ok := value.Interface().(json.Marshaler); ok {
			return nil
		}
		if _, ok := value.Interface().(encoding.TextMarshaler); ok {
			return nil
		}
	}
	switch value.Kind() {
	case reflect.Pointer:
		return checkSessionJSONCycles(value.Elem(), path)
	case reflect.Map:
		entries := value.MapRange()
		for entries.Next() {
			if err := checkSessionJSONCycles(entries.Value(), path); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if err := checkSessionJSONCycles(value.Index(i), path); err != nil {
				return err
			}
		}
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			field := value.Type().Field(i)
			if field.Tag.Get("json") == "-" {
				continue
			}
			if !field.IsExported() {
				typ := field.Type
				if typ.Kind() == reflect.Pointer {
					typ = typ.Elem()
				}
				if !field.Anonymous || typ.Kind() != reflect.Struct {
					continue
				}
			}
			if err := checkSessionJSONCycles(value.Field(i), path); err != nil {
				return err
			}
		}
	}
	return nil
}

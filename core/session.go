package core

// Session 是登录主体级别的服务端 KV 容器，按 LoginSubject 存取。
type Session struct {
	Subject LoginSubject   `json:"subject"`
	Data    map[string]any `json:"data"`
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
	if s.Data == nil {
		s.Data = make(map[string]any)
	}
	s.Data[key] = value
}

func (s *Session) Get(key string) (any, bool) {
	if s == nil || s.Data == nil {
		return nil, false
	}
	value, ok := s.Data[key]
	return value, ok
}

func (s *Session) Remove(key string) {
	if s == nil || s.Data == nil {
		return
	}
	delete(s.Data, key)
}

func (s *Session) Clear() {
	if s == nil {
		return
	}
	s.Data = make(map[string]any)
}

func (s *Session) Has(key string) bool {
	_, ok := s.Get(key)
	return ok
}

func (s *Session) Clone() *Session {
	if s == nil {
		return nil
	}
	clone := &Session{
		Subject: s.Subject,
		Data:    make(map[string]any, len(s.Data)),
	}
	for k, v := range s.Data {
		clone.Data[k] = v
	}
	return clone
}

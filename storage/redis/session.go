package redis

import (
	"context"
	"encoding/json"
	"time"

	"github.com/apus-run/better-token/core"
	"github.com/apus-run/gala/components/rdb"
)

func (s *Store) SaveSession(ctx context.Context, session *core.Session, ttl time.Duration) error {
	if session == nil {
		return core.ErrEmptySessionID
	}
	return s.writeSession(ctx, session, ttl, false)
}

func (s *Store) EnsureSession(ctx context.Context, subject core.LoginSubject, ttl time.Duration) error {
	return s.writeSession(ctx, core.NewSessionForSubject(subject), ttl, true)
}

func (s *Store) writeSession(ctx context.Context, session *core.Session, ttl time.Duration, ensure bool) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	subject := session.Subject.Normalize()
	if subject.IsZero() {
		return core.ErrEmptySessionID
	}
	clone := session.Clone()
	clone.Subject = subject
	data, err := json.Marshal(clone)
	if err != nil {
		return err
	}
	millis := ttl.Milliseconds()
	if ttl > 0 && millis < 1 {
		millis = 1
	}
	for attempt := 0; attempt < 32; attempt++ {
		legacy, owned, err := s.legacySession(ctx, subject)
		if err != nil {
			return err
		}
		value := data
		if ensure && owned {
			value = []byte(legacy)
		}
		mode := "save"
		if ensure {
			mode = "ensure"
		}
		removeLegacy := "0"
		if owned {
			removeLegacy = "1"
		}
		updated, err := s.rdb.DB(ctx).Eval(ctx, sessionWriteScript,
			[]string{s.sessionKey(subject), s.legacySubjectKey("session", subject)},
			legacy, string(value), millis, removeLegacy, mode).Int()
		if err != nil {
			return err
		}
		if updated == 1 {
			return nil
		}
	}
	return core.ErrTokenInvalid
}

// A legacy delimiter key is trusted only if its serialized owner matches. A
// v2 write removes that matching old value atomically, preventing it from
// reappearing through fallback after the new key expires.
func (s *Store) legacySession(ctx context.Context, subject core.LoginSubject) (string, bool, error) {
	raw, err := s.rdb.DB(ctx).Get(ctx, s.legacySubjectKey("session", subject)).Result()
	if rdb.IsNilError(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	session := new(core.Session)
	if err = json.Unmarshal([]byte(raw), session); err != nil {
		return "", false, err
	}
	return raw, session.Subject.Normalize() == subject.Normalize(), nil
}

const sessionWriteScript = `
if (redis.call("GET",KEYS[2]) or "") ~= ARGV[1] then return 0 end
if ARGV[5] == "save" or not redis.call("GET",KEYS[1]) then redis.call("SET",KEYS[1],ARGV[2]) end
if tonumber(ARGV[3]) > 0 then redis.call("PEXPIRE",KEYS[1],ARGV[3]) else redis.call("PERSIST",KEYS[1]) end
if ARGV[4] == "1" then redis.call("DEL",KEYS[2]) end
return 1
`

func (s *Store) GetSession(ctx context.Context, subject core.LoginSubject) (*core.Session, bool, error) {
	if err := contextErr(ctx); err != nil {
		return nil, false, err
	}
	subject = subject.Normalize()
	for _, key := range []string{s.sessionKey(subject), s.legacySubjectKey("session", subject)} {
		raw, err := s.rdb.DB(ctx).Get(ctx, key).Bytes()
		if rdb.IsNilError(err) {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		session := new(core.Session)
		if err = json.Unmarshal(raw, session); err != nil {
			return nil, false, err
		}
		if session.Subject.Normalize() == subject {
			return session, true, nil
		}
	}
	return nil, false, nil
}

func (s *Store) DeleteSession(ctx context.Context, subject core.LoginSubject) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	subject = subject.Normalize()
	for _, key := range []string{s.sessionKey(subject), s.legacySubjectKey("session", subject)} {
		raw, err := s.rdb.DB(ctx).Get(ctx, key).Result()
		if rdb.IsNilError(err) {
			continue
		}
		if err != nil {
			return err
		}
		session := new(core.Session)
		if err = json.Unmarshal([]byte(raw), session); err != nil {
			return err
		}
		if session.Subject.Normalize() != subject {
			continue
		}
		if err = s.rdb.DB(ctx).Eval(ctx, sessionDeleteScript, []string{key}, raw).Err(); err != nil {
			return err
		}
	}
	return nil
}

const sessionDeleteScript = `
if redis.call("GET",KEYS[1]) == ARGV[1] then return redis.call("DEL",KEYS[1]) end
return 0
`

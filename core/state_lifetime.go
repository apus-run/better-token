package core

import "time"

type stateLifetime struct {
	expiresAt *time.Time
}

func lifetimeOf(expiresAt *time.Time) stateLifetime {
	return stateLifetime{expiresAt: expiresAt}
}

func (l stateLifetime) IsExpired(now time.Time) bool {
	return l.expiresAt != nil && !now.UTC().Before(l.expiresAt.UTC())
}

func (l stateLifetime) EffectiveExpiresAt(now time.Time, ttl time.Duration) *time.Time {
	var effective *time.Time
	if ttl > 0 {
		effective = utcTimePtr(now.Add(ttl))
	}
	if l.expiresAt != nil {
		candidate := cloneTimePtrUTC(l.expiresAt)
		if effective == nil || candidate.Before(*effective) {
			effective = candidate
		}
	}
	return effective
}

func utcTimePtr(t time.Time) *time.Time {
	utc := t.UTC()
	return &utc
}

func cloneTimePtrUTC(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	return utcTimePtr(*t)
}

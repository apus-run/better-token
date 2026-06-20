package core

import "time"

type NowFunc func() time.Time

type Runtime struct {
	Now NowFunc
}

func DefaultRuntime() Runtime {
	return Runtime{
		Now: func() time.Time {
			return time.Now().UTC()
		},
	}
}

func (r *Runtime) ensureDefaults() {
	if r.Now == nil {
		r.Now = DefaultRuntime().Now
	}
}

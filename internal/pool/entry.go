package pool

import (
	"sync/atomic"
	"time"
)

type State int

const (
	StateActive State = iota
	StateQuarantine
)

func (s State) String() string {
	switch s {
	case StateActive:
		return "active"
	case StateQuarantine:
		return "quarantine"
	}
	return "unknown"
}

type Auth struct {
	User string
	Pass string
}

type Entry struct {
	Addr     string
	Source   string
	Auth     *Auth
	Metadata map[string]string

	EwmaLatency     time.Duration
	SuccessRate     float64
	ConsecFails     int
	LastOk          time.Time
	State           State
	QuarantineUntil time.Time

	inflight atomic.Int32
}

func (e *Entry) Inflight() int32 { return e.inflight.Load() }
func (e *Entry) IncInflight()    { e.inflight.Add(1) }
func (e *Entry) DecInflight()    { e.inflight.Add(-1) }

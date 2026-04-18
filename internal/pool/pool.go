package pool

import (
	"errors"
	"math/rand/v2"
	"sync"
	"time"
)

var ErrEmpty = errors.New("pool: no active entries")

type Config struct {
	Max              int
	EjectConsecFails int
	QuarantineDur    time.Duration
	EwmaAlpha        float64
}

func DefaultConfig() Config {
	return Config{
		Max:              50,
		EjectConsecFails: 3,
		QuarantineDur:    5 * time.Minute,
		EwmaAlpha:        0.3,
	}
}

type Pool struct {
	cfg     Config
	mu      sync.RWMutex
	entries map[string]*Entry
	now     func() time.Time
}

func New(cfg Config) *Pool {
	return &Pool{
		cfg:     cfg,
		entries: make(map[string]*Entry),
		now:     time.Now,
	}
}

type AddInput struct {
	Addr     string
	Source   string
	Auth     *Auth
	Metadata map[string]string
}

func (p *Pool) Add(in AddInput) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.entries[in.Addr]; exists {
		return false
	}
	if len(p.entries) >= p.cfg.Max {
		return false
	}
	p.entries[in.Addr] = &Entry{
		Addr:     in.Addr,
		Source:   in.Source,
		Auth:     in.Auth,
		Metadata: in.Metadata,
		State:    StateActive,
		LastOk:   p.now(),
	}
	return true
}

func (p *Pool) Size() (active, quarantine int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, e := range p.entries {
		if e.State == StateActive {
			active++
		} else {
			quarantine++
		}
	}
	return
}

func (p *Pool) promoteExpiredQuarantine() {
	now := p.now()
	for _, e := range p.entries {
		if e.State == StateQuarantine && now.After(e.QuarantineUntil) {
			e.State = StateActive
			e.ConsecFails = 0
		}
	}
}

// Select picks one active entry via Power-of-Two-Choices. Entries in
// exclude (keyed by Addr) are skipped; pass nil to select from all active.
// The returned entry has Inflight incremented; callers must DecInflight.
func (p *Pool) Select(exclude map[string]bool) (*Entry, error) {
	p.mu.Lock()
	p.promoteExpiredQuarantine()
	active := make([]*Entry, 0, len(p.entries))
	for _, e := range p.entries {
		if e.State != StateActive {
			continue
		}
		if exclude[e.Addr] {
			continue
		}
		active = append(active, e)
	}
	p.mu.Unlock()

	if len(active) == 0 {
		return nil, ErrEmpty
	}
	if len(active) == 1 {
		active[0].IncInflight()
		return active[0], nil
	}

	a := rand.IntN(len(active))
	b := rand.IntN(len(active) - 1)
	if b >= a {
		b++
	}
	ea, eb := active[a], active[b]
	chosen := ea
	if eb.Inflight() < ea.Inflight() {
		chosen = eb
	}
	chosen.IncInflight()
	return chosen, nil
}

func (p *Pool) RecordSuccess(addr string, latency time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[addr]
	if !ok {
		return
	}
	a := p.cfg.EwmaAlpha
	if e.EwmaLatency == 0 {
		e.EwmaLatency = latency
	} else {
		e.EwmaLatency = time.Duration(a*float64(latency) + (1-a)*float64(e.EwmaLatency))
	}
	e.SuccessRate = a*1.0 + (1-a)*e.SuccessRate
	e.ConsecFails = 0
	e.LastOk = p.now()
}

func (p *Pool) RecordFailure(addr string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[addr]
	if !ok {
		return
	}
	a := p.cfg.EwmaAlpha
	e.SuccessRate = (1 - a) * e.SuccessRate
	e.ConsecFails++
	if e.ConsecFails >= p.cfg.EjectConsecFails {
		e.State = StateQuarantine
		e.QuarantineUntil = p.now().Add(p.cfg.QuarantineDur)
	}
}

type Snapshot struct {
	Addr        string            `json:"addr"`
	Source      string            `json:"source"`
	State       string            `json:"state"`
	EwmaLatency string            `json:"ewma_latency"`
	SuccessRate float64           `json:"success_rate"`
	ConsecFails int               `json:"consec_fails"`
	Inflight    int32             `json:"inflight"`
	LastOk      time.Time         `json:"last_ok"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

func (p *Pool) Snapshot() []Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Snapshot, 0, len(p.entries))
	for _, e := range p.entries {
		out = append(out, Snapshot{
			Addr:        e.Addr,
			Source:      e.Source,
			State:       e.State.String(),
			EwmaLatency: e.EwmaLatency.String(),
			SuccessRate: e.SuccessRate,
			ConsecFails: e.ConsecFails,
			Inflight:    e.Inflight(),
			LastOk:      e.LastOk,
			Metadata:    e.Metadata,
		})
	}
	return out
}

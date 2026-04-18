package pool

import (
	"errors"
	"testing"
	"time"
)

func TestAddRejectsDuplicate(t *testing.T) {
	p := New(DefaultConfig())
	if !p.Add(AddInput{Addr: "1.1.1.1:1080", Source: "test"}) {
		t.Fatal("first add should succeed")
	}
	if p.Add(AddInput{Addr: "1.1.1.1:1080", Source: "test"}) {
		t.Fatal("duplicate add should fail")
	}
	active, _ := p.Size()
	if active != 1 {
		t.Fatalf("want 1 active, got %d", active)
	}
}

func TestAddRespectsMax(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Max = 2
	p := New(cfg)
	p.Add(AddInput{Addr: "1.1.1.1:1"})
	p.Add(AddInput{Addr: "1.1.1.2:1"})
	if p.Add(AddInput{Addr: "1.1.1.3:1"}) {
		t.Fatal("add over Max should fail")
	}
}

func TestSelectEmpty(t *testing.T) {
	p := New(DefaultConfig())
	_, err := p.Select(nil)
	if !errors.Is(err, ErrEmpty) {
		t.Fatalf("want ErrEmpty, got %v", err)
	}
}

func TestQuarantineAfterConsecFailures(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EjectConsecFails = 2
	cfg.QuarantineDur = 100 * time.Millisecond
	p := New(cfg)
	p.Add(AddInput{Addr: "1.1.1.1:1"})

	p.RecordFailure("1.1.1.1:1")
	p.RecordFailure("1.1.1.1:1")

	active, quarantine := p.Size()
	if active != 0 || quarantine != 1 {
		t.Fatalf("want 0 active, 1 quarantine; got %d,%d", active, quarantine)
	}

	// Select skips quarantined entries.
	if _, err := p.Select(nil); !errors.Is(err, ErrEmpty) {
		t.Fatalf("want ErrEmpty while all quarantined, got %v", err)
	}
}

func TestSelectExcludes(t *testing.T) {
	p := New(DefaultConfig())
	p.Add(AddInput{Addr: "a:1"})
	p.Add(AddInput{Addr: "b:1"})

	got, err := p.Select(map[string]bool{"a:1": true})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.Addr != "b:1" {
		t.Fatalf("excluded entry was selected: %s", got.Addr)
	}
}

func TestRecordSuccessResetsFails(t *testing.T) {
	p := New(DefaultConfig())
	p.Add(AddInput{Addr: "x:1"})
	p.RecordFailure("x:1")
	p.RecordSuccess("x:1", 10*time.Millisecond)

	snap := p.Snapshot()
	if len(snap) != 1 || snap[0].ConsecFails != 0 {
		t.Fatalf("consec fails not reset: %+v", snap)
	}
}

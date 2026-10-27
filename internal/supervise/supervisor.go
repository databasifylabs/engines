package supervise

import (
	"cmp"
	"context"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/databasifylabs/engines/internal/layout"
	"github.com/databasifylabs/engines/internal/manifest"
)

type Options struct {
	Layout      layout.Layout
	Console     io.Writer
	Clock       Clock
	MaxBackoff  time.Duration
	StableAfter time.Duration
	LogMaxSize  int64
}

type Supervisor struct {
	layout      layout.Layout
	clock       Clock
	maxBackoff  time.Duration
	stableAfter time.Duration
	reaper      *reaper
	logs        *logSink
	stopReaper  context.CancelFunc

	mu            sync.Mutex
	units         map[string]*unit
	order         []string
	enabled       *enabledStore
	stopSchedules context.CancelFunc
}

func New(options Options) *Supervisor {
	return &Supervisor{
		layout:        options.Layout,
		clock:         cmp.Or[Clock](options.Clock, realClock{}),
		maxBackoff:    cmp.Or(options.MaxBackoff, 30*time.Second),
		stableAfter:   cmp.Or(options.StableAfter, 30*time.Second),
		reaper:        newReaper(),
		logs:          newLogSink(options.Layout.Logs(), cmp.Or(options.LogMaxSize, layout.LogMaxSize), options.Console),
		units:         map[string]*unit{},
		enabled:       loadEnabledStore(options.Layout.EnabledState()),
		stopSchedules: func() {},
	}
}

func (s *Supervisor) Boot() {
	reaping, stop := context.WithCancel(context.Background())
	s.stopReaper = stop
	go s.reaper.run(reaping)
	loaded, err := manifest.Load(s.layout.Manifest())
	if err != nil {
		s.logs.event("databasify", "ignoring manifest: %v", err)
	}
	s.apply(loaded)
	s.startEnabled()
}

func (s *Supervisor) ReloadManifest() error {
	loaded, err := manifest.Load(s.layout.Manifest())
	if err != nil {
		return err
	}
	removed := s.apply(loaded)
	s.stopInOrder(removed)
	s.logs.event("databasify", "manifest reloaded")

	return nil
}

func (s *Supervisor) Shutdown() {
	s.mu.Lock()
	s.stopSchedules()
	auxiliary := s.unitsWhere(func(u *unit) bool { return u.kind != kindProcess })
	processes := s.processUnits()
	s.mu.Unlock()
	s.stopTogether(auxiliary)
	slices.Reverse(processes)
	s.stopInOrder(processes)
	s.logs.drain(2 * time.Second)
	s.stopReaper()
}

func (s *Supervisor) apply(loaded manifest.Manifest) []*unit {
	ordered, _ := loaded.StartOrder()
	s.mu.Lock()
	defer s.mu.Unlock()
	wanted := map[string]bool{}
	s.order = s.order[:0]
	for _, p := range ordered {
		wanted[p.Name] = true
		s.order = append(s.order, p.Name)
		s.define(p.Name, kindProcess, processSpec(p), p.Enabled)
	}
	for _, schedule := range loaded.Schedules {
		wanted[schedule.Name] = true
		s.define(schedule.Name, kindSchedule, scheduleSpec(schedule), true)
	}
	removed := s.unitsWhere(func(u *unit) bool { return u.kind != kindTransient && !wanted[u.name] })
	for _, u := range removed {
		delete(s.units, u.name)
	}
	s.restartSchedules(loaded.Schedules)

	return removed
}

func (s *Supervisor) define(name string, kind unitKind, spec spec, enabledByDefault bool) {
	enabled := s.enabled.resolve(name, enabledByDefault)
	if existing, ok := s.units[name]; ok {
		existing.kind, existing.spec, existing.enabled = kind, spec, enabled

		return
	}
	s.units[name] = newUnit(name, kind, spec, enabled)
}

func (s *Supervisor) startEnabled() {
	for _, u := range s.processUnitsLocked() {
		s.mu.Lock()
		if !u.enabled || u.running() {
			s.mu.Unlock()

			continue
		}
		settled := s.launch(u)
		s.mu.Unlock()
		<-settled
	}
}

func (s *Supervisor) processUnitsLocked() []*unit {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.processUnits()
}

func (s *Supervisor) processUnits() []*unit {
	units := make([]*unit, 0, len(s.order))
	for _, name := range s.order {
		if u, ok := s.units[name]; ok {
			units = append(units, u)
		}
	}

	return units
}

func (s *Supervisor) unitsWhere(keep func(*unit) bool) []*unit {
	var matched []*unit
	for _, u := range s.units {
		if keep(u) {
			matched = append(matched, u)
		}
	}

	return matched
}

func (s *Supervisor) stopInOrder(units []*unit) {
	for _, u := range units {
		<-s.requestStop(u)
	}
}

func (s *Supervisor) stopTogether(units []*unit) {
	pending := make([]<-chan struct{}, 0, len(units))
	for _, u := range units {
		pending = append(pending, s.requestStop(u))
	}
	for _, finished := range pending {
		<-finished
	}
}

func (s *Supervisor) requestStop(u *unit) <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !u.running() {
		return closed
	}
	u.cancel()

	return u.finished
}

var closed = func() chan struct{} {
	ch := make(chan struct{})
	close(ch)

	return ch
}()

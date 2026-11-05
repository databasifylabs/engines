package supervise

import (
	"path"
	"slices"
	"syscall"

	"github.com/databasifylabs/engines/internal/ctl"
	"github.com/databasifylabs/engines/internal/manifest"
)

var _ ctl.Controller = (*Supervisor)(nil)

func (s *Supervisor) Start(name string, block bool) error {
	s.mu.Lock()
	u, ok := s.units[name]
	if !ok {
		s.mu.Unlock()

		return ctl.ErrNotFound
	}
	if u.running() || u.activeState == "active" {
		finished, oneshot := u.finished, u.spec.oneshot
		s.mu.Unlock()
		if !block || !oneshot || finished == nil {
			return nil
		}
		<-finished

		return s.resultOf(u)
	}
	settled := s.launch(u)
	s.mu.Unlock()
	if !block {
		return nil
	}

	return <-settled
}

func (s *Supervisor) resultOf(u *unit) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u.result != "success" {
		return ctl.ErrFailed
	}

	return nil
}

func (s *Supervisor) Stop(name string, block bool) error {
	s.mu.Lock()
	u, ok := s.units[name]
	if !ok {
		s.mu.Unlock()

		return ctl.ErrNotFound
	}
	if !u.running() {
		if u.activeState == "active" {
			u.transition("inactive", "dead")
			s.collect(u)
		}
		s.mu.Unlock()

		return nil
	}
	s.mu.Unlock()
	finished := s.requestStop(u)
	if block {
		<-finished
	}

	return nil
}

func (s *Supervisor) Restart(name string) error {
	if err := s.Stop(name, true); err != nil {
		return err
	}

	return s.Start(name, true)
}

func (s *Supervisor) Reload(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.units[name]
	if !ok {
		return ctl.ErrNotFound
	}
	if u.process == nil {
		return ctl.ErrNotActive
	}
	u.process.signal(u.spec.reloadSignal)
	s.logs.event(name, "reloading with %s", manifest.SignalName(u.spec.reloadSignal))

	return nil
}

func (s *Supervisor) SetEnabled(name string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.units[name]
	if !ok {
		return ctl.ErrNotFound
	}
	if u.kind == kindTransient {
		return nil
	}
	u.enabled = enabled

	return s.enabled.set(name, enabled)
}

func (s *Supervisor) ResetFailed(pattern string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, u := range s.units {
		if matched, _ := path.Match(pattern, name); pattern != "" && !matched {
			continue
		}
		if u.running() {
			continue
		}
		u.restarts, u.result = 0, "success"
		if u.activeState == "failed" {
			u.transition("inactive", "dead")
		}
		s.collect(u)
	}
}

func (s *Supervisor) Kill(name string, signal syscall.Signal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.units[name]
	if !ok {
		return ctl.ErrNotFound
	}
	if u.process != nil {
		u.process.signalGroup(signal)
	}

	return nil
}

func (s *Supervisor) RunTransient(t ctl.Transient) (ctl.Status, error) {
	s.mu.Lock()
	if _, exists := s.units[t.Unit]; exists {
		s.mu.Unlock()

		return ctl.Missing(t.Unit), ctl.ErrLoaded
	}
	u := newUnit(t.Unit, kindTransient, transientSpec(t), false)
	s.units[t.Unit] = u
	settled := s.launch(u)
	finished := u.finished
	s.mu.Unlock()
	err := <-settled
	if !t.Wait {
		return s.snapshot(u), err
	}
	<-finished

	return s.snapshot(u), nil
}

func (s *Supervisor) snapshot(u *unit) ctl.Status {
	s.mu.Lock()
	defer s.mu.Unlock()

	return u.status()
}

func (s *Supervisor) Status(name string) ctl.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.units[name]; ok {
		return u.status()
	}

	return ctl.Missing(name)
}

func (s *Supervisor) Processes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.order)
}

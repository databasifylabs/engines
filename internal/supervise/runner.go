package supervise

import (
	"context"
	"syscall"
	"time"

	"github.com/databasifylabs/engines/internal/ctl"
	"github.com/databasifylabs/engines/internal/manifest"
)

func (s *Supervisor) launch(u *unit) <-chan error {
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	settled := make(chan error, 1)
	u.cancel, u.finished = cancel, finished
	u.restarts, u.result = 0, "success"
	u.transition("activating", "start")
	go s.supervise(ctx, u, finished, settled)

	return settled
}

func (s *Supervisor) supervise(ctx context.Context, u *unit, finished chan struct{}, settled chan error) {
	defer close(finished)
	notify := func(err error) {
		select {
		case settled <- err:
		default:
		}
	}
	streak := 0
	for {
		spec := s.specOf(u)
		run := s.runOnce(ctx, u, spec, notify)
		if !run.restartsUnder(spec.restart) {
			notify(s.conclude(u, spec, run))

			return
		}
		if run.ran >= s.stableAfter {
			streak = 0
		}
		delay := backoff(spec.restartSec, streak, s.maxBackoff)
		streak++
		s.restartPending(u, run)
		s.logs.event(u.name, "restarting in %s", delay)
		if !sleep(ctx, delay) {
			notify(s.conclude(u, spec, outcome{stopped: true}))

			return
		}
	}
}

func (s *Supervisor) runOnce(ctx context.Context, u *unit, spec spec, notify func(error)) outcome {
	began := time.Now()
	proc, err := s.spawn(u.name, spec)
	if err != nil {
		s.logs.event(u.name, "failed to start: %v", err)
		notify(ctl.ErrFailed)

		return outcome{spawnErr: err}
	}
	s.started(u, spec, proc)
	s.logs.event(u.name, "started %s (pid %d)", spec.argv[0], proc.pid)
	if !spec.oneshot {
		notify(nil)
	}
	run := s.await(ctx, u, spec, proc)
	proc.release()
	run.exited, run.ran = true, time.Since(began)
	s.logs.event(u.name, "%s", run.describe())

	return run
}

func (s *Supervisor) await(ctx context.Context, u *unit, spec spec, proc *process) outcome {
	var deadline <-chan time.Time
	if spec.timeout > 0 {
		timer := time.NewTimer(spec.timeout)
		defer timer.Stop()
		deadline = timer.C
	}
	select {
	case status := <-proc.exited:
		return outcome{status: status}
	case <-ctx.Done():
		return outcome{status: s.terminate(u, spec, proc), stopped: true}
	case <-deadline:
		return outcome{status: s.terminate(u, spec, proc), timedOut: true}
	}
}

func (s *Supervisor) terminate(u *unit, spec spec, proc *process) exitStatus {
	s.mu.Lock()
	u.transition("deactivating", "stop-sigterm")
	s.mu.Unlock()
	signal := manifest.SignalName(spec.stopSignal)
	s.logs.event(u.name, "stopping with %s", signal)
	proc.signal(spec.stopSignal)
	deadline := time.NewTimer(spec.stopTimeout)
	defer deadline.Stop()
	resend := time.NewTicker(s.stopResend)
	defer resend.Stop()
	for {
		select {
		case status := <-proc.exited:
			return status
		case <-resend.C:
			s.logs.event(u.name, "still running, sending %s again", signal)
			proc.signal(spec.stopSignal)
		case <-deadline.C:
			s.logs.event(u.name, "did not stop within %s, killing", spec.stopTimeout)
			proc.signalGroup(syscall.SIGKILL)

			return <-proc.exited
		}
	}
}

func (s *Supervisor) specOf(u *unit) spec {
	s.mu.Lock()
	defer s.mu.Unlock()

	return u.spec
}

func (s *Supervisor) started(u *unit, spec spec, proc *process) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u.process = proc
	if spec.oneshot {
		u.transition("activating", "start")

		return
	}
	u.transition("active", "running")
}

func (s *Supervisor) restartPending(u *unit, run outcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.record(u, run)
	u.process = nil
	u.restarts++
	u.transition("activating", "auto-restart")
}

func (s *Supervisor) conclude(u *unit, spec spec, run outcome) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u.cancel, u.process = nil, nil
	s.record(u, run)
	switch {
	case run.stopped:
		u.transition("inactive", "dead")
	case u.result != "success":
		u.transition("failed", "failed")
	case spec.remainAfterExit:
		u.transition("active", "exited")
	default:
		u.transition("inactive", "dead")
	}
	s.collect(u)
	if u.result != "success" {
		return ctl.ErrFailed
	}

	return nil
}

func (s *Supervisor) record(u *unit, run outcome) {
	if run.exited {
		u.mainStatus = run.status.mainStatus()
	}
	u.result = run.result()
}

func (s *Supervisor) collect(u *unit) {
	if u.collectable() && s.units[u.name] == u {
		delete(s.units, u.name)
	}
}

func sleep(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

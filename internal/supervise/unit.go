package supervise

import (
	"context"
	"syscall"
	"time"

	"github.com/databasifylabs/engines/internal/ctl"
	"github.com/databasifylabs/engines/internal/manifest"
)

type unitKind int

const (
	kindProcess unitKind = iota
	kindSchedule
	kindTransient
)

type spec struct {
	argv            []string
	user            string
	env             map[string]string
	workdir         string
	oneshot         bool
	restart         manifest.RestartPolicy
	restartSec      time.Duration
	stopSignal      syscall.Signal
	reloadSignal    syscall.Signal
	stopTimeout     time.Duration
	timeout         time.Duration
	remainAfterExit bool
	collect         bool
}

func processSpec(p manifest.Process) spec {
	stop, _ := p.StopSignal.Value()
	reload, _ := p.ReloadSignal.Value()

	return spec{
		argv:         p.Argv,
		user:         p.User,
		env:          p.Env,
		workdir:      p.Workdir,
		oneshot:      p.Oneshot(),
		restart:      p.Restart,
		restartSec:   p.RestartSec.Std(),
		stopSignal:   stop,
		reloadSignal: reload,
		stopTimeout:  p.StopTimeout.Std(),
	}
}

func scheduleSpec(s manifest.Schedule) spec {
	return spec{
		argv:         s.Argv,
		user:         s.User,
		env:          s.Env,
		oneshot:      true,
		restart:      manifest.RestartNever,
		stopSignal:   syscall.SIGTERM,
		reloadSignal: syscall.SIGHUP,
		stopTimeout:  manifest.DefaultStopTimeout,
		timeout:      s.Timeout.Std(),
	}
}

func transientSpec(t ctl.Transient) spec {
	return spec{
		argv:            t.Argv,
		user:            t.User,
		env:             t.Env,
		workdir:         t.Workdir,
		restart:         manifest.RestartNever,
		stopSignal:      syscall.SIGTERM,
		reloadSignal:    syscall.SIGHUP,
		stopTimeout:     manifest.DefaultStopTimeout,
		remainAfterExit: t.RemainAfterExit,
		collect:         t.Collect,
	}
}

type unit struct {
	name    string
	kind    unitKind
	spec    spec
	enabled bool

	activeState string
	subState    string
	restarts    int
	mainStatus  int
	result      string
	process     *process
	cancel      context.CancelFunc
	finished    chan struct{}
}

func newUnit(name string, kind unitKind, spec spec, enabled bool) *unit {
	return &unit{name: name, kind: kind, spec: spec, enabled: enabled, activeState: "inactive", subState: "dead", result: "success"}
}

func (u *unit) running() bool {
	return u.cancel != nil
}

func (u *unit) transition(active, sub string) {
	u.activeState, u.subState = active, sub
}

func (u *unit) collectable() bool {
	if u.kind != kindTransient {
		return false
	}

	return u.activeState == "inactive" || (u.activeState == "failed" && u.spec.collect)
}

func (u *unit) status() ctl.Status {
	pid := 0
	if u.process != nil {
		pid = u.process.pid
	}

	return ctl.Status{
		Name:           u.name,
		Loaded:         true,
		Enabled:        u.enabled,
		ActiveState:    u.activeState,
		SubState:       u.subState,
		MainPID:        pid,
		NRestarts:      u.restarts,
		ExecMainStatus: u.mainStatus,
		Result:         u.result,
	}
}

type outcome struct {
	status   exitStatus
	spawnErr error
	exited   bool
	stopped  bool
	timedOut bool
	ran      time.Duration
}

func (o outcome) result() string {
	switch {
	case o.spawnErr != nil:
		return "resources"
	case o.timedOut:
		return "timeout"
	case o.stopped:
		return "success"
	case o.status.signal != 0:
		return "signal"
	case o.status.code != 0:
		return "exit-code"
	}

	return "success"
}

func (o outcome) describe() string {
	switch {
	case o.spawnErr != nil:
		return "failed to start: " + o.spawnErr.Error()
	case o.timedOut:
		return "timed out, " + o.status.String()
	}

	return o.status.String()
}

func (o outcome) restartsUnder(policy manifest.RestartPolicy) bool {
	if o.stopped {
		return false
	}
	switch policy {
	case manifest.RestartAlways:
		return true
	case manifest.RestartOnFailure:
		return o.result() != "success"
	}

	return false
}

func backoff(base time.Duration, streak int, ceiling time.Duration) time.Duration {
	ceiling = max(ceiling, base)
	delay := base
	for range streak {
		if delay >= ceiling {
			break
		}
		delay *= 2
	}

	return min(delay, ceiling)
}

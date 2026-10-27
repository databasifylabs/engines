package supervise

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/databasifylabs/engines/internal/manifest"
)

type exitStatus struct {
	code   int
	signal syscall.Signal
}

func statusOf(wait syscall.WaitStatus) exitStatus {
	if wait.Signaled() {
		return exitStatus{signal: wait.Signal()}
	}

	return exitStatus{code: wait.ExitStatus()}
}

func (e exitStatus) success() bool {
	return e.code == 0 && e.signal == 0
}

func (e exitStatus) mainStatus() int {
	if e.signal != 0 {
		return int(e.signal)
	}

	return e.code
}

func (e exitStatus) String() string {
	if e.signal != 0 {
		return "killed by " + manifest.SignalName(e.signal)
	}
	if e.code == 0 {
		return "exited successfully"
	}

	return "exited with status " + strconv.Itoa(e.code)
}

type process struct {
	pid     int
	handle  *os.Process
	exited  <-chan exitStatus
	drained <-chan struct{}
}

func (p *process) signal(sig syscall.Signal) {
	_ = p.handle.Signal(sig)
}

func (p *process) signalGroup(sig syscall.Signal) {
	_ = syscall.Kill(-p.pid, sig)
}

func (p *process) release() {
	p.signalGroup(syscall.SIGKILL)
	_ = p.handle.Release()
	select {
	case <-p.drained:
	case <-time.After(time.Second):
	}
}

type reaper struct {
	mu      sync.Mutex
	waiting map[int]chan exitStatus
}

func newReaper() *reaper {
	return &reaper{waiting: map[int]chan exitStatus{}}
}

func (r *reaper) start(cmd *exec.Cmd) (*process, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	exited := make(chan exitStatus, 1)
	r.waiting[cmd.Process.Pid] = exited

	return &process{pid: cmd.Process.Pid, handle: cmd.Process, exited: exited}, nil
}

func (r *reaper) run(ctx context.Context) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGCHLD)
	defer signal.Stop(signals)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		r.reap()
		select {
		case <-ctx.Done():
			return
		case <-signals:
		case <-ticker.C:
		}
	}
}

func (r *reaper) reap() {
	for {
		var wait syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &wait, syscall.WNOHANG, nil)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil || pid <= 0 {
			return
		}
		r.deliver(pid, statusOf(wait))
	}
}

func (r *reaper) deliver(pid int, status exitStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if exited, ok := r.waiting[pid]; ok {
		exited <- status
		delete(r.waiting, pid)
	}
}

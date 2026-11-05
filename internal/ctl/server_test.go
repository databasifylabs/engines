package ctl

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type fakeController struct {
	statuses   map[string]Status
	startErr   map[string]error
	calls      []string
	awaited    Status
	reloadErr  error
	lastSignal syscall.Signal
}

func (f *fakeController) record(call string) { f.calls = append(f.calls, call) }

func (f *fakeController) known(name string) error {
	if _, ok := f.statuses[name]; !ok {
		return ErrNotFound
	}

	return nil
}

func (f *fakeController) Start(name string, block bool) error {
	f.record("start " + name)
	if err := f.startErr[name]; err != nil {
		return err
	}

	return f.known(name)
}

func (f *fakeController) Stop(name string, block bool) error {
	f.record("stop " + name)

	return f.known(name)
}

func (f *fakeController) Restart(name string) error { return f.known(name) }
func (f *fakeController) Reload(name string) error  { return f.known(name) }

func (f *fakeController) SetEnabled(name string, enabled bool) error {
	f.record(map[bool]string{true: "enable ", false: "disable "}[enabled] + name)

	return f.known(name)
}

func (f *fakeController) ResetFailed(pattern string) { f.record("reset-failed " + pattern) }

func (f *fakeController) Kill(name string, signal syscall.Signal) error {
	f.lastSignal = signal

	return f.known(name)
}

func (f *fakeController) ReloadManifest() error { return f.reloadErr }

func (f *fakeController) RunTransient(t Transient) (Status, error) {
	if _, ok := f.statuses[t.Unit]; ok {
		return Status{}, ErrLoaded
	}
	f.record("run " + t.Unit)

	return f.awaited, nil
}

func (f *fakeController) Status(name string) Status {
	if status, ok := f.statuses[name]; ok {
		return status
	}

	return Missing(name)
}

func (f *fakeController) Processes() []string { return []string{"postgres", "backup"} }

func newFake() *fakeController {
	return &fakeController{statuses: map[string]Status{
		"postgres": {Name: "postgres", Loaded: true, Enabled: true, ActiveState: "active", SubState: "running", MainPID: 42, NRestarts: 1, Result: "success"},
		"backup":   {Name: "backup", Loaded: true, ActiveState: "failed", SubState: "failed", ExecMainStatus: 3, Result: "exit-code"},
	}}
}

func handle(controller Controller, args ...string) Response {
	command, err := Parse(args)
	if err != nil {
		panic(err)
	}

	return NewServer(controller, func(string) string { return "/nonexistent" }).Handle(*command.Request)
}

func TestStateQueriesMirrorSystemctlExitCodes(t *testing.T) {
	fake := newFake()
	cases := []struct {
		args   []string
		stdout string
		exit   int
	}{
		{[]string{"is-active", "postgres.service"}, "active\n", 0},
		{[]string{"is-active", "--quiet", "postgres"}, "", 0},
		{[]string{"is-active", "backup"}, "failed\n", 3},
		{[]string{"is-active", "missing"}, "inactive\n", 3},
		{[]string{"is-active", "--quiet", "missing", "postgres"}, "", 0},
		{[]string{"is-failed", "--quiet", "backup"}, "", 0},
		{[]string{"is-failed", "postgres"}, "active\n", 1},
		{[]string{"show", "-p", "NRestarts", "--value", "postgres"}, "1\n", 0},
		{[]string{"show", "-p", "MainPID", "--value", "missing"}, "0\n", 0},
		{[]string{"show", "-p", "ActiveState,ExecMainStatus,Result", "backup"}, "ActiveState=failed\nExecMainStatus=3\nResult=exit-code\n", 0},
		{[]string{"show", "-p", "LoadState", "--value", "missing"}, "not-found\n", 0},
	}
	for _, c := range cases {
		response := handle(fake, c.args...)
		if response.Stdout != c.stdout || response.Exit != c.exit {
			t.Errorf("%v: got %q exit %d, want %q exit %d", c.args, response.Stdout, response.Exit, c.stdout, c.exit)
		}
	}
}

func TestActionsMapErrorsToSystemctlExitCodes(t *testing.T) {
	fake := newFake()
	fake.startErr = map[string]error{"backup": ErrFailed}
	cases := []struct {
		args []string
		exit int
	}{
		{[]string{"start", "postgres"}, 0},
		{[]string{"start", "missing.service"}, 5},
		{[]string{"start", "backup"}, 1},
		{[]string{"stop", "missing"}, 5},
		{[]string{"restart", "postgres"}, 0},
		{[]string{"reload", "missing"}, 5},
		{[]string{"kill", "-s", "KILL", "postgres"}, 0},
		{[]string{"kill", "-s", "BOGUS", "postgres"}, ExitUsage},
		{[]string{"mask", "patroni.service"}, 0},
		{[]string{"status", "postgres"}, 0},
		{[]string{"status", "backup"}, 3},
		{[]string{"status", "missing"}, 4},
		{[]string{"run-schedule", "backup"}, 3},
		{[]string{"run-schedule", "missing"}, 5},
		{[]string{"run", "--unit=postgres", "true"}, 1},
	}
	for _, c := range cases {
		if response := handle(fake, c.args...); response.Exit != c.exit {
			t.Errorf("%v: exit %d (%q), want %d", c.args, response.Exit, response.Stderr, c.exit)
		}
	}
	if fake.lastSignal != syscall.SIGKILL {
		t.Errorf("kill sent %v", fake.lastSignal)
	}
}

func TestStatusWithoutUnitsListsEveryProcess(t *testing.T) {
	response := handle(newFake(), "status")
	want := "● postgres.service enabled, active (running), pid 42, 1 restarts, result success\n" +
		"● backup.service disabled, failed (failed), pid 0, 0 restarts, result exit-code\n"
	if response.Stdout != want || response.Exit != 0 {
		t.Fatalf("got %q exit %d", response.Stdout, response.Exit)
	}
}

func TestEnableNowAndResetFailedDispatch(t *testing.T) {
	fake := newFake()
	handle(fake, "enable", "--now", "postgres.service")
	handle(fake, "disable", "postgres")
	handle(fake, "reset-failed")
	handle(fake, "reset-failed", "databasify-*")
	want := "enable postgres,start postgres,disable postgres,reset-failed ,reset-failed databasify-*"
	if got := strings.Join(fake.calls, ","); got != want {
		t.Fatalf("calls = %s", got)
	}
}

func TestRunWaitPassesExitCodeThrough(t *testing.T) {
	fake := newFake()
	fake.awaited = Status{Result: "exit-code", ExecMainStatus: 7}
	response := handle(fake, "run", "--wait", "--unit=copy", "sh", "-c", "exit 7")
	if response.Exit != 7 || !strings.Contains(response.Stderr, "Running as unit: copy.service") {
		t.Fatalf("got %+v", response)
	}
	if response := handle(fake, "run", "--quiet", "--unit=other", "true"); response.Exit != 0 || response.Stderr != "" {
		t.Fatalf("quiet run = %+v", response)
	}
}

func TestDaemonReloadReportsFailure(t *testing.T) {
	fake := newFake()
	fake.reloadErr = os.ErrInvalid
	if response := handle(fake, "daemon-reload"); response.Exit != 1 {
		t.Fatalf("got %+v", response)
	}
}

func TestCLIOverSocket(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "databasify.sock")
	listener, err := Listen(socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = NewServer(newFake(), func(string) string { return "" }).Serve(ctx, listener) }()

	var stdout, stderr bytes.Buffer
	cli := CLI{Socket: socket, Stdout: &stdout, Stderr: &stderr}
	if code := cli.Run([]string{"is-active", "backup.service"}); code != 3 || stdout.String() != "failed\n" {
		t.Fatalf("exit %d, stdout %q", code, stdout.String())
	}
	if code := cli.Run([]string{"start", "--bogus", "x"}); code != ExitUsage {
		t.Fatalf("usage exit = %d", code)
	}
	cancel()

	unavailable := CLI{Socket: filepath.Join(t.TempDir(), "none.sock"), Stdout: &stdout, Stderr: &stderr}
	if code := unavailable.Run([]string{"is-active", "x"}); code != ExitUnavailable {
		t.Fatalf("unavailable exit = %d", code)
	}
}

func TestLogsReadAcrossRotations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "valkey-migration.log")
	write := func(name, text string) {
		if err := os.WriteFile(name, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(path+".1", "2026-10-27T10:00:00Z one\n2026-10-27T10:00:01Z two\n")
	write(path, "2026-10-27T10:00:02Z syncing aof, diff=[0]\n")

	var out bytes.Buffer
	cli := CLI{LogFile: func(unit string) string { return filepath.Join(dir, unit+".log") }, Stdout: &out, Stderr: &out}
	if code := cli.Run([]string{"logs", "--unit", "valkey-migration.service", "--lines", "2", "--output", "cat"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if out.String() != "two\nsyncing aof, diff=[0]\n" {
		t.Fatalf("cat output = %q", out.String())
	}
	out.Reset()
	cli.Run([]string{"logs", "-u", "valkey-migration", "-n", "1"})
	if out.String() != "2026-10-27T10:00:02Z valkey-migration: syncing aof, diff=[0]\n" {
		t.Fatalf("short-iso output = %q", out.String())
	}
	out.Reset()
	cli.Run([]string{"logs", "valkey-migration.service", "-n", "1", "-o", "cat"})
	if out.String() != "syncing aof, diff=[0]\n" {
		t.Fatalf("positional unit output = %q", out.String())
	}
	out.Reset()
	cli.Run([]string{"logs", "-u", "nothing"})
	if out.String() != "-- No entries --\n" {
		t.Fatalf("empty output = %q", out.String())
	}
}

func TestLogsFollowAcrossRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "redis.log")
	if err := os.WriteFile(path, []byte("2026-10-27T10:00:00Z old\n2026-10-27T10:00:01Z recent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ShowLogs(ctx, path, LogQuery{Unit: "redis", Lines: 1, Output: "cat", Follow: true}, out)
	}()

	waitForOutput(t, out, "recent\n")
	appendTo(t, path, "2026-10-27T10:00:02Z appended\n")
	waitForOutput(t, out, "recent\nappended\n")
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	appendTo(t, path, "2026-10-27T10:00:03Z rotated\n")
	waitForOutput(t, out, "recent\nappended\nrotated\n")
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buffer.Write(data)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buffer.String()
}

func appendTo(t *testing.T, path, text string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func waitForOutput(t *testing.T, out *lockedBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for out.String() != want {
		if time.Now().After(deadline) {
			t.Fatalf("output = %q, want %q", out.String(), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

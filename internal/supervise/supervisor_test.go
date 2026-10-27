package supervise

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/databasifylabs/engines/internal/ctl"
	"github.com/databasifylabs/engines/internal/layout"
)

type harness struct {
	t          *testing.T
	supervisor *Supervisor
	layout     layout.Layout
	clock      *fakeClock
}

func boot(t *testing.T, document string) *harness {
	t.Helper()
	paths := layout.Layout{Home: t.TempDir(), Run: t.TempDir()}
	h := &harness{t: t, layout: paths, clock: newFakeClock()}
	h.writeManifest(document)
	h.start()

	return h
}

func (h *harness) start() {
	h.supervisor = New(Options{
		Layout:      h.layout,
		Console:     io.Discard,
		Clock:       h.clock,
		MaxBackoff:  400 * time.Millisecond,
		StableAfter: time.Hour,
	})
	h.supervisor.Boot()
	h.t.Cleanup(h.supervisor.Shutdown)
}

func (h *harness) writeManifest(document string) {
	h.t.Helper()
	document = strings.ReplaceAll(document, "$DIR", h.layout.Home)
	if err := os.MkdirAll(filepath.Dir(h.layout.Manifest()), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(h.layout.Manifest(), []byte(document), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) file(name string) string {
	data, _ := os.ReadFile(filepath.Join(h.layout.Home, name))

	return string(data)
}

func fileExists(h *harness, name string) bool {
	_, err := os.Stat(filepath.Join(h.layout.Home, name))

	return err == nil
}

func (h *harness) status(name string) ctl.Status {
	return h.supervisor.Status(name)
}

func (h *harness) waitFor(what string, condition func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (h *harness) waitForState(name, state string) {
	h.t.Helper()
	h.waitFor(name+" to be "+state, func() bool { return h.status(name).ActiveState == state })
}

func entry(name, script, extra string) string {
	return fmt.Sprintf(`{"name":%q,"argv":["sh","-c",%q],"stopTimeout":"2s"%s}`, name, script, extra)
}

func manifestOf(processes ...string) string {
	return `{"processes":[` + strings.Join(processes, ",") + `]}`
}

const idle = "trap 'exit 0' TERM; while :; do sleep 0.05; done"

func TestBootStartsEnabledProcessesAfterTheirDependencies(t *testing.T) {
	h := boot(t, manifestOf(
		entry("app", "echo app >> $DIR/order; "+idle, `,"after":["migrate"],"enabled":true`),
		entry("migrate", "sleep 0.2; echo migrate >> $DIR/order", `,"type":"oneshot","enabled":true`),
		entry("spare", idle, `,"enabled":false`),
	))

	h.waitFor("app to write", func() bool { return strings.Contains(h.file("order"), "app") })
	if got := h.file("order"); got != "migrate\napp\n" {
		t.Fatalf("order = %q", got)
	}
	if h.status("app").ActiveState != "active" || h.status("app").MainPID == 0 {
		t.Fatalf("app status = %+v", h.status("app"))
	}
	if state := h.status("spare").ActiveState; state != "inactive" {
		t.Fatalf("disabled process is %s", state)
	}
	if status := h.status("migrate"); status.ActiveState != "inactive" || status.Result != "success" {
		t.Fatalf("oneshot status = %+v", status)
	}

	previous := h.status("app").MainPID
	if err := h.supervisor.Restart("app"); err != nil {
		t.Fatal(err)
	}
	if status := h.status("app"); status.ActiveState != "active" || status.MainPID == previous || status.NRestarts != 0 {
		t.Fatalf("after restart = %+v (previous pid %d)", status, previous)
	}
}

func TestShutdownStopsProcessesInReverseOrder(t *testing.T) {
	stopper := func(name string) string {
		return fmt.Sprintf("trap 'echo %s >> $DIR/stopped; exit 0' TERM; echo >> $DIR/ready; while :; do sleep 0.05; done", name)
	}
	h := boot(t, manifestOf(
		entry("etcd", stopper("etcd"), `,"enabled":true`),
		entry("patroni", stopper("patroni"), `,"after":["etcd"],"enabled":true`),
		entry("pgbouncer", stopper("pgbouncer"), `,"after":["patroni"],"enabled":true`),
	))
	h.waitFor("traps to be set", func() bool { return h.file("ready") == "\n\n\n" })

	h.supervisor.Shutdown()

	if got := h.file("stopped"); got != "pgbouncer\npatroni\netcd\n" {
		t.Fatalf("stop order = %q", got)
	}
}

func TestRestartsCrashingProcessWithBackoff(t *testing.T) {
	h := boot(t, manifestOf(entry("crashy", "exit 3", `,"restart":"always","restartSec":"20ms","enabled":true`)))

	h.waitFor("three restarts", func() bool { return h.status("crashy").NRestarts >= 3 })
	if status := h.status("crashy"); status.ExecMainStatus != 3 {
		t.Fatalf("status = %+v", status)
	}
	if err := h.supervisor.Stop("crashy", true); err != nil {
		t.Fatal(err)
	}
	if state := h.status("crashy").ActiveState; state != "inactive" {
		t.Fatalf("stopped crash loop is %s", state)
	}
}

func TestRestartPolicies(t *testing.T) {
	h := boot(t, manifestOf(
		entry("clean", "exit 0", `,"restart":"on-failure","restartSec":"20ms","enabled":true`),
		entry("broken", "exit 4", `,"restart":"never","enabled":true`),
		entry("retried", "exit 5", `,"restart":"on-failure","restartSec":"20ms","enabled":true`),
	))

	h.waitForState("broken", "failed")
	h.waitFor("retried to restart", func() bool { return h.status("retried").NRestarts >= 1 })
	h.waitForState("clean", "inactive")
	if status := h.status("clean"); status.NRestarts != 0 || status.Result != "success" {
		t.Fatalf("clean exit status = %+v", status)
	}
	if status := h.status("broken"); status.Result != "exit-code" || status.ExecMainStatus != 4 {
		t.Fatalf("broken status = %+v", status)
	}
}

func TestBackoffDoublesUpToCeiling(t *testing.T) {
	cases := []struct {
		streak int
		want   time.Duration
	}{{0, time.Second}, {1, 2 * time.Second}, {3, 8 * time.Second}, {10, 30 * time.Second}}
	for _, c := range cases {
		if got := backoff(time.Second, c.streak, 30*time.Second); got != c.want {
			t.Errorf("backoff streak %d = %s, want %s", c.streak, got, c.want)
		}
	}
	if got := backoff(time.Minute, 2, 30*time.Second); got != time.Minute {
		t.Errorf("base above ceiling = %s", got)
	}
}

func TestOneshotStartBlocksAndReportsFailure(t *testing.T) {
	h := boot(t, manifestOf(
		entry("ok", "sleep 0.1; echo done > $DIR/ok", `,"type":"oneshot"`),
		entry("bad", "exit 9", `,"type":"oneshot"`),
	))

	if err := h.supervisor.Start("ok", true); err != nil {
		t.Fatal(err)
	}
	if h.file("ok") != "done\n" {
		t.Fatal("start returned before the oneshot finished")
	}
	if err := h.supervisor.Start("bad", true); !errors.Is(err, ctl.ErrFailed) {
		t.Fatalf("start of failing oneshot = %v", err)
	}
	if status := h.status("bad"); status.ActiveState != "failed" || status.ExecMainStatus != 9 {
		t.Fatalf("status = %+v", status)
	}
	h.supervisor.ResetFailed("ba*")
	if status := h.status("bad"); status.ActiveState != "inactive" || status.Result != "success" {
		t.Fatalf("after reset-failed = %+v", status)
	}
}

func TestProcessThatCannotSpawnFailsItsStartAndKeepsRetrying(t *testing.T) {
	h := boot(t, manifestOf(
		`{"name":"broken","argv":["/nonexistent/daemon"],"restart":"always","enabled":true}`,
		`{"name":"spare","argv":["/nonexistent/daemon"],"restart":"on-failure"}`,
	))

	started := make(chan error, 1)
	go func() { started <- h.supervisor.Start("spare", true) }()
	select {
	case err := <-started:
		if !errors.Is(err, ctl.ErrFailed) {
			t.Fatalf("start of unspawnable process = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("start hung on a process that cannot spawn")
	}
	h.waitFor("broken to retry", func() bool { return h.status("broken").NRestarts > 0 })
	if result := h.status("broken").Result; result != "resources" {
		t.Fatalf("broken result = %s", result)
	}
}

func TestStopEscalatesToKillAfterTimeout(t *testing.T) {
	h := boot(t, manifestOf(entry("stubborn", "trap '' TERM; touch $DIR/ready; while :; do sleep 0.05; done", `,"stopTimeout":"200ms","enabled":true`)))
	h.waitFor("traps to be set", func() bool { return fileExists(h, "ready") })

	began := time.Now()
	if err := h.supervisor.Stop("stubborn", true); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(began); elapsed < 200*time.Millisecond {
		t.Fatalf("stopped after %s, before the stop timeout", elapsed)
	}
	if status := h.status("stubborn"); status.ActiveState != "inactive" || status.ExecMainStatus != 9 {
		t.Fatalf("status = %+v", status)
	}
}

func TestReapsOrphanedGrandchildren(t *testing.T) {
	if err := BecomeSubreaper(); err != nil {
		t.Skip("subreaper unavailable:", err)
	}
	h := boot(t, manifestOf(entry("forker", "for i in 1 2 3; do setsid sleep 0.1 & done; exit 0", `,"type":"oneshot"`)))

	if err := h.supervisor.Start("forker", true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	h.waitFor("orphans to be reaped", func() bool { return len(zombieChildren(t)) == 0 })
}

func zombieChildren(t *testing.T) []string {
	t.Helper()
	stats, _ := filepath.Glob("/proc/[0-9]*/stat")
	parent := fmt.Sprint(os.Getpid())
	var zombies []string
	for _, stat := range stats {
		data, err := os.ReadFile(stat)
		if err != nil {
			continue
		}
		fields := strings.Fields(string(data[strings.LastIndexByte(string(data), ')')+1:]))
		if len(fields) > 1 && fields[0] == "Z" && fields[1] == parent {
			zombies = append(zombies, stat)
		}
	}

	return zombies
}

func TestTransientUnits(t *testing.T) {
	h := boot(t, manifestOf())

	if _, err := h.supervisor.RunTransient(ctl.Transient{Unit: "shake", Argv: []string{"sh", "-c", "exit 2"}, RemainAfterExit: true}); err != nil {
		t.Fatal(err)
	}
	h.waitForState("shake", "failed")
	if _, err := h.supervisor.RunTransient(ctl.Transient{Unit: "shake", Argv: []string{"true"}}); !errors.Is(err, ctl.ErrLoaded) {
		t.Fatalf("rerun of a loaded unit = %v", err)
	}
	h.supervisor.ResetFailed("shake")
	if h.status("shake").Loaded {
		t.Fatal("reset-failed did not unload the transient unit")
	}

	status, err := h.supervisor.RunTransient(ctl.Transient{Unit: "sync", Argv: []string{"sh", "-c", "pwd > out; echo $GREETING >> out"}, Workdir: h.layout.Home, Env: map[string]string{"GREETING": "hi"}, RemainAfterExit: true, Wait: true})
	if err != nil || status.ActiveState != "active" || status.SubState != "exited" {
		t.Fatalf("remain-after-exit status = %+v, %v", status, err)
	}
	if got := h.file("out"); got != h.layout.Home+"\nhi\n" {
		t.Fatalf("workdir/env output = %q", got)
	}
	if err := h.supervisor.Stop("sync", true); err != nil || h.status("sync").Loaded {
		t.Fatalf("stop did not unload the exited unit: %v", err)
	}

	status, _ = h.supervisor.RunTransient(ctl.Transient{Unit: "copy", Argv: []string{"sh", "-c", "exit 6"}, Collect: true, Wait: true})
	if status.ExecMainStatus != 6 || status.Result != "exit-code" || h.status("copy").Loaded {
		t.Fatalf("collected unit status = %+v, still loaded = %v", status, h.status("copy").Loaded)
	}
}

func TestSchedulesFireOnTheClock(t *testing.T) {
	h := boot(t, `{"schedules":[
		{"name":"archive","every":"1m","argv":["sh","-c","echo run >> $DIR/archive"]},
		{"name":"backup","onCalendar":"02:00","argv":["sh","-c","echo run >> $DIR/backup"]}
	]}`)
	h.clock.waitForWaiters(t, 2)

	h.clock.Advance(time.Minute)
	h.waitFor("archive to run", func() bool { return h.file("archive") == "run\n" })
	h.waitForState("archive", "inactive")
	h.clock.waitForWaiters(t, 2)
	h.clock.Advance(time.Minute)
	h.waitFor("archive to run again", func() bool { return h.file("archive") == "run\nrun\n" })
	h.waitForState("archive", "inactive")
	if h.file("backup") != "" {
		t.Fatal("daily schedule fired early")
	}

	if err := h.supervisor.SetEnabled("archive", false); err != nil {
		t.Fatal(err)
	}
	h.clock.waitForWaiters(t, 2)
	h.clock.Advance(time.Minute)
	h.clock.waitForWaiters(t, 2)
	if h.file("archive") != "run\nrun\n" {
		t.Fatal("disabled schedule fired")
	}

	if err := h.supervisor.Start("backup", true); err != nil {
		t.Fatal(err)
	}
	if h.file("backup") != "run\n" {
		t.Fatal("manual run of a schedule did not run it")
	}
}

func TestReloadManifestAddsUpdatesAndRemovesUnits(t *testing.T) {
	h := boot(t, manifestOf(entry("old", idle, `,"enabled":true`), entry("kept", idle, `,"enabled":true`)))
	h.waitForState("old", "active")

	h.writeManifest(manifestOf(entry("kept", idle, `,"enabled":true`), entry("new", idle, `,"enabled":true`)))
	if err := h.supervisor.ReloadManifest(); err != nil {
		t.Fatal(err)
	}

	if h.status("old").Loaded {
		t.Fatal("removed unit is still loaded")
	}
	if state := h.status("kept").ActiveState; state != "active" {
		t.Fatalf("kept unit is %s", state)
	}
	if status := h.status("new"); !status.Loaded || status.ActiveState != "inactive" {
		t.Fatalf("new unit = %+v", status)
	}

	h.writeManifest(`{"processes":[{"name":"broken"}]}`)
	if err := h.supervisor.ReloadManifest(); err == nil {
		t.Fatal("invalid manifest was accepted")
	}
	if !h.status("kept").Loaded {
		t.Fatal("a rejected manifest changed the units")
	}
}

func TestReloadKillAndEnabledState(t *testing.T) {
	h := boot(t, manifestOf(entry("pooler", "trap 'echo hup >> $DIR/signals' HUP; trap 'exit 0' TERM; touch $DIR/ready; while :; do sleep 0.05; done", `,"enabled":true`)))
	h.waitFor("traps to be set", func() bool { return fileExists(h, "ready") })

	if err := h.supervisor.Reload("pooler"); err != nil {
		t.Fatal(err)
	}
	h.waitFor("reload signal", func() bool { return h.file("signals") == "hup\n" })
	if err := h.supervisor.Reload("missing"); !errors.Is(err, ctl.ErrNotFound) {
		t.Fatalf("reload of a missing unit = %v", err)
	}

	if err := h.supervisor.SetEnabled("pooler", false); err != nil {
		t.Fatal(err)
	}
	if err := h.supervisor.Kill("pooler", 9); err != nil {
		t.Fatal(err)
	}
	h.waitForState("pooler", "failed")
	h.supervisor.Shutdown()

	h.start()
	if status := h.status("pooler"); status.Enabled || status.ActiveState != "inactive" {
		t.Fatalf("disabled unit after restart = %+v", status)
	}
}

func TestProcessOutputIsLoggedAndRotated(t *testing.T) {
	var console lockedBuffer
	paths := layout.Layout{Home: t.TempDir(), Run: t.TempDir()}
	sink := newLogSink(paths.Logs(), 64, &console)

	for i := range 6 {
		sink.line("redis", []byte(fmt.Sprintf("line %d", i)))
	}

	if !strings.Contains(console.String(), "[redis] line 5\n") {
		t.Fatalf("console = %q", console.String())
	}
	lines, err := ctl.Tail(paths.LogFile("redis"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 6 || !strings.HasSuffix(lines[0], " line 0") || !strings.HasSuffix(lines[5], " line 5") {
		t.Fatalf("lines across generations = %q", lines)
	}
	if _, err := os.Stat(paths.LogFile("redis") + ".1"); err != nil {
		t.Fatal("log was not rotated")
	}
}

type lockedBuffer struct {
	mu   sync.Mutex
	text strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.text.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.text.String()
}

type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []fakeWaiter
}

type fakeWaiter struct {
	at time.Time
	ch chan time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.waiters = append(c.waiters, fakeWaiter{at: c.now.Add(d), ch: ch})

	return ch
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	pending := c.waiters[:0]
	for _, waiter := range c.waiters {
		if waiter.at.After(c.now) {
			pending = append(pending, waiter)

			continue
		}
		waiter.ch <- c.now
	}
	c.waiters = pending
}

func (c *fakeClock) waitForWaiters(t *testing.T, count int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.mu.Lock()
		waiting := len(c.waiters)
		c.mu.Unlock()
		if waiting >= count {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d timers registered", waiting, count)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

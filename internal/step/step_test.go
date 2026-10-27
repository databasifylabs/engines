package step

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunPassesExitCodeAndOutputThrough(t *testing.T) {
	cases := map[string]struct {
		script string
		code   int
	}{
		"success":     {"echo hello; echo oops >&2", 0},
		"exit code":   {"echo hello; echo oops >&2; exit 7", 7},
		"strict mode": {"set -euo pipefail\necho hello; echo oops >&2; false\necho never", 1},
		"signal":      {"echo hello; echo oops >&2; kill -TERM $$", 143},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code, err := Step{Name: "probe", Dir: t.TempDir(), Stdout: &stdout, Stderr: &stderr}.Run(strings.NewReader(c.script))
			if err != nil {
				t.Fatal(err)
			}
			if code != c.code || stdout.String() != "hello\n" || stderr.String() != "oops\n" {
				t.Fatalf("code %d stdout %q stderr %q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunRecordsPidAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	var stdout bytes.Buffer
	script := `cat "${0%.sh}.pid"; echo "$$"; echo "$0"`
	if _, err := (Step{Name: "probe", Task: "task-1", Dir: dir, Stdout: &stdout, Stderr: &stdout}).Run(strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 3 || lines[0] != lines[1]+" task-1" || lines[2] != filepath.Join(dir, "task-1.sh") {
		t.Fatalf("output = %q", stdout.String())
	}
	if leftovers, _ := os.ReadDir(dir); len(leftovers) != 0 {
		t.Fatalf("left %d files behind", len(leftovers))
	}
}

func TestKillStopsTheStepProcessGroup(t *testing.T) {
	dir := t.TempDir()
	result := make(chan int, 1)
	go func() {
		code, _ := Step{Name: "long", Task: "task-2", Dir: dir, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}.Run(strings.NewReader("sleep 30 & sleep 30"))
		result <- code
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "task-2.pid")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pid file never appeared")
		}
		time.Sleep(10 * time.Millisecond)
	}

	stopped, err := Kill(dir, "task-2", 2*time.Second)
	if err != nil || !stopped {
		t.Fatalf("kill = %v, %v", stopped, err)
	}
	select {
	case code := <-result:
		if code != 143 {
			t.Fatalf("exit code = %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("step kept running after step-kill")
	}

	if stopped, err := Kill(dir, "task-2", time.Second); err != nil || stopped {
		t.Fatalf("second kill = %v, %v", stopped, err)
	}
	if _, err := Kill(dir, "../etc/passwd", time.Second); err == nil {
		t.Fatal("accepted a path as task id")
	}
}

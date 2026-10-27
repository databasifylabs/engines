package manifest

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestParseAppliesDefaultsAndDurations(t *testing.T) {
	m, err := Parse([]byte(`{"processes":[
		{"name":"postgres","argv":["postgres"],"enabled":true},
		{"name":"pgbouncer","argv":["pgbouncer"],"restart":"always","restartSec":2,"stopTimeout":"1m30s","stopSignal":"INT"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	postgres, pgbouncer := m.Processes[0], m.Processes[1]
	if postgres.Type != KindSimple || postgres.Restart != RestartNever || postgres.RestartSec.Std() != DefaultRestartSec || postgres.StopTimeout.Std() != DefaultStopTimeout {
		t.Fatalf("defaults not applied: %+v", postgres)
	}
	if pgbouncer.RestartSec.Std() != 2*time.Second || pgbouncer.StopTimeout.Std() != 90*time.Second {
		t.Fatalf("durations not parsed: %+v", pgbouncer)
	}
	if signal, _ := pgbouncer.StopSignal.Value(); signal != syscall.SIGINT {
		t.Fatalf("stop signal = %v", signal)
	}
}

func TestParseRejectsInvalidManifests(t *testing.T) {
	cases := map[string]string{
		"duplicate":      `{"processes":[{"name":"a","argv":["x"]},{"name":"a","argv":["x"]}]}`,
		"empty argv":     `{"processes":[{"name":"a","argv":[]}]}`,
		"bad name":       `{"processes":[{"name":"a/b","argv":["x"]}]}`,
		"bad type":       `{"processes":[{"name":"a","argv":["x"],"type":"forking"}]}`,
		"bad restart":    `{"processes":[{"name":"a","argv":["x"],"restart":"sometimes"}]}`,
		"bad signal":     `{"processes":[{"name":"a","argv":["x"],"stopSignal":"NOPE"}]}`,
		"cycle":          `{"processes":[{"name":"a","argv":["x"],"after":["b"]},{"name":"b","argv":["x"],"after":["a"]}]}`,
		"both schedules": `{"schedules":[{"name":"s","argv":["x"],"every":"1m","onCalendar":"02:00"}]}`,
		"no schedule":    `{"schedules":[{"name":"s","argv":["x"]}]}`,
		"bad calendar":   `{"schedules":[{"name":"s","argv":["x"],"onCalendar":"2am"}]}`,
		"shared name":    `{"processes":[{"name":"a","argv":["x"]}],"schedules":[{"name":"a","argv":["x"],"every":"1m"}]}`,
	}
	for name, document := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(document)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestLoadTreatsMissingFileAsEmpty(t *testing.T) {
	m, err := Load(filepath.Join(t.TempDir(), "processes.json"))
	if err != nil || len(m.Processes) != 0 || len(m.Schedules) != 0 {
		t.Fatalf("got %+v, %v", m, err)
	}
}

func TestStartOrderFollowsAfter(t *testing.T) {
	m, err := Parse([]byte(`{"processes":[
		{"name":"pgbouncer","argv":["x"],"after":["patroni"]},
		{"name":"patroni","argv":["x"],"after":["etcd","missing"]},
		{"name":"etcd","argv":["x"]}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	ordered, _ := m.StartOrder()
	names := make([]string, 0, len(ordered))
	for _, p := range ordered {
		names = append(names, p.Name)
	}
	if got := strings.Join(names, ","); got != "etcd,patroni,pgbouncer" {
		t.Fatalf("order = %s", got)
	}
}

func TestScheduleNext(t *testing.T) {
	now := time.Date(2026, 10, 27, 3, 30, 0, 0, time.UTC)
	cases := []struct {
		schedule Schedule
		want     time.Time
	}{
		{Schedule{Every: Duration(time.Minute)}, now.Add(time.Minute)},
		{Schedule{OnCalendar: "04:15"}, time.Date(2026, 10, 27, 4, 15, 0, 0, time.UTC)},
		{Schedule{OnCalendar: "03:30"}, time.Date(2026, 10, 28, 3, 30, 0, 0, time.UTC)},
		{Schedule{OnCalendar: "02:00"}, time.Date(2026, 10, 28, 2, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		if got := c.schedule.Next(now); !got.Equal(c.want) {
			t.Errorf("%+v: next = %s, want %s", c.schedule, got, c.want)
		}
	}
}

func TestParseSignal(t *testing.T) {
	for text, want := range map[string]syscall.Signal{"KILL": syscall.SIGKILL, "SIGTERM": syscall.SIGTERM, "hup": syscall.SIGHUP, "9": syscall.SIGKILL} {
		if got, err := ParseSignal(text); err != nil || got != want {
			t.Errorf("ParseSignal(%q) = %v, %v", text, got, err)
		}
	}
	if _, err := ParseSignal("BOGUS"); err == nil {
		t.Error("expected an error for an unknown signal")
	}
}

package ctl

import (
	"errors"
	"reflect"
	"testing"
)

func TestParseSystemctlStyleCommands(t *testing.T) {
	cases := map[string]struct {
		args []string
		want Request
	}{
		"is-active quiet": {
			[]string{"is-active", "--quiet", "databasify-patroni.service", "databasify-postgres.service"},
			Request{Verb: "is-active", Quiet: true, Units: []string{"databasify-patroni", "databasify-postgres"}},
		},
		"show value": {
			[]string{"show", "-p", "NRestarts", "--value", "mariadb.service"},
			Request{Verb: "show", Value: true, Properties: []string{"NRestarts"}, Units: []string{"mariadb"}},
		},
		"show inline properties": {
			[]string{"show", "--property=ActiveState,SubState", "-pMainPID", "x"},
			Request{Verb: "show", Properties: []string{"ActiveState", "SubState", "MainPID"}, Units: []string{"x"}},
		},
		"stop no block": {
			[]string{"stop", "--no-block", "mysql.service"},
			Request{Verb: "stop", NoBlock: true, Units: []string{"mysql"}},
		},
		"enable now": {
			[]string{"enable", "--now", "databasify-etcd.service"},
			Request{Verb: "enable", Now: true, Units: []string{"databasify-etcd"}},
		},
		"disable timer": {
			[]string{"disable", "--now", "databasify-postgres-backup.timer"},
			Request{Verb: "disable", Now: true, Units: []string{"databasify-postgres-backup"}},
		},
		"kill": {
			[]string{"kill", "-s", "KILL", "redis.service"},
			Request{Verb: "kill", Signal: "KILL", Units: []string{"redis"}},
		},
		"reset-failed glob": {
			[]string{"reset-failed", "databasify-postgres-*"},
			Request{Verb: "reset-failed", Units: []string{"databasify-postgres-*"}},
		},
		"reset-failed all": {
			[]string{"reset-failed"},
			Request{Verb: "reset-failed"},
		},
		"daemon-reload": {
			[]string{"daemon-reload"},
			Request{Verb: "daemon-reload"},
		},
		"status of every process": {
			[]string{"status", "--no-pager"},
			Request{Verb: "status"},
		},
		"run-schedule": {
			[]string{"run-schedule", "databasify-postgres-backup"},
			Request{Verb: "run-schedule", Units: []string{"databasify-postgres-backup"}},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			command, err := Parse(c.args)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(*command.Request, c.want) {
				t.Fatalf("got %+v, want %+v", *command.Request, c.want)
			}
		})
	}
}

func TestParseSystemdRunStyleCommand(t *testing.T) {
	command, err := Parse([]string{
		"run", "--quiet", "--collect", "--unit=databasify-postgres-migration", "--uid=postgres",
		"--setenv=PGPORT=5432", "-E", "MODE=copy", "--property=WorkingDirectory=/srv/postgres/migration",
		"sh", "-c", "sh copy.sh --flag",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := Request{Verb: "run", Quiet: true, Transient: &Transient{
		Unit:    "databasify-postgres-migration",
		User:    "postgres",
		Env:     map[string]string{"PGPORT": "5432", "MODE": "copy"},
		Workdir: "/srv/postgres/migration",
		Collect: true,
		Argv:    []string{"sh", "-c", "sh copy.sh --flag"},
	}}
	if !reflect.DeepEqual(*command.Request, want) {
		t.Fatalf("got %+v / %+v", *command.Request, *command.Request.Transient)
	}

	command, err = Parse([]string{"run", "-r", "--wait", "--unit", "valkey-migration.service", "--", "/usr/local/bin/redis-shake", "-x"})
	if err != nil {
		t.Fatal(err)
	}
	transient := command.Request.Transient
	if !transient.RemainAfterExit || !transient.Wait || transient.Unit != "valkey-migration" || !reflect.DeepEqual(transient.Argv, []string{"/usr/local/bin/redis-shake", "-x"}) {
		t.Fatalf("got %+v", transient)
	}
}

func TestParseJournalctlStyleCommand(t *testing.T) {
	command, err := Parse([]string{"logs", "--unit", "valkey-migration.service", "--lines", "3", "--output", "cat", "--no-pager"})
	if err != nil {
		t.Fatal(err)
	}
	if *command.Logs != (LogQuery{Unit: "valkey-migration", Lines: 3, Output: "cat"}) {
		t.Fatalf("got %+v", *command.Logs)
	}
	command, err = Parse([]string{"logs", "-u", "mysql", "-n", "20"})
	if err != nil || *command.Logs != (LogQuery{Unit: "mysql", Lines: 20, Output: "short-iso"}) {
		t.Fatalf("got %+v, %v", command.Logs, err)
	}
	command, err = Parse([]string{"logs", "redis.service", "-n", "50", "-f"})
	if err != nil || *command.Logs != (LogQuery{Unit: "redis", Lines: 50, Output: "short-iso", Follow: true}) {
		t.Fatalf("got %+v, %v", command.Logs, err)
	}
	command, err = Parse([]string{"logs", "--follow", "redis"})
	if err != nil || *command.Logs != (LogQuery{Unit: "redis", Lines: 10, Output: "short-iso", Follow: true}) {
		t.Fatalf("got %+v, %v", command.Logs, err)
	}
}

func TestParseRejectsBadUsage(t *testing.T) {
	cases := map[string][]string{
		"no verb":            {},
		"unknown verb":       {"frobnicate", "x"},
		"unknown flag":       {"start", "--force", "x"},
		"missing unit":       {"start"},
		"reload with unit":   {"daemon-reload", "x"},
		"property no value":  {"show", "x", "-p"},
		"run without unit":   {"run", "true"},
		"run without argv":   {"run", "--unit=x"},
		"run bad property":   {"run", "--unit=x", "-p", "MemoryMax=1G", "true"},
		"logs without unit":  {"logs", "-n", "3"},
		"logs two units":     {"logs", "redis", "valkey"},
		"logs bad lines":     {"logs", "-u", "x", "-n", "many"},
		"logs bad output":    {"logs", "-u", "x", "-o", "json"},
		"two schedule names": {"run-schedule", "a", "b"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			var usageErr UsageError
			if _, err := Parse(args); !errors.As(err, &usageErr) {
				t.Fatalf("expected a usage error, got %v", err)
			}
		})
	}
}

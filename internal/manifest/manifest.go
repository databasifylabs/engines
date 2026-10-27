package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"time"
)

type RestartPolicy string

const (
	RestartAlways    RestartPolicy = "always"
	RestartOnFailure RestartPolicy = "on-failure"
	RestartNever     RestartPolicy = "never"
)

type Kind string

const (
	KindSimple     Kind = "simple"
	KindOneshot    Kind = "oneshot"
	KindNotifyLess Kind = "notify-less"
)

const (
	DefaultRestartSec  = time.Second
	DefaultStopTimeout = 90 * time.Second
)

type Process struct {
	Name         string            `json:"name"`
	Argv         []string          `json:"argv"`
	User         string            `json:"user,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	Workdir      string            `json:"workdir,omitempty"`
	After        []string          `json:"after,omitempty"`
	Restart      RestartPolicy     `json:"restart,omitempty"`
	RestartSec   Duration          `json:"restartSec,omitempty"`
	StopSignal   Signal            `json:"stopSignal,omitempty"`
	StopTimeout  Duration          `json:"stopTimeout,omitempty"`
	ReloadSignal Signal            `json:"reloadSignal,omitempty"`
	Enabled      bool              `json:"enabled"`
	Type         Kind              `json:"type,omitempty"`
}

type Schedule struct {
	Name       string            `json:"name"`
	Every      Duration          `json:"every,omitempty"`
	OnCalendar string            `json:"onCalendar,omitempty"`
	Argv       []string          `json:"argv"`
	User       string            `json:"user,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Timeout    Duration          `json:"timeout,omitempty"`
}

type Manifest struct {
	Processes []Process  `json:"processes"`
	Schedules []Schedule `json:"schedules"`
}

var validName = regexp.MustCompile(`^[A-Za-z0-9@._-]+$`)

func Load(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, nil
	}
	if err != nil {
		return Manifest{}, err
	}

	return Parse(data)
}

func Parse(data []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest: %w", err)
	}
	m.applyDefaults()

	return m, m.Validate()
}

func (m *Manifest) applyDefaults() {
	for i := range m.Processes {
		m.Processes[i].applyDefaults()
	}
}

func (p *Process) applyDefaults() {
	if p.Type == "" {
		p.Type = KindSimple
	}
	if p.Restart == "" {
		p.Restart = RestartNever
	}
	if p.RestartSec == 0 {
		p.RestartSec = Duration(DefaultRestartSec)
	}
	if p.StopSignal == "" {
		p.StopSignal = "SIGTERM"
	}
	if p.StopTimeout == 0 {
		p.StopTimeout = Duration(DefaultStopTimeout)
	}
	if p.ReloadSignal == "" {
		p.ReloadSignal = "SIGHUP"
	}
}

func (m Manifest) Validate() error {
	seen := map[string]bool{}
	for _, p := range m.Processes {
		if err := p.validate(); err != nil {
			return err
		}
		if err := claim(seen, p.Name); err != nil {
			return err
		}
	}
	for _, s := range m.Schedules {
		if err := s.validate(); err != nil {
			return err
		}
		if err := claim(seen, s.Name); err != nil {
			return err
		}
	}
	_, err := m.StartOrder()

	return err
}

func claim(seen map[string]bool, name string) error {
	if seen[name] {
		return fmt.Errorf("duplicate unit %q", name)
	}
	seen[name] = true

	return nil
}

func (p Process) validate() error {
	if err := validateName(p.Name, p.Argv); err != nil {
		return err
	}
	switch p.Type {
	case KindSimple, KindOneshot, KindNotifyLess:
	default:
		return fmt.Errorf("process %q: unknown type %q", p.Name, p.Type)
	}
	switch p.Restart {
	case RestartAlways, RestartOnFailure, RestartNever:
	default:
		return fmt.Errorf("process %q: unknown restart policy %q", p.Name, p.Restart)
	}
	for _, signal := range []Signal{p.StopSignal, p.ReloadSignal} {
		if _, err := signal.Value(); err != nil {
			return fmt.Errorf("process %q: %w", p.Name, err)
		}
	}

	return nil
}

func (s Schedule) validate() error {
	if err := validateName(s.Name, s.Argv); err != nil {
		return err
	}
	if (s.Every > 0) == (s.OnCalendar != "") {
		return fmt.Errorf("schedule %q: set exactly one of every or onCalendar", s.Name)
	}
	if s.OnCalendar == "" {
		return nil
	}
	if _, err := time.Parse("15:04", s.OnCalendar); err != nil {
		return fmt.Errorf("schedule %q: onCalendar must be HH:MM: %w", s.Name, err)
	}

	return nil
}

func validateName(name string, argv []string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("invalid unit name %q", name)
	}
	if len(argv) == 0 {
		return fmt.Errorf("unit %q: argv is empty", name)
	}

	return nil
}

func (p Process) Oneshot() bool {
	return p.Type == KindOneshot
}

func (s Schedule) Next(after time.Time) time.Time {
	if s.Every > 0 {
		return after.Add(time.Duration(s.Every))
	}
	clock, _ := time.Parse("15:04", s.OnCalendar)
	day := after.UTC()
	next := time.Date(day.Year(), day.Month(), day.Day(), clock.Hour(), clock.Minute(), 0, 0, time.UTC)
	if !next.After(after) {
		next = next.AddDate(0, 0, 1)
	}

	return next
}

func (m Manifest) StartOrder() ([]Process, error) {
	byName := make(map[string]Process, len(m.Processes))
	for _, p := range m.Processes {
		byName[p.Name] = p
	}
	ordered := make([]Process, 0, len(m.Processes))
	state := map[string]int{}
	var visit func(p Process) error
	visit = func(p Process) error {
		switch state[p.Name] {
		case 1:
			return fmt.Errorf("ordering cycle through %q", p.Name)
		case 2:
			return nil
		}
		state[p.Name] = 1
		for _, dependency := range p.After {
			if before, ok := byName[dependency]; ok {
				if err := visit(before); err != nil {
					return err
				}
			}
		}
		state[p.Name] = 2
		ordered = append(ordered, p)

		return nil
	}
	for _, p := range m.Processes {
		if err := visit(p); err != nil {
			return nil, err
		}
	}

	return ordered, nil
}

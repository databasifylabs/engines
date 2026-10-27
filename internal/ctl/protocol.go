package ctl

import (
	"errors"
	"strconv"
	"strings"
	"syscall"
)

const (
	ExitUsage       = 64
	ExitUnavailable = 69
	ExitInternal    = 70
)

var (
	ErrNotFound  = errors.New("unit not found")
	ErrFailed    = errors.New("unit failed")
	ErrNotActive = errors.New("unit not active")
	ErrLoaded    = errors.New("unit already loaded")
)

type Request struct {
	Verb       string     `json:"verb"`
	Units      []string   `json:"units,omitempty"`
	Quiet      bool       `json:"quiet,omitempty"`
	NoBlock    bool       `json:"noBlock,omitempty"`
	Now        bool       `json:"now,omitempty"`
	Value      bool       `json:"value,omitempty"`
	Properties []string   `json:"properties,omitempty"`
	Signal     string     `json:"signal,omitempty"`
	Transient  *Transient `json:"transient,omitempty"`
}

type Transient struct {
	Unit            string            `json:"unit"`
	Argv            []string          `json:"argv"`
	Env             map[string]string `json:"env,omitempty"`
	Workdir         string            `json:"workdir,omitempty"`
	User            string            `json:"user,omitempty"`
	RemainAfterExit bool              `json:"remainAfterExit,omitempty"`
	Collect         bool              `json:"collect,omitempty"`
	Wait            bool              `json:"wait,omitempty"`
}

type Response struct {
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
	Exit   int    `json:"exit"`
}

type Status struct {
	Name           string `json:"name"`
	Loaded         bool   `json:"loaded"`
	Enabled        bool   `json:"enabled"`
	ActiveState    string `json:"activeState"`
	SubState       string `json:"subState"`
	MainPID        int    `json:"mainPid"`
	NRestarts      int    `json:"nRestarts"`
	ExecMainStatus int    `json:"execMainStatus"`
	Result         string `json:"result"`
}

func Missing(name string) Status {
	return Status{Name: name, ActiveState: "inactive", SubState: "dead", Result: "success"}
}

var propertyNames = []string{"Id", "LoadState", "ActiveState", "SubState", "MainPID", "NRestarts", "ExecMainStatus", "Result", "UnitFileState"}

func (s Status) Property(name string) string {
	switch name {
	case "Id":
		return s.Name + ".service"
	case "LoadState":
		return map[bool]string{true: "loaded", false: "not-found"}[s.Loaded]
	case "ActiveState":
		return s.ActiveState
	case "SubState":
		return s.SubState
	case "MainPID":
		return strconv.Itoa(s.MainPID)
	case "NRestarts":
		return strconv.Itoa(s.NRestarts)
	case "ExecMainStatus":
		return strconv.Itoa(s.ExecMainStatus)
	case "Result":
		return s.Result
	case "UnitFileState":
		return map[bool]string{true: "enabled", false: "disabled"}[s.Enabled]
	}

	return ""
}

type Controller interface {
	Start(name string, block bool) error
	Stop(name string, block bool) error
	Restart(name string) error
	Reload(name string) error
	SetEnabled(name string, enabled bool) error
	ResetFailed(pattern string)
	Kill(name string, signal syscall.Signal) error
	ReloadManifest() error
	RunTransient(t Transient) (Status, error)
	Status(name string) Status
}

func UnitName(name string) string {
	for _, suffix := range []string{".service", ".timer"} {
		name = strings.TrimSuffix(name, suffix)
	}

	return name
}

package ctl

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/databasifylabs/engines/internal/manifest"
)

type Server struct {
	controller Controller
	logFile    func(unit string) string
}

func NewServer(controller Controller, logFile func(unit string) string) *Server {
	return &Server{controller: controller, logFile: logFile}
}

func Listen(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	_ = os.Remove(path)
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}

	return listener, os.Chmod(path, 0o600)
}

func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}

			return err
		}
		go s.answer(conn)
	}
}

func (s *Server) answer(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	var request Request
	if err := json.NewDecoder(conn).Decode(&request); err != nil {
		return
	}
	_ = json.NewEncoder(conn).Encode(s.Handle(request))
}

func (s *Server) Handle(request Request) Response {
	r := &reply{}
	units := request.Units
	switch request.Verb {
	case "start":
		s.each(r, "start", units, func(name string) error { return s.controller.Start(name, !request.NoBlock) })
	case "stop":
		s.each(r, "stop", units, func(name string) error { return s.controller.Stop(name, !request.NoBlock) })
	case "restart":
		s.each(r, "restart", units, s.controller.Restart)
	case "reload":
		s.each(r, "reload", units, s.controller.Reload)
	case "enable":
		s.each(r, "enable", units, s.toggle(true, request))
	case "disable":
		s.each(r, "disable", units, s.toggle(false, request))
	case "kill":
		s.kill(r, request)
	case "mask", "unmask":
	case "daemon-reload":
		if err := s.controller.ReloadManifest(); err != nil {
			r.fail(1, "Failed to reload daemon: %v\n", err)
		}
	case "reset-failed":
		s.resetFailed(units)
	case "is-active":
		s.isActive(r, request)
	case "is-failed":
		s.isFailed(r, request)
	case "show":
		s.show(r, request)
	case "status":
		s.status(r, units)
	case "run":
		s.run(r, request)
	case "run-schedule":
		s.runSchedule(r, units[0])
	default:
		r.fail(ExitUsage, "unknown verb %q\n", request.Verb)
	}

	return r.response()
}

func (s *Server) each(r *reply, verb string, units []string, action func(string) error) {
	for _, name := range units {
		if err := action(name); err != nil {
			r.failWith(verb, name, err)
		}
	}
}

func (s *Server) toggle(enabled bool, request Request) func(string) error {
	return func(name string) error {
		if err := s.controller.SetEnabled(name, enabled); err != nil {
			return err
		}
		if !request.Now {
			return nil
		}
		if enabled {
			return s.controller.Start(name, !request.NoBlock)
		}

		return s.controller.Stop(name, !request.NoBlock)
	}
}

func (s *Server) kill(r *reply, request Request) {
	signal, err := manifest.ParseSignal(cmp.Or(request.Signal, "TERM"))
	if err != nil {
		r.fail(ExitUsage, "%v\n", err)

		return
	}
	s.each(r, "kill", request.Units, func(name string) error { return s.controller.Kill(name, signal) })
}

func (s *Server) resetFailed(units []string) {
	if len(units) == 0 {
		s.controller.ResetFailed("")
	}
	for _, pattern := range units {
		s.controller.ResetFailed(pattern)
	}
}

func (s *Server) isActive(r *reply, request Request) {
	anyActive := false
	for _, name := range request.Units {
		state := s.controller.Status(name).ActiveState
		r.say(request.Quiet, "%s\n", state)
		anyActive = anyActive || state == "active"
	}
	if !anyActive {
		r.exit = 3
	}
}

func (s *Server) isFailed(r *reply, request Request) {
	anyFailed := false
	for _, name := range request.Units {
		state := s.controller.Status(name).ActiveState
		r.say(request.Quiet, "%s\n", state)
		anyFailed = anyFailed || state == "failed"
	}
	if !anyFailed {
		r.exit = 1
	}
}

func (s *Server) show(r *reply, request Request) {
	properties := request.Properties
	if len(properties) == 0 {
		properties = propertyNames
	}
	for i, name := range request.Units {
		if i > 0 && !request.Value {
			r.say(false, "\n")
		}
		status := s.controller.Status(name)
		for _, property := range properties {
			if request.Value {
				r.say(false, "%s\n", status.Property(property))

				continue
			}
			r.say(false, "%s=%s\n", property, status.Property(property))
		}
	}
}

func (s *Server) status(r *reply, units []string) {
	if len(units) == 0 {
		s.listProcesses(r)

		return
	}
	for _, name := range units {
		status := s.controller.Status(name)
		if !status.Loaded {
			r.fail(4, "Unit %s.service could not be found.\n", name)

			continue
		}
		r.say(false, "%s", describe(status))
		lines, _ := Tail(s.logFile(name), 10)
		for _, line := range lines {
			r.say(false, "%s\n", formatLine(name, line, "short-iso"))
		}
		if status.ActiveState != "active" && r.exit == 0 {
			r.exit = 3
		}
	}
}

func (s *Server) listProcesses(r *reply) {
	for _, name := range s.controller.Processes() {
		r.say(false, "%s", summarize(s.controller.Status(name)))
	}
}

func summarize(status Status) string {
	return fmt.Sprintf("● %s.service %s, %s (%s), pid %d, %d restarts, result %s\n",
		status.Name, status.Property("UnitFileState"), status.ActiveState, status.SubState, status.MainPID, status.NRestarts, status.Result)
}

func describe(status Status) string {
	return fmt.Sprintf("● %s.service\n     Loaded: loaded (%s)\n     Active: %s (%s)\n   Main PID: %d\n   Restarts: %d\n     Result: %s (status=%d)\n\n",
		status.Name, status.Property("UnitFileState"), status.ActiveState, status.SubState, status.MainPID, status.NRestarts, status.Result, status.ExecMainStatus)
}

func (s *Server) run(r *reply, request Request) {
	transient := *request.Transient
	status, err := s.controller.RunTransient(transient)
	if errors.Is(err, ErrLoaded) {
		r.fail(1, "Failed to start transient service unit: Unit %s.service was already loaded or has a fragment file.\n", transient.Unit)

		return
	}
	if err != nil {
		r.failWith("run", transient.Unit, err)

		return
	}
	if !request.Quiet {
		r.err.WriteString("Running as unit: " + transient.Unit + ".service\n")
	}
	if transient.Wait {
		r.exit = exitCodeOf(status)
	}
}

func (s *Server) runSchedule(r *reply, name string) {
	err := s.controller.Start(name, true)
	if err == nil {
		return
	}
	if errors.Is(err, ErrNotFound) {
		r.failWith("run", name, err)

		return
	}
	status := s.controller.Status(name)
	r.fail(max(exitCodeOf(status), 1), "Schedule %s finished with %s.\n", name, status.Result)
}

func exitCodeOf(status Status) int {
	switch status.Result {
	case "success":
		return 0
	case "exit-code":
		return status.ExecMainStatus
	}

	return 1
}

type reply struct {
	out  strings.Builder
	err  strings.Builder
	exit int
}

func (r *reply) say(quiet bool, format string, args ...any) {
	if !quiet {
		fmt.Fprintf(&r.out, format, args...)
	}
}

func (r *reply) fail(code int, format string, args ...any) {
	fmt.Fprintf(&r.err, format, args...)
	if r.exit == 0 {
		r.exit = code
	}
}

func (r *reply) failWith(verb, name string, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		r.fail(5, "Failed to %s %s.service: Unit %s.service not found.\n", verb, name, name)
	case errors.Is(err, ErrFailed):
		r.fail(1, "Job for %s.service failed.\n", name)
	case errors.Is(err, ErrNotActive):
		r.fail(1, "Failed to %s %s.service: Unit %s.service is not active.\n", verb, name, name)
	default:
		r.fail(1, "Failed to %s %s.service: %v\n", verb, name, err)
	}
}

func (r *reply) response() Response {
	return Response{Stdout: r.out.String(), Stderr: r.err.String(), Exit: r.exit}
}

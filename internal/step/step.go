package step

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var validName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

const gatedExec = `read -r -u 3 _ || exit 70; exec 3<&-; exec bash "$0"`

type Step struct {
	Name   string
	Task   string
	Dir    string
	Stdout io.Writer
	Stderr io.Writer
}

func (s Step) Run(script io.Reader) (int, error) {
	path, err := s.writeScript(script)
	if err != nil {
		return 0, err
	}
	defer s.cleanUp(path)
	cmd, opener, err := s.startGated(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = opener.Close() }()
	if err := s.recordPid(path, cmd.Process.Pid); err != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()

		return 0, err
	}
	defer forwardSignals(cmd.Process.Pid)()
	_, _ = opener.WriteString("go\n")

	return exitCodeOf(cmd.Wait())
}

func (s Step) startGated(path string) (*exec.Cmd, *os.File, error) {
	gate, opener, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	cmd := exec.Command("bash", "-c", gatedExec, path)
	cmd.Stdout, cmd.Stderr = s.Stdout, s.Stderr
	cmd.ExtraFiles = []*os.File{gate}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = cmd.Start()
	_ = gate.Close()
	if err != nil {
		_ = opener.Close()

		return nil, nil, err
	}

	return cmd, opener, nil
}

func (s Step) writeScript(script io.Reader) (string, error) {
	if !validName.MatchString(s.Name) || (s.Task != "" && !validName.MatchString(s.Task)) {
		return "", fmt.Errorf("invalid step %q or task %q", s.Name, s.Task)
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return "", err
	}
	file, err := s.createScript()
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	if _, err := io.Copy(file, script); err != nil {
		_ = os.Remove(file.Name())

		return "", err
	}

	return file.Name(), nil
}

func (s Step) createScript() (*os.File, error) {
	if s.Task == "" {
		return os.CreateTemp(s.Dir, s.Name+"-*.sh")
	}

	return os.OpenFile(filepath.Join(s.Dir, s.Task+".sh"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
}

func (s Step) recordPid(script string, pid int) error {
	return os.WriteFile(pidFileOf(script), []byte(fmt.Sprintf("%d %s\n", pid, s.Task)), 0o600)
}

func (s Step) cleanUp(script string) {
	_ = os.Remove(script)
	_ = os.Remove(pidFileOf(script))
}

func pidFileOf(script string) string {
	return strings.TrimSuffix(script, ".sh") + ".pid"
}

func forwardSignals(pid int) func() {
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case received := <-signals:
				_ = syscall.Kill(-pid, received.(syscall.Signal))
			case <-done:
				return
			}
		}
	}()

	return func() {
		signal.Stop(signals)
		close(done)
	}
}

func exitCodeOf(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return 0, err
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if ok && status.Signaled() {
		return 128 + int(status.Signal()), nil
	}

	return exitErr.ExitCode(), nil
}

func Kill(dir, task string, grace time.Duration) (bool, error) {
	if !validName.MatchString(task) {
		return false, fmt.Errorf("invalid task id %q", task)
	}
	data, err := os.ReadFile(filepath.Join(dir, task+".pid"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return false, nil
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil || pid <= 1 {
		return false, fmt.Errorf("invalid pid file for task %s", task)
	}

	return terminateGroup(pid, grace), nil
}

func terminateGroup(pgid int, grace time.Duration) bool {
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
		return false
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if syscall.Kill(-pgid, 0) != nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)

	return true
}

package manifest

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Duration time.Duration

func (d *Duration) UnmarshalJSON(data []byte) error {
	var seconds float64
	if err := json.Unmarshal(data, &seconds); err == nil {
		*d = Duration(seconds * float64(time.Second))

		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("duration must be seconds or a string like \"5s\": %s", data)
	}
	parsed, err := parseDuration(text)
	if err != nil {
		return err
	}
	*d = Duration(parsed)

	return nil
}

func (d Duration) Std() time.Duration {
	return time.Duration(d)
}

func parseDuration(text string) (time.Duration, error) {
	if seconds, err := strconv.ParseFloat(text, 64); err == nil {
		return time.Duration(seconds * float64(time.Second)), nil
	}

	return time.ParseDuration(text)
}

type Signal string

var signals = map[string]syscall.Signal{
	"HUP":   syscall.SIGHUP,
	"INT":   syscall.SIGINT,
	"QUIT":  syscall.SIGQUIT,
	"KILL":  syscall.SIGKILL,
	"USR1":  syscall.SIGUSR1,
	"USR2":  syscall.SIGUSR2,
	"TERM":  syscall.SIGTERM,
	"CONT":  syscall.SIGCONT,
	"STOP":  syscall.SIGSTOP,
	"WINCH": syscall.SIGWINCH,
}

func SignalName(signal syscall.Signal) string {
	for name, known := range signals {
		if known == signal {
			return "SIG" + name
		}
	}

	return strconv.Itoa(int(signal))
}

func (s Signal) Value() (syscall.Signal, error) {
	return ParseSignal(string(s))
}

func ParseSignal(text string) (syscall.Signal, error) {
	if number, err := strconv.Atoi(text); err == nil && number > 0 && number < 65 {
		return syscall.Signal(number), nil
	}
	if signal, ok := signals[strings.TrimPrefix(strings.ToUpper(text), "SIG")]; ok {
		return signal, nil
	}

	return 0, fmt.Errorf("unknown signal %q", text)
}

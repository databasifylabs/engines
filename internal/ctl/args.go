package ctl

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Command struct {
	Request *Request
	Logs    *LogQuery
}

type LogQuery struct {
	Unit   string
	Lines  int
	Output string
}

type UsageError struct {
	message string
}

func (e UsageError) Error() string {
	return e.message
}

func usage(format string, args ...any) error {
	return UsageError{message: fmt.Sprintf(format, args...)}
}

var unitCounts = map[string][2]int{
	"start":         {1, -1},
	"stop":          {1, -1},
	"restart":       {1, -1},
	"reload":        {1, -1},
	"enable":        {1, -1},
	"disable":       {1, -1},
	"is-active":     {1, -1},
	"is-failed":     {1, -1},
	"show":          {1, -1},
	"status":        {1, -1},
	"kill":          {1, -1},
	"mask":          {1, -1},
	"unmask":        {1, -1},
	"reset-failed":  {0, -1},
	"daemon-reload": {0, 0},
	"run-schedule":  {1, 1},
}

func Parse(args []string) (Command, error) {
	if len(args) == 0 {
		return Command{}, usage("usage: databasify ctl <verb> [options] [unit...]")
	}
	verb, rest := args[0], &arguments{list: args[1:]}
	switch verb {
	case "run":
		request, err := parseRun(rest)

		return Command{Request: request}, err
	case "logs":
		query, err := parseLogs(rest)

		return Command{Logs: query}, err
	}
	if _, ok := unitCounts[verb]; !ok {
		return Command{}, usage("unknown verb %q", verb)
	}
	request, err := parseSystemctl(verb, rest)

	return Command{Request: request}, err
}

func parseSystemctl(verb string, args *arguments) (*Request, error) {
	request := &Request{Verb: verb}
	for args.more() {
		arg := args.next()
		if !isFlag(arg) {
			request.Units = append(request.Units, UnitName(arg))

			continue
		}
		if err := applySystemctlFlag(request, arg, args); err != nil {
			return nil, err
		}
	}

	return request, checkUnitCount(verb, len(request.Units))
}

func applySystemctlFlag(request *Request, arg string, args *arguments) error {
	name, inline, hasInline := splitFlag(arg)
	switch name {
	case "-q", "--quiet":
		request.Quiet = true
	case "--no-block":
		request.NoBlock = true
	case "--now":
		request.Now = true
	case "--value":
		request.Value = true
	case "--no-pager", "--no-ask-password", "--full", "-l":
	case "-p", "--property":
		value, err := args.value(name, inline, hasInline)
		if err != nil {
			return err
		}
		request.Properties = append(request.Properties, strings.Split(value, ",")...)
	case "-s", "--signal":
		value, err := args.value(name, inline, hasInline)
		if err != nil {
			return err
		}
		request.Signal = value
	default:
		return usage("unknown option %s", arg)
	}

	return nil
}

func checkUnitCount(verb string, count int) error {
	bounds := unitCounts[verb]
	if count < bounds[0] || (bounds[1] >= 0 && count > bounds[1]) {
		return usage("%s: wrong number of units (%d)", verb, count)
	}

	return nil
}

func parseRun(args *arguments) (*Request, error) {
	request := &Request{Verb: "run", Transient: &Transient{}}
	for args.more() && isFlag(args.peek()) {
		arg := args.next()
		if arg == "--" {
			break
		}
		if err := applyRunFlag(request, arg, args); err != nil {
			return nil, err
		}
	}
	request.Transient.Argv = args.rest()
	if request.Transient.Unit == "" {
		return nil, usage("run: --unit is required")
	}
	if len(request.Transient.Argv) == 0 {
		return nil, usage("run: command is required")
	}

	return request, nil
}

func applyRunFlag(request *Request, arg string, args *arguments) error {
	transient := request.Transient
	name, inline, hasInline := splitFlag(arg)
	switch name {
	case "-q", "--quiet":
		request.Quiet = true

		return nil
	case "-G", "--collect":
		transient.Collect = true

		return nil
	case "-r", "--remain-after-exit":
		transient.RemainAfterExit = true

		return nil
	case "-W", "--wait":
		transient.Wait = true

		return nil
	case "-u", "--unit", "--uid", "-E", "--setenv", "-p", "--property":
	default:
		return usage("run: unknown option %s", arg)
	}
	value, err := args.value(name, inline, hasInline)
	if err != nil {
		return err
	}

	return applyRunValue(transient, name, value)
}

func applyRunValue(transient *Transient, name, value string) error {
	switch name {
	case "-u", "--unit":
		transient.Unit = UnitName(value)
	case "--uid":
		transient.User = value
	case "-E", "--setenv":
		key, assigned, hasValue := strings.Cut(value, "=")
		if !hasValue {
			assigned = os.Getenv(key)
		}
		if transient.Env == nil {
			transient.Env = map[string]string{}
		}
		transient.Env[key] = assigned
	case "-p", "--property":
		key, assigned, _ := strings.Cut(value, "=")
		if key != "WorkingDirectory" {
			return usage("run: unsupported property %s", key)
		}
		transient.Workdir = assigned
	}

	return nil
}

func parseLogs(args *arguments) (*LogQuery, error) {
	query := &LogQuery{Output: "short-iso"}
	for args.more() {
		arg := args.next()
		if !isFlag(arg) {
			return nil, usage("logs: unexpected argument %s", arg)
		}
		if err := applyLogsFlag(query, arg, args); err != nil {
			return nil, err
		}
	}
	if query.Unit == "" {
		return nil, usage("logs: --unit is required")
	}

	return query, nil
}

func applyLogsFlag(query *LogQuery, arg string, args *arguments) error {
	name, inline, hasInline := splitFlag(arg)
	switch name {
	case "--no-pager", "-q", "--quiet":
		return nil
	case "-u", "--unit", "-n", "--lines", "-o", "--output":
	default:
		return usage("logs: unknown option %s", arg)
	}
	value, err := args.value(name, inline, hasInline)
	if err != nil {
		return err
	}
	switch name {
	case "-u", "--unit":
		query.Unit = UnitName(value)
	case "-n", "--lines":
		lines, err := strconv.Atoi(value)
		if err != nil || lines < 0 {
			return usage("logs: invalid line count %q", value)
		}
		query.Lines = lines
	case "-o", "--output":
		if value != "cat" && value != "short" && value != "short-iso" {
			return usage("logs: unsupported output %q", value)
		}
		query.Output = value
	}

	return nil
}

type arguments struct {
	list []string
}

func (a *arguments) more() bool {
	return len(a.list) > 0
}

func (a *arguments) peek() string {
	return a.list[0]
}

func (a *arguments) next() string {
	head := a.list[0]
	a.list = a.list[1:]

	return head
}

func (a *arguments) rest() []string {
	rest := a.list
	a.list = nil

	return rest
}

func (a *arguments) value(flag, inline string, hasInline bool) (string, error) {
	if hasInline {
		return inline, nil
	}
	if !a.more() {
		return "", usage("option %s needs a value", flag)
	}

	return a.next(), nil
}

func isFlag(arg string) bool {
	return len(arg) > 1 && arg[0] == '-'
}

func splitFlag(arg string) (name, value string, hasValue bool) {
	if strings.HasPrefix(arg, "--") {
		return strings.Cut(arg, "=")
	}
	if len(arg) > 2 {
		return arg[:2], arg[2:], true
	}

	return arg, "", false
}

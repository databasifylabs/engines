package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/databasifylabs/engines/internal/ctl"
	"github.com/databasifylabs/engines/internal/layout"
	"github.com/databasifylabs/engines/internal/step"
	"github.com/databasifylabs/engines/internal/supervise"
)

var version = "dev"

const usageText = `usage: databasify <command>

commands:
  serve                       supervise the processes in $DATABASIFY_HOME/conf/processes.json
  ctl <verb> [options] [unit] systemctl, systemd-run and journalctl style control
  step <name> [--task <id>]   run the bash script read from stdin
  step-kill <id>              stop a running step
  info                        print image facts as key=value lines
  version                     print the wrapper version
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		return usage(os.Stderr)
	}
	paths := layout.FromEnv()
	switch args[0] {
	case "serve":
		return serve(paths)
	case "ctl":
		return ctl.CLI{Socket: paths.Socket(), LogFile: paths.LogFile, Stdout: os.Stdout, Stderr: os.Stderr}.Run(args[1:])
	case "step":
		return runStep(paths, args[1:])
	case "step-kill":
		return killStep(paths, args[1:])
	case "info":
		return info(os.Stdout)
	case "version":
		fmt.Println(version)

		return 0
	}

	return usage(os.Stderr)
}

func usage(out io.Writer) int {
	_, _ = io.WriteString(out, usageText)

	return ctl.ExitUsage
}

func serve(paths layout.Layout) int {
	if err := supervise.BecomeSubreaper(); err != nil {
		fmt.Fprintf(os.Stderr, "databasify: cannot become subreaper: %v\n", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	supervisor := supervise.New(supervise.Options{Layout: paths, Console: os.Stdout})
	listener, err := ctl.Listen(paths.Socket())
	if err != nil {
		fmt.Fprintf(os.Stderr, "databasify: cannot listen on %s: %v\n", paths.Socket(), err)

		return ctl.ExitInternal
	}
	supervisor.Boot()
	go reloadOnHangup(ctx, supervisor)
	go func() {
		if err := ctl.NewServer(supervisor, paths.LogFile).Serve(ctx, listener); err != nil {
			fmt.Fprintf(os.Stderr, "databasify: control socket stopped: %v\n", err)
		}
	}()
	<-ctx.Done()
	supervisor.Shutdown()

	return 0
}

func reloadOnHangup(ctx context.Context, supervisor *supervise.Supervisor) {
	hangups := make(chan os.Signal, 1)
	signal.Notify(hangups, syscall.SIGHUP)
	defer signal.Stop(hangups)
	for {
		select {
		case <-ctx.Done():
			return
		case <-hangups:
			if err := supervisor.ReloadManifest(); err != nil {
				fmt.Fprintf(os.Stderr, "databasify: reload failed: %v\n", err)
			}
		}
	}
}

func runStep(paths layout.Layout, args []string) int {
	name, task, err := stepArguments(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "databasify step: %v\n", err)

		return ctl.ExitUsage
	}
	code, err := step.Step{Name: name, Task: task, Dir: paths.Tasks(), Stdout: os.Stdout, Stderr: os.Stderr}.Run(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "databasify step: %v\n", err)

		return ctl.ExitInternal
	}

	return code
}

func stepArguments(args []string) (name, task string, err error) {
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--task" && i+1 < len(args):
			i++
			task = args[i]
		case strings.HasPrefix(arg, "--task="):
			task = strings.TrimPrefix(arg, "--task=")
		case !strings.HasPrefix(arg, "-") && name == "":
			name = arg
		default:
			return "", "", fmt.Errorf("unexpected argument %q", arg)
		}
	}
	if name == "" {
		return "", "", fmt.Errorf("usage: databasify step <name> [--task <id>]")
	}

	return name, task, nil
}

func killStep(paths layout.Layout, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: databasify step-kill <task>")

		return ctl.ExitUsage
	}
	stopped, err := step.Kill(paths.Tasks(), args[0], 10*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "databasify step-kill: %v\n", err)

		return ctl.ExitInternal
	}
	if stopped {
		fmt.Println("stopped the script running in this container")

		return 0
	}
	fmt.Println("nothing was running in this container")

	return 0
}

func info(out io.Writer) int {
	facts := [][2]string{
		{"engine", os.Getenv("DATABASIFY_ENGINE")},
		{"version", os.Getenv("DATABASIFY_RELEASE")},
		{"image", os.Getenv("DATABASIFY_IMAGE")},
		{"wrapper", version},
	}
	for _, fact := range facts {
		if fact[1] != "" {
			fmt.Fprintf(out, "%s=%s\n", fact[0], fact[1])
		}
	}

	return 0
}

package ctl

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
)

var ErrUnavailable = errors.New("supervisor is not running")

func Send(socket string, request Request) (Response, error) {
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return Response{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer func() { _ = conn.Close() }()
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return Response{}, err
	}
	var response Response
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return Response{}, fmt.Errorf("read response: %w", err)
	}

	return response, nil
}

type CLI struct {
	Socket  string
	LogFile func(unit string) string
	Stdout  io.Writer
	Stderr  io.Writer
}

func (c CLI) Run(args []string) int {
	command, err := Parse(args)
	if err != nil {
		fmt.Fprintf(c.Stderr, "databasify ctl: %v\n", err)

		return ExitUsage
	}
	if command.Logs != nil {
		return c.showLogs(*command.Logs)
	}
	response, err := Send(c.Socket, *command.Request)
	if errors.Is(err, ErrUnavailable) {
		fmt.Fprintf(c.Stderr, "databasify ctl: %v\n", err)

		return ExitUnavailable
	}
	if err != nil {
		fmt.Fprintf(c.Stderr, "databasify ctl: %v\n", err)

		return ExitInternal
	}
	_, _ = io.WriteString(c.Stdout, response.Stdout)
	_, _ = io.WriteString(c.Stderr, response.Stderr)

	return response.Exit
}

func (c CLI) showLogs(query LogQuery) int {
	if err := ShowLogs(c.LogFile(query.Unit), query, c.Stdout); err != nil {
		fmt.Fprintf(c.Stderr, "databasify ctl: %v\n", err)

		return ExitInternal
	}

	return 0
}

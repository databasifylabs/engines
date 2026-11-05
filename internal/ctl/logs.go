package ctl

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/databasifylabs/engines/internal/layout"
)

func Tail(path string, count int) ([]string, error) {
	var lines []string
	for _, file := range logGenerations(path) {
		older, err := tailOf(file, count-len(lines))
		if err != nil {
			return nil, err
		}
		lines = append(older, lines...)
		if count > 0 && len(lines) >= count {
			break
		}
	}

	return lines, nil
}

func logGenerations(path string) []string {
	generations := []string{path}
	for i := 1; i <= layout.LogBackups; i++ {
		generations = append(generations, fmt.Sprintf("%s.%d", path, i))
	}

	return generations
}

func tailOf(path string, count int) ([]string, error) {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	var lines []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if count > 0 && len(lines) > count {
			lines = lines[1:]
		}
	}

	return lines, scanner.Err()
}

const followEvery = 200 * time.Millisecond

func ShowLogs(ctx context.Context, path string, query LogQuery, out io.Writer) error {
	var tail *follower
	if query.Follow {
		tail = followerAtEndOf(path)
		defer tail.close()
	}
	if err := showRecent(path, query, out); err != nil {
		return err
	}
	if tail == nil {
		return nil
	}

	return tail.follow(ctx, func(line string) error {
		_, err := fmt.Fprintln(out, formatLine(query.Unit, line, query.Output))

		return err
	})
}

func showRecent(path string, query LogQuery, out io.Writer) error {
	lines, err := Tail(path, query.Lines)
	if err != nil {
		return err
	}
	if len(lines) == 0 {
		_, err = fmt.Fprintln(out, "-- No entries --")

		return err
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(out, formatLine(query.Unit, line, query.Output)); err != nil {
			return err
		}
	}

	return nil
}

type follower struct {
	path    string
	file    *os.File
	reader  *bufio.Reader
	pending string
}

func followerAtEndOf(path string) *follower {
	f := &follower{path: path}
	f.open(io.SeekEnd)

	return f
}

func (f *follower) follow(ctx context.Context, emit func(string) error) error {
	ticker := time.NewTicker(followEvery)
	defer ticker.Stop()
	for {
		if err := f.poll(emit); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (f *follower) poll(emit func(string) error) error {
	if f.file == nil {
		f.open(io.SeekStart)
	}
	if err := f.drain(emit); err != nil {
		return err
	}
	if !f.rotated() {
		return nil
	}
	f.close()
	f.open(io.SeekStart)

	return f.drain(emit)
}

func (f *follower) open(whence int) {
	file, err := os.Open(f.path)
	if err != nil {
		return
	}
	if _, err := file.Seek(0, whence); err != nil {
		_ = file.Close()

		return
	}
	f.file, f.reader = file, bufio.NewReader(file)
}

func (f *follower) drain(emit func(string) error) error {
	if f.file == nil {
		return nil
	}
	for {
		chunk, err := f.reader.ReadString('\n')
		f.pending += chunk
		if err != nil {
			return nil
		}
		line := strings.TrimSuffix(f.pending, "\n")
		f.pending = ""
		if err := emit(line); err != nil {
			return err
		}
	}
}

func (f *follower) rotated() bool {
	if f.file == nil {
		return false
	}
	current, err := os.Stat(f.path)
	if err != nil {
		return false
	}
	opened, err := f.file.Stat()

	return err == nil && !os.SameFile(current, opened)
}

func (f *follower) close() {
	if f.file != nil {
		_ = f.file.Close()
		f.file = nil
	}
}

func formatLine(unit, line, output string) string {
	stamp, message, _ := strings.Cut(line, " ")
	if output == "cat" {
		return message
	}

	return stamp + " " + unit + ": " + message
}

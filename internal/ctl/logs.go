package ctl

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

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

func ShowLogs(path string, query LogQuery, out io.Writer) error {
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

func formatLine(unit, line, output string) string {
	stamp, message, _ := strings.Cut(line, " ")
	if output == "cat" {
		return message
	}

	return stamp + " " + unit + ": " + message
}

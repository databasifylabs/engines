package supervise

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/databasifylabs/engines/internal/layout"
)

type logSink struct {
	dir     string
	maxSize int64
	console io.Writer

	consoleMu sync.Mutex
	filesMu   sync.Mutex
	files     map[string]*rotatingFile
	copies    sync.WaitGroup
}

func newLogSink(dir string, maxSize int64, console io.Writer) *logSink {
	return &logSink{dir: dir, maxSize: maxSize, console: console, files: map[string]*rotatingFile{}}
}

func (l *logSink) pipe(unit string) (*os.File, <-chan struct{}, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	drained := make(chan struct{})
	l.copies.Add(1)
	go func() {
		defer l.copies.Done()
		defer close(drained)
		defer func() { _ = reader.Close() }()
		copyLines(reader, func(line []byte) { l.line(unit, line) })
	}()

	return writer, drained, nil
}

func (l *logSink) event(unit, format string, args ...any) {
	l.line(unit, []byte("databasify: "+fmt.Sprintf(format, args...)))
}

func (l *logSink) line(unit string, text []byte) {
	l.toConsole(unit, text)
	stamped := append([]byte(time.Now().UTC().Format(time.RFC3339)+" "), text...)
	if err := l.fileOf(unit).write(append(stamped, '\n')); err != nil {
		l.toConsole("databasify", []byte(fmt.Sprintf("cannot write log of %s: %v", unit, err)))
	}
}

func (l *logSink) toConsole(unit string, text []byte) {
	line := make([]byte, 0, len(unit)+len(text)+4)
	line = append(line, '[')
	line = append(line, unit...)
	line = append(line, "] "...)
	line = append(line, text...)
	line = append(line, '\n')
	l.consoleMu.Lock()
	defer l.consoleMu.Unlock()
	_, _ = l.console.Write(line)
}

func (l *logSink) fileOf(unit string) *rotatingFile {
	l.filesMu.Lock()
	defer l.filesMu.Unlock()
	file, ok := l.files[unit]
	if !ok {
		file = &rotatingFile{path: filepath.Join(l.dir, unit+".log"), maxSize: l.maxSize}
		l.files[unit] = file
	}

	return file
}

func (l *logSink) drain(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		l.copies.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

func copyLines(r io.Reader, emit func([]byte)) {
	reader := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := reader.ReadSlice('\n')
		if len(line) > 0 {
			emit(bytes.TrimRight(line, "\r\n"))
		}
		if err != nil && !errors.Is(err, bufio.ErrBufferFull) {
			return
		}
	}
}

type rotatingFile struct {
	mu      sync.Mutex
	path    string
	maxSize int64
	file    *os.File
	size    int64
}

func (r *rotatingFile) write(data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.open(); err != nil {
		return err
	}
	if r.size > 0 && r.size+int64(len(data)) > r.maxSize {
		if err := r.rotate(); err != nil {
			return err
		}
	}
	written, err := r.file.Write(data)
	r.size += int64(written)

	return err
}

func (r *rotatingFile) open() error {
	if r.file != nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(r.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o640)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()

		return err
	}
	r.file, r.size = file, info.Size()

	return nil
}

func (r *rotatingFile) rotate() error {
	_ = r.file.Close()
	r.file = nil
	for generation := layout.LogBackups - 1; generation >= 1; generation-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", r.path, generation), fmt.Sprintf("%s.%d", r.path, generation+1))
	}
	if err := os.Rename(r.path, r.path+".1"); err != nil {
		return err
	}

	return r.open()
}

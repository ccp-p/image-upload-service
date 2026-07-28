package main

import (
	"bufio"
	"io"
	"sync"
)

// SharedLineReader reads lines from an io.Reader in a background goroutine
// and distributes them via a channel. This allows multiple consumers (the
// REPL and the OTP prompter) to share a single stdin without conflicts.
type SharedLineReader struct {
	lines chan string
	done  chan struct{}
	once  sync.Once
}

// NewSharedLineReader starts a goroutine that reads lines from r and sends
// them to the returned channel. When r returns EOF or is closed, the lines
// channel is closed.
func NewSharedLineReader(r io.Reader) *SharedLineReader {
	slr := &SharedLineReader{
		lines: make(chan string),
		done:  make(chan struct{}),
	}
	go func() {
		defer close(slr.lines)
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			select {
			case slr.lines <- scanner.Text():
			case <-slr.done:
				return
			}
		}
	}()
	return slr
}

// Lines returns the receive-only channel of input lines.
func (s *SharedLineReader) Lines() <-chan string {
	return s.lines
}

// TryLine attempts a non-blocking read of one line. It returns the line and
// true if a line was available, or "" and false if no line was immediately
// ready or the channel is closed.
func (s *SharedLineReader) TryLine() (string, bool) {
	select {
	case line, ok := <-s.lines:
		return line, ok
	default:
		return "", false
	}
}

// ReadLine blocks until a line is available or the reader is exhausted.
// Returns the line and true on success, or "" and false when closed.
func (s *SharedLineReader) ReadLine() (string, bool) {
	line, ok := <-s.lines
	return line, ok
}

// Close signals the background goroutine to stop. It is safe to call
// multiple times.
func (s *SharedLineReader) Close() {
	s.once.Do(func() {
		close(s.done)
	})
}

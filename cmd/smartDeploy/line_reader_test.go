package main

import (
	"strings"
	"testing"
	"time"
)

func TestSharedLineReader_BasicRead(t *testing.T) {
	lr := NewSharedLineReader(strings.NewReader("hello\nworld\n"))
	defer lr.Close()

	line, ok := lr.ReadLine()
	if !ok || line != "hello" {
		t.Errorf("first line = %q, ok=%v; want 'hello', true", line, ok)
	}
	line, ok = lr.ReadLine()
	if !ok || line != "world" {
		t.Errorf("second line = %q, ok=%v; want 'world', true", line, ok)
	}
	_, ok = lr.ReadLine()
	if ok {
		t.Error("should return false after input exhausted")
	}
}

func TestSharedLineReader_EmptyInput(t *testing.T) {
	lr := NewSharedLineReader(strings.NewReader(""))
	defer lr.Close()

	// Give the goroutine time to read EOF and close the channel.
	time.Sleep(50 * time.Millisecond)
	_, ok := lr.ReadLine()
	if ok {
		t.Error("should return false for empty input")
	}
}

func TestSharedLineReader_TryLine(t *testing.T) {
	lr := NewSharedLineReader(strings.NewReader("data\n"))
	defer lr.Close()

	// Give the goroutine time to send the line.
	time.Sleep(50 * time.Millisecond)
	line, ok := lr.TryLine()
	if !ok || line != "data" {
		t.Errorf("TryLine = %q, ok=%v; want 'data', true", line, ok)
	}

	// Second TryLine should return false (no more data yet or channel closed).
	_, ok = lr.TryLine()
	// Could be false because no data or because closed; either way not ok=true.
	// We just verify it doesn't block.
}

func TestSharedLineReader_CloseIsIdempotent(t *testing.T) {
	lr := NewSharedLineReader(strings.NewReader("x\n"))
	lr.Close()
	lr.Close() // should not panic
}

func TestSharedLineReader_LinesChannel(t *testing.T) {
	lr := NewSharedLineReader(strings.NewReader("a\nb\nc\n"))
	defer lr.Close()

	ch := lr.Lines()
	var got []string
	for line := range ch {
		got = append(got, line)
	}
	if len(got) != 3 {
		t.Fatalf("got %d lines, want 3", len(got))
	}
	if got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("lines = %v, want [a b c]", got)
	}
}

func TestSharedLineReader_LargeInput(t *testing.T) {
	// Build a large input to test the scanner buffer.
	var sb strings.Builder
	for i := 0; i < 1000; i++ {
		sb.WriteString("line-")
		sb.WriteString(strings.Repeat("x", 50))
		sb.WriteByte('\n')
	}
	lr := NewSharedLineReader(strings.NewReader(sb.String()))
	defer lr.Close()

	count := 0
	for range lr.Lines() {
		count++
	}
	if count != 1000 {
		t.Errorf("count = %d, want 1000", count)
	}
}

package main

import (
	"encoding/hex"
	"testing"
)

func TestSM3ABC(t *testing.T) {
	// GB/T 32905-2016 standard test vector.
	sum := sm3Sum([]byte("abc"))
	got := hex.EncodeToString(sum[:])
	want := "66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0"
	if got != want {
		t.Fatalf("sm3(abc) = %s, want %s", got, want)
	}
}

func TestSM3Empty(t *testing.T) {
	// SM3 of empty input.
	sum := sm3Sum([]byte(""))
	got := hex.EncodeToString(sum[:])
	want := "1ab21d8355cfa17f8e61194831e81a8f22bec8c728fefb747ed035eb5082aa2b"
	if got != want {
		t.Fatalf("sm3(empty) = %s, want %s", got, want)
	}
}

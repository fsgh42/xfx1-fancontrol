package main

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseNvidiaTempLine(t *testing.T) {
	tests := []struct {
		line    string
		want    int
		wantErr bool
	}{
		{"76", 76, false},
		{"  42  ", 42, false},
		{"0", 0, false},
		{"", 0, true},
		{"   ", 0, true},
		{"N/A", 0, true},
		{"76°C", 0, true}, // noheader,nounits shouldn't include °C but be defensive
	}

	for _, tc := range tests {
		t.Run(tc.line, func(t *testing.T) {
			got, err := parseNvidiaTempLine(tc.line)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseNvidiaTempLine(%q) err=%v, wantErr=%v", tc.line, err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("parseNvidiaTempLine(%q) = %d, want %d", tc.line, got, tc.want)
			}
		})
	}
}

func TestReadSysfsTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "temp1_input")

	// hwmon reports millidegrees
	if err := os.WriteFile(path, []byte("73250\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := readSysfsTemp(path)
	if err != nil {
		t.Fatalf("readSysfsTemp: %v", err)
	}

	if got != 73 {
		t.Errorf("readSysfsTemp = %d, want 73 (integer division of 73250/1000)", got)
	}
}

func TestReadSysfsTemp_Errors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		if _, err := readSysfsTemp("/nonexistent/path/xfx1/temp"); err == nil {
			t.Error("expected error for missing file")
		}
	})

	t.Run("garbage contents", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "temp1_input")
		if err := os.WriteFile(path, []byte("not a number"), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := readSysfsTemp(path); err == nil {
			t.Error("expected parse error")
		}
	})
}

func TestNvidiaStream_NoReadingYet(t *testing.T) {
	s := &nvidiaStream{maxStale: 10 * time.Second}

	if _, err := s.read(); err == nil {
		t.Error("expected error when no reading has been set")
	}
}

func TestNvidiaStream_FreshReading(t *testing.T) {
	s := &nvidiaStream{maxStale: 10 * time.Second}
	s.set(77)

	got, err := s.read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != 77 {
		t.Errorf("read = %d, want 77", got)
	}
}

func TestNvidiaStream_StaleReading(t *testing.T) {
	s := &nvidiaStream{maxStale: 1 * time.Second}
	s.latestC.Store(66)

	// Set the update time to 5 seconds ago.
	s.latestAtUnix.Store(time.Now().Add(-5 * time.Second).Unix())

	if _, err := s.read(); err == nil {
		t.Error("expected staleness error for 5s-old reading with 1s maxStale")
	}
}

// Sanity check that atomic storage round-trips correctly.
func TestNvidiaStream_AtomicRoundtrip(t *testing.T) {
	var x atomic.Int64
	x.Store(int64(42))
	if got := int(x.Load()); got != 42 {
		t.Errorf("roundtrip = %d, want 42", got)
	}
}

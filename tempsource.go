package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// tempReader returns the current GPU temperature in °C.
type tempReader func() (int, error)

// newTempReader builds a tempReader for the configured source.
//
// For a sysfs path the returned reader reads the file on each call
// (cheap — a few bytes from /sys).
//
// For "nvidia-smi" a single long-lived `nvidia-smi --loop=N` subprocess
// is started in the background. Its stdout is parsed line-by-line and the
// latest value is published atomically; the returned reader returns that
// value. The subprocess is tied to ctx and will be killed on shutdown.
// A one-shot query is run first to validate that nvidia-smi works and
// that exactly one GPU is addressable (unless gpu_index is set).
//
// Background goroutines register themselves with wg; the caller must
// Wait on wg after cancelling ctx to ensure a clean shutdown.
func newTempReader(ctx context.Context, wg *sync.WaitGroup, cfg *Config) (tempReader, error) {
	if cfg.TempSource != "nvidia-smi" {
		path := cfg.TempSource
		return func() (int, error) { return readSysfsTemp(path) }, nil
	}

	return startNvidiaStream(ctx, wg, cfg.GPUIndex, cfg.Interval)
}

func readSysfsTemp(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read temp %s: %w", path, err)
	}

	milli, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parse temp %s: %w", path, err)
	}

	return milli / 1000, nil
}

// parseNvidiaTempLine parses one line of nvidia-smi --query-gpu output
// (csv,noheader,nounits). Empty / blank lines return an error.
func parseNvidiaTempLine(line string) (int, error) {
	s := strings.TrimSpace(line)
	if s == "" {
		return 0, errors.New("empty line")
	}

	return strconv.Atoi(s)
}

// readNvidiaOnce runs a single `nvidia-smi --query-gpu=temperature.gpu` invocation.
// Used at startup to validate the environment before going into streaming mode,
// and to seed the initial value so the main loop doesn't have to wait for the
// first --loop tick.
func readNvidiaOnce(gpuIndex int) (int, error) {
	args := []string{
		"--query-gpu=temperature.gpu",
		"--format=csv,noheader,nounits",
	}
	if gpuIndex >= 0 {
		args = append(args, fmt.Sprintf("--id=%d", gpuIndex))
	}

	out, err := exec.Command("nvidia-smi", args...).Output()
	if err != nil {
		return 0, fmt.Errorf("nvidia-smi: %w", err)
	}

	s := strings.TrimSpace(string(out))

	if strings.ContainsRune(s, '\n') {
		return 0, fmt.Errorf("nvidia-smi returned multiple GPUs; set gpu_index in config to select one:\n%s", s)
	}

	return strconv.Atoi(s)
}

// nvidiaStream holds the most recent temperature from the long-lived
// nvidia-smi subprocess. Values are published via atomics so reads from
// the main loop don't need locking.
type nvidiaStream struct {
	latestC      atomic.Int64 // degrees C
	latestAtUnix atomic.Int64 // time.Now().Unix() of latest update
	maxStale     time.Duration
}

func (s *nvidiaStream) set(temp int) {
	s.latestC.Store(int64(temp))
	s.latestAtUnix.Store(time.Now().Unix())
}

func (s *nvidiaStream) read() (int, error) {
	at := s.latestAtUnix.Load()
	if at == 0 {
		return 0, errors.New("nvidia-smi stream: no reading yet")
	}

	if age := time.Since(time.Unix(at, 0)); age > s.maxStale {
		return 0, fmt.Errorf("nvidia-smi stream: reading stale (%.0fs old, max %.0fs)",
			age.Seconds(), s.maxStale.Seconds())
	}

	return int(s.latestC.Load()), nil
}

func startNvidiaStream(ctx context.Context, wg *sync.WaitGroup, gpuIndex, intervalSec int) (tempReader, error) {
	// Validate and seed with a one-shot query before kicking off the stream.
	initial, err := readNvidiaOnce(gpuIndex)
	if err != nil {
		return nil, err
	}

	// Consider a reading stale if we've missed roughly 3 poll cycles
	// (but not less than 10s — guards against intervalSec=1).
	maxStale := time.Duration(intervalSec*3) * time.Second
	if maxStale < 10*time.Second {
		maxStale = 10 * time.Second
	}

	s := &nvidiaStream{maxStale: maxStale}
	s.set(initial)

	wg.Add(1)
	go func() {
		defer wg.Done()
		runNvidiaStream(ctx, gpuIndex, intervalSec, s)
	}()

	return s.read, nil
}

// runNvidiaStream owns the nvidia-smi child process for the lifetime of ctx.
// If the child dies unexpectedly, it's restarted with exponential backoff.
func runNvidiaStream(ctx context.Context, gpuIndex, intervalSec int, s *nvidiaStream) {
	backoff := time.Second

	for {
		if ctx.Err() != nil {
			return
		}

		args := []string{
			"--query-gpu=temperature.gpu",
			"--format=csv,noheader,nounits",
			fmt.Sprintf("--loop=%d", intervalSec),
		}
		if gpuIndex >= 0 {
			args = append(args, fmt.Sprintf("--id=%d", gpuIndex))
		}

		cmd := exec.CommandContext(ctx, "nvidia-smi", args...)

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			log.Printf("nvidia-smi stream: stdout pipe: %v", err)
			return
		}

		if err := cmd.Start(); err != nil {
			log.Printf("nvidia-smi stream: start failed: %v", err)
			if !waitBackoff(ctx, &backoff) {
				return
			}
			continue
		}

		log.Printf("nvidia-smi stream: started (--loop=%d)", intervalSec)

		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if t, err := parseNvidiaTempLine(scanner.Text()); err == nil {
				s.set(t)
				backoff = time.Second // reset backoff after a successful read
			}
		}

		_ = cmd.Wait()

		if ctx.Err() != nil {
			return
		}

		log.Printf("nvidia-smi stream: subprocess ended, restarting in %s", backoff)
		if !waitBackoff(ctx, &backoff) {
			return
		}
	}
}

// waitBackoff sleeps for *d, then doubles it (capped at 30s).
// Returns false if ctx was cancelled during the wait.
func waitBackoff(ctx context.Context, d *time.Duration) bool {
	select {
	case <-time.After(*d):
	case <-ctx.Done():
		return false
	}

	if *d < 30*time.Second {
		*d *= 2
	}

	return true
}

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

func main() {
	configPath := flag.String("config", "/etc/xfx1-fancontrol/fancontrol.conf", "path to config file")
	dryRun := flag.Bool("dry-run", false, "read temps and log target PWM without writing")
	verbose := flag.Bool("verbose", false, "log every tick (bypass the normal log policy)")

	flag.Parse()

	cfg, err := parseConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	enablePath := cfg.FanPWM + "_enable"

	// Validate paths exist
	if _, err := os.Stat(cfg.FanPWM); err != nil {
		log.Fatalf("fan_pwm path not found: %s", cfg.FanPWM)
	}

	if _, err := os.Stat(enablePath); err != nil {
		log.Fatalf("pwm enable path not found: %s", enablePath)
	}

	if cfg.FanRPM != "" {
		if _, err := os.Stat(cfg.FanRPM); err != nil {
			log.Fatalf("fan_rpm path not found: %s", cfg.FanRPM)
		}
	}

	// Save original enable mode to restore on exit
	origEnable, err := readSysfs(enablePath)
	if err != nil {
		log.Fatalf("read %s: %v", enablePath, err)
	}

	log.Printf("saved original pwm_enable=%d from %s", origEnable, enablePath)

	// Restore on exit
	restore := func() {
		log.Printf("restoring pwm_enable=%d", origEnable)
		if err := writeSysfs(enablePath, origEnable); err != nil {
			log.Printf("WARNING: failed to restore pwm_enable: %v", err)
		}
	}

	if !*dryRun {
		// Take manual control
		if err := writeSysfs(enablePath, 1); err != nil {
			log.Fatalf("failed to set manual PWM mode: %v", err)
		}

		log.Printf("set %s=1 (manual mode)", enablePath)
	}

	// Shutdown context: cancelled on SIGINT/SIGTERM. The nvidia-smi stream
	// goroutine (if any) tears down when this is cancelled.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Track background goroutines (e.g. the nvidia-smi streamer) so we can
	// wait for them to fully tear down before returning.
	var bgWG sync.WaitGroup

	// Handle signals for clean shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Build the temperature reader. For nvidia-smi this spawns a long-lived
	// `nvidia-smi --loop=N` subprocess and reads lines from its stdout.
	readTemp, err := newTempReader(ctx, &bgWG, cfg)
	if err != nil {
		log.Fatalf("tempsource: %v", err)
	}

	log.Printf("starting fan control: temp_source=%s fan_pwm=%s interval=%ds dry_run=%v",
		cfg.TempSource, cfg.FanPWM, cfg.Interval, *dryRun)
	log.Printf("fan curve: %v", formatCurve(cfg.Curve))

	ticker := time.NewTicker(time.Duration(cfg.Interval) * time.Second)
	defer ticker.Stop()

	var (
		// Previous-tick state, used to decide "tickChanged" in the hot regime.
		haveLastTick                           bool
		lastTickTemp, lastTickPWM, lastTickRPM int
		lastTickHadRPM                         bool

		// Previous-*log* state, used for the cold-regime delta + heartbeat.
		haveLastLogged bool
		lastLoggedTemp int
		lastLogTime    time.Time
	)

	// Run once immediately, then on tick
	run := func() {
		temp, err := readTemp()
		if err != nil {
			log.Printf("ERROR reading temp: %v", err)
			return
		}

		targetPWM := interpolatePWM(cfg.Curve, temp)

		rpm := 0
		haveRPM := false
		rpmStr := ""
		if cfg.FanRPM != "" {
			if r, err := readRPM(cfg.FanRPM); err == nil {
				rpm = r
				haveRPM = true
				rpmStr = fmt.Sprintf(" rpm=%d", r)
			}
		}

		if !*dryRun {
			if err := writeSysfs(cfg.FanPWM, targetPWM); err != nil {
				log.Fatalf("ERROR writing pwm: %v", err)
			}
		}

		tickChanged := !haveLastTick ||
			temp != lastTickTemp ||
			targetPWM != lastTickPWM ||
			haveRPM != lastTickHadRPM ||
			(haveRPM && rpm != lastTickRPM)

		haveLastTick = true
		lastTickTemp = temp
		lastTickPWM = targetPWM
		lastTickRPM = rpm
		lastTickHadRPM = haveRPM

		now := time.Now()
		doLog := *verbose || shouldLog(temp, lastLoggedTemp, haveLastLogged, lastLogTime, now, tickChanged)
		if !doLog {
			return
		}

		suffix := ""
		if *dryRun {
			suffix = " [dry-run]"
		}

		label := "pwm"
		if *dryRun {
			label = "target_pwm"
		}

		log.Printf("temp=%d°C %s=%d/255 (%.0f%%)%s%s",
			temp, label, targetPWM, float64(targetPWM)/255*100, rpmStr, suffix)

		haveLastLogged = true
		lastLoggedTemp = temp
		lastLogTime = now
	}

	run()

	for {
		select {
		case <-ticker.C:
			run()
		case sig := <-sigCh:
			log.Printf("received %v, shutting down", sig)

			cancel()    // signal the nvidia-smi streamer to exit
			bgWG.Wait() // wait for the child process to be reaped

			if !*dryRun {
				restore()
			}

			return
		}
	}
}

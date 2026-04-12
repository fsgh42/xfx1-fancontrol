package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

type CurvePoint struct {
	TempC int
	PWM   int
}

type Config struct {
	TempSource string       // "nvidia-smi" or sysfs path
	GPUIndex   int          // nvidia-smi GPU index; -1 = unset (single-GPU auto)
	FanPWM     string       // sysfs pwm path, e.g. /sys/class/hwmon/hwmon3/pwm4
	FanRPM     string       // sysfs fan rpm path (optional, for logging)
	Interval   int          // poll interval in seconds
	Curve      []CurvePoint // sorted by TempC ascending
}

func parseConfig(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	cfg := &Config{
		TempSource: "nvidia-smi",
		GPUIndex:   -1,
		Interval:   3,
	}

	inCurve := false
	scanner := bufio.NewScanner(f)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if line == "[curve]" {
			inCurve = true
			continue
		}

		if strings.HasPrefix(line, "[") {
			inCurve = false
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("line %d: expected key = value", lineNum)
		}

		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		if inCurve {
			temp, err := strconv.Atoi(key)
			if err != nil {
				return nil, fmt.Errorf("line %d: invalid temperature %q: %w", lineNum, key, err)
			}

			pwm, err := strconv.Atoi(val)
			if err != nil {
				return nil, fmt.Errorf("line %d: invalid pwm value %q: %w", lineNum, val, err)
			}

			if pwm < 0 || pwm > 255 {
				return nil, fmt.Errorf("line %d: pwm value must be 0-255, got %d", lineNum, pwm)
			}

			cfg.Curve = append(cfg.Curve, CurvePoint{TempC: temp, PWM: pwm})
			continue
		}

		switch key {
		case "temp_source":
			cfg.TempSource = val
		case "gpu_index":
			n, err := strconv.Atoi(val)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("line %d: invalid gpu_index %q (must be non-negative integer)", lineNum, val)
			}

			cfg.GPUIndex = n
		case "fan_pwm":
			cfg.FanPWM = val
		case "fan_rpm":
			cfg.FanRPM = val
		case "interval":
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				return nil, fmt.Errorf("line %d: invalid interval %q", lineNum, val)
			}

			cfg.Interval = n
		default:
			return nil, fmt.Errorf("line %d: unknown key %q", lineNum, key)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	if cfg.FanPWM == "" {
		return nil, fmt.Errorf("fan_pwm is required")
	}
	if len(cfg.Curve) < 2 {
		return nil, fmt.Errorf("fan curve needs at least 2 points")
	}

	sort.Slice(
		cfg.Curve,
		func(i, j int) bool {
			return cfg.Curve[i].TempC < cfg.Curve[j].TempC
		},
	)

	return cfg, nil
}

// interpolatePWM returns the PWM value for a given temperature
// using linear interpolation between curve points.
func interpolatePWM(curve []CurvePoint, tempC int) int {
	if tempC <= curve[0].TempC {
		return curve[0].PWM
	}

	last := curve[len(curve)-1]
	if tempC >= last.TempC {
		return last.PWM
	}

	for i := 1; i < len(curve); i++ {
		if tempC <= curve[i].TempC {
			lo := curve[i-1]
			hi := curve[i]

			frac := float64(tempC-lo.TempC) / float64(hi.TempC-lo.TempC)
			pwm := float64(lo.PWM) + frac*float64(hi.PWM-lo.PWM)

			return int(pwm + 0.5)
		}
	}

	return last.PWM
}

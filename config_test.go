package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInterpolatePWM(t *testing.T) {
	curve := []CurvePoint{
		{TempC: 30, PWM: 60},
		{TempC: 50, PWM: 120},
		{TempC: 80, PWM: 240},
	}

	tests := []struct {
		name string
		temp int
		want int
	}{
		{"below first point clamps to first", 20, 60},
		{"exactly first point", 30, 60},
		{"exactly middle point", 50, 120},
		{"exactly last point", 80, 240},
		{"above last point clamps to last", 95, 240},
		{"midway in first segment", 40, 90},    // 60 + 0.5*(120-60)
		{"midway in second segment", 65, 180},  // 120 + 0.5*(240-120)
		{"quarter into first segment", 35, 75}, // 60 + 0.25*60
	}

	for _, tc := range tests {
		t.Run(
			tc.name,
			func(t *testing.T) {
				got := interpolatePWM(curve, tc.temp)
				if got != tc.want {
					t.Errorf("interpolatePWM(%d) = %d, want %d", tc.temp, got, tc.want)
				}
			},
		)
	}
}

func TestInterpolatePWM_SinglePointBounds(t *testing.T) {
	// Two identical-PWM points: should always return that PWM
	curve := []CurvePoint{
		{TempC: 40, PWM: 100},
		{TempC: 80, PWM: 100},
	}

	for _, temp := range []int{0, 40, 60, 80, 200} {
		if got := interpolatePWM(curve, temp); got != 100 {
			t.Errorf("interpolatePWM(%d) = %d, want 100", temp, got)
		}
	}
}

// writeConfig is a test helper that writes a config to a temp file and returns its path.
func writeConfig(t *testing.T, contents string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "fancontrol.conf")

	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatalf("write test config: %v", err)
	}

	return path
}

func TestParseConfig_Valid(t *testing.T) {
	path := writeConfig(t, `
# a comment
temp_source = nvidia-smi
fan_pwm = /sys/class/hwmon/hwmon3/pwm4
fan_rpm = /sys/class/hwmon/hwmon3/fan4_input
interval = 5

[curve]
30 = 60
80 = 240
50 = 120
`)

	cfg, err := parseConfig(path)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}

	if cfg.TempSource != "nvidia-smi" {
		t.Errorf("TempSource = %q, want nvidia-smi", cfg.TempSource)
	}

	if cfg.FanPWM != "/sys/class/hwmon/hwmon3/pwm4" {
		t.Errorf("FanPWM = %q", cfg.FanPWM)
	}

	if cfg.Interval != 5 {
		t.Errorf("Interval = %d, want 5", cfg.Interval)
	}

	// Curve must be sorted ascending by temperature.
	wantTemps := []int{30, 50, 80}
	if len(cfg.Curve) != len(wantTemps) {
		t.Fatalf("curve length = %d, want %d", len(cfg.Curve), len(wantTemps))
	}

	for i, wt := range wantTemps {
		if cfg.Curve[i].TempC != wt {
			t.Errorf("curve[%d].TempC = %d, want %d (curve not sorted)", i, cfg.Curve[i].TempC, wt)
		}
	}
}

func TestParseConfig_Defaults(t *testing.T) {
	path := writeConfig(t, `
fan_pwm = /sys/class/hwmon/hwmon3/pwm4

[curve]
30 = 60
80 = 240
`)

	cfg, err := parseConfig(path)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}

	if cfg.TempSource != "nvidia-smi" {
		t.Errorf("default TempSource = %q, want nvidia-smi", cfg.TempSource)
	}

	if cfg.Interval != 3 {
		t.Errorf("default Interval = %d, want 3", cfg.Interval)
	}

	if cfg.GPUIndex != -1 {
		t.Errorf("default GPUIndex = %d, want -1 (unset)", cfg.GPUIndex)
	}
}

func TestParseConfig_GPUIndex(t *testing.T) {
	path := writeConfig(t, `
fan_pwm = /sys/foo
gpu_index = 2

[curve]
30 = 60
80 = 240
`)

	cfg, err := parseConfig(path)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}

	if cfg.GPUIndex != 2 {
		t.Errorf("GPUIndex = %d, want 2", cfg.GPUIndex)
	}
}

func TestParseConfig_Errors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantSub string
	}{
		{
			name:    "missing fan_pwm",
			content: "[curve]\n30 = 60\n80 = 240\n",
			wantSub: "fan_pwm",
		},
		{
			name:    "curve needs two points",
			content: "fan_pwm = /sys/foo\n[curve]\n50 = 120\n",
			wantSub: "at least 2 points",
		},
		{
			name:    "pwm out of range",
			content: "fan_pwm = /sys/foo\n[curve]\n30 = 60\n80 = 999\n",
			wantSub: "0-255",
		},
		{
			name:    "unknown key",
			content: "wobble = 42\nfan_pwm = /sys/foo\n[curve]\n30 = 60\n80 = 240\n",
			wantSub: "unknown key",
		},
		{
			name:    "malformed line",
			content: "fan_pwm /sys/foo\n[curve]\n30 = 60\n80 = 240\n",
			wantSub: "expected key = value",
		},
		{
			name:    "invalid interval",
			content: "fan_pwm = /sys/foo\ninterval = zero\n[curve]\n30 = 60\n80 = 240\n",
			wantSub: "invalid interval",
		},
		{
			name:    "invalid gpu_index",
			content: "fan_pwm = /sys/foo\ngpu_index = -1\n[curve]\n30 = 60\n80 = 240\n",
			wantSub: "invalid gpu_index",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, tc.content)

			_, err := parseConfig(path)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSub)
			}

			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantSub)
			}
		})
	}
}

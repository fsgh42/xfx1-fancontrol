package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func readTemp(source string, gpuIndex int) (int, error) {
	if source == "nvidia-smi" {
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

		// Require an unambiguous single value. If nvidia-smi reports more
		// than one GPU, the user must pick one with gpu_index in the config.
		if strings.ContainsRune(s, '\n') {
			return 0, fmt.Errorf("nvidia-smi returned multiple GPUs; set gpu_index in config to select one:\n%s", s)
		}

		return strconv.Atoi(s)
	}

	// sysfs hwmon path — value is in millidegrees
	data, err := os.ReadFile(source)
	if err != nil {
		return 0, fmt.Errorf("read temp %s: %w", source, err)
	}

	milli, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parse temp %s: %w", source, err)
	}

	return milli / 1000, nil
}

func readRPM(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}

	return strconv.Atoi(strings.TrimSpace(string(data)))
}

func writeSysfs(path string, value int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(value)), 0644)
}

func readSysfs(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}

	return strconv.Atoi(strings.TrimSpace(string(data)))
}

func formatCurve(curve []CurvePoint) string {
	parts := make([]string, len(curve))

	for i, p := range curve {
		parts[i] = fmt.Sprintf("%d°C->%d", p.TempC, p.PWM)
	}

	return strings.Join(parts, " ")
}

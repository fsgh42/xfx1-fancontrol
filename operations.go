package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

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

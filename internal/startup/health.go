package startup

import (
	"os"
	"path/filepath"
	"strings"
)

func schedulerRunning() bool {
	dirs, _ := filepath.Glob("/proc/[0-9]*")
	for _, d := range dirs {
		b, err := os.ReadFile(filepath.Join(d, "comm"))
		if err == nil && strings.TrimSpace(string(b)) == "supercronic" {
			return true
		}
	}
	return false
}

func healthyWith(skippedFile string, running func() bool) bool {
	if !running() {
		return false
	}
	_, err := os.Stat(skippedFile)
	return err != nil
}

func Healthy(skippedFile string) bool {
	return healthyWith(skippedFile, schedulerRunning)
}

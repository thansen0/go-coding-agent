package tools

import (
	"fmt"
	"os"
	"os/exec"
)

var allowedGoActions = map[string][]string{
	"build":    {"go", "build", "."},
	"test":     {"go", "test", "./..."},
	"format":   {"go", "fmt", "./..."},
	"mod_tidy": {"go", "mod", "tidy"},
}

func ReadFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func WriteFile(path string, content string) (string, error) {
	err := os.WriteFile(path, []byte(content), 0o644)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Wrote %d bytes to %s", len(content), path), nil
}

func RunGoAction(action string) (string, error) {
	commandArgs, ok := allowedGoActions[action]
	if !ok {
		return "", fmt.Errorf("unsupported go action %q", action)
	}

	c := exec.Command(commandArgs[0], commandArgs[1:]...)
	out, err := c.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("command failed: %w\n%s", err, string(out))
	}
	return string(out), nil
}

package tools

import (
	"fmt"
	"os"
	"os/exec"
)

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

func RunCommand(cmd string) (string, error) {
	c := exec.Command("bash", "-lc", cmd)
	out, err := c.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("command failed: %w\n%s", err, string(out))
	}
	return string(out), nil
}

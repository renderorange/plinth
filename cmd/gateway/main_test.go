package main

import (
	"os/exec"
	"testing"
)

func TestMainCompiles(t *testing.T) {
	cmd := exec.Command("go", "build", "-o", "/dev/null", ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gateway binary failed to compile: %v\n%s", err, out)
	}
}

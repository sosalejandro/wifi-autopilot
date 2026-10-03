// Package cmdexec runs external commands for the CLI-backed adapters.
package cmdexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner executes a command and returns its stdout. Adapters take a Runner so tests can fake it.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Exec runs commands for real, with a C locale so output is parseable.
func Exec(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, err
}

package bench

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// CommandString renders the uv command RunScript would execute, for
// --dry-run previews and log lines.
func CommandString(dir, entrypoint string, args []string) string {
	parts := append([]string{"uv", "run", "--project", dir, "python", entrypoint}, args...)
	return strings.Join(parts, " ")
}

// RunScript runs `uv run --project dir python entrypoint args...`,
// streaming the child's stderr straight through and capturing its stdout.
// Per the script protocol, all logging goes to stderr; RunScript parses the
// last non-blank stdout line as JSON and returns it, discarding any lines
// before it.
func RunScript(ctx context.Context, dir, entrypoint string, args []string, stderr io.Writer) (json.RawMessage, error) {
	cmdArgs := append([]string{"run", "--project", dir, "python", entrypoint}, args...)
	cmd := exec.CommandContext(ctx, "uv", cmdArgs...)
	cmd.Stderr = stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("run %s: %w", entrypoint, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("run %s: %w", entrypoint, err)
	}

	var last string
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			last = line
		}
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()

	if waitErr != nil {
		return nil, fmt.Errorf("run %s: %w", entrypoint, waitErr)
	}
	if scanErr != nil {
		return nil, fmt.Errorf("run %s: read stdout: %w", entrypoint, scanErr)
	}
	if last == "" {
		return nil, fmt.Errorf("run %s: produced no stdout output", entrypoint)
	}

	var raw json.RawMessage
	if err := json.Unmarshal([]byte(last), &raw); err != nil {
		return nil, fmt.Errorf("run %s: last stdout line is not valid JSON: %w", entrypoint, err)
	}
	return raw, nil
}

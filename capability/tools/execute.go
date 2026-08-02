package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/pengjunchen/go-agent-core/capability/registry"
)

// executeMaxOutputLength is the default maximum combined output length (stdout+stderr)
// for execute in UTF-16 code units. Outputs exceeding this are truncated.
const executeMaxOutputLength = 50000

// OnExecutePartialOutput is an optional callback for streaming partial output
// during command execution. When set by the agent loop, execute.go calls it
// with each chunk of output read from stdout/stderr pipes.
// The toolCallID allows the agent loop to correlate partial output with the
// originating tool call; the execute tool passes an empty string for this
// parameter (the agent loop should capture the current toolCallID in its closure).
var OnExecutePartialOutput func(toolCallID string, partial string)

// NewExecuteTool builds an execute ToolDefinition scoped to workDir.
// The returned handler runs shell commands with cmd.Dir locked to workDir.
// Non-zero exit codes return full output (stdout + stderr) rather than an error,
// allowing the LLM to self-correct.
func NewExecuteTool(workDir string) registry.ToolDefinition {
	return registry.ToolDefinition{
		Name: "execute",
		Description: "Execute a shell command. The working directory is locked to the project root. Non-zero exit codes return output for LLM self-correction rather than failing. Default timeout is 120 seconds. Streams output in real-time.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type": "string",
					"description": "Shell command to execute",
				},
				"timeout": map[string]any{
					"type": "integer",
					"description": "Timeout in seconds (default: 120)",
				},
			},
			"required": []any{"command"},
		},
		Handler: executeHandler(workDir),
		ParallelSafe: false,
		ValidateArgs: true,
	}
}

// resolveShell finds an available shell: tries bash first, falls back to /bin/sh.
func resolveShell() string {
	if path, err := exec.LookPath("bash"); err == nil {
		_ = path // Use "bash" directly (not the full path) to match original behavior.
		return "bash"
	}
	return "/bin/sh"
}

// executeHandler returns a ToolHandler closure capturing workDir for sandboxing.
// It uses streaming output via pipes and goroutines for real-time capture.
func executeHandler(workDir string) registry.ToolHandler {
	shell := resolveShell()
	return func(ctx context.Context, args map[string]any) (*registry.ToolResult, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		rawCmd, ok := args["command"]
		if !ok {
			return &registry.ToolResult{
				Content: "missing required parameter: command",
				IsError: true,
			}, nil
		}
		command, ok := rawCmd.(string)
		if !ok {
			return &registry.ToolResult{
				Content: fmt.Sprintf("command must be a string, got %T", rawCmd),
				IsError: true,
			}, nil
		}

		timeoutSec := 120
		if rawTimeout, exists := args["timeout"]; exists && rawTimeout != nil {
			n, err := toInt(rawTimeout)
			if err != nil {
				return &registry.ToolResult{
					Content: fmt.Sprintf("timeout must be an integer: %v", err),
					IsError: true,
				}, nil
			}
			if n > 0 {
				timeoutSec = n
			}
		}

		// Create a context with the specified timeout.
		execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		defer cancel()

		cmd := exec.CommandContext(execCtx, shell, "-c", command)
		cmd.Dir = workDir

		// Use pipes for streaming output.
		stdoutPipe, err := cmd.StdoutPipe()
		if err != nil {
			return &registry.ToolResult{
				Content: fmt.Sprintf("failed to create stdout pipe: %v", err),
				IsError: true,
			}, nil
		}
		stderrPipe, err := cmd.StderrPipe()
		if err != nil {
			return &registry.ToolResult{
				Content: fmt.Sprintf("failed to create stderr pipe: %v", err),
				IsError: true,
			}, nil
		}

		if err := cmd.Start(); err != nil {
			return &registry.ToolResult{
				Content: fmt.Sprintf("Command execution failed: %v", err),
				IsError: true,
				Details: map[string]any{
					"exit_code": -1,
				},
			}, nil
		}

		// Stream stdout and stderr in goroutines, collecting output into buffers.
		var stdoutBuf, stderrBuf bytes.Buffer
		var wg sync.WaitGroup
		wg.Add(2)

		// Stream stdout.
		go func() {
			defer wg.Done()
			buf := make([]byte, 4096)
			for {
				n, readErr := stdoutPipe.Read(buf)
				if n > 0 {
					chunk := string(buf[:n])
					stdoutBuf.WriteString(chunk)
					if OnExecutePartialOutput != nil {
						OnExecutePartialOutput("", chunk)
					}
				}
				if readErr != nil {
					break
				}
			}
		}()

		// Stream stderr.
		go func() {
			defer wg.Done()
			buf := make([]byte, 4096)
			for {
				n, readErr := stderrPipe.Read(buf)
				if n > 0 {
					chunk := string(buf[:n])
					stderrBuf.WriteString(chunk)
					if OnExecutePartialOutput != nil {
						OnExecutePartialOutput("", chunk)
					}
				}
				if readErr != nil {
					break
				}
			}
		}()

		// Wait for streaming goroutines to finish reading.
		wg.Wait()

		// Wait for the command to complete.
		err = cmd.Wait()

		// Build the output.
		out := stdoutBuf.String()
		errOut := stderrBuf.String()

		// Non-zero exit code: return output for LLM self-correction, NOT an error.
		if err != nil {
			if execCtx.Err() == context.DeadlineExceeded {
				// Timeout exceeded — this IS an error worth reporting.
				output := out
				if errOut != "" {
					output += "\n[stderr]\n" + errOut
				}
				truncated := TruncateContent(output, executeMaxOutputLength)
				return &registry.ToolResult{
					Content: fmt.Sprintf("Command timed out after %d seconds.\n%s", timeoutSec, truncated.Content),
					IsError: true,
					Details: map[string]any{
						"exit_code": -1,
						"timed_out": true,
						"timeout_sec": timeoutSec,
						"truncated": truncated.WasTruncated,
					},
				}, nil
			}

			// Other execution errors (command not found, etc.).
			if out == "" && errOut == "" {
				return &registry.ToolResult{
					Content: fmt.Sprintf("Command execution failed: %v", err),
					IsError: true,
					Details: map[string]any{
						"exit_code": -1,
					},
				}, nil
			}

			// Non-zero exit code with output — return for LLM self-correction.
			output := out
			if errOut != "" {
				output += "\n[stderr]\n" + errOut
			}
			exitCode := -1
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}
			truncated := TruncateContent(output, executeMaxOutputLength)
			return &registry.ToolResult{
				Content: fmt.Sprintf("[exit code: %d]\n%s", exitCode, truncated.Content),
				IsError: false, // Non-zero exit is NOT an error — LLM should self-correct.
				Details: map[string]any{
					"exit_code": exitCode,
					"truncated": truncated.WasTruncated,
				},
			}, nil
		}

		// Success (exit code 0).
		output := out
		if errOut != "" {
			output += "\n[stderr]\n" + errOut
		}
		truncated := TruncateContent(output, executeMaxOutputLength)
		return &registry.ToolResult{
			Content: truncated.Content,
			Details: map[string]any{
				"exit_code": 0,
				"truncated": truncated.WasTruncated,
			},
		}, nil
	}
}

// parseTimeout is a helper for parsing timeout from args (used in tests).
func parseTimeout(args map[string]any) int {
	timeoutSec := 120
	if rawTimeout, exists := args["timeout"]; exists && rawTimeout != nil {
		if n, err := toInt(rawTimeout); err == nil && n > 0 {
			timeoutSec = n
		}
	}
	return timeoutSec
}

// formatExitCode extracts the exit code from an exec.Cmd error.
func formatExitCode(err error) string {
	if err == nil {
		return "0"
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return strconv.Itoa(exitErr.ExitCode())
	}
	return "-1"
}

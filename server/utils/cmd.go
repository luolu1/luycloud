package utils

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
	"time"

	"kvm_console/logger"
)

// SafeGo 启动带 panic recovery 的 goroutine
func SafeGo(fn func()) {
	go func() {
		defer RecoverAndLog("goroutine")
		fn()
	}()
}

// RecoverAndLog 在 defer 中调用，捕获 panic 并记录错误日志
func RecoverAndLog(scope string) {
	if r := recover(); r != nil {
		logger.App.Error("panic recovered",
			"scope", scope,
			"panic", fmt.Sprintf("%v", r),
			"stack", string(debug.Stack()),
		)
	}
}

// CmdResult 命令执行结果
type CmdResult struct {
	Stdout   string // 标准输出
	Stderr   string // 标准错误
	ExitCode int    // 退出码
	Error    error  // 错误信息
}

// ExecCommand 执行系统命令
func ExecCommand(name string, args ...string) *CmdResult {
	return ExecCommandWithTimeout(name, 30*time.Second, args...)
}

// ExecCommandWithTimeout 执行系统命令（带超时）
func ExecCommandWithTimeout(name string, timeout time.Duration, args ...string) *CmdResult {
	return ExecCommandContextWithTimeout(context.Background(), name, timeout, args...)
}

// ExecCommandContextWithTimeout 执行系统命令（支持取消和超时）
func ExecCommandContextWithTimeout(ctx context.Context, name string, timeout time.Duration, args ...string) *CmdResult {
	return execCommandContextWithTimeout(ctx, name, timeout, false, args...)
}

func execCommandContextWithTimeout(ctx context.Context, name string, timeout time.Duration, sensitive bool, args ...string) *CmdResult {
	return execCommandContextWithTimeoutEnv(ctx, name, timeout, sensitive, nil, args...)
}

// execCommandContextWithTimeoutEnv 在标准执行流程基础上，额外注入自定义环境变量。
// extraEnv 为形如 "KEY=VALUE" 的切片，追加在 LANG/LC_ALL 之后；为空则行为与原函数一致。
func execCommandContextWithTimeoutEnv(ctx context.Context, name string, timeout time.Duration, sensitive bool, extraEnv []string, args ...string) *CmdResult {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.Command(name, args...)
	// 强制使用 C 语言环境，确保 virsh 等命令输出英文便于解析
	cmd.Env = append(os.Environ(), "LANG=C", "LC_ALL=C")
	if len(extraEnv) > 0 {
		cmd.Env = append(cmd.Env, extraEnv...)
	}
	prepareProcessGroup(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	argsStr := strings.Join(args, " ")
	if sensitive {
		argsStr = "[敏感参数已隐藏]"
	}
	logger.CMD.Info("执行命令", "cmd", name, "args", argsStr)

	start := time.Now()

	// 启动命令
	if err := cmd.Start(); err != nil {
		logger.CMD.Error("命令启动失败", "cmd", name, "args", argsStr, "error", err)
		return &CmdResult{
			Stderr:   err.Error(),
			ExitCode: -1,
			Error:    fmt.Errorf("启动命令失败: %w", err),
		}
	}

	// 超时控制。timeout 小于等于 0 时仅响应上下文取消，不自动终止命令。
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	var timeoutCh <-chan time.Time
	var timer *time.Timer
	if timeout > 0 {
		timer = time.NewTimer(timeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}

	select {
	case err := <-done:
		elapsed := time.Since(start)
		result := &CmdResult{
			Stdout: strings.TrimSpace(stdout.String()),
			Stderr: strings.TrimSpace(stderr.String()),
		}
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				result.ExitCode = exitErr.ExitCode()
			} else {
				result.ExitCode = -1
			}
			result.Error = fmt.Errorf("命令执行失败: %w, stderr: %s", err, result.Stderr)
			logger.CMD.Error("命令执行失败", "cmd", name, "args", argsStr, "exit_code", result.ExitCode, "error", result.Error, "stderr", truncate(result.Stderr, 500), "duration", elapsed.String())
		} else {
			logger.CMD.Info("命令执行完成", "cmd", name, "args", argsStr, "exit_code", result.ExitCode, "duration", elapsed.String())
		}
		return result

	case <-timeoutCh:
		killProcessTree(cmd)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		logger.CMD.Error("命令执行超时", "cmd", name, "args", argsStr, "timeout", timeout.String())
		return &CmdResult{
			Stderr:   "命令执行超时",
			ExitCode: -1,
			Error:    fmt.Errorf("命令执行超时: %s", name),
		}

	case <-ctx.Done():
		killProcessTree(cmd)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		logger.CMD.Warn("命令已取消", "cmd", name, "args", argsStr, "reason", ctx.Err())
		return &CmdResult{
			Stderr:   "命令已取消",
			ExitCode: -1,
			Error:    fmt.Errorf("命令已取消: %s: %w", name, ctx.Err()),
		}
	}
}

// ExecCommandLongRunning 执行长时间运行的命令（超时 10 分钟）
func ExecCommandLongRunning(name string, args ...string) *CmdResult {
	return ExecCommandWithTimeout(name, 10*time.Minute, args...)
}

// ExecCommandSensitiveLongRunning 执行包含密码、令牌等敏感参数的长任务，日志不记录参数正文。
func ExecCommandSensitiveLongRunning(name string, args ...string) *CmdResult {
	return execCommandContextWithTimeout(context.Background(), name, 10*time.Minute, true, args...)
}

// ExecCommandNoTimeout 执行命令，不设置自动超时（如文件复制、网络传输等大 IO 操作，等待自然完成）。
func ExecCommandNoTimeout(name string, args ...string) *CmdResult {
	return execCommandContextWithTimeout(context.Background(), name, 0, false, args...)
}

// ExecCommandSensitiveNoTimeout 执行包含密码、令牌等敏感参数的 IO 操作，不设置自动超时，日志不记录参数正文。
func ExecCommandSensitiveNoTimeout(name string, args ...string) *CmdResult {
	return execCommandContextWithTimeout(context.Background(), name, 0, true, args...)
}

// ExecCommandWithEnv 执行系统命令（带 30s 超时），并注入自定义环境变量。
// extraEnv 形如 "KEY=VALUE"，用于 libguestfs 等需要额外环境变量的场景。
func ExecCommandWithEnv(extraEnv []string, name string, args ...string) *CmdResult {
	return execCommandContextWithTimeoutEnv(context.Background(), name, 30*time.Second, false, extraEnv, args...)
}

// ExecCommandWithEnvNoTimeout 执行命令并注入自定义环境变量，不设置自动超时（大 IO 操作）。
func ExecCommandWithEnvNoTimeout(extraEnv []string, name string, args ...string) *CmdResult {
	return execCommandContextWithTimeoutEnv(context.Background(), name, 0, false, extraEnv, args...)
}

// ExecCommandSensitiveWithEnvNoTimeout 执行含敏感参数的命令并注入自定义环境变量，不设置自动超时，日志不记录参数正文。
func ExecCommandSensitiveWithEnvNoTimeout(extraEnv []string, name string, args ...string) *CmdResult {
	return execCommandContextWithTimeoutEnv(context.Background(), name, 0, true, extraEnv, args...)
}

// ExecShellNoTimeout 执行 Shell 命令，不设置自动超时（如文件复制、网络传输等大 IO 操作）。
func ExecShellNoTimeout(command string) *CmdResult {
	return ExecCommandNoTimeout("bash", "-c", command)
}

// ExecShell 执行 Shell 命令（通过 bash -c）
func ExecShell(command string) *CmdResult {
	return ExecCommand("bash", "-c", command)
}

// ExecShellWithTimeout 执行 Shell 命令（带超时）
func ExecShellWithTimeout(command string, timeout time.Duration) *CmdResult {
	return ExecCommandWithTimeout("bash", timeout, "-c", command)
}

// ExecShellContext 执行 Shell 命令，仅响应上下文取消，不设置自动超时。
func ExecShellContext(ctx context.Context, command string) *CmdResult {
	return ExecCommandContextWithTimeout(ctx, "bash", 0, "-c", command)
}

// ExecShellWithEnvNoTimeout 执行 Shell 命令并注入自定义环境变量，不设置自动超时。
// extraEnv 形如 "KEY=VALUE"，会被 bash 及其子进程（如 guestfish）继承。
func ExecShellWithEnvNoTimeout(extraEnv []string, command string) *CmdResult {
	return ExecCommandWithEnvNoTimeout(extraEnv, "bash", "-c", command)
}

// ExecShellContextWithEnv 执行 Shell 命令并注入自定义环境变量，仅响应上下文取消，不设置自动超时。
func ExecShellContextWithEnv(ctx context.Context, extraEnv []string, command string) *CmdResult {
	return execCommandContextWithTimeoutEnv(ctx, "bash", 0, false, extraEnv, "-c", command)
}

// ExecShellContextWithTimeout 执行 Shell 命令（支持取消和超时）
func ExecShellContextWithTimeout(ctx context.Context, command string, timeout time.Duration) *CmdResult {
	return ExecCommandContextWithTimeout(ctx, "bash", timeout, "-c", command)
}

// ── Quiet 变体：非零退出码仅记录 DEBUG 日志（适用于预期可能失败的查询/清理命令）──

// ExecCommandQuiet 与 ExecCommand 相同，但非零退出码仅记录 DEBUG
func ExecCommandQuiet(name string, args ...string) *CmdResult {
	return execCommandWithLogLevel(name, logger.CMD.Debug, 30*time.Second, args...)
}

// ExecCommandQuietWithTimeout 与 ExecCommandWithTimeout 相同，但非零退出码仅记录 DEBUG
func ExecCommandQuietWithTimeout(name string, timeout time.Duration, args ...string) *CmdResult {
	return execCommandWithLogLevel(name, logger.CMD.Debug, timeout, args...)
}

// ExecShellQuiet 与 ExecShell 相同，但非零退出码仅记录 DEBUG
func ExecShellQuiet(command string) *CmdResult {
	return ExecCommandQuiet("bash", "-c", command)
}

// execCommandWithLogLevel 执行命令，使用指定日志级别记录非零退出码
func execCommandWithLogLevel(name string, logFn func(string, ...any), timeout time.Duration, args ...string) *CmdResult {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "LANG=C", "LC_ALL=C")
	prepareProcessGroup(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	argsStr := strings.Join(args, " ")
	logger.CMD.Info("执行命令", "cmd", name, "args", argsStr)

	start := time.Now()

	if err := cmd.Start(); err != nil {
		logFn("命令启动失败", "cmd", name, "args", argsStr, "error", err)
		return &CmdResult{
			Stderr:   err.Error(),
			ExitCode: -1,
			Error:    fmt.Errorf("启动命令失败: %w", err),
		}
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		elapsed := time.Since(start)
		result := &CmdResult{
			Stdout: strings.TrimSpace(stdout.String()),
			Stderr: strings.TrimSpace(stderr.String()),
		}
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				result.ExitCode = exitErr.ExitCode()
			} else {
				result.ExitCode = -1
			}
			result.Error = fmt.Errorf("命令执行失败: %w, stderr: %s", err, result.Stderr)
			// 使用调用方指定的日志级别（DEBUG 而非 ERROR）
			logFn("命令执行失败", "cmd", name, "args", argsStr, "exit_code", result.ExitCode, "error", result.Error, "stderr", truncate(result.Stderr, 500), "duration", elapsed.String())
		} else {
			logger.CMD.Info("命令执行完成", "cmd", name, "args", argsStr, "exit_code", result.ExitCode, "duration", elapsed.String())
		}
		return result

	case <-time.After(timeout):
		killProcessTree(cmd)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		logFn("命令执行超时", "cmd", name, "args", argsStr, "timeout", timeout.String())
		return &CmdResult{
			Stderr:   "命令执行超时",
			ExitCode: -1,
			Error:    fmt.Errorf("命令执行超时: %s %s", name, strings.Join(args, " ")),
		}
	}
}

// ShellSingleQuote 对 shell 参数做单引号转义，防止命令注入。
// 将单引号替换为 '"'"'（结束引号、转义单引号、开始引号），
// 使参数在 shell 单引号上下文中安全使用。
func ShellSingleQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// truncate 截断字符串到指定长度，超过部分用 "..." 替代
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

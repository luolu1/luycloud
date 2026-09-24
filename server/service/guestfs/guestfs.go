// Package guestfs 为 libguestfs 系列命令（virt-customize/guestfish/virt-cat 等）
// 提供统一的执行入口，并在 appliance 内部 qemu 崩溃时自动逐级降级重试。
//
// 背景：在嵌套虚拟化（宿主机本身是一台虚拟机）等环境下，libguestfs 会启动其内部
// appliance 专用 qemu，且该 qemu 的 -cpu 会启用 vPMU，进而尝试设置 MSR 0x345
// (IA32_PERF_CAPABILITIES)。该 MSR 写入被上层 KVM 拒绝时，内部 qemu 直接 SIGABRT
// （signal 6），表现为 `guestfs_launch failed` / `appliance closed the connection`。
//
// 解决方案（分级，逐级更强但更慢，仅 x86_64 生效）：
//
//	level 0 —— 不干预，行为完全不变（默认）。
//	level 1 —— 通过 LIBGUESTFS_HV 指向自动生成的 qemu 包装脚本，为 -cpu 追加
//	           pmu=off，并强制 LIBGUESTFS_BACKEND=direct。规避 MSR 0x345 崩溃。
//	level 2 —— 强制 TCG 纯软件模拟（LIBGUESTFS_BACKEND_SETTINGS=force_tcg），
//	           彻底不使用 KVM，因此绝不会触发 MSR 0x345；速度较慢，作为兜底。
//
// 触发方式有二：
//  1. 快速路径：探测到「嵌套虚拟化」时基线直接从 level 1 起步；
//  2. 自愈路径：任一 libguestfs 命令命中 appliance 崩溃特征时，自动升一级并重试，
//     升级对本进程后续所有调用持续生效，直至成功或到达最高级。
package guestfs

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"kvm_console/logger"
	"kvm_console/service/vm_xml"
	"kvm_console/utils"
)

const (
	// wrapperScriptName qemu 包装脚本文件名
	wrapperScriptName = "qemu-pmuoff.sh"
	// defaultWrapperDir 默认包装脚本目录，可通过环境变量覆盖
	defaultWrapperDir = "/var/lib/kvm-console/bin"
	// wrapperDirEnv 覆盖包装脚本目录的环境变量名
	wrapperDirEnv = "KVM_LIBGUESTFS_WRAPPER_DIR"
	// forceLevelEnv 手动强制规避级别（0/1/2）的环境变量名。
	// 设置后作为基线下限，无需依赖嵌套探测或运行时自愈即可立即生效，
	// 便于在探测失效的宿主机上手动兜底（推荐设为 2 = force_tcg）。
	forceLevelEnv = "KVM_LIBGUESTFS_FORCE_LEVEL"

	// 降级级别
	levelNone   = 0 // 不干预
	levelPMUOff = 1 // pmu=off 包装脚本 + direct 后端
	levelTCG    = 2 // 强制 TCG 纯软件模拟（最高级兜底）
)

// wrapperScript qemu 包装脚本内容：为 libguestfs 内部 qemu 的每个 -cpu 追加 pmu=off。
const wrapperScript = `#!/bin/bash
# 由 kvm_console 自动生成：为 libguestfs 内部 qemu 追加 pmu=off，
# 规避 QEMU 设置 MSR 0x345 (IA32_PERF_CAPABILITIES) 导致的 SIGABRT。
real_qemu="$(command -v qemu-system-x86_64 2>/dev/null || echo /usr/bin/qemu-system-x86_64)"
args=()
while [ $# -gt 0 ]; do
  if [ "$1" = "-cpu" ]; then
    args+=("-cpu")
    shift
    args+=("$1,pmu=off")
  else
    args+=("$1")
  fi
  shift
done
exec "$real_qemu" "${args[@]}"
`

var (
	// nestedOnce 缓存「是否探测为嵌套虚拟化」的结果
	nestedOnce sync.Once
	nestedVal  bool

	// forcedLevel 自愈级别：命中 appliance 崩溃特征时逐级升高（0→1→2），
	// 使本进程后续所有 libguestfs 调用都按该级别启用规避。
	forcedLevel int32

	// provisionOnce 保证包装脚本仅生成一次
	provisionOnce sync.Once
	provisionPath string
	provisionErr  error
)

// wrapperDir 返回包装脚本所在目录（支持环境变量覆盖）。
func wrapperDir() string {
	if dir := os.Getenv(wrapperDirEnv); dir != "" {
		return dir
	}
	return defaultWrapperDir
}

// detectNested 探测宿主机是否运行在虚拟化环境（嵌套场景），结果缓存。
func detectNested() bool {
	nestedOnce.Do(func() {
		nestedVal = vm_xml.HostIsNestedVirtualization()
	})
	return nestedVal
}

// forcedEnvLevel 读取手动强制级别环境变量（0/1/2），无效或未设置返回 -1。
func forcedEnvLevel() int {
	switch strings.TrimSpace(os.Getenv(forceLevelEnv)) {
	case "0":
		return levelNone
	case "1":
		return levelPMUOff
	case "2":
		return levelTCG
	default:
		return -1
	}
}

// baselineLevel 返回无运行时自愈时的基线级别：
// 非 x86_64 恒为 0；否则取「手动强制级别」与「嵌套探测结果」的较大值。
func baselineLevel() int {
	if runtime.GOARCH != "amd64" {
		return levelNone
	}
	lvl := levelNone
	if detectNested() {
		lvl = levelPMUOff
	}
	if env := forcedEnvLevel(); env > lvl {
		lvl = env
	}
	return lvl
}

// effectiveLevel 返回当前生效级别 = max(基线, 自愈级别)。
func effectiveLevel() int {
	lvl := baselineLevel()
	if f := int(atomic.LoadInt32(&forcedLevel)); f > lvl {
		lvl = f
	}
	return lvl
}

// provisionWrapper 惰性生成 qemu 包装脚本，返回脚本绝对路径。
func provisionWrapper() (string, error) {
	provisionOnce.Do(func() {
		dir := wrapperDir()
		if err := os.MkdirAll(dir, 0755); err != nil {
			provisionErr = err
			return
		}
		path := filepath.Join(dir, wrapperScriptName)
		if err := os.WriteFile(path, []byte(wrapperScript), 0755); err != nil {
			provisionErr = err
			return
		}
		// 确保可执行位（部分文件系统/umask 下 WriteFile 权限可能被削减）
		_ = os.Chmod(path, 0755)
		provisionPath = path
	})
	return provisionPath, provisionErr
}

// envForLevel 返回指定级别对应的额外环境变量。
// level 0 返回 nil；level 1 生成包装脚本失败时降级为 nil。
func envForLevel(level int) []string {
	switch level {
	case levelPMUOff:
		path, err := provisionWrapper()
		if err != nil {
			logger.App.Warn("libguestfs qemu 包装脚本生成失败，回退默认行为", "error", err)
			return nil
		}
		return []string{
			"LIBGUESTFS_HV=" + path,
			"LIBGUESTFS_BACKEND=direct",
		}
	case levelTCG:
		// 强制 TCG：彻底不使用 KVM，无需包装脚本即可规避 MSR 0x345。
		return []string{
			"LIBGUESTFS_BACKEND=direct",
			"LIBGUESTFS_BACKEND_SETTINGS=force_tcg",
		}
	default:
		return nil
	}
}

// Env 返回 libguestfs 命令在当前生效级别下所需的额外环境变量。
// 供外部（如兼容性检查）直接读取；常规执行请使用本包的 Exec* 函数以获得自愈能力。
func Env() []string {
	return envForLevel(effectiveLevel())
}

// LogStartupState 在服务启动时输出 libguestfs 规避的初始状态，
// 并在基线级别 >= 1 时预生成 qemu 包装脚本，便于运维排查与提前暴露生成失败。
// 该函数应在 logger 初始化之后调用。
func LogStartupState() {
	base := baselineLevel()
	logger.App.Info("libguestfs 规避初始化",
		"arch", runtime.GOARCH,
		"nested", detectNested(),
		"force_level_env", strings.TrimSpace(os.Getenv(forceLevelEnv)),
		"baseline", levelName(base),
	)
	// 基线需要包装脚本时提前生成，暴露潜在的目录/权限问题
	if base == levelPMUOff {
		if path, err := provisionWrapper(); err != nil {
			logger.App.Warn("libguestfs qemu 包装脚本预生成失败，运行时将降级重试", "error", err)
		} else {
			logger.App.Info("libguestfs qemu 包装脚本已就绪", "path", path)
		}
	}
}

// isApplianceCrash 判断 libguestfs 命令是否因 appliance 内部 qemu 崩溃而失败。
// 匹配 signal 6(Aborted)、guestfs_launch 失败或 appliance 连接中断等特征。
func isApplianceCrash(res *utils.CmdResult) bool {
	if res == nil || res.Error == nil {
		return false
	}
	s := res.Stderr
	return strings.Contains(s, "guestfs_launch failed") ||
		strings.Contains(s, "appliance closed the connection") ||
		strings.Contains(s, "killed by signal 6")
}

// levelName 返回级别的可读名称，用于日志。
func levelName(level int) string {
	switch level {
	case levelPMUOff:
		return "pmu=off"
	case levelTCG:
		return "force_tcg"
	default:
		return "none"
	}
}

// execHeal 执行 libguestfs 命令，并在命中 appliance 崩溃特征时逐级升级重试。
// 仅 x86_64 参与升级；每次实际提升生效级别后重试一次，直至成功或到达最高级。
func execHeal(run func(env []string) *utils.CmdResult) *utils.CmdResult {
	level := effectiveLevel()
	res := run(envForLevel(level))

	if runtime.GOARCH != "amd64" {
		return res
	}

	for isApplianceCrash(res) && level < levelTCG {
		next := level + 1
		// 提升全局自愈级别（对后续调用持续生效）；若已被并发提升到更高级则跟随。
		for {
			cur := atomic.LoadInt32(&forcedLevel)
			if int(cur) >= next {
				break
			}
			if atomic.CompareAndSwapInt32(&forcedLevel, cur, int32(next)) {
				break
			}
		}
		logger.App.Warn("libguestfs appliance 崩溃(signal 6)，升级规避级别后重试",
			"from", levelName(level), "to", levelName(next))

		env := envForLevel(next)
		if len(env) == 0 && next == levelPMUOff {
			// 包装脚本生成失败：跳过 level 1，直接尝试更高级
			level = next
			continue
		}
		res = run(env)
		level = next
	}
	return res
}

// ExecNoTimeout 执行 libguestfs 命令（不设置自动超时，适用于大 IO 操作）。
func ExecNoTimeout(name string, args ...string) *utils.CmdResult {
	return execHeal(func(env []string) *utils.CmdResult {
		return utils.ExecCommandWithEnvNoTimeout(env, name, args...)
	})
}

// ExecSensitiveNoTimeout 执行含敏感参数的 libguestfs 命令（不设置自动超时，日志不记录参数正文）。
func ExecSensitiveNoTimeout(name string, args ...string) *utils.CmdResult {
	return execHeal(func(env []string) *utils.CmdResult {
		return utils.ExecCommandSensitiveWithEnvNoTimeout(env, name, args...)
	})
}

// Exec 执行 libguestfs 命令。libguestfs 操作属于磁盘 IO，且在 force_tcg 兜底
// 模式下 appliance 启动会显著变慢，故不设置自动超时（遵循 IO 操作不限时约定）。
func Exec(name string, args ...string) *utils.CmdResult {
	return execHeal(func(env []string) *utils.CmdResult {
		return utils.ExecCommandWithEnvNoTimeout(env, name, args...)
	})
}

// ExecShellNoTimeout 通过 bash -c 执行含 guestfish here-doc 的脚本，注入规避环境变量并具备
// appliance 崩溃自愈能力。适用于 windows/fnos/linux 离线磁盘调整中构造的 guestfish 脚本。
func ExecShellNoTimeout(script string) *utils.CmdResult {
	return execHeal(func(env []string) *utils.CmdResult {
		return utils.ExecShellWithEnvNoTimeout(env, script)
	})
}

// ExecShellContext 通过 bash -c 执行 guestfish 脚本，注入规避环境变量并具备自愈能力，
// 仅响应上下文取消、不设置自动超时。适用于写盘型 guestfish 操作。
func ExecShellContext(ctx context.Context, script string) *utils.CmdResult {
	return execHeal(func(env []string) *utils.CmdResult {
		return utils.ExecShellContextWithEnv(ctx, env, script)
	})
}

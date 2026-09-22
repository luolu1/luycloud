package vm_xml

import (
	"regexp"
	"strconv"
	"strings"
	"sync"

	"kvm_console/logger"
	"kvm_console/utils"
)

// libvirt UEFI NVRAM 能力阈值（libvirt 版本编码：major*1000000 + minor*1000 + patch）。
//
// 背景：domain XML 的 <nvram> 元素属性支持随 libvirt 版本演进：
//   - format 属性         首次支持于 libvirt 9.2.0（此前会被静默丢弃）
//   - templateFormat 属性 首次支持于 libvirt 10.10.0
//   - UEFI（pflash）内部快照 首次支持于 libvirt 10.9.0（且要求 NVRAM 为 qcow2）
//
// 关键陷阱：在低于 9.2.0 的 libvirt（如 Ubuntu 22.04 的 8.0.0）上，
// libvirt 会静默丢弃 <nvram format='qcow2'>，并按 raw 加载 pflash。
// 若磁盘上的 NVRAM 实为 qcow2，OVMF 会把 qcow2 头部当作变量存储读取，
// 导致固件无法初始化显示（VNC 黑屏 "Guest has not initialized the display (yet)."）。
// 因此唯一安全的不变量是：libvirt 实际使用的 pflash 格式必须与磁盘文件真实格式一致。
const (
	libvirtVerNVRAMFormatAttr         uint32 = 9002000  // 9.2.0
	libvirtVerPflashInternalSnapshot  uint32 = 10009000 // 10.9.0
	libvirtVerNVRAMTemplateFormatAttr uint32 = 10010000 // 10.10.0
)

var (
	libvirtVersionOnce  sync.Once
	libvirtVersionCache uint32

	// 匹配 virsh version 输出中的 "Using library: libvirt 8.0.0" 行（LANG=C 下为英文）。
	libvirtVersionLibraryRegexp = regexp.MustCompile(`(?i)Using library:\s*libvirt\s+(\d+)\.(\d+)\.(\d+)`)
	// 匹配 libvirtd --version 输出中的 "libvirtd (libvirt) 8.0.0"。
	libvirtVersionDaemonRegexp = regexp.MustCompile(`(?i)libvirtd\b.*?(\d+)\.(\d+)\.(\d+)`)
)

// LibvirtVersion 返回宿主机 libvirt 守护进程版本（归一化编码），进程内只探测一次。
// 探测失败返回 0，所有能力判定据此退化到最保守（raw、无 format 属性）的安全行为。
func LibvirtVersion() uint32 {
	libvirtVersionOnce.Do(func() {
		libvirtVersionCache = detectLibvirtVersion()
		logger.App.Info("检测 libvirt 版本",
			"version_encoded", libvirtVersionCache,
			"version", LibvirtVersionString(),
			"nvram_format_attr", supportsNVRAMFormatAttr(libvirtVersionCache),
			"preferred_nvram_format", preferredNVRAMFormat(libvirtVersionCache),
		)
	})
	return libvirtVersionCache
}

// RefreshLibvirtVersion 强制重新探测 libvirt 版本（仅供升级 libvirtd 后或测试使用）。
func RefreshLibvirtVersion() uint32 {
	v := detectLibvirtVersion()
	libvirtVersionCacheMu.Lock()
	libvirtVersionCache = v
	libvirtVersionCacheMu.Unlock()
	// 保证后续 LibvirtVersion() 直接返回新值。
	libvirtVersionOnce.Do(func() {})
	return v
}

var libvirtVersionCacheMu sync.RWMutex

func detectLibvirtVersion() uint32 {
	// 首选 virsh version（反映实际连接的守护进程/库版本）。
	if result := utils.ExecCommand("virsh", "version"); result.Error == nil {
		if v := parseLibvirtVersion(libvirtVersionLibraryRegexp, result.Stdout); v > 0 {
			return v
		}
	}
	// 回退 libvirtd --version。
	if result := utils.ExecCommand("libvirtd", "--version"); result.Error == nil {
		if v := parseLibvirtVersion(libvirtVersionDaemonRegexp, result.Stdout); v > 0 {
			return v
		}
	}
	logger.App.Warn("无法探测 libvirt 版本，NVRAM 相关行为将退化为最保守方案（raw）")
	return 0
}

func parseLibvirtVersion(re *regexp.Regexp, output string) uint32 {
	m := re.FindStringSubmatch(output)
	if len(m) < 4 {
		return 0
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	return encodeLibvirtVersion(major, minor, patch)
}

func encodeLibvirtVersion(major, minor, patch int) uint32 {
	if major < 0 || minor < 0 || patch < 0 {
		return 0
	}
	return uint32(major)*1000000 + uint32(minor)*1000 + uint32(patch)
}

// LibvirtVersionString 返回人类可读的 libvirt 版本（如 "8.0.0"），未知时返回 "unknown"。
func LibvirtVersionString() string {
	v := currentLibvirtVersion()
	if v == 0 {
		return "unknown"
	}
	return strconv.Itoa(int(v/1000000)) + "." +
		strconv.Itoa(int((v/1000)%1000)) + "." +
		strconv.Itoa(int(v%1000))
}

func currentLibvirtVersion() uint32 {
	libvirtVersionCacheMu.RLock()
	cached := libvirtVersionCache
	libvirtVersionCacheMu.RUnlock()
	if cached != 0 {
		return cached
	}
	return LibvirtVersion()
}

// SupportsNVRAMFormatAttr 报告当前 libvirt 是否支持 <nvram> 的 format 属性（>= 9.2.0）。
func SupportsNVRAMFormatAttr() bool {
	return supportsNVRAMFormatAttr(currentLibvirtVersion())
}

func supportsNVRAMFormatAttr(v uint32) bool {
	return v >= libvirtVerNVRAMFormatAttr
}

// SupportsNVRAMTemplateFormatAttr 报告当前 libvirt 是否支持 <nvram> 的 templateFormat 属性（>= 10.10.0）。
func SupportsNVRAMTemplateFormatAttr() bool {
	return currentLibvirtVersion() >= libvirtVerNVRAMTemplateFormatAttr
}

// SupportsPflashInternalSnapshot 报告当前 libvirt 是否支持 UEFI（pflash）虚拟机的内部快照（>= 10.9.0，且要求 qcow2 NVRAM）。
func SupportsPflashInternalSnapshot() bool {
	return currentLibvirtVersion() >= libvirtVerPflashInternalSnapshot
}

// PreferredNVRAMFormat 返回当前 libvirt 版本下应当使用的 NVRAM 磁盘格式。
//
// 策略说明：
//   - raw 在所有 libvirt 版本（8.0 至今）均可安全工作，是默认与兜底格式。
//   - 仅当 libvirt 支持 UEFI 内部快照（>= 10.9.0）时才有理由使用 qcow2，
//     因为 qcow2 的唯一收益就是让 UEFI 虚拟机可做内部内存快照。
//     注意：此收益与 format 属性是否可用（9.2.0）无关，切勿据后者耦合。
//
// 本次发布策略：始终返回 raw（在所有目标宿主机上均已验证可用）。
// 当具备 >= 10.9.0 的宿主机可供验证时，可将下方判断改为按版本返回 qcow2。
func PreferredNVRAMFormat() string {
	return preferredNVRAMFormat(currentLibvirtVersion())
}

func preferredNVRAMFormat(_ uint32) string {
	// 本次发布：raw-only 策略。raw 在 libvirt 8.0→最新 全版本可用。
	// 未来若要在 >= 10.9.0 上启用 qcow2 以支持 UEFI 内部快照，改为：
	//   if v >= libvirtVerPflashInternalSnapshot { return "qcow2" }
	return "raw"
}

// NVRAMFormatForVersion 供迁移等跨主机场景使用：按给定（目标主机）libvirt 版本返回应使用的 NVRAM 格式。
func NVRAMFormatForVersion(v uint32) string {
	return preferredNVRAMFormat(v)
}

// SupportsNVRAMFormatAttrForVersion 供跨主机场景判定给定版本是否支持 format 属性。
func SupportsNVRAMFormatAttrForVersion(v uint32) bool {
	return supportsNVRAMFormatAttr(v)
}

// SupportsNVRAMTemplateFormatAttrForVersion 供跨主机场景判定给定版本是否支持 templateFormat 属性。
func SupportsNVRAMTemplateFormatAttrForVersion(v uint32) bool {
	return v >= libvirtVerNVRAMTemplateFormatAttr
}

// ParseLibvirtVersionFromVirshOutput 从 `virsh version` 文本中解析 libvirt 库版本（跨主机 SSH 探测用）。
func ParseLibvirtVersionFromVirshOutput(output string) uint32 {
	if v := parseLibvirtVersion(libvirtVersionLibraryRegexp, output); v > 0 {
		return v
	}
	return parseLibvirtVersion(libvirtVersionDaemonRegexp, output)
}

// ParseLibvirtVersionFromDaemonOutput 从 `libvirtd --version` 文本中解析版本（跨主机 SSH 探测用）。
func ParseLibvirtVersionFromDaemonOutput(output string) uint32 {
	return parseLibvirtVersion(libvirtVersionDaemonRegexp, output)
}

// NormalizeQemuImgFormat 归一化 qemu-img 格式字符串。
func NormalizeQemuImgFormat(format string) string {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		return ""
	}
	return format
}

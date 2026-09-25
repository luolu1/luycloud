package vm

import (
	"context"
	"strings"
	"sync"
	"time"

	"kvm_console/service/guest_agent"
	"kvm_console/service/libvirt_rpc"
	"kvm_console/utils"
)

// ==================== 系统版本信息（QGA 探测 + 缓存） ====================

// osInfoProbeInFlight 去重：同一台虚拟机同一时刻仅允许一个探测 goroutine。
var osInfoProbeInFlight sync.Map

// osInfoProbeDeadline QGA 探测总时长上限（到点后安静退出，不设整体 context 超时）。
const osInfoProbeDeadline = 3 * time.Minute

// osInfoProbeInterval QGA 探测轮询间隔。
const osInfoProbeInterval = 5 * time.Second

// resolveVMOSFallback 计算关机/无 QGA 时的系统信息回退值：
// os_type 依据模板 meta 的 Type 实时判定，os_version 回退为模板分类（如 "Ubuntu"/"Windows11"）。
func resolveVMOSFallback(templateName string) (osType, osVersion string) {
	osType = DetectVMOSType(templateName, "")
	if templateName != "" && D != nil && D.GetTemplateMeta != nil {
		if meta := D.GetTemplateMeta(templateName); meta != nil && strings.TrimSpace(meta.Category) != "" {
			osVersion = strings.TrimSpace(meta.Category)
		}
	}
	return osType, osVersion
}

// probeVMOSInfoDisplay 通过 QGA 读取一次精确的系统版本显示串。
// 返回 (显示串, 是否成功)；QGA 不可达或无可用信息时返回 ("", false)。
func probeVMOSInfoDisplay(ctx context.Context, name string) (string, bool) {
	client := guest_agent.NewClient(name)

	pingCtx, cancelPing := context.WithTimeout(ctx, guest_agent.ConnectTimeout)
	if err := client.Ping(pingCtx); err != nil {
		cancelPing()
		return "", false
	}
	cancelPing()

	infoCtx, cancelInfo := context.WithTimeout(ctx, guest_agent.ConnectTimeout)
	info, err := client.OSInfo(infoCtx)
	cancelInfo()
	if err != nil {
		return "", false
	}

	display := strings.TrimSpace(info.PrettyName)
	if display == "" {
		display = strings.TrimSpace(info.Name + " " + info.VersionID)
	}
	if display == "" {
		return "", false
	}
	return display, true
}

// ScheduleVMOSInfoProbe 在虚拟机开机/重启/重置后异步上报一次 QGA 精确系统版本。
// 事件驱动：仅在生命周期事件触发时调用，绝不在列表读取路径同步调用 QGA。
// 同一虚拟机已有探测在途时直接返回（去重）。
func ScheduleVMOSInfoProbe(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	if _, loaded := osInfoProbeInFlight.LoadOrStore(name, struct{}{}); loaded {
		return
	}
	go func() {
		defer utils.RecoverAndLog("vm-osinfo-probe")
		defer osInfoProbeInFlight.Delete(name)

		state, stateErr := libvirt_rpc.GetDomainStateRPC(name)
		if stateErr != nil || !strings.EqualFold(strings.TrimSpace(state), "running") {
			return
		}

		// QGA 通道预检：未配置 guest agent 通道的虚拟机直接放弃，
		// 避免对没有 agent 的机器空轮询整个 deadline 窗口。
		if xmlStr, err := libvirt_rpc.GetDomainXMLRPC(name, 0); err == nil {
			if !strings.Contains(xmlStr, "org.qemu.guest_agent.0") {
				return
			}
		}

		templateName := GetVMDiskInfo(name).Template
		osType := DetectVMOSType(templateName, "")

		deadline := time.Now().Add(osInfoProbeDeadline)
		for time.Now().Before(deadline) {
			if display, ok := probeVMOSInfoDisplay(context.Background(), name); ok {
				UpdateVMCacheOSInfo(name, osType, display)
				return
			}
			time.Sleep(osInfoProbeInterval)
		}
		// 超时：QGA 未就绪或虚拟机未装 agent，安静退出，保留缓存中已有的回退值。
	}()
}

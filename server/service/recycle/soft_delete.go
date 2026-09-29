package recycle

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"kvm_console/logger"
	"kvm_console/model"
	"kvm_console/service/ip_resolver"
	libvirt_rpc "kvm_console/service/libvirt_rpc"
	netpkg "kvm_console/service/network"
	"kvm_console/service/ovs"
	"kvm_console/service/snapshot"
	"kvm_console/service/storage/disk"
	"kvm_console/service/vm_xml"
	"kvm_console/utils"

	"github.com/digitalocean/go-libvirt"
)

// SoftDeleteVM 将虚拟机移入回收站（保留磁盘，可恢复）。
// 磁盘保持原位不移动；仅将 domain.xml 与 NVRAM 拷贝到侧车目录。
// 非致命的绑定清理失败会累积进 warnings 并持久化到 DB 行与侧车文件。
func SoftDeleteVM(opts SoftDeleteOptions, progress func(int, string)) (*model.VMRecycleItem, error) {
	if progress == nil {
		progress = func(int, string) {}
	}
	name := strings.TrimSpace(opts.VMName)
	if name == "" {
		return nil, fmt.Errorf("虚拟机名称不能为空")
	}
	source := opts.Source
	if source == "" {
		source = model.VMRecycleSourceUserDelete
	}

	// 1. 迁移互斥保护（迁移场景跳过：迁移任务本身持有锁，否则会自死锁）
	if source != model.VMRecycleSourceMigration {
		if err := ensureVMNotMigrating(name, "移入回收站"); err != nil {
			return nil, err
		}
	}

	// 2. 解析归属用户
	owner := strings.TrimSpace(opts.Owner)
	if owner == "" {
		owner = findVMOwner(name)
	}
	isAdmin := owner == "" || owner == "admin"

	// 3. 导出 inactive XML（恢复契约；失败则硬性中止，尚未改动任何状态）
	progress(5, "正在导出虚拟机配置...")
	xmlResult := utils.ExecCommand("virsh", "dumpxml", name, "--inactive")
	if xmlResult.Error != nil || strings.TrimSpace(xmlResult.Stdout) == "" {
		return nil, fmt.Errorf("导出虚拟机 XML 失败: %v", firstErr(xmlResult))
	}
	xmlText := xmlResult.Stdout

	// 4. 枚举磁盘（保留路径记录，不移动文件）
	progress(15, "正在收集磁盘与网络信息...")
	disks, totalBytes := collectRecycleDisks(name)

	// 5. 采集绑定信息（恢复时重新绑定用）
	cloudType := getUserCloudType(owner)
	var vpcBinding model.VPCVMBinding
	_ = model.DB.Where("vm_name = ? AND interface_order = ?", name, 0).First(&vpcBinding).Error

	// 5.1 必须在 undefine 之前收集 IP（collectVMIPs 依赖 virsh domifaddr）
	vmIPs := collectVMIPs(name)

	// 6. 创建侧车目录，写入 domain.xml
	progress(25, "正在保存回收站元数据...")
	storeDir, err := ensureStoreDir(name)
	if err != nil {
		return nil, fmt.Errorf("创建回收站目录失败: %v", err)
	}
	xmlPath := storeDir + "/domain.xml"
	if err := os.WriteFile(xmlPath, []byte(xmlText), 0600); err != nil {
		_ = os.RemoveAll(storeDir)
		return nil, fmt.Errorf("写入回收站 domain.xml 失败: %v", err)
	}

	// 7. 在 undefine 之前将 NVRAM 拷贝出（UEFI 恢复依赖；拷贝失败 = 硬失败）
	nvramOrigPath := vm_xml.ExtractDomainNVRAMPath(xmlText)
	if nvramOrigPath == "" && vm_xml.ParseVMBootTypeFromDomainXML(xmlText) != vm_xml.VMBootTypeBIOS {
		// UEFI 域 XML 未带 nvram 属性时，回退到规范路径
		nvramOrigPath = vm_xml.GetVMNVRAMPath(name)
	}
	var nvramPath string
	if nvramOrigPath != "" {
		if _, statErr := os.Stat(nvramOrigPath); statErr == nil {
			nvramPath = storeDir + "/nvram.fd"
			if err := copyFile(nvramOrigPath, nvramPath, 0600); err != nil {
				_ = os.RemoveAll(storeDir)
				return nil, fmt.Errorf("拷贝 NVRAM 到回收站失败: %v（UEFI 虚拟机恢复将依赖此副本）", err)
			}
		}
	}

	// 8. 快照：不做任何删除（保留磁盘数据）；若存在快照则提示元数据将丢失
	var warnings []string
	if snaps, err := snapshot.ListSnapshots(name); err == nil && len(snaps) > 0 {
		warnings = append(warnings, "libvirt 快照元数据将在移入回收站时丢失（磁盘内快照数据保留）")
	}

	// 9. 强制关机（迁移场景源域已被接管，跳过）
	if !opts.SkipDestroy {
		progress(40, "正在关机...")
		if err := libvirt_rpc.DestroyDomainRPC(name); err != nil {
			logger.App.Warn("移入回收站前关机失败（可能已关机）", "vm", name, "error", err)
		}
		time.Sleep(1 * time.Second)
	}

	// 10. 弹出 cdrom（防止残留 ISO 引用阻塞恢复）
	utils.ExecShell(fmt.Sprintf("virsh change-media %s hda --eject 2>/dev/null || true", utils.ShellSingleQuote(name)))

	// 11. 取消定义 —— 关键：绝不带 Nvram 标志（不删除 NVRAM，恢复依赖它）。
	// 但 UEFI 域在 libvirt 上 undefine 必须显式指定 nvram 处置（--nvram 或 --keep-nvram），
	// 否则报 "cannot undefine domain with nvram"。这里已在第 7 步把 NVRAM 拷入侧车，
	// 故统一带 KeepNvram：保留原 NVRAM 文件，同时满足 libvirt 的显式处置要求。
	// KeepNvram 对 BIOS 域无副作用（libvirt 会忽略）。
	progress(60, "正在取消虚拟机定义...")
	if undefineErr := libvirt_rpc.UndefineDomainRPC(name, libvirt.DomainUndefineSnapshotsMetadata|libvirt.DomainUndefineKeepNvram); undefineErr != nil {
		if err2 := libvirt_rpc.UndefineDomainRPC(name, libvirt.DomainUndefineKeepNvram); err2 != nil {
			if !strings.Contains(err2.Error(), "not found") {
				_ = os.RemoveAll(storeDir)
				logger.App.Error("取消虚拟机定义失败，回收站侧车已回滚", "vm", name, "error", err2)
				return nil, fmt.Errorf("取消虚拟机定义失败: %v", err2)
			}
		}
	}

	// 13. 先写侧车 meta.json，再插入 DB 行（侧车优先：DB 失败时磁盘产物仍可手动恢复）
	progress(80, "正在写入回收站记录...")
	meta := &VMRecycleMeta{
		VMName:          name,
		Owner:           owner,
		IsAdmin:         isAdmin,
		Source:          source,
		TargetNode:      opts.TargetNode,
		Note:            opts.Note,
		CloudType:       cloudType,
		SwitchID:        vpcBinding.SwitchID,
		SecurityGroupID: vpcBinding.SecurityGroupID,
		NVRAMPath:       nvramPath,
		NVRAMOrigPath:   nvramOrigPath,
		XMLPath:         xmlPath,
		Disks:           disks,
		TotalBytes:      totalBytes,
		IPs:             vmIPs,
		DeletedBy:       opts.DeletedBy,
		Warnings:        warnings,
	}
	if err := writeSidecar(storeDir, meta); err != nil {
		logger.App.Error("写入回收站 meta.json 失败（虚拟机已取消定义，请手动恢复）", "vm", name, "dir", storeDir, "error", err)
		return nil, fmt.Errorf("写入回收站侧车文件失败: %v", err)
	}

	deletedAt := time.Now()
	// 保留期 <=0 表示不自动清除：ExpireAt 推到 100 年后（保持列非空可排序）
	var expireAt time.Time
	if retentionDays := RetentionDays(); retentionDays > 0 {
		expireAt = deletedAt.AddDate(0, 0, retentionDays)
	} else {
		expireAt = deletedAt.AddDate(100, 0, 0)
	}

	disksJSON := marshalStringSlice(disks)
	item := &model.VMRecycleItem{
		VMName:          name,
		Owner:           owner,
		IsAdmin:         isAdmin,
		Source:          source,
		TargetNode:      opts.TargetNode,
		Status:          model.VMRecycleStatusRecycled,
		Note:            opts.Note,
		Warnings:        marshalStringSlice(warnings),
		StoreDir:        storeDir,
		XMLPath:         xmlPath,
		NVRAMPath:       nvramPath,
		NVRAMOrigPath:   nvramOrigPath,
		Disks:           disksJSON,
		TotalBytes:      totalBytes,
		CloudType:       cloudType,
		SwitchID:        vpcBinding.SwitchID,
		SecurityGroupID: vpcBinding.SecurityGroupID,
		DeletedBy:       opts.DeletedBy,
		DeletedAt:       deletedAt,
		ExpireAt:        expireAt,
	}
	if err := model.CreateVMRecycleItem(item); err != nil {
		// 侧车文件仍在磁盘上（可手动 virsh define 恢复），loudly 报错
		logger.App.Error("回收站 DB 记录写入失败，虚拟机已取消定义", "vm", name, "dir", storeDir, "error", err)
		return nil, fmt.Errorf("写入回收站记录失败: %v", err)
	}

	// 14. 释放运行时绑定（全部 best-effort，失败累积进 warnings）
	progress(95, "正在释放网络与运行时绑定...")
	releaseBindings(name, owner, vmIPs, &warnings)

	// 持久化累积的 warnings 到 DB 行
	if len(warnings) > 0 {
		wJSON := marshalStringSlice(warnings)
		_ = model.DB.Model(&model.VMRecycleItem{}).Where("id = ?", item.ID).
			Update("warnings", wJSON).Error
		item.Warnings = wJSON
	}

	progress(100, "虚拟机已移入回收站")
	return item, nil
}

// releaseBindings 释放回收站条目的运行时占用（静态 IP、端口转发、统计、VPC、轻量云、定时任务、锁、用户列表、缓存）。
// 凭据保留：恢复后仍需要 guest 登录信息。
func releaseBindings(name, owner string, vmIPs []string, warnings *[]string) {
	if err := netpkg.UnbindStaticIP(name); err != nil {
		*warnings = append(*warnings, fmt.Sprintf("释放静态IP绑定失败: %v", err))
	}
	for _, ip := range vmIPs {
		netpkg.RemovePortForwardsForIP(ip)
	}
	deleteVMStatsRecords(name)
	deleteVMRuntimeRecord(name)
	cleanupVMVPCBinding(name)
	cleanupLightweightVMResources(name)
	if err := deleteVMSchedules(name); err != nil {
		*warnings = append(*warnings, fmt.Sprintf("清理定时任务失败: %v", err))
	}
	if err := model.DeleteVMLock(name); err != nil {
		*warnings = append(*warnings, fmt.Sprintf("删除虚拟机锁失败: %v", err))
	}
	if owner != "" && owner != "admin" {
		if err := removeVMFromUser(owner, name); err != nil {
			*warnings = append(*warnings, fmt.Sprintf("从用户列表移除虚拟机失败: %v", err))
		}
	}
	_ = markVMCacheMissing(name)
}

// collectRecycleDisks 收集磁盘描述与总占用（du -b 口径），首个非 cdrom 盘标记为系统盘。
func collectRecycleDisks(name string) ([]model.VMRecycleDisk, int64) {
	disks, err := disk.ListDisks(name)
	if err != nil {
		return nil, 0
	}
	var result []model.VMRecycleDisk
	var total int64
	first := true
	for _, d := range disks {
		if d.DeviceType == "cdrom" || d.Path == "" {
			continue
		}
		sizeBytes := diskFileSize(d.Path)
		diskEntry := model.VMRecycleDisk{
			Device:     d.Device,
			Path:       d.Path,
			Format:     d.Format,
			IsSystem:   first,
			CapacityGB: d.CapacityGB,
			SizeBytes:  sizeBytes,
		}
		total += sizeBytes
		result = append(result, diskEntry)
		first = false
	}
	return result, total
}

// diskFileSize 通过 du -b 获取磁盘文件实际大小（与 clone/delete.go 口径一致）。
func diskFileSize(path string) int64 {
	duResult := utils.ExecShell(fmt.Sprintf("du -b %s 2>/dev/null | awk '{print $1}'", utils.ShellSingleQuote(path)))
	if duResult.Error == nil {
		size, _ := strconv.ParseInt(strings.TrimSpace(duResult.Stdout), 10, 64)
		return size
	}
	return 0
}

// collectVMIPs 收集虚拟机所有关联 IP（静态绑定 + VPC static hosts + DHCP lease + domifaddr）。
// 移植自 service/clone/delete.go 的同名函数（依赖面不同，避免跨包导出）。
func collectVMIPs(vmName string) []string {
	ipSet := make(map[string]bool)

	mac := ip_resolver.GetFirstVMMAC(vmName)
	if mac == "" {
		return nil
	}

	if ip := ovs.GetOVSStaticIPByMAC(mac); ip != "" {
		ipSet[ip] = true
	}
	if allVpcHosts, err := ovs.ListAllVPCStaticHosts(); err == nil {
		for _, host := range allVpcHosts {
			if strings.EqualFold(host.MAC, mac) {
				ipSet[host.IP] = true
			}
		}
	}
	if ip := ovs.GetOVSLeaseIPByMAC(mac); ip != "" {
		ipSet[ip] = true
	}

	ipRe := regexp.MustCompile(`(\d+\.\d+\.\d+\.\d+)`)
	for _, source := range []string{"agent", "arp", "lease"} {
		addrResult := utils.ExecCommandQuiet("virsh", "domifaddr", vmName, "--source", source)
		if addrResult.Error == nil {
			for _, m := range ipRe.FindAllStringSubmatch(addrResult.Stdout, -1) {
				if m[1] != "127.0.0.1" {
					ipSet[m[1]] = true
				}
			}
		}
	}

	var ips []string
	for ip := range ipSet {
		ips = append(ips, ip)
	}
	return ips
}

// copyFile 拷贝文件并设置权限。
func copyFile(src, dst string, perm os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, perm)
}

// firstErr 返回命令结果中的首个非空错误文本。
func firstErr(r *utils.CmdResult) error {
	if r.Error != nil {
		return r.Error
	}
	if strings.TrimSpace(r.Stderr) != "" {
		return fmt.Errorf("%s", strings.TrimSpace(r.Stderr))
	}
	return fmt.Errorf("命令执行失败")
}

// marshalStringSlice 将任意可 JSON 序列化的值编码为字符串（nil 输入返回 "[]"）。
func marshalStringSlice(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(data)
}

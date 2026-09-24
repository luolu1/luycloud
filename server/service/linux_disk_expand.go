package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"kvm_console/logger"
	"kvm_console/service/guestfs"
	"kvm_console/utils"
)

const linuxSwapPartitionTypeGUID = "0657FD6D-A4AB-43C4-84E5-0933C84B4F4F"

// prepareLinuxSystemDiskExpansion 在 Linux 克隆首次开机前离线调整分区表，使根分区
// 与 qemu-img resize 释放出的空闲空间连续。处理常见布局：swap 分区物理排在 root 之后，
// 阻挡 growpart。此时将 swap 迁移到磁盘末尾，并保留其 UUID/类型/GUID，使 /etc/fstab
// 继续有效；随后由 guest 首次启动的 cloud-init(resize_rootfs) 负责扩展文件系统本身，
// 因为宿主机/appliance 的 e2fsprogs 版本可能过旧，无法处理来宾的 ext4 特性。
func prepareLinuxSystemDiskExpansion(ctx context.Context, cloneDisk string, progressFn func(int, string)) error {
	layout, err := inspectGuestDiskLayout(cloneDisk)
	if err != nil {
		return fmt.Errorf("检测 Linux 磁盘分区失败: %v", err)
	}
	if layout.SectorSize <= 0 || layout.DiskSectors <= 0 {
		return nil
	}
	// 仅处理 GPT；MBR 布局交由 guest cloud-init growpart 处理
	if !strings.EqualFold(layout.PartType, "gpt") {
		return nil
	}

	rootPart := layout.findLinuxRootPartition(cloneDisk)
	if rootPart == nil {
		logger.App.Warn("未识别到 Linux 根分区，跳过离线分区调整", "disk", cloneDisk)
		return nil
	}
	lastPart := layout.lastPartition()
	if lastPart == nil {
		return nil
	}
	lastUsableSector := layout.lastUsableSector()

	// Case A: root 已是最后一个分区 → 直接扩展根分区（文件系统交给 guest resize）
	if rootPart.Num == lastPart.Num {
		rootEnd := bytesToSectorEnd(rootPart.EndBytes, layout.SectorSize)
		if lastUsableSector-rootEnd < defaultPartitionAlignmentSectors {
			return nil
		}
		progressFn(24, "扩展 Linux 根分区...")
		return runWritableGuestfishOperation(ctx, cloneDisk, []string{
			"run",
			fmt.Sprintf("part-expand-gpt %s", layout.Device),
			fmt.Sprintf("part-resize %s %d %d", layout.Device, rootPart.Num, lastUsableSector),
			fmt.Sprintf("blockdev-rereadpt %s", layout.Device),
		}, nil, "Linux 根分区扩容")
	}

	// Case B: root 之后紧跟 swap，且 swap 是最后一个分区 → 迁移 swap 到磁盘末尾
	swapPart := layout.partitionImmediatelyAfter(rootPart)
	if swapPart == nil || !strings.EqualFold(swapPart.FileSystem, "swap") {
		logger.App.Warn("Linux 根分区后不是 swap，跳过离线分区调整", "disk", cloneDisk, "root", rootPart.Num)
		return nil
	}
	if swapPart.Num != lastPart.Num {
		logger.App.Warn("swap 分区后仍有其它分区，无法安全离线扩容", "disk", cloneDisk, "swap", swapPart.Num, "last", lastPart.Num)
		return nil
	}
	// 二次确认 GPT 类型为 Linux swap，避免误删非 swap 分区
	if swapPart.GPTType != "" && !strings.EqualFold(swapPart.GPTType, linuxSwapPartitionTypeGUID) {
		logger.App.Warn("root 后分区 GPT 类型非 Linux swap，拒绝离线扩容", "disk", cloneDisk, "gpt_type", swapPart.GPTType)
		return nil
	}

	swapSectors := bytesToSectorsCeil(swapPart.SizeBytes, layout.SectorSize)
	newSwapStart := alignDown(lastUsableSector-swapSectors+1, defaultPartitionAlignmentSectors)
	newRootEnd := newSwapStart - 1
	rootEnd := bytesToSectorEnd(rootPart.EndBytes, layout.SectorSize)
	if newSwapStart <= rootEnd || newRootEnd-rootEnd < defaultPartitionAlignmentSectors {
		return nil
	}

	// 采集 swap 原 UUID，mkswap -U 保证 /etc/fstab 的 UUID= 与 RESUME= 仍然有效
	swapUUID := readFilesystemUUID(cloneDisk, fmt.Sprintf("%s%d", layout.Device, swapPart.Num))

	progressFn(24, "重排 Linux 分区并扩展根分区...")
	commands := []string{
		"run",
		fmt.Sprintf("part-expand-gpt %s", layout.Device),
		fmt.Sprintf("part-del %s %d", layout.Device, swapPart.Num),
		fmt.Sprintf("part-resize %s %d %d", layout.Device, rootPart.Num, newRootEnd),
		fmt.Sprintf("part-add %s p %d %d", layout.Device, newSwapStart, lastUsableSector),
		fmt.Sprintf("part-set-gpt-type %s %d %s", layout.Device, swapPart.Num, coalesceGUID(swapPart.GPTType, linuxSwapPartitionTypeGUID)),
	}
	if swapPart.GPTGUID != "" {
		commands = append(commands, fmt.Sprintf("part-set-gpt-guid %s %d %s", layout.Device, swapPart.Num, swapPart.GPTGUID))
	}
	if swapPart.Name != "" {
		commands = append(commands, fmt.Sprintf("part-set-name %s %d %s", layout.Device, swapPart.Num, guestfishQuote(swapPart.Name)))
	}
	commands = append(commands, fmt.Sprintf("blockdev-rereadpt %s", layout.Device))
	if swapUUID != "" {
		commands = append(commands, fmt.Sprintf("mkswap %s%d uuid:%s", layout.Device, swapPart.Num, swapUUID))
	} else {
		commands = append(commands, fmt.Sprintf("mkswap %s%d", layout.Device, swapPart.Num))
	}

	if err := runWritableGuestfishOperation(ctx, cloneDisk, commands, nil, "Linux 分区重排扩容"); err != nil {
		return fmt.Errorf("Linux 分区重排扩容失败: %v", err)
	}
	progressFn(26, "Linux 根分区扩容完成（文件系统将在首次启动时扩展）")
	return nil
}

// findLinuxRootPartition 通过 libguestfs inspect 识别根文件系统所在分区，避免"最大 ext 分区"启发式误判。
func (layout *guestDiskLayout) findLinuxRootPartition(diskPath string) *guestDiskPartition {
	rootDev := inspectLinuxRootDevice(diskPath)
	if rootDev == "" {
		return nil
	}
	num := partitionNumFromDevice(layout.Device, rootDev)
	if num <= 0 {
		return nil
	}
	part := layout.partitionByNum(num)
	if part == nil || !isExtFilesystem(part.FileSystem) {
		return nil
	}
	return part
}

// partitionImmediatelyAfter 返回按起始扇区排序后紧跟在 part 之后的分区。
func (layout *guestDiskLayout) partitionImmediatelyAfter(part *guestDiskPartition) *guestDiskPartition {
	var next *guestDiskPartition
	for i := range layout.Partitions {
		p := &layout.Partitions[i]
		if p.StartBytes <= part.StartBytes {
			continue
		}
		if next == nil || p.StartBytes < next.StartBytes {
			next = p
		}
	}
	return next
}

// inspectLinuxRootDevice 运行 guestfish inspect-os，返回根设备（如 /dev/sda2）。
func inspectLinuxRootDevice(diskPath string) string {
	script := fmt.Sprintf(`guestfish --ro -a %s <<'GUESTFISH'
run
inspect-os
GUESTFISH`, utils.ShellSingleQuote(diskPath))
	result := guestfs.ExecShellNoTimeout(script)
	if result.Error != nil {
		return ""
	}
	for _, line := range strings.Split(result.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "/dev/") {
			return line
		}
	}
	return ""
}

// readFilesystemUUID 通过 guestfish vfs-uuid 读取指定分区的文件系统 UUID。
func readFilesystemUUID(diskPath, dev string) string {
	script := fmt.Sprintf(`guestfish --ro -a %s <<'GUESTFISH'
run
vfs-uuid %s
GUESTFISH`, utils.ShellSingleQuote(diskPath), dev)
	result := guestfs.ExecShellNoTimeout(script)
	if result.Error != nil {
		return ""
	}
	return strings.TrimSpace(result.Stdout)
}

// partitionNumFromDevice 从分区设备名（/dev/sda2 或 /dev/nvme0n1p2）解析分区号，要求前缀匹配磁盘设备。
func partitionNumFromDevice(device, partDev string) int {
	if !strings.HasPrefix(partDev, device) {
		return 0
	}
	suffix := strings.TrimPrefix(partDev, device)
	suffix = strings.TrimPrefix(suffix, "p")
	n, err := strconv.Atoi(suffix)
	if err != nil {
		return 0
	}
	return n
}

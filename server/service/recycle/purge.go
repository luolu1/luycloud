package recycle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"kvm_console/config"
	"kvm_console/logger"
	"kvm_console/model"
	"kvm_console/service/clone"
	libvirt_rpc "kvm_console/service/libvirt_rpc"
)

// PurgeItem 永久清除回收站条目：删除磁盘文件、NVRAM、Windows Config Drive ISO、
// 侧车目录与 DB 行。恢复与清除通过 ClaimVMRecycleItem CAS 互斥。
func PurgeItem(itemID uint, operator string, progress func(int, string)) error {
	if progress == nil {
		progress = func(int, string) {}
	}

	item, err := model.GetVMRecycleItem(itemID)
	if err != nil {
		return fmt.Errorf("回收站记录不存在: %v", err)
	}
	if item.Status != model.VMRecycleStatusRecycled {
		return fmt.Errorf("该记录正在被其它操作占用，请稍后重试")
	}

	// CAS 抢占（也是自动清理与用户恢复的竞态保护）
	ok, err := model.ClaimVMRecycleItem(itemID, model.VMRecycleStatusRecycled, model.VMRecycleStatusPurging)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("该记录正在被其它操作占用，请稍后重试")
	}
	completed := false
	defer func() {
		if !completed {
			_ = model.ReleaseVMRecycleItemStatus(itemID, model.VMRecycleStatusRecycled)
		}
	}()

	// 安全闸门：若已存在同名虚拟机（新创建），其磁盘可能位于相同路径 —— 硬中止
	if exists, _ := libvirt_rpc.DomainExistsRPC(item.VMName); exists {
		return fmt.Errorf("存在同名虚拟机 %s，为避免误删其磁盘已拒绝清除，请先处理同名虚拟机", item.VMName)
	}

	var warnings []string

	// 删除记录的磁盘文件（模板目录保护 + 同目录 <name>. 前缀残留扫描）
	progress(20, "正在删除磁盘文件...")
	templateDir := ""
	if config.GlobalConfig != nil {
		templateDir = filepath.Clean(config.GlobalConfig.TemplateDir)
	}
	disks := ParseItemDisks(item.Disks)
	diskDirs := make(map[string]bool)
	for _, d := range disks {
		if d.Path == "" {
			continue
		}
		if templateDir != "" && isPathUnderDir(d.Path, templateDir) {
			logger.App.Warn("跳过删除模板目录下的磁盘文件（模板保护）", "vm", item.VMName, "disk", d.Path)
			warnings = append(warnings, fmt.Sprintf("磁盘 %s 位于模板目录，已跳过删除", d.Path))
			continue
		}
		if err := os.Remove(d.Path); err != nil && !os.IsNotExist(err) {
			warnings = append(warnings, fmt.Sprintf("删除磁盘 %s 失败: %v", d.Path, err))
		}
		if _, seen := diskDirs[filepath.Dir(d.Path)]; !seen {
			diskDirs[filepath.Dir(d.Path)] = true
		}
	}
	// 扫描同目录下的 <name>. 前缀残留（外部快照 overlay 等）
	for dir := range diskDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasPrefix(e.Name(), item.VMName+".") {
				continue
			}
			fullPath := filepath.Join(dir, e.Name())
			if templateDir != "" && isPathUnderDir(fullPath, templateDir) {
				continue
			}
			if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
				warnings = append(warnings, fmt.Sprintf("删除残留文件 %s 失败: %v", fullPath, err))
			}
		}
	}

	// 删除原始 NVRAM（软删除时保留在原地）
	progress(50, "正在清理 NVRAM...")
	if item.NVRAMOrigPath != "" {
		if err := os.Remove(item.NVRAMOrigPath); err != nil && !os.IsNotExist(err) {
			warnings = append(warnings, fmt.Sprintf("删除 NVRAM %s 失败: %v", item.NVRAMOrigPath, err))
		}
	}

	// 清理 Windows Config Drive ISO（若存在）
	clone.CleanupWindowsConfigDriveISO(item.VMName)

	// 删除凭据（清除是终态；恢复路径保留凭据）
	if err := deleteVMCredential(item.VMName); err != nil {
		warnings = append(warnings, fmt.Sprintf("删除虚拟机凭据失败: %v", err))
	}

	// 删除侧车目录与 DB 行
	progress(80, "正在删除回收站记录...")
	if err := removeStoreDir(item.StoreDir); err != nil {
		logger.App.Warn("删除回收站侧车目录失败", "dir", item.StoreDir, "error", err)
	}
	if err := model.DeleteVMRecycleItem(itemID); err != nil {
		logger.App.Error("删除回收站 DB 记录失败", "id", itemID, "error", err)
		warnings = append(warnings, fmt.Sprintf("删除回收站记录失败: %v", err))
	}
	_ = markVMCacheMissing(item.VMName)

	completed = true
	if len(warnings) > 0 {
		logger.App.Warn("回收站清除完成（含告警）", "vm", item.VMName, "operator", operator, "warnings", warnings)
	}
	progress(100, "回收站条目已清除")
	return nil
}

package recycle

import (
	"fmt"
	"os"
	"path/filepath"

	"kvm_console/logger"
	"kvm_console/model"
	libvirt_rpc "kvm_console/service/libvirt_rpc"
	"kvm_console/utils"
)

// RestoreVM 从回收站恢复虚拟机。
// 定义优先、绑定在后：define 成功即认为恢复成立，后续绑定失败只记入 warnings，不回滚 define。
func RestoreVM(itemID uint, operator string, progress func(int, string)) (*model.VMRecycleItem, error) {
	if progress == nil {
		progress = func(int, string) {}
	}

	item, err := model.GetVMRecycleItem(itemID)
	if err != nil {
		return nil, fmt.Errorf("回收站记录不存在: %v", err)
	}
	if item.Status != model.VMRecycleStatusRecycled {
		return nil, fmt.Errorf("该记录正在被其它操作占用，请稍后重试")
	}

	// CAS 抢占：恢复与清除/自动清理互斥
	ok, err := model.ClaimVMRecycleItem(itemID, model.VMRecycleStatusRecycled, model.VMRecycleStatusRestoring)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("该记录正在被其它操作占用，请稍后重试")
	}
	restored := false
	defer func() {
		if !restored {
			_ = model.ReleaseVMRecycleItemStatus(itemID, model.VMRecycleStatusRecycled)
		}
	}()

	// 4. 名称冲突：已存在同名虚拟机时拒绝自动恢复（不自动改名，避免高风险的 XML 重写）
	if exists, _ := libvirt_rpc.DomainExistsRPC(item.VMName); exists {
		return nil, fmt.Errorf("已存在同名虚拟机 %s，请先重命名或删除后再恢复", item.VMName)
	}

	// 5. 磁盘存在性检查：系统盘缺失 = 硬失败；数据盘缺失 = 警告后继续
	progress(20, "正在检查磁盘文件...")
	var warnings []string
	for _, d := range ParseItemDisks(item.Disks) {
		if d.Path == "" {
			continue
		}
		if _, err := os.Stat(d.Path); err != nil {
			if d.IsSystem {
				return nil, fmt.Errorf("系统盘文件 %s 不存在，无法恢复", d.Path)
			}
			warnings = append(warnings, fmt.Sprintf("数据盘 %s 已不存在，恢复后请自行重新挂载", d.Path))
		}
	}

	// 6. 恢复 NVRAM（若存在副本）
	progress(40, "正在恢复虚拟机配置...")
	if item.NVRAMPath != "" && item.NVRAMOrigPath != "" {
		if err := restoreNVRAM(item.NVRAMPath, item.NVRAMOrigPath); err != nil {
			return nil, fmt.Errorf("恢复 NVRAM 失败: %v", err)
		}
	}

	// 7. 定义虚拟机（磁盘路径不变，无需改写 XML）
	if item.XMLPath == "" {
		return nil, fmt.Errorf("回收站记录缺少 XML 路径")
	}
	if _, statErr := os.Stat(item.XMLPath); statErr != nil {
		return nil, fmt.Errorf("回收站 XML 文件不存在: %v", statErr)
	}
	defineResult := utils.ExecCommand("virsh", "define", item.XMLPath)
	if defineResult.Error != nil {
		logger.App.Error("回收站恢复 define 失败", "vm", item.VMName, "xml", item.XMLPath, "error", defineResult.Error)
		return nil, fmt.Errorf("重新定义虚拟机失败: %v", defineResult.Error)
	}

	// 8. 重新绑定（define 成功之后，全部非致命，失败进 warnings）
	progress(70, "正在恢复网络绑定...")
	if item.Owner != "" && item.Owner != "admin" {
		if err := addVMToUser(item.Owner, item.VMName); err != nil {
			warnings = append(warnings, fmt.Sprintf("恢复用户归属失败: %v", err))
		}
	}
	if isLightweightCloudType(item.CloudType) {
		if err := ensureLightweightVMNetwork(item.Owner, item.VMName); err != nil {
			warnings = append(warnings, fmt.Sprintf("恢复轻量云网络失败: %v", err))
		}
	} else if item.SwitchID > 0 {
		if err := bindVMToVPCAsAdmin(item.VMName, item.SwitchID, item.SecurityGroupID); err != nil {
			warnings = append(warnings, fmt.Sprintf("重新绑定 VPC 交换机失败: %v", err))
		}
	}
	// 端口转发规则不自动恢复（VM IP 可能已变化，需用户重新配置）
	warnings = append(warnings, "端口转发规则未自动恢复，请重新配置")
	if err := refreshVMCacheByName(item.VMName); err != nil {
		warnings = append(warnings, fmt.Sprintf("刷新虚拟机缓存失败: %v", err))
	}

	// 9. 保持关机状态，不自动开机

	// 10. 删除 DB 行与侧车目录（DB 先行：残留目录是可观察的无害状态）
	if err := model.DeleteVMRecycleItem(itemID); err != nil {
		logger.App.Warn("删除回收站 DB 记录失败", "id", itemID, "error", err)
	}
	if err := removeStoreDir(item.StoreDir); err != nil {
		logger.App.Warn("删除回收站侧车目录失败", "dir", item.StoreDir, "error", err)
	}
	restored = true

	if len(warnings) > 0 {
		logger.App.Warn("回收站恢复完成（含告警）", "vm", item.VMName, "operator", operator, "warnings", warnings)
	}
	progress(100, "虚拟机已恢复")
	return item, nil
}

// restoreNVRAM 将回收站 NVRAM 副本拷回规范路径。
// 规范路径已有文件则覆盖（该路径属于本 VM；残留文件是删除时的遗留）。
func restoreNVRAM(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("读取回收站 NVRAM 副本失败: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0600); err != nil {
		return err
	}
	return utils.ChownLibvirtQEMU(dst)
}

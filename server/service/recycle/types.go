package recycle

import (
	"encoding/json"

	"kvm_console/model"
)

// VMRecycleMeta 回收站侧车文件（meta.json）结构。
// 恢复操作以侧车文件为数据来源；DB 行仅作为可查询索引。
type VMRecycleMeta struct {
	VMName          string              `json:"vm_name"`
	Owner           string              `json:"owner"`
	IsAdmin         bool                `json:"is_admin"`
	Source          string              `json:"source"`
	TargetNode      string              `json:"target_node,omitempty"`
	Note            string              `json:"note,omitempty"`
	StoragePoolID   string              `json:"storage_pool_id,omitempty"`
	CloudType       string              `json:"cloud_type"`
	SwitchID        uint                `json:"switch_id"`
	SecurityGroupID uint                `json:"security_group_id"`
	NVRAMPath       string              `json:"nvram_path"`      // 回收站内副本路径
	NVRAMOrigPath   string              `json:"nvram_orig_path"` // 原始规范路径
	XMLPath         string              `json:"xml_path"`
	Disks           []model.VMRecycleDisk `json:"disks"`
	TotalBytes      int64               `json:"total_bytes"`
	IPs             []string            `json:"ips,omitempty"` // 删除时收集到的 IP（日志用）
	DeletedBy       string              `json:"deleted_by"`
	Warnings        []string            `json:"warnings,omitempty"`
}

// RestoreTaskParams 恢复任务参数。
type RestoreTaskParams struct {
	ItemID uint `json:"item_id"`
}

// PurgeTaskParams 清除任务参数。
type PurgeTaskParams struct {
	ItemID uint   `json:"item_id"`
	Reason string `json:"reason,omitempty"`
}

// SoftDeleteOptions 软删除选项。
type SoftDeleteOptions struct {
	VMName     string
	Source     string // model.VMRecycleSource*
	Owner      string // 空则通过 FindVMOwner 解析
	DeletedBy  string
	TargetNode string // 仅 migration
	Note       string
	// SkipDestroy: 迁移场景源域已被 virsh migrate 接管/关闭，避免多余 destroy
	SkipDestroy bool
}

// ParseItemDisks 解析 DB 行中的磁盘 JSON。
func ParseItemDisks(raw string) []model.VMRecycleDisk {
	var disks []model.VMRecycleDisk
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &disks)
	}
	return disks
}

// ParseItemWarnings 解析 DB 行中的告警 JSON。
func ParseItemWarnings(raw string) []string {
	var warnings []string
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &warnings)
	}
	return warnings
}

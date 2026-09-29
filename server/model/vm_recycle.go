package model

import (
	"time"

	"kvm_console/logger"
)

// 回收站来源
const (
	VMRecycleSourceUserDelete  = "user_delete"  // 用户自助删除
	VMRecycleSourceAdminDelete = "admin_delete" // 管理员删除
	VMRecycleSourceMigration   = "migration"    // 跨节点迁移后源节点清理
)

// 回收站条目状态（用于避免恢复/清除/自动清理相互竞争）
const (
	VMRecycleStatusRecycled  = "recycled"  // 正常在回收站中
	VMRecycleStatusRestoring = "restoring" // 恢复任务占用中
	VMRecycleStatusPurging   = "purging"   // 清除任务占用中
)

// VMRecycleItem 虚拟机回收站记录。
// 磁盘保持原位不移动；本表是可查询索引，恢复以磁盘上的 meta.json 侧车文件为准。
type VMRecycleItem struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	VMName          string    `gorm:"index;size:255;not null" json:"vm_name"`
	Owner           string    `gorm:"index;size:100;not null;default:''" json:"owner"`
	IsAdmin         bool      `gorm:"not null;default:false" json:"is_admin"`
	Source          string    `gorm:"index;size:32;not null" json:"source"`                   // user_delete/admin_delete/migration
	TargetNode      string    `gorm:"size:255;not null;default:''" json:"target_node"`        // 仅 migration
	Status          string    `gorm:"index;size:16;not null;default:'recycled'" json:"status"`
	Note            string    `gorm:"type:text" json:"note"`
	Warnings        string    `gorm:"type:text" json:"warnings"` // JSON []string
	StoragePoolID   string    `gorm:"size:128;not null;default:''" json:"storage_pool_id"`
	StoreDir        string    `gorm:"size:1024;not null" json:"store_dir"`    // 侧车目录
	XMLPath         string    `gorm:"size:1024;not null" json:"xml_path"`    // 回收站内 domain.xml
	NVRAMPath       string    `gorm:"size:1024;not null;default:''" json:"nvram_path"`       // 回收站内 NVRAM 副本
	NVRAMOrigPath   string    `gorm:"size:1024;not null;default:''" json:"nvram_orig_path"`  // 原始规范路径
	Disks           string    `gorm:"type:text" json:"disks"`               // JSON []VMRecycleDisk
	TotalBytes      int64     `gorm:"not null;default:0" json:"total_bytes"`
	CloudType       string    `gorm:"size:32;not null;default:''" json:"cloud_type"`
	SwitchID        uint      `gorm:"not null;default:0" json:"switch_id"`
	SecurityGroupID uint      `gorm:"not null;default:0" json:"security_group_id"`
	DeletedBy       string    `gorm:"index;size:100;not null;default:''" json:"deleted_by"`
	DeletedAt       time.Time `gorm:"index" json:"deleted_at"`
	ExpireAt        time.Time `gorm:"index" json:"expire_at"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (VMRecycleItem) TableName() string { return "vm_recycle_items" }

// VMRecycleDisk 回收站磁盘描述（序列化进 Disks 字段与 meta.json）。
type VMRecycleDisk struct {
	Device     string `json:"device"`
	Path       string `json:"path"`
	Format     string `json:"format"`
	IsSystem   bool   `json:"is_system"`
	CapacityGB string `json:"capacity_gb"`
	SizeBytes  int64  `json:"size_bytes"`
}

// CreateVMRecycleItem 插入回收站记录。
func CreateVMRecycleItem(item *VMRecycleItem) error {
	if err := DB.Create(item).Error; err != nil {
		logger.App.Error("创建回收站记录失败", "vm", item.VMName, "error", err)
		return err
	}
	return nil
}

// GetVMRecycleItem 按主键查询回收站记录。
func GetVMRecycleItem(id uint) (*VMRecycleItem, error) {
	var item VMRecycleItem
	if err := DB.First(&item, id).Error; err != nil {
		return nil, err
	}
	return &item, nil
}

// ListVMRecycleItems 列出回收站记录。owner 为空表示管理员视角（全部），否则按归属用户过滤。
func ListVMRecycleItems(owner string) ([]VMRecycleItem, error) {
	var items []VMRecycleItem
	query := DB
	if owner != "" {
		query = query.Where("owner = ?", owner)
	}
	if err := query.Order("deleted_at DESC").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// ListExpiredVMRecycleItems 列出到期且仍可清除的回收站记录。
func ListExpiredVMRecycleItems(now time.Time) ([]VMRecycleItem, error) {
	var items []VMRecycleItem
	err := DB.Where("status = ? AND expire_at <= ?", VMRecycleStatusRecycled, now).Find(&items).Error
	return items, err
}

// HasActiveVMRecycleItem 判断是否存在占用该虚拟机名称的回收站记录（非 purging 状态）。
func HasActiveVMRecycleItem(vmName string) bool {
	var count int64
	DB.Model(&VMRecycleItem{}).
		Where("vm_name = ? AND status <> ?", vmName, VMRecycleStatusPurging).
		Count(&count)
	return count > 0
}

// ClaimVMRecycleItem 以单条 CAS UPDATE 抢占状态变更（恢复/清除/自动清理的竞态保护）。
// 成功抢占返回 true；状态不符返回 false。
func ClaimVMRecycleItem(id uint, from, to string) (bool, error) {
	res := DB.Model(&VMRecycleItem{}).
		Where("id = ? AND status = ?", id, from).
		Update("status", to)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// ReleaseVMRecycleItemStatus 释放状态（操作失败时回滚）。
func ReleaseVMRecycleItemStatus(id uint, to string) error {
	return DB.Model(&VMRecycleItem{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"status": to,
		}).Error
}

// DeleteVMRecycleItem 永久删除回收站记录。
func DeleteVMRecycleItem(id uint) error {
	return DB.Delete(&VMRecycleItem{}, id).Error
}

// ResetStuckVMRecycleItems 启动时清理卡死状态：
// 停留在 restoring/purging 超过 maxAge 的记录重置回 recycled。
func ResetStuckVMRecycleItems(maxAge time.Duration) (int64, error) {
	threshold := time.Now().Add(-maxAge)
	res := DB.Model(&VMRecycleItem{}).
		Where("status IN ? AND updated_at < ?", []string{VMRecycleStatusRestoring, VMRecycleStatusPurging}, threshold).
		Update("status", VMRecycleStatusRecycled)
	return res.RowsAffected, res.Error
}

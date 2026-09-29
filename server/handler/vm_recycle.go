package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"kvm_console/model"
	recyclepkg "kvm_console/service/recycle"
	"kvm_console/taskqueue"
)

// vmRecycleItemView 回收站条目响应视图（磁盘/告警解析为数组，对齐前端 VmRecycleItem 接口）。
type vmRecycleItemView struct {
	ID         uint                  `json:"id"`
	VMName     string                `json:"vm_name"`
	Owner      string                `json:"owner"`
	IsAdmin    bool                  `json:"is_admin"`
	Source     string                `json:"source"`
	TargetNode string                `json:"target_node"`
	Status     string                `json:"status"`
	Note       string                `json:"note"`
	Warnings   []string              `json:"warnings"`
	Disks      []model.VMRecycleDisk `json:"disks"`
	TotalBytes int64                 `json:"total_bytes"`
	DeletedBy  string                `json:"deleted_by"`
	DeletedAt  *time.Time            `json:"deleted_at"`
	ExpireAt   *time.Time            `json:"expire_at"`
}

// respondVmRecycleList 回收站列表统一响应（管理员返回全部，普通用户返回本人条目）。
func respondVmRecycleList(c *gin.Context) {
	role, _ := c.Get("role")
	username, _ := c.Get("username")
	usernameStr, _ := username.(string)

	var owner string
	if role != "admin" {
		owner = usernameStr
	}
	items, err := model.ListVMRecycleItems(owner)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "获取回收站列表失败: " + err.Error(),
		})
		return
	}

	views := make([]vmRecycleItemView, 0, len(items))
	for _, item := range items {
		deletedAt := item.DeletedAt
		expireAt := item.ExpireAt
		views = append(views, vmRecycleItemView{
			ID:         item.ID,
			VMName:     item.VMName,
			Owner:      item.Owner,
			IsAdmin:    item.IsAdmin,
			Source:     item.Source,
			TargetNode: item.TargetNode,
			Status:     item.Status,
			Note:       item.Note,
			Warnings:   recyclepkg.ParseItemWarnings(item.Warnings),
			Disks:      recyclepkg.ParseItemDisks(item.Disks),
			TotalBytes: item.TotalBytes,
			DeletedBy:  item.DeletedBy,
			DeletedAt:  &deletedAt,
			ExpireAt:   &expireAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"code":           200,
		"message":        "ok",
		"data":           views,
		"retention_days": recyclepkg.RetentionDays(),
	})
}

// GetVmRecycleList 管理员回收站列表
func GetVmRecycleList(c *gin.Context) {
	respondVmRecycleList(c)
}

// SelfGetVmRecycleList 用户自助回收站列表（管理员仍返回全部）
func SelfGetVmRecycleList(c *gin.Context) {
	respondVmRecycleList(c)
}

// loadVmRecycleItemWithOwnership 加载回收站条目并校验归属（非管理员仅限本人条目）。
func loadVmRecycleItemWithOwnership(c *gin.Context, id uint) (*model.VMRecycleItem, bool) {
	item, err := model.GetVMRecycleItem(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"code":    404,
			"message": "回收站条目不存在",
		})
		return nil, false
	}

	role, _ := c.Get("role")
	username, _ := c.Get("username")
	usernameStr, _ := username.(string)
	if role != "admin" && item.Owner != usernameStr {
		c.JSON(http.StatusForbidden, gin.H{
			"code":    403,
			"message": "无权操作此虚拟机",
		})
		return nil, false
	}
	return item, true
}

// restoreOrPurgeSubmit 提交回收站恢复/清除任务（二次验证与归属校验在调用方完成）。
func restoreOrPurgeSubmit(c *gin.Context, taskType string, params interface{}, message string) {
	username, _ := c.Get("username")
	usernameStr, _ := username.(string)

	task, err := taskqueue.SubmitWithStruct(taskType, params, usernameStr)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "提交任务失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": message,
		"data": gin.H{
			"task_id": task.ID,
		},
	})
}

// parseRecycleItemID 解析 :id 路径参数。
func parseRecycleItemID(c *gin.Context) (uint, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "无效的回收站条目 ID",
		})
		return 0, false
	}
	return uint(id), true
}

// RestoreVmRecycle 从回收站恢复虚拟机（异步任务，管理员/自助路由共用，非管理员限本人条目）
func RestoreVmRecycle(c *gin.Context) {
	if !requireHighRiskVerification(c, "restore_vm") {
		return
	}
	id, ok := parseRecycleItemID(c)
	if !ok {
		return
	}
	if _, ok := loadVmRecycleItemWithOwnership(c, id); !ok {
		return
	}
	restoreOrPurgeSubmit(c, model.TaskTypeRecycleRestore,
		recyclepkg.RestoreTaskParams{ItemID: id}, "恢复任务已提交")
}

// PurgeVmRecycle 永久清除回收站虚拟机（异步任务，管理员/自助路由共用，非管理员限本人条目）
func PurgeVmRecycle(c *gin.Context) {
	if !requireHighRiskVerification(c, "purge_vm") {
		return
	}
	id, ok := parseRecycleItemID(c)
	if !ok {
		return
	}
	if _, ok := loadVmRecycleItemWithOwnership(c, id); !ok {
		return
	}
	restoreOrPurgeSubmit(c, model.TaskTypeRecyclePurge,
		recyclepkg.PurgeTaskParams{ItemID: id, Reason: "手动永久删除"}, "清除任务已提交")
}

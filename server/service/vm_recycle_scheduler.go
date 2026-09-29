package service

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"kvm_console/config"
	"kvm_console/logger"
	"kvm_console/model"
	recyclepkg "kvm_console/service/recycle"
	"kvm_console/taskqueue"
	"kvm_console/utils"
)

const vmRecycleSweeperKey = "vm_recycle_purge_daily"

var vmRecycleSweeperOnce sync.Once

// StartVMRecycleSweeper 启动回收站到期自动清除调度器。
// 每天本地时间 03:00 为每个到期条目提交一个清除任务（条目级隔离）。
// 启动时同时清理卡死状态（restoring/purging 停留超过 1 小时的记录重置回 recycled）。
func StartVMRecycleSweeper() {
	// 启动对账：重置卡死状态行
	if n, err := model.ResetStuckVMRecycleItems(1 * time.Hour); err != nil {
		logger.App.Warn("回收站卡死状态重置失败", "error", err)
	} else if n > 0 {
		logger.App.Info("回收站卡死状态重置完成", "count", n)
	}

	RegisterScheduler(SchedulerDefinition{
		Key:         vmRecycleSweeperKey,
		Name:        "回收站到期自动清除",
		Group:       "虚拟机维护",
		Description: "每天本地时间 03:00 自动清除保留期到期的回收站条目（保留天数在系统设置中调整，0 = 不自动清除）",
		Enabled: func() bool {
			return config.GlobalConfig != nil && config.GlobalConfig.VMRecycleRetentionDays > 0
		},
	})
	vmRecycleSweeperOnce.Do(func() {
		go func() {
			defer utils.RecoverAndLog("vm-recycle-sweeper")
			for {
				now := time.Now()
				next := time.Date(now.Year(), now.Month(), now.Day()+1, 3, 0, 0, 0, now.Location())
				timer := time.NewTimer(time.Until(next))
				<-timer.C
				if config.GlobalConfig != nil && config.GlobalConfig.VMRecycleRetentionDays > 0 {
					if _, err := RunVMRecycleSweeper("每日 03:00 自动清除到期条目"); err != nil {
						logger.App.Warn("回收站到期自动清除失败", "error", err)
					}
				}
			}
		}()
	})
}

// SweepExpiredRecycleItems 为每个到期条目提交一个清除任务，返回提交数量。
func SweepExpiredRecycleItems(createdBy string) (int, error) {
	items, err := model.ListExpiredVMRecycleItems(time.Now())
	if err != nil {
		return 0, err
	}
	submitted := 0
	for _, item := range items {
		// 已有等待中/运行中的同条目清除任务则跳过，避免队列刷屏
		itemID := item.ID
		if taskqueue.HasActiveTask(model.TaskTypeRecyclePurge, func(raw string) bool {
			var params recyclepkg.PurgeTaskParams
			if json.Unmarshal([]byte(raw), &params) != nil {
				return false
			}
			return params.ItemID == itemID
		}) {
			continue
		}
		if _, err := taskqueue.SubmitWithStruct(model.TaskTypeRecyclePurge, recyclepkg.PurgeTaskParams{
			ItemID: item.ID,
			Reason: "保留期到期自动清除",
		}, createdBy); err != nil {
			logger.App.Warn("提交回收站清除任务失败", "item_id", item.ID, "vm", item.VMName, "error", err)
			continue
		}
		submitted++
	}
	return submitted, nil
}

// RunVMRecycleSweeper 调度事件记录入口（调度器中心手动触发时使用）。
func RunVMRecycleSweeper(triggerReason string) (string, error) {
	if triggerReason == "" {
		triggerReason = "回收站到期自动清除"
	}
	event, _ := StartSchedulerEvent(SchedulerEventStartInput{
		SchedulerKey: vmRecycleSweeperKey, SchedulerName: "回收站到期自动清除",
		SchedulerGroup: "虚拟机维护", TriggerReason: triggerReason,
	})
	n, err := SweepExpiredRecycleItems("system:scheduler")
	if event != nil {
		if err != nil {
			_ = FinishSchedulerEventFailed(event, err.Error())
			return "", err
		}
		_ = FinishSchedulerEventSuccess(event, fmt.Sprintf("提交清除任务 %d 个", n))
	}
	return fmt.Sprintf("提交清除任务 %d 个", n), nil
}

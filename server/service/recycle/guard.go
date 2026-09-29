package recycle

import (
	"fmt"

	"kvm_console/model"
)

// EnsureNameNotRecycled 阻止新建虚拟机占用回收站中仍存在的名称。
// 在虚拟机名称校验的单一入口处调用（覆盖创建、克隆、批量克隆、导入）。
func EnsureNameNotRecycled(name string) error {
	if model.HasActiveVMRecycleItem(name) {
		return fmt.Errorf("虚拟机名称 %s 已被回收站中的记录占用，请先清除或恢复该回收站条目后再使用此名称", name)
	}
	return nil
}

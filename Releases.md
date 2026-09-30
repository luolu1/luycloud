# Releases

## v1.1.1

Release date: 2026-09-30

### English (Default)

#### Added

- Added the VM creation time to the VM detail page under **System Information**.
- The creation time is displayed in the basic configuration section and uses the existing VM detail API field.

#### Fixed

- After a successful cross-node migration, the source VM definition is now moved to the recycle bin instead of remaining as an active source-side copy.
- Source VM disks remain in place and can be recovered through the recycle bin.
- A source recycle-bin failure is reported as a migration warning without incorrectly marking an already completed migration as failed.
- Fixed migration failures caused by CD-ROM ISO files that exist on the source node but are unavailable on the target node.

#### Documentation

- Updated the sponsor attribution in `README.md` to credit ForZTN.

### 中文

#### 新增

- 在虚拟机详情页的**系统信息**中增加虚拟机创建时间。
- 创建时间显示在基本配置区域，使用现有虚拟机详情接口字段。

#### 修复

- 跨节点迁移成功后，源节点上的虚拟机定义现在会自动移入回收站，不再作为源节点副本继续保留。
- 源虚拟机磁盘保持原位置，可通过回收站恢复。
- 源副本移入回收站失败时会记录为迁移警告，不会错误地将已经完成的迁移标记为失败。
- 修复源节点存在但目标节点不存在 CD-ROM ISO 文件时导致迁移失败的问题。

#### 文档

- 更新 `README.md` 赞助说明，改为感谢 ForZTN 赞助测试机器。

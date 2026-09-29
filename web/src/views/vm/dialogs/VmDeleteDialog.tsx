/**
 * 删除虚拟机确认弹窗
 * - 单台：可勾选要删除的磁盘，未勾选磁盘转移到「我的存储 - 虚拟磁盘」
 * - 批量：同时删除所有虚拟机及其磁盘文件
 */
import { useEffect, useMemo, useState } from 'react'
import { Banner, Checkbox, Modal, Spin, Tag, Toast } from '@douyinfe/semi-ui'
import type { VmListItem, VmQcow2Disk, DeleteVmData } from '@/api/vm'
import { deleteVm, getVmQcow2Disks, selfDeleteVm, selfGetVmQcow2Disks } from '@/api/vm'
import { getStorageInfo } from '@/api/storage'

interface VmDeleteDialogProps {
  visible: boolean
  /** 单台删除 */
  vm?: VmListItem
  /** 批量删除 */
  batch?: VmListItem[]
  isAdmin: boolean
  onClose: () => void
  onSuccess: () => void
}

export default function VmDeleteDialog({
  visible,
  vm,
  batch,
  isAdmin,
  onClose,
  onSuccess,
}: VmDeleteDialogProps) {
  const isBatch = !!batch && batch.length > 0
  const [loadingDisks, setLoadingDisks] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [diskList, setDiskList] = useState<VmQcow2Disk[]>([])
  const [selectedDisks, setSelectedDisks] = useState<string[]>([])
  const [storageInitialized, setStorageInitialized] = useState(false)
  // 跳过回收站，直接永久删除（默认关闭 = 移入回收站）
  const [skipRecycle, setSkipRecycle] = useState(false)

  // 弹窗打开时重置「跳过回收站」为关闭
  useEffect(() => {
    if (visible) setSkipRecycle(false)
  }, [visible])

  // 单台删除时加载磁盘列表（非管理员同时检查存储池状态）
  useEffect(() => {
    if (!visible || isBatch || !vm) return
    let cancelled = false
    setDiskList([])
    setSelectedDisks([])
    setLoadingDisks(true)
    const load = async () => {
      try {
        const [diskRes, storageRes] = await Promise.all([
          isAdmin ? getVmQcow2Disks(vm.name) : selfGetVmQcow2Disks(vm.name),
          isAdmin ? Promise.resolve(null) : getStorageInfo().catch(() => null),
        ])
        if (cancelled) return
        const disks = Array.isArray(diskRes.data) ? diskRes.data : []
        setDiskList(disks)
        setSelectedDisks(disks.map((d) => d.path))
        setStorageInitialized(isAdmin ? true : (storageRes?.data?.initialized ?? false))
      } catch (err) {
        console.error('获取磁盘列表失败', err)
      } finally {
        if (!cancelled) setLoadingDisks(false)
      }
    }
    void load()
    return () => {
      cancelled = true
    }
  }, [visible, isBatch, vm, isAdmin])

  // 未勾选的磁盘（需要转移的）
  const transferDisks = useMemo(
    () => diskList.filter((d) => !selectedDisks.includes(d.path)),
    [diskList, selectedDisks],
  )
  // 磁盘转移仅在永久删除时才涉及，移入回收站时不会转移磁盘
  const transferBlocked =
    skipRecycle && transferDisks.length > 0 && !storageInitialized && !isAdmin

  const handleConfirm = async () => {
    setDeleting(true)
    try {
      if (isBatch) {
        const results = await Promise.allSettled(
          batch!.map((item) =>
            isAdmin
              ? deleteVm(item.name, { skip_recycle: skipRecycle })
              : selfDeleteVm(item.name, { skip_recycle: skipRecycle }),
          ),
        )
        const successCount = results.filter((r) => r.status === 'fulfilled').length
        const failCount = results.length - successCount
        if (failCount === 0) {
          Toast.success(
            skipRecycle
              ? `批量删除完成，成功 ${successCount} 台`
              : `已提交，${successCount} 台虚拟机将移入回收站`,
          )
        } else {
          Toast.warning(`批量删除完成。成功: ${successCount}, 失败: ${failCount}`)
        }
      } else if (vm) {
        // 移入回收站时不透传磁盘删除/转移选项，磁盘保持原位以便恢复
        const data: DeleteVmData = skipRecycle
          ? {
              skip_recycle: true,
              ...(diskList.length > 0
                ? {
                    delete_disks: selectedDisks,
                    transfer_disks: transferDisks.map((d) => d.path),
                  }
                : {}),
            }
          : { skip_recycle: false }
        if (isAdmin) {
          await deleteVm(vm.name, data)
        } else {
          await selfDeleteVm(vm.name, data)
        }
        if (!skipRecycle) {
          Toast.success('已提交，虚拟机将移入回收站')
        } else {
          Toast.success(
            transferDisks.length > 0
              ? '删除任务已提交，未勾选的磁盘将转移到「我的存储 - 虚拟磁盘」'
              : '删除任务已提交',
          )
        }
      }
      onClose()
      onSuccess()
    } catch {
      // 错误提示由请求层统一处理
    } finally {
      setDeleting(false)
    }
  }

  return (
    <Modal
      title={isBatch ? '批量删除虚拟机' : `删除虚拟机 - ${vm?.name || ''}`}
      visible={visible}
      onCancel={onClose}
      onOk={() => void handleConfirm()}
      okText={skipRecycle ? '永久删除' : '移入回收站'}
      cancelText="取消"
      okButtonProps={{ type: 'danger', loading: deleting, disabled: transferBlocked }}
      width={600}
      closeOnEsc
    >
      <Spin spinning={loadingDisks}>
        {skipRecycle ? (
          <Banner
            type="danger"
            closeIcon={null}
            style={{ marginBottom: 14 }}
            description={
              isBatch ? (
                `确定要永久删除选中的 ${batch?.length || 0} 台虚拟机吗？永久删除不可恢复！磁盘文件将被立即删除。`
              ) : (
                <>
                  确定要永久删除虚拟机 <strong>{vm?.name}</strong> 吗？永久删除不可恢复！磁盘文件将被立即删除。
                </>
              )
            }
          />
        ) : (
          <Banner
            type="warning"
            closeIcon={null}
            style={{ marginBottom: 14 }}
            description={
              isBatch ? (
                `选中的 ${batch?.length || 0} 台虚拟机将移入回收站，可在「回收站」页面恢复。`
              ) : (
                <>
                  虚拟机 <strong>{vm?.name}</strong> 将移入回收站，可在「回收站」页面恢复。
                </>
              )
            }
          />
        )}

        {/* 跳过回收站开关（单台 / 批量均适用） */}
        <div style={{ marginBottom: 14 }}>
          <Checkbox
            checked={skipRecycle}
            onChange={(e) => setSkipRecycle(!!e.target.checked)}
          >
            跳过回收站，永久删除
          </Checkbox>
        </div>

        {/* 单台永久删除：磁盘选择（仅永久删除时展示） */}
        {skipRecycle && !isBatch && diskList.length > 0 && (
          <>
            <div className="qvm-del-label">选择要删除的磁盘：</div>
            <Checkbox.Group
              style={{ display: 'flex', flexDirection: 'column', gap: 8 }}
              value={selectedDisks}
              onChange={(values) => setSelectedDisks(values as string[])}
            >
              {diskList.map((disk) => (
                <Checkbox key={disk.path} value={disk.path} disabled={disk.is_system}>
                  <span className="qvm-mono">{disk.device}</span>
                  <Tag size="small" style={{ marginLeft: 8 }}>
                    {disk.format}
                  </Tag>
                  <span className="qvm-del-disk-meta">容量: {disk.capacity_gb} GB</span>
                  {disk.is_system && (
                    <Tag size="small" color="red" style={{ marginLeft: 8 }}>
                      系统盘
                    </Tag>
                  )}
                  <div className="qvm-del-disk-path">{disk.path}</div>
                </Checkbox>
              ))}
            </Checkbox.Group>

            {transferBlocked ? (
              <Banner
                type="danger"
                closeIcon={null}
                style={{ marginTop: 14 }}
                description="您尚未开通「我的存储」，无法转移磁盘。请先前往「我的存储」页面初始化存储池，或勾选所有磁盘直接删除。"
              />
            ) : (
              transferDisks.length > 0 && (
                <Banner
                  type="warning"
                  closeIcon={null}
                  style={{ marginTop: 14 }}
                  description={
                    <>
                      以下磁盘将转移到「我的存储 - 虚拟磁盘」中：
                      {transferDisks.map((disk) => (
                        <div key={disk.path} style={{ marginTop: 4 }}>
                          <span className="qvm-mono">{disk.device}</span>
                          <span style={{ marginLeft: 8 }}>{disk.path}</span>
                        </div>
                      ))}
                    </>
                  }
                />
              )
            )}
          </>
        )}

        {/* 批量永久删除：简化提示（仅永久删除时展示磁盘警告） */}
        {isBatch && skipRecycle && (
          <>
            <div className="qvm-del-batch-tip">将同时删除所有虚拟机的磁盘文件！</div>
            <div className="qvm-del-batch-list">
              {batch!.map((item) => (
                <Tag key={item.name} style={{ margin: 4 }}>
                  {item.name}
                </Tag>
              ))}
            </div>
          </>
        )}

        {/* 批量移入回收站：仅列出虚拟机 */}
        {isBatch && !skipRecycle && (
          <div className="qvm-del-batch-list">
            {batch!.map((item) => (
              <Tag key={item.name} style={{ margin: 4 }}>
                {item.name}
              </Tag>
            ))}
          </div>
        )}
      </Spin>
    </Modal>
  )
}

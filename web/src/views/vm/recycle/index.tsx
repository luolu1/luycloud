/**
 * 回收站页（管理员看全部，普通用户看自己的）
 * - 被软删除的虚拟机在此列出，磁盘原位保留，可在保留期内恢复
 * - 行内一个「恢复」图标（Tooltip）+ ⋯ 下拉（永久删除，type=danger），符合 rule 28
 * - 恢复 / 永久删除均入队任务，任务进行中时对应行图标显示 IconRefresh spin
 * - 展开行显示磁盘路径与警告（如快照元数据丢失提示）
 */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Button, Dropdown, Empty, Table, Tag, Toast, Tooltip } from '@douyinfe/semi-ui'
import {
  IconRefresh,
  IconMore,
  IconUndo,
  IconDelete,
  IconAlertTriangle,
} from '@douyinfe/semi-icons'
import type { ColumnProps } from '@douyinfe/semi-ui/lib/es/table'
import {
  getVmRecycleList,
  selfGetVmRecycleList,
  restoreVmRecycle,
  selfRestoreVmRecycle,
  purgeVmRecycle,
  selfPurgeVmRecycle,
  type VmRecycleItem,
} from '@/api/vm'
import { useUserStore } from '@/stores/user'
import { useTaskStore, TERMINAL_STATUSES } from '@/stores/task'
import { ROLES } from '@/config/constants'
import { formatBytes, formatDateTime } from '@/utils/format'
import { confirmModal } from '@/utils/confirm'
import './recycle.css'

/** 来源标签配色与文案 */
function sourceMeta(source: VmRecycleItem['source']): {
  text: string
  color: 'blue' | 'orange' | 'violet' | 'grey'
} {
  const map: Record<VmRecycleItem['source'], { text: string; color: 'blue' | 'orange' | 'violet' }> = {
    user_delete: { text: '用户删除', color: 'blue' },
    admin_delete: { text: '管理员删除', color: 'orange' },
    migration: { text: '迁移', color: 'violet' },
  }
  return map[source] || { text: source, color: 'grey' }
}

/** 剩余时间是否不足 24 小时 */
function isExpiringSoon(expireAt: string): boolean {
  const t = new Date(expireAt).getTime()
  if (Number.isNaN(t)) return false
  const remain = t - Date.now()
  return remain > 0 && remain < 24 * 3600 * 1000
}

export default function VmRecyclePage() {
  const role = useUserStore((s) => s.role)
  const isAdmin = role === ROLES.admin

  const [items, setItems] = useState<VmRecycleItem[]>([])
  const [loading, setLoading] = useState(false)
  const [retentionDays, setRetentionDays] = useState<number | null>(null)

  // itemId -> 关联的任务 id（恢复 / 清除进行中，用于行内 spin）
  const [pendingTasks, setPendingTasks] = useState<Record<number, number>>({})
  const pendingTasksRef = useRef(pendingTasks)
  pendingTasksRef.current = pendingTasks

  const tasks = useTaskStore((s) => s.tasks)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const res = isAdmin ? await getVmRecycleList() : await selfGetVmRecycleList()
      setItems(Array.isArray(res.data) ? res.data : [])
      // 保留天数由后端在响应外层附带（若无则不展示具体数字）
      const raw = res as unknown as { retention_days?: number }
      if (typeof raw.retention_days === 'number') setRetentionDays(raw.retention_days)
    } catch {
      // 请求层已统一提示
    } finally {
      setLoading(false)
    }
  }, [isAdmin])

  useEffect(() => {
    void load()
  }, [load])

  // 监听任务进度：关联的恢复/清除任务进入终态后，清理 pending 并刷新列表
  useEffect(() => {
    const current = pendingTasksRef.current
    const activeTaskIds = Object.values(current)
    if (activeTaskIds.length === 0) return
    let anyFinished = false
    const nextPending: Record<number, number> = {}
    for (const [itemIdStr, taskId] of Object.entries(current)) {
      const task = tasks.find((t) => t.id === taskId)
      if (task && TERMINAL_STATUSES.includes(task.status)) {
        anyFinished = true
      } else {
        nextPending[Number(itemIdStr)] = taskId
      }
    }
    if (anyFinished) {
      setPendingTasks(nextPending)
      void load()
    }
  }, [tasks, load])

  const handleRestore = useCallback(
    async (item: VmRecycleItem) => {
      const ok = await confirmModal({
        title: `恢复虚拟机 - ${item.vm_name}`,
        content: (
          <div>
            <div>确定要从回收站恢复虚拟机「{item.vm_name}」吗？</div>
            <div style={{ marginTop: 8, color: 'var(--qvm-text-2)', fontSize: 12.5 }}>
              恢复后虚拟机将保持关机状态；若存在同名在线虚拟机则无法恢复。
            </div>
          </div>
        ),
        okText: '恢复',
      })
      if (!ok) return
      try {
        const res = isAdmin
          ? await restoreVmRecycle(item.id)
          : await selfRestoreVmRecycle(item.id)
        const taskId = Number(res.data?.task_id)
        if (Number.isFinite(taskId) && taskId > 0) {
          setPendingTasks((prev) => ({ ...prev, [item.id]: taskId }))
        }
        Toast.success('恢复任务已提交，请在任务中心查看进度')
      } catch {
        // 请求层已统一提示（含二次验证流程）
      }
    },
    [isAdmin],
  )

  const handlePurge = useCallback(
    async (item: VmRecycleItem) => {
      const ok = await confirmModal({
        title: `永久删除 - ${item.vm_name}`,
        content: (
          <div>
            <div style={{ color: 'var(--qvm-danger)', fontWeight: 600 }}>
              此操作不可恢复！以下磁盘文件将被永久删除：
            </div>
            <div className="rcb-purge-disks">
              {item.disks.length > 0 ? (
                item.disks.map((d) => (
                  <div className="rcb-purge-disk-item" key={d.path}>
                    {d.path}
                  </div>
                ))
              ) : (
                <div className="rcb-purge-disk-item">（无磁盘记录）</div>
              )}
            </div>
          </div>
        ),
        okText: '永久删除',
        danger: true,
      })
      if (!ok) return
      try {
        const res = isAdmin ? await purgeVmRecycle(item.id) : await selfPurgeVmRecycle(item.id)
        const taskId = Number(res.data?.task_id)
        if (Number.isFinite(taskId) && taskId > 0) {
          setPendingTasks((prev) => ({ ...prev, [item.id]: taskId }))
        }
        Toast.success('永久删除任务已提交，请在任务中心查看进度')
      } catch {
        // 请求层已统一提示（含二次验证流程）
      }
    },
    [isAdmin],
  )

  const columns = useMemo<ColumnProps<VmRecycleItem>[]>(() => {
    const cols: ColumnProps<VmRecycleItem>[] = [
      {
        title: '虚拟机名',
        dataIndex: 'vm_name',
        width: 180,
        render: (text) => <span className="qvm-mono">{text}</span>,
      },
    ]

    if (isAdmin) {
      cols.push({
        title: '归属用户',
        dataIndex: 'owner',
        width: 130,
        render: (text, row) =>
          row.is_admin ? <Tag size="small" color="orange">管理员</Tag> : text || '-',
      })
    }

    cols.push(
      {
        title: '来源',
        dataIndex: 'source',
        width: 130,
        render: (_text, row) => {
          const meta = sourceMeta(row.source)
          const tag = (
            <Tag size="small" color={meta.color}>
              {meta.text}
            </Tag>
          )
          if (row.source === 'migration' && row.target_node) {
            return (
              <Tooltip content={`迁移至 ${row.target_node}`} position="top">
                <span className="rcb-source-cell">{tag}</span>
              </Tooltip>
            )
          }
          return <span className="rcb-source-cell">{tag}</span>
        },
      },
      {
        title: '磁盘 / 占用',
        dataIndex: 'total_bytes',
        width: 140,
        render: (_text, row) => (
          <div className="rcb-disk-cell">
            <span className="rcb-disk-count">{row.disks.length} 块磁盘</span>
            <span className="rcb-disk-size">{formatBytes(row.total_bytes)}</span>
          </div>
        ),
      },
      {
        title: '删除时间',
        dataIndex: 'deleted_at',
        width: 170,
        render: (text) => <span className="qvm-mono">{formatDateTime(text)}</span>,
      },
      {
        title: '到期时间',
        dataIndex: 'expire_at',
        width: 190,
        render: (text) => {
          const soon = isExpiringSoon(text)
          return (
            <Tag size="small" color={soon ? 'red' : 'grey'}>
              {formatDateTime(text)}
            </Tag>
          )
        },
      },
      {
        title: '操作',
        dataIndex: 'ops',
        width: 110,
        align: 'center',
        render: (_text, row) => {
          const running = !!pendingTasks[row.id]
          return (
            <div className="qvm-act-cell" style={{ justifyContent: 'center' }}>
              <Tooltip content={running ? '任务进行中' : '恢复'} position="top">
                <span
                  className={`qvm-act-ic vnc ${running ? 'disabled' : ''}`}
                  onClick={() => !running && void handleRestore(row)}
                >
                  {running ? <IconRefresh spin /> : <IconUndo />}
                </span>
              </Tooltip>
              <Dropdown
                trigger="click"
                position="bottomRight"
                clickToHide
                render={
                  <Dropdown.Menu>
                    <Dropdown.Item
                      icon={<IconDelete />}
                      type="danger"
                      disabled={running}
                      onClick={() => void handlePurge(row)}
                    >
                      永久删除
                    </Dropdown.Item>
                  </Dropdown.Menu>
                }
              >
                <span className={`qvm-act-ic more ${running ? 'disabled' : ''}`}>
                  {running ? <IconRefresh spin /> : <IconMore />}
                </span>
              </Dropdown>
            </div>
          )
        },
      },
    )

    return cols
  }, [isAdmin, pendingTasks, handleRestore, handlePurge])

  const retentionHint =
    retentionDays === null
      ? '到期后自动永久删除（保留天数可在系统设置中调整）'
      : retentionDays === 0
        ? '当前设置为不自动清除，回收站中的虚拟机需手动永久删除'
        : `到期后自动永久删除（保留 ${retentionDays} 天，可在系统设置中调整）`

  return (
    <div className="rcb-page">
      <div className="rcb-page-header qvm-fade-up">
        <div>
          <h2>
            <IconDelete style={{ marginRight: 8, color: 'var(--qvm-acc-ink)' }} />
            回收站
          </h2>
          <p className="rcb-page-sub">{retentionHint}</p>
        </div>
        <div className="rcb-header-actions">
          <Button icon={<IconRefresh />} loading={loading} onClick={() => void load()}>
            刷新
          </Button>
        </div>
      </div>

      <div className="rcb-section-card qvm-fade-up">
        <Table<VmRecycleItem>
          rowKey="id"
          columns={columns}
          dataSource={items}
          loading={loading}
          pagination={false}
          size="small"
          empty={<Empty description="回收站为空" style={{ padding: '32px 0' }} />}
          expandedRowRender={(row) =>
            row && (row.disks.length > 0 || row.warnings.length > 0) ? (
              <div className="rcb-expand">
                {row.disks.length > 0 && (
                  <div>
                    <div className="rcb-expand-title">磁盘文件</div>
                    {row.disks.map((d) => (
                      <div className="rcb-disk-row" key={d.path}>
                        <span className="qvm-mono">{d.device}</span>
                        <Tag size="small">{d.format}</Tag>
                        {d.is_system && (
                          <Tag size="small" color="red">
                            系统盘
                          </Tag>
                        )}
                        <span>{d.capacity_gb} GB</span>
                        <span className="rcb-disk-path">{d.path}</span>
                      </div>
                    ))}
                  </div>
                )}
                {row.warnings.length > 0 && (
                  <div>
                    <div className="rcb-expand-title">提示</div>
                    {row.warnings.map((w, i) => (
                      <div className="rcb-warning-line" key={i}>
                        <IconAlertTriangle size="small" />
                        <span>{w}</span>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            ) : null
          }
        />
      </div>
    </div>
  )
}

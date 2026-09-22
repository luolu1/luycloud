# UEFI NVRAM 格式随 libvirt 版本自适应（黑屏修复）

## 现象

UEFI（含安全启动）虚拟机开机后 VNC 一直提示 `Guest has not initialized the display (yet).`，截图为 QEMU 占位画面（640×480、极少颜色），或 RIP 卡在固定地址的紧循环、甚至 `KVM internal error emulation failure`。更换显卡型号（virtio-gpu / virtio-vga / bochs / std-VGA）都无法解决。

## 根因

历史代码把 UEFI NVRAM 一律创建为 **qcow2**，并在 domain XML 写入 `<nvram template='...' templateFormat='raw' format='qcow2'>`。但 `<nvram>` 的相关属性是逐版本引入的：

| 能力 | 最低 libvirt 版本 |
|------|-------------------|
| `<nvram format=...>` | 9.2.0 |
| UEFI(pflash) 内部快照（需 qcow2 NVRAM） | 10.9.0 |
| `<nvram templateFormat=...>` | 10.10.0 |

在低于 9.2.0 的 libvirt（例如 Ubuntu 22.04 的 **8.0.0**）上，libvirt 会在 `define` 时**静默丢弃** `format`/`templateFormat` 属性，然后按 **raw** 加载 pflash（可在生成的 QEMU 命令行看到 `"driver":"raw"`）。而磁盘文件实际是 qcow2，OVMF 就把 qcow2 容器头当成变量存储读取，固件无法初始化显示——即黑屏。

实测：在同一台 libvirt 8.0.0 主机上，把该 NVRAM 重新生成为 **raw** 后，virtio 显卡即可正常出画面（800×600），无需改用 bochs。此前“OVMF 不支持 virtio-gpu，需改 bochs”的判断是误诊——真正原因是 NVRAM 格式与 libvirt 实际加载格式不一致。

## 核心不变量

> 磁盘上 NVRAM 的真实格式，必须等于 libvirt 实际使用的 pflash 格式。当 XML 无法声明 format 时，libvirt 用 raw，磁盘文件就必须是 raw。

## 实现

- `server/service/vm_xml/nvram_capability.go`：探测并缓存宿主机 libvirt 版本（`virsh version`，回退 `libvirtd --version`），提供 `SupportsNVRAMFormatAttr`、`SupportsNVRAMTemplateFormatAttr`、`SupportsPflashInternalSnapshot` 与 `PreferredNVRAMFormat`。探测失败按最旧版本降级（raw、无 format 属性），fail-safe。
  - **本次发布策略：`PreferredNVRAMFormat` 始终返回 raw**（raw 在 libvirt 8.0→最新全版本可用）。待具备 ≥10.9.0 的宿主机可供验证后，再放开 qcow2 分支以支持 UEFI 内部快照。
- `BuildNVRAMElementXML`：所有创建/克隆/导入路径写 `<nvram>` 的唯一入口，按版本决定是否声明 `format`/`templateFormat`，杜绝各处手写 qcow2 字面量再次引入格式错配。
- `CreateNVRAMFromTemplate` / `ConvertNVRAMFormat` / `EnsureNVRAMFormatMatches`：按策略生成/转换 NVRAM，格式一致时按字节复制（位精确），带备份与失败回滚。
- `EnsureVMUEFINVRAMFile`：生命周期统一自愈入口。文件缺失则按策略生成；格式不符时**仅在虚拟机关机状态下**转换（运行/暂停时 QEMU 持有 pflash，仅告警并跳过，待下次关机自愈）；无法识别格式时明确报错，不再把“未知”当“兼容”。
- 快照：`server/service/snapshot/nvram.go` 在 libvirt < 10.9.0 时直接返回明确的终止错误（UEFI pflash 内部快照不受支持），**不再**把工作正常的 raw NVRAM 转成 qcow2——那会把健康虚拟机变成黑屏机，且快照仍会失败。`CheckInternalSnapshotNVRAMRepairRequired` 在旧版本上返回“无需修复”，避免提示用户做无意义的关机修复。
- 迁移：`prepareMigrationNVRAMOnTarget` 探测**目标节点** libvirt 版本决定目标格式，并传输**源虚拟机真实 NVRAM**（保留启动项与安全启动密钥）而非从模板重建；目标不支持 `format` 属性时从 XML 中一并移除该属性。
- `SetShimFallbackNoReboot`：`virt-fw-vars` 只认 raw 变量存储，qcow2 输入会被误扫描（静默损坏）。现改为“qcow2→raw→写标记→转回原格式”，并用 `virt-fw-vars --print` 成功解析来校验结果。
- 兼容性实机测试新增断言：UEFI 虚拟机创建后，磁盘 NVRAM 真实格式必须与当前版本策略一致（正是历史被静默违反的不变量）。
- 撤销此前的 `ApplyUEFIVideoModelWorkaround`（virtio→bochs），因其基于误诊，且 bochs 相比 virtio-gpu 存在无 3D、单头、动态分辨率差、Windows 无厂商驱动等能力回退。

## 既有虚拟机修复

在 libvirt 8.0 上被旧代码创建的黑屏虚拟机，其持久化 XML 实际已是干净的 `<nvram template='...'>`（属性在 define 时被 libvirt 剥离），**只需修复磁盘文件**：关机后由任一生命周期操作（或引导方式设置）触发 `EnsureVMUEFINVRAMFile`，会把 qcow2 就地转换为 raw（原文件备份为 `*.qcow2.bak`）。备份文件不会自动删除，以便回滚。

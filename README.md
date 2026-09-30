<div align="center">

<img width="1280" height="625" alt="luycloud management interface preview" src="README-preview.jpg" />

# ☁️ luycloud

**Open-source, lightweight, all-in-one KVM virtual machine management console**

An integrated private cloud platform built around KVM/QEMU, covering virtual machine lifecycles, network and storage orchestration, snapshot cloning, firewall management, and bandwidth governance.

<br/>

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![GitHub Stars](https://img.shields.io/github/stars/luolu1/luycloud?style=social)](https://github.com/luolu1/luycloud)
[![GitHub Forks](https://img.shields.io/github/forks/luolu1/luycloud?style=social)](https://github.com/luolu1/luycloud)
[![GitHub Issues](https://img.shields.io/github/issues/luolu1/luycloud)](https://github.com/luolu1/luycloud/issues)
[![GitHub Pull Requests](https://img.shields.io/github/issues-pr/luolu1/luycloud)](https://github.com/luolu1/luycloud/pulls)

<br/>

[English](README.md) · [简体中文](README.zh-CN.md) · [繁體中文](README.zh-TW.md) · [日本語](README.ja.md)

[**🚀 Quick Deployment**](#-quick-deployment) · [**✨ Core Features**](#-core-features) · [**🧰 Technology Stack**](#-technology-stack) · [**🤝 Contribution Guide**](#-contribution-guide) · [**💬 Report an Issue**](https://github.com/luolu1/luycloud/issues)

</div>

---

## 📖 Overview

luycloud is an open-source virtual machine management platform for small businesses and personal private cloud deployments. It deeply integrates KVM/QEMU virtualization and provides a complete solution spanning virtual machine lifecycle management, network and storage orchestration, snapshots and cloning, firewall and bandwidth governance, and an integrated Web console and API.

> 💡 luycloud is derived from QVMConsole, removes some of the original limitations, and uses an independent brand and deployment model for flexible extension and private deployment.

### Core Value

| | Value | Description |
|:---:|:---|:---|
| 🎯 | **Lower the operations barrier** | Provides a ready-to-use virtualization management platform and reduces the cost of reinventing the wheel |
| ⚡ | **Click-to-use templates** | Includes common Linux/Windows/OpenWrt system templates; create a VM in minutes by filling out a few fields without knowing low-level KVM commands. The system handles disk formats, boot types, network configuration, and other details automatically |
| 🧩 | **Modular design** | Pluggable network backends such as Open vSwitch support diverse network topologies and security policies |
| 🔀 | **Dual entry points** | The Web console and RESTful API support both automation and efficient manual operations |
| 📊 | **Observability** | Task queues and SSE make long-running operations observable and interruptible, improving stability under high concurrency |

---

## 🚀 Quick Deployment

luycloud provides a one-click installation script that automatically installs dependencies (libvirt / qemu-kvm / Open vSwitch, etc.), configures user storage, and registers and starts the systemd service.

### Option 1: One-click Remote Deployment (Recommended)

No manual source download is required. The script clones the latest source, prepares the Go/Node toolchains, builds locally, and starts the interactive installation:

```bash
# Run as root (choose one)
bash <(curl -fsSL https://raw.githubusercontent.com/luolu1/luycloud/main/luycloud-deploy.sh)
bash <(wget -qO- https://raw.githubusercontent.com/luolu1/luycloud/main/luycloud-deploy.sh)
```

The bootstrap script performs these steps: detect the CPU architecture → detect/install the git, Go, and Node.js toolchains → clone or update the `main` branch → locally build the frontend and backend with `build.sh` → call `install.sh` for interactive installation. When already installed, the script switches to an “Update / Uninstall” menu.

- 🌐 Access URL: `http://<server-IP>:8080`
- 🔑 Default account: `admin` / `admin123` (change it immediately after the first login)

> 📌 The source is cloned and built directly, keeping it synchronized with the repository’s `main` branch. Override behavior with these environment variables: `LUYCLOUD_GIT` (repository URL), `LUYCLOUD_BRANCH` (branch), `LUYCLOUD_SRC` (source checkout directory, default `/opt/luycloud-src`), `LUYCLOUD_VARIANT` (`native` native build / `compat` zig-compatible build, default `native`), `GOPROXY` (China acceleration), and `LUYCLOUD_MIRROR` (GitHub CDN proxy prefix, such as `https://ghfast.top`).

### ⚡ One-click Update (Recommended for Existing Installations)

When upgrading an existing installation to a new version (for example, one adding a “Recycle Bin” feature), there is no need to rebuild. The script downloads the **precompiled release package** from GitHub Releases, hot-swaps the binary and frontend, and restarts the service:

```bash
# Run as root (choose one)
bash <(curl -fsSL https://raw.githubusercontent.com/luolu1/luycloud/main/update.sh)
bash <(wget -qO- https://raw.githubusercontent.com/luolu1/luycloud/main/update.sh)
```

- 🚀 Updates in seconds without Go / Node toolchains; automatically selects the native or compatible binary based on GLIBC / AVX2
- 🛡️ Automatically backs up the old version before updating and rolls back if the new version fails to start
- 🌏 **CDN acceleration**: when GitHub access is slow in China, the script provides a built-in proxy mirror menu and also supports an environment variable:

  ```bash
   # Use a CDN proxy prefix to accelerate downloads
  LUYCLOUD_MIRROR="https://ghfast.top" bash <(curl -fsSL https://raw.githubusercontent.com/luolu1/luycloud/main/update.sh)

   # Update to a specified version (latest by default)
  LUYCLOUD_RELEASE_TAG="v1.1.0" bash update.sh
  ```

> 💡 `update.sh` is only for **updating** an existing installation; use `luycloud-deploy.sh` above for the first installation. `luycloud-deploy.sh` also supports the `LUYCLOUD_MIRROR` proxy prefix to speed up `git clone`.

<details>
<summary><b>Option 2: One-click Deployment from a Release Package</b></summary>

<br/>

If you already have a built release package:

```bash
# 1. Download and extract the release package (amd64 / arm64)
tar -xzf luycloud-linux-amd64.tar.gz
cd luycloud-linux-amd64

# 2. Run the one-click installation script as root
sudo bash install.sh
```

The script performs these steps: hardware virtualization detection → dependency installation → user storage initialization → network foundation (OVS/DHCP/forwarding) → systemd service registration → service startup. The terminal prints the access URL after installation.

> The installation script is interactive. Follow the prompts to select the storage disk, capacity, Web port, whether to enable public access, and whether to run the compatibility hardware test.

</details>

<details>
<summary><b>Option 3: Build Manually from Source</b></summary>

<br/>

Requires Go 1.27+ and Node.js 22+ (Vite 8 / rolldown requires Node ≥ 20.19).

```bash
# Build the frontend and backend and generate a release package
bash build.sh --variant native      # Use native compilation on the host
# or
bash build.sh                       # Also build the zig-compatible version (zig required)

# Artifact: release/luycloud-linux-<arch>.tar.gz
# Extract it, then run sudo bash install.sh to deploy
```

Build tip for networks in China: set `GOPROXY=https://goproxy.cn,direct` for Go dependencies; frontend dependencies require Node ≥ 20.19 to install rolldown native bindings correctly.

</details>

### Development Mode

```bash
bash start-dev.sh
# Backend air hot reload (:8080), frontend Vite (:5173)
```

### Common Operations Commands

```bash
systemctl status kvm-console      # View service status
journalctl -u kvm-console -f      # View live logs
systemctl restart kvm-console     # Restart the service
sudo bash qvmc-manage.sh          # Account and security management (reset passwords, clear 2FA, change ports, public access, etc.)
```

<details>
<summary><b>⚙️ Nested Virtualization Deployment Notes</b></summary>

<br/>

When the host itself runs inside a virtual machine (nested KVM), `host-passthrough` can make QEMU attempt to set `MSR 0x345 (IA32_PERF_CAPABILITIES)` and crash:

```
qemu-system-x86_64: error: failed to set MSR 0x345 to 0x2000
kvm_buf_set_msrs: Assertion `ret == cpu->kvm_msr_buf->nmsrs' failed.
```

luycloud automatically injects `<pmu state='off'/>` into the VM domain XML when it detects nested virtualization (the `/proc/cpuinfo` `hypervisor` flag), disabling vPMU to avoid this crash. This is a no-op on bare-metal hosts and does not affect normal performance counter functionality.

</details>

---

## ✨ Core Features

<table>
<tr>
<td width="50%" valign="top">

### 🖥️ Virtual Machine Lifecycle Management
- Complete power operations (start/shut down/restart/force power off/reset)
- Quota controls and permission checks
- Maintenance mode and graceful shutdown

### 🌐 Network Virtualization
- VPC logical switches and security groups
- Port forwarding and static IP management
- Firewall policies (VM/host dual layer)
- Network diagnostics and packet capture tools

### 💾 Storage Management
- Host storage pool management (formatting/partitioning/LVM volumes)
- Template management (create/import/export/delete)
- Disk management and IOPS limits
- User ISO mounting

</td>
<td width="50%" valign="top">

### 👥 User Permissions and Quotas
- Multi-tenant support (elastic cloud/lightweight cloud)
- Fine-grained quota management (CPU/memory/disk/VM count/storage/bandwidth/traffic/public IP/port forwarding/snapshots)
- SSH access control and invitation registration flow

### 📈 Monitoring and Task Scheduling
- VM/host statistics and historical data
- Asynchronous task queue and real-time SSE updates
- Scheduled event center and resource cleanup

### 📸 Snapshot Backups
- Create/restore/delete/bulk-delete snapshots
- NVRAM and shared directory compatibility checks
- Quota validation and task tracking

</td>
</tr>
</table>

<details>
<summary><b>🧬 Create VMs from Templates (Click to Expand)</b></summary>

<br/>

- **Template management**: Create templates from running VMs with one click, import/export template packages (tar.gz), and preview import integrity checks
- **Multiple template types**: Linux (cloud-init), Windows (ConfigDrive), OpenWrt (UCI configuration injection), FnOS (virt-customize), and a “do not initialize” mode
- **Unified cloning architecture**: Supports full and linked cloning. Full clones create independent disk images, while linked clones use a backing chain for rapid deployment
- **System initialization control**: Disable system initialization to preserve the template’s original configuration; supports blocking and non-blocking post-start commands
- **Smart boot detection**: Automatically detects UEFI/BIOS boot type and copies the NVRAM path to ensure cross-architecture compatibility
- **OpenWrt dual-mode initialization**: Automatically detects ext4 root partitions and squashfs+overlay disk layouts, then intelligently selects virt-customize or guestfish to inject network configuration
- **Windows ConfigDrive**: An OpenStack-compliant ISO image that uses cloudbase-init to initialize the hostname, password, and other settings automatically
- **Metadata-driven**: Template type, category, default hardware configuration, hash verification, template family relationships, and more are managed by `.meta.json` metadata files
- **Version and integrity checks**: Dual MD5 + SHA256 hash verification ensures template disk integrity
- **Template family management**: Supports parent-child relationships, node trees, cascading deletion, silent promotion, and hot promotion

</details>

---

## 🧰 Technology Stack

<table>
<tr>
<td valign="top" width="50%">

**Backend**

| Component | Selection |
|:---|:---|
| Language | Go 1.27+ |
| Web framework | Gin v1.12.0 |
| Database | SQLite + GORM v1.31.1 |
| Virtualization | go-libvirt RPC |
| Authentication | JWT v5.3.1 + TOTP v1.5.0 + crypto |
| WebSocket | gorilla/websocket v1.5.3 |
| Logging | lumberjack v2.2.1 |

</td>
<td valign="top" width="50%">

**Frontend**

| Component | Selection |
|:---|:---|
| UI framework | React v19.2.7 + TypeScript v6.0.2 |
| Component library | Semi Design v2.101.1 |
| Build tool | Vite v8.1.1 |
| Routing | react-router-dom v7.18.1 |
| State management | Zustand v5.0.14 |
| HTTP client | Axios v1.18.1 |
| Charts / terminal / VNC | ECharts v6.1.0 · @xterm/xterm v6.0.0 · @novnc/novnc v1.7.0 |

</td>
</tr>
</table>

> Legacy frontend (backup): Vue 3.5.30 + Element Plus, located in `web-backup/`.

**Virtualization infrastructure**: KVM/QEMU · Open vSwitch · Windows initialization (ConfigDrive standard support)

---

## 📋 System Requirements

<table>
<tr>
<td valign="top" width="50%">

**Hardware Requirements**

- ✅ CPU with VT-x/AMD-V support
- 🧠 At least 4GB RAM (8GB+ recommended)
- 💽 At least 50GB of available disk space

</td>
<td valign="top" width="50%">

**Software Requirements**

- 🐧 Operating system: Debian/Ubuntu (Debian 12+ recommended)
- 🖧 Virtualization: KVM/QEMU
- 🌉 Networking: Open vSwitch
- 🛠️ Dependency tool: genisoimage (for Windows VM initialization)

</td>
</tr>
</table>

---

## 🤝 Contribution Guide

As a large open-source project maintained by an independent developer, luycloud needs community support to continue improving. We welcome and encourage the use of AI and other tools for fixes and development, but please follow these guidelines:

1. **Follow the rules**: When using AI tools, the `AGENTS.md` file in the repository root must be used as the core prompt rules
2. **General-purpose scenarios**: Submitted features should target general use cases and meet the needs of a broad user base. For scenario-specific customizations, fork the repository and maintain them independently

### 🔒 Security Vulnerability Reports

If you discover a security vulnerability in the project, regardless of severity, do not report it publicly in GitHub Issues, to prevent malicious exploitation. Contact the maintainer through the repository’s private channel for responsible disclosure: [Submit private security feedback](https://github.com/luolu1/luycloud/security).

---

## 🔄 Merging Upstream Changes

When the standard repository backend changes, see [`docs/merge-from-upstream.md`](docs/merge-from-upstream.md) for detailed merge instructions.

**Core principles:**

1. Merge only backend changes in the `server/` directory
2. Reject all frontend changes in the `web/` directory
3. Unique content in this repository’s `web-backup/`, `.gitignore`, and `docs/` will not be overwritten by upstream

---

## 💖 Acknowledgements

Thanks to all developers who have contributed to luycloud!

Special thanks to [**ForZTN**](https://sponsorship.forztn.com/github.com/luolu1/luycloud) for sponsoring the test machine

---

<div align="center">

**☁️ luycloud** — Making virtualization management simpler

[Official Website](https://github.com/luolu1/luycloud) · [Documentation](https://github.com/luolu1/luycloud) · [Deployment Guide](https://github.com/luolu1/luycloud)

<sub>If this project helps you, please support it with a ⭐ Star!</sub>

</div>

#!/bin/bash
# ============================================================
# luycloud 一键部署引导脚本 (git-based bootstrap)
# ------------------------------------------------------------
# 用法（任选其一）：
#   bash <(curl -fsSL https://raw.githubusercontent.com/luolu1/luycloud/main/luycloud-deploy.sh)
#   bash <(wget -qO- https://raw.githubusercontent.com/luolu1/luycloud/main/luycloud-deploy.sh)
#
# 本脚本自动完成：
#   - 克隆 / 更新 luycloud 源码仓库（实时跟随 main 分支）
#   - 检测并（可选）安装 Go / Node.js 构建工具链
#   - 调用 build.sh 本地编译前端 + 后端，产出发行目录
#   - 调用 install.sh 完成【安装 / 更新】（交互式）
#   - 提供快速【卸载】（无需克隆源码）
#
# 安装 / 更新 的交互流程由 install.sh 提供，本脚本仅负责
# 拉取源码、准备工具链、编译，并把发行目录交给 install.sh。
# ============================================================

set -Eeuo pipefail

# ---------- 颜色与日志 ----------
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

info()    { echo -e "${GREEN}[INFO]${NC} $1"; }
warn()    { echo -e "${YELLOW}[WARN]${NC} $1"; }
error()   { echo -e "${RED}[ERROR]${NC} $1"; }
success() { echo -e "${GREEN}[✓]${NC} $1"; }

# ---------- 基础配置（与 install.sh 保持一致） ----------
APP_NAME="luycloud"
GIT_REPO="${LUYCLOUD_GIT:-https://github.com/luolu1/luycloud.git}"
GIT_BRANCH="${LUYCLOUD_BRANCH:-main}"

# CDN / 代理前缀：国内 git clone GitHub 较慢时，可为 https://github.com 链接
# 加代理前缀（如 https://ghfast.top），脚本会拼成 <前缀>/https://github.com/...。
# 可用环境变量 LUYCLOUD_MIRROR 预置；留空表示直连。仅对 https 的 GitHub 地址生效。
MIRROR_PREFIX="${LUYCLOUD_MIRROR:-}"
BUILTIN_MIRRORS=(
    "https://ghfast.top"
    "https://gh-proxy.com"
    "https://ghproxy.net"
    "https://mirror.ghproxy.com"
)
# 源码检出目录（默认放在 /opt/luycloud-src，可用 LUYCLOUD_SRC 覆盖）
SRC_DIR="${LUYCLOUD_SRC:-/opt/luycloud-src}"

INSTALL_DIR="/opt/kvm-console"
SERVICE_NAME="kvm-console"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
ENV_FILE="${INSTALL_DIR}/.env"
MANAGE_COMMAND="luycloud-manage"
MANAGE_SCRIPT="qvmc-manage.sh"
COMPATIBILITY_CHECK_SCRIPT="check-system-compatibility.sh"
OVS_DNSMASQ_UNIT="kvm-console-ovs-dnsmasq.service"
OVS_DNSMASQ_SERVICE_FILE="/etc/systemd/system/${OVS_DNSMASQ_UNIT}"

# 工具链版本要求（参考 README / server/go.mod）
GO_MIN_MAJOR=1
GO_MIN_MINOR=26           # go.mod: go 1.26；README 推荐 1.27+
NODE_MIN_MAJOR=20         # rolldown 原生绑定要求 Node ≥ 20.19
NODE_MIN_MINOR=19
GO_INSTALL_VERSION="1.27.1"   # 需自动安装 Go 时下载的版本

# 国内网络可设 GOPROXY（默认不强制，交由用户环境）
GOPROXY_HINT="${GOPROXY:-}"

# 构建变体：默认原生版（native），可用 LUYCLOUD_VARIANT=compat 切换
BUILD_VARIANT="${LUYCLOUD_VARIANT:-native}"

ARCH=""
GO_ARCH=""
DOWNLOADER=""
GO_INSTALLED_BY_SCRIPT=0

# ---------- 前置检查 ----------
check_root() {
    if [ "$(id -u)" -ne 0 ]; then
        error "请使用 root 运行（sudo bash ...）"
        exit 1
    fi
}

detect_downloader() {
    if command -v curl >/dev/null 2>&1; then
        DOWNLOADER="curl"
    elif command -v wget >/dev/null 2>&1; then
        DOWNLOADER="wget"
    else
        DOWNLOADER=""
    fi
}

detect_arch() {
    local m
    m=$(uname -m)
    case "$m" in
        x86_64|amd64)  ARCH="x86_64"; GO_ARCH="amd64" ;;
        aarch64|arm64) ARCH="aarch64"; GO_ARCH="arm64" ;;
        *)
            error "不支持的 CPU 架构: ${m}（仅支持 x86_64 / aarch64）"
            exit 1
            ;;
    esac
    info "检测到 CPU 架构: ${ARCH} (${GO_ARCH})"
}

detect_existing_install() {
    [ -x "${INSTALL_DIR}/kvm-console" ] || [ -f "$SERVICE_FILE" ]
}

# fetch_url URL OUTFILE
fetch_url() {
    local url="$1" out="$2"
    if [ "$DOWNLOADER" = "curl" ]; then
        curl -fL --progress-bar -o "$out" "$url"
    else
        wget -O "$out" "$url"
    fi
}

# apply_mirror URL —— 为 https 的 GitHub 地址拼接 CDN 代理前缀（其它地址原样返回）
apply_mirror() {
    local url="$1"
    if [ -n "$MIRROR_PREFIX" ] && [[ "$url" == https://github.com/* || "$url" == https://raw.githubusercontent.com/* ]]; then
        echo "${MIRROR_PREFIX%/}/${url}"
    else
        echo "$url"
    fi
}

# ---------- CDN / 代理前缀配置（交互式，非交互沿用环境变量） ----------
configure_mirror() {
    if [ -n "$MIRROR_PREFIX" ]; then
        info "使用环境变量指定的 CDN 代理前缀: ${MIRROR_PREFIX}"
        return
    fi
    if [ ! -t 0 ]; then
        return
    fi
    echo ""
    echo -e "${CYAN}是否为 GitHub 克隆/下载配置 CDN 代理加速？（国内访问较慢时推荐）${NC}"
    echo -e "  ${CYAN}0.${NC} 直连 GitHub（默认）"
    local i=1
    for m in "${BUILTIN_MIRRORS[@]}"; do
        echo -e "  ${CYAN}${i}.${NC} ${m}"
        i=$((i + 1))
    done
    echo -e "  ${CYAN}c.${NC} 自定义代理前缀"
    echo ""
    local choice
    read -rp "请选择 [0-${#BUILTIN_MIRRORS[@]}/c，默认 0]: " choice
    choice=${choice:-0}
    case "$choice" in
        0) MIRROR_PREFIX=""; info "将直连 GitHub" ;;
        c|C)
            read -rp "请输入代理前缀（形如 https://ghfast.top）: " MIRROR_PREFIX
            MIRROR_PREFIX="$(echo "$MIRROR_PREFIX" | tr -d '[:space:]')"
            [ -z "$MIRROR_PREFIX" ] && warn "未输入前缀，改为直连 GitHub" || info "使用自定义代理前缀: ${MIRROR_PREFIX}"
            ;;
        *)
            if [[ "$choice" =~ ^[0-9]+$ ]] && [ "$choice" -ge 1 ] && [ "$choice" -le "${#BUILTIN_MIRRORS[@]}" ]; then
                MIRROR_PREFIX="${BUILTIN_MIRRORS[$((choice - 1))]}"
                info "使用内置代理前缀: ${MIRROR_PREFIX}"
            else
                warn "无效选择，改为直连 GitHub"; MIRROR_PREFIX=""
            fi
            ;;
    esac
}

# ---------- 包管理器封装（用于安装 git / go / node） ----------
PKG_MGR=""
detect_pkg_mgr() {
    if command -v apt-get >/dev/null 2>&1; then
        PKG_MGR="apt"
    elif command -v dnf >/dev/null 2>&1; then
        PKG_MGR="dnf"
    elif command -v yum >/dev/null 2>&1; then
        PKG_MGR="yum"
    else
        PKG_MGR=""
    fi
}

pkg_install() {
    # pkg_install <包名...>
    case "$PKG_MGR" in
        apt) DEBIAN_FRONTEND=noninteractive apt-get install -y "$@" ;;
        dnf) dnf install -y "$@" ;;
        yum) yum install -y "$@" ;;
        *)   return 1 ;;
    esac
}

pkg_update_index() {
    case "$PKG_MGR" in
        apt) apt-get update -y ;;
        *)   return 0 ;;
    esac
}

# ---------- 确保 git ----------
ensure_git() {
    if command -v git >/dev/null 2>&1; then
        return 0
    fi
    warn "未检测到 git，尝试自动安装..."
    detect_pkg_mgr
    if [ -z "$PKG_MGR" ]; then
        error "无法自动安装 git（未识别包管理器），请手动安装后重试"
        exit 1
    fi
    pkg_update_index || true
    if ! pkg_install git; then
        error "git 安装失败，请手动安装后重试"
        exit 1
    fi
    success "git 已安装"
}

# ---------- 确保 Go ----------
# 返回 0 表示满足版本，非 0 表示不满足
go_version_ok() {
    command -v go >/dev/null 2>&1 || return 1
    local v major minor
    v=$(go version 2>/dev/null | grep -oE 'go[0-9]+\.[0-9]+(\.[0-9]+)?' | head -n1 | sed 's/^go//')
    [ -n "$v" ] || return 1
    major=${v%%.*}
    minor=$(echo "$v" | cut -d. -f2)
    if [ "$major" -gt "$GO_MIN_MAJOR" ]; then return 0; fi
    if [ "$major" -eq "$GO_MIN_MAJOR" ] && [ "$minor" -ge "$GO_MIN_MINOR" ]; then return 0; fi
    return 1
}

install_go() {
    info "安装 Go ${GO_INSTALL_VERSION} (${GO_ARCH})..."
    if [ -z "$DOWNLOADER" ]; then
        error "需要 curl 或 wget 才能下载 Go，请先安装其一"
        exit 1
    fi
    local tgz="/tmp/go${GO_INSTALL_VERSION}.linux-${GO_ARCH}.tar.gz"
    local url="https://go.dev/dl/go${GO_INSTALL_VERSION}.linux-${GO_ARCH}.tar.gz"
    if ! fetch_url "$url" "$tgz"; then
        error "Go 下载失败: ${url}"
        exit 1
    fi
    rm -rf /usr/local/go
    tar -C /usr/local -xzf "$tgz"
    rm -f "$tgz"
    export PATH="/usr/local/go/bin:${PATH}"
    # 持久化到 profile（幂等）
    if ! grep -q '/usr/local/go/bin' /etc/profile.d/luycloud-go.sh 2>/dev/null; then
        echo 'export PATH=/usr/local/go/bin:$PATH' > /etc/profile.d/luycloud-go.sh
    fi
    GO_INSTALLED_BY_SCRIPT=1
    if go_version_ok; then
        success "Go 已安装: $(go version)"
    else
        error "Go 安装后版本校验未通过"
        exit 1
    fi
}

ensure_go() {
    if go_version_ok; then
        info "Go 版本满足要求: $(go version)"
        return 0
    fi
    if command -v go >/dev/null 2>&1; then
        warn "现有 Go 版本过低（需 ≥ ${GO_MIN_MAJOR}.${GO_MIN_MINOR}），将安装 ${GO_INSTALL_VERSION} 到 /usr/local/go"
    else
        warn "未检测到 Go，将自动安装 ${GO_INSTALL_VERSION}"
    fi
    install_go
}

# ---------- 确保 Node.js ----------
node_version_ok() {
    command -v node >/dev/null 2>&1 || return 1
    command -v npm >/dev/null 2>&1 || return 1
    local v major minor
    v=$(node -v 2>/dev/null | sed 's/^v//')
    [ -n "$v" ] || return 1
    major=${v%%.*}
    minor=$(echo "$v" | cut -d. -f2)
    if [ "$major" -gt "$NODE_MIN_MAJOR" ]; then return 0; fi
    if [ "$major" -eq "$NODE_MIN_MAJOR" ] && [ "$minor" -ge "$NODE_MIN_MINOR" ]; then return 0; fi
    return 1
}

install_node() {
    info "安装 Node.js 22.x (NodeSource)..."
    detect_pkg_mgr
    if [ -z "$DOWNLOADER" ]; then
        error "需要 curl 或 wget 才能安装 Node.js"
        exit 1
    fi
    case "$PKG_MGR" in
        apt)
            if [ "$DOWNLOADER" = "curl" ]; then
                curl -fsSL https://deb.nodesource.com/setup_22.x | bash -
            else
                wget -qO- https://deb.nodesource.com/setup_22.x | bash -
            fi
            DEBIAN_FRONTEND=noninteractive apt-get install -y nodejs
            ;;
        dnf|yum)
            if [ "$DOWNLOADER" = "curl" ]; then
                curl -fsSL https://rpm.nodesource.com/setup_22.x | bash -
            else
                wget -qO- https://rpm.nodesource.com/setup_22.x | bash -
            fi
            pkg_install nodejs
            ;;
        *)
            error "无法自动安装 Node.js（未识别包管理器），请手动安装 Node ≥ ${NODE_MIN_MAJOR}.${NODE_MIN_MINOR} 后重试"
            exit 1
            ;;
    esac
    if node_version_ok; then
        success "Node.js 已安装: $(node -v)"
    else
        error "Node.js 安装后版本校验未通过（需 ≥ ${NODE_MIN_MAJOR}.${NODE_MIN_MINOR}）"
        exit 1
    fi
}

ensure_node() {
    if node_version_ok; then
        info "Node.js 版本满足要求: $(node -v)"
        return 0
    fi
    if command -v node >/dev/null 2>&1; then
        warn "现有 Node.js 版本过低（需 ≥ ${NODE_MIN_MAJOR}.${NODE_MIN_MINOR}），将通过 NodeSource 安装 22.x"
    else
        warn "未检测到 Node.js，将自动安装 22.x"
    fi
    install_node
}

# ---------- 克隆 / 更新源码 ----------
sync_source() {
    local clone_repo
    clone_repo="$(apply_mirror "$GIT_REPO")"
    if [ "$clone_repo" != "$GIT_REPO" ]; then
        info "通过 CDN 代理克隆: ${clone_repo}"
    fi
    if [ -d "${SRC_DIR}/.git" ]; then
        info "更新已有源码: ${SRC_DIR}（分支 ${GIT_BRANCH}）"
        git -C "$SRC_DIR" remote set-url origin "$clone_repo" 2>/dev/null || true
        git -C "$SRC_DIR" fetch --depth 1 origin "$GIT_BRANCH"
        git -C "$SRC_DIR" checkout -B "$GIT_BRANCH" "origin/${GIT_BRANCH}"
        git -C "$SRC_DIR" reset --hard "origin/${GIT_BRANCH}"
        # 还原为原始仓库地址，避免代理前缀被持久化到 origin
        git -C "$SRC_DIR" remote set-url origin "$GIT_REPO" 2>/dev/null || true
    else
        if [ -e "$SRC_DIR" ] && [ -n "$(ls -A "$SRC_DIR" 2>/dev/null)" ]; then
            error "源码目录 ${SRC_DIR} 已存在且非 git 仓库，请清理后重试或设置 LUYCLOUD_SRC"
            exit 1
        fi
        info "克隆源码到 ${SRC_DIR}（分支 ${GIT_BRANCH}）..."
        git clone --depth 1 --branch "$GIT_BRANCH" "$clone_repo" "$SRC_DIR"
        git -C "$SRC_DIR" remote set-url origin "$GIT_REPO" 2>/dev/null || true
    fi
    local head
    head=$(git -C "$SRC_DIR" rev-parse --short HEAD 2>/dev/null || echo "unknown")
    success "源码就绪，当前提交: ${head}"
}

# ---------- 编译 ----------
build_release() {
    local build_script="${SRC_DIR}/build.sh"
    if [ ! -f "$build_script" ]; then
        error "源码中未找到 build.sh"
        exit 1
    fi
    chmod +x "$build_script" 2>/dev/null || true

    info "开始编译（变体: ${BUILD_VARIANT}，架构: ${GO_ARCH}）..."
    if [ -n "$GOPROXY_HINT" ]; then
        info "使用 GOPROXY=${GOPROXY_HINT}"
    fi
    # 保证本会话内 Go 在 PATH（自动安装时）
    export PATH="/usr/local/go/bin:${PATH}"

    ( cd "$SRC_DIR" && bash build.sh --variant "$BUILD_VARIANT" )

    # 定位构建产物目录
    local out_name="luycloud-linux-${GO_ARCH}"
    local release_dir="${SRC_DIR}/release/${out_name}"
    if [ ! -d "$release_dir" ] || [ ! -f "${release_dir}/install.sh" ]; then
        error "构建产物缺失: ${release_dir}（未找到 install.sh）"
        exit 1
    fi
    if [ ! -x "${release_dir}/kvm-console" ] && [ ! -x "${release_dir}/kvm-console-native" ]; then
        error "构建产物缺失: 未在 ${release_dir} 找到后端二进制"
        exit 1
    fi
    success "编译完成: ${release_dir}"
    RELEASE_DIR="$release_dir"
}

# ---------- 安装 / 更新：委托给产物内 install.sh ----------
run_install() {
    configure_mirror
    ensure_git
    ensure_go
    ensure_node
    sync_source
    build_release

    success "发行目录就绪，进入交互式安装/更新流程"
    echo ""
    chmod +x "${RELEASE_DIR}/install.sh" 2>/dev/null || true
    # install.sh 自身会检测已装/未装并给出 安装 或 更新/卸载/修复 菜单
    ( cd "$RELEASE_DIR" && bash install.sh )
}

# ---------- 卸载：内联实现，无需克隆源码 ----------
uninstall_app() {
    if ! detect_existing_install; then
        warn "未检测到已安装的 ${APP_NAME}，无需卸载"
        return
    fi

    echo ""
    warn "卸载不会删除已有虚拟机磁盘、模板、libvirt 定义和用户存储镜像，除非你手动清理。"
    read -rp "确认卸载 ${APP_NAME}? 请输入 UNINSTALL 确认: " confirm
    if [ "$confirm" != "UNINSTALL" ]; then
        warn "已取消卸载"
        return
    fi

    info "停止并禁用面板服务..."
    systemctl stop "$SERVICE_NAME" 2>/dev/null || true
    systemctl disable "$SERVICE_NAME" 2>/dev/null || true
    rm -f "$SERVICE_FILE"

    read -rp "是否同时停用 OVS DHCP 辅助服务? [Y/n]: " stop_ovs
    stop_ovs=${stop_ovs:-Y}
    if [[ "$stop_ovs" =~ ^[Yy]$ ]]; then
        systemctl disable --now "$OVS_DNSMASQ_UNIT" 2>/dev/null || true
        rm -f "$OVS_DNSMASQ_SERVICE_FILE"
    fi

    systemctl daemon-reload 2>/dev/null || true

    rm -f "/usr/local/bin/${MANAGE_COMMAND}"

    read -rp "是否删除安装目录 ${INSTALL_DIR}（包含数据库和配置）? [y/N]: " purge
    purge=${purge:-N}
    if [[ "$purge" =~ ^[Yy]$ ]]; then
        rm -rf "$INSTALL_DIR"
        success "安装目录已删除"
    else
        rm -f "${INSTALL_DIR}/kvm-console" \
              "${INSTALL_DIR}/kvm-console-native" \
              "${INSTALL_DIR}/kvm-console-compat" \
              "${INSTALL_DIR}/${MANAGE_SCRIPT}"
        rm -f "${INSTALL_DIR}/scripts/${COMPATIBILITY_CHECK_SCRIPT}"
        rmdir "${INSTALL_DIR}/scripts" 2>/dev/null || true
        rm -rf "${INSTALL_DIR}/web-dist"
        warn "已保留 ${INSTALL_DIR}/data 与 ${ENV_FILE}"
    fi

    # 可选清理源码检出目录
    if [ -d "${SRC_DIR}/.git" ]; then
        read -rp "是否删除源码检出目录 ${SRC_DIR}? [y/N]: " rm_src
        rm_src=${rm_src:-N}
        if [[ "$rm_src" =~ ^[Yy]$ ]]; then
            rm -rf "$SRC_DIR"
            success "源码目录已删除"
        fi
    fi

    success "${APP_NAME} 已卸载"
}

# ---------- 主菜单 ----------
show_banner() {
    echo ""
    echo -e "${CYAN}╔══════════════════════════════════════════════════╗${NC}"
    echo -e "${CYAN}║      ${APP_NAME} 一键部署引导（源码编译 · 安装/更新/卸载）    ║${NC}"
    echo -e "${CYAN}╚══════════════════════════════════════════════════╝${NC}"
    echo ""
}

main() {
    check_root
    detect_downloader
    detect_arch
    show_banner

    local choice
    if detect_existing_install; then
        echo -e "${CYAN}检测到已安装的 ${APP_NAME}${NC}"
        echo -e "  ${CYAN}1.${NC} 更新（拉取最新源码 → 编译 → 重装/修复地基）"
        echo -e "  ${CYAN}2.${NC} 卸载"
        echo -e "  ${CYAN}0.${NC} 退出"
        echo ""
        read -rp "请选择操作 [1/2/0，默认 1]: " choice
        choice=${choice:-1}
    else
        echo -e "${CYAN}未检测到已安装的 ${APP_NAME}${NC}"
        echo -e "  ${CYAN}1.${NC} 安装（拉取源码 → 编译 → 交互式安装）"
        echo -e "  ${CYAN}0.${NC} 退出"
        echo ""
        read -rp "请选择操作 [1/0，默认 1]: " choice
        choice=${choice:-1}
    fi

    case "$choice" in
        1)
            run_install
            ;;
        2)
            if detect_existing_install; then
                uninstall_app
            else
                warn "当前未安装，无法卸载"
            fi
            ;;
        0)
            info "已退出"
            exit 0
            ;;
        *)
            error "无效的选择: ${choice}"
            exit 1
            ;;
    esac
}

main "$@"

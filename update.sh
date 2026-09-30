#!/bin/bash
# ============================================================
# luycloud 一键更新脚本 (release-based updater)
# ------------------------------------------------------------
# 用法（任选其一）：
#   bash <(curl -fsSL https://raw.githubusercontent.com/luolu1/luycloud/main/update.sh)
#   bash <(wget -qO- https://raw.githubusercontent.com/luolu1/luycloud/main/update.sh)
#   sudo bash update.sh                     # 已下载脚本本地执行
#
# 本脚本直接从 GitHub Releases 拉取【预编译】发行包（tar.gz），
# 热替换后端二进制 + 前端静态资源并重启服务，无需 Go / Node 工具链，
# 适合快速更新（例如新增「回收站」功能后的版本升级）。
#
# 若需从源码重新编译（切换分支、二次开发），请使用 luycloud-deploy.sh。
#
# ---------------- CDN / 代理加速 ----------------
# 国内访问 GitHub 较慢时，可为 GitHub 下载链接配置代理前缀（CDN 加速）：
#   - 交互式菜单可选择内置镜像或输入自定义前缀；
#   - 非交互可用环境变量 LUYCLOUD_MIRROR 指定，例如：
#       LUYCLOUD_MIRROR="https://ghfast.top" bash update.sh
#     脚本会把它拼到 https://github.com/... 前，得到
#       https://ghfast.top/https://github.com/luolu1/luycloud/releases/...
#   - LUYCLOUD_MIRROR 留空表示直连 GitHub（默认）。
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

# ---------- 基础配置（与 install.sh / luycloud-deploy.sh 保持一致） ----------
APP_NAME="luycloud"
GITHUB_OWNER="${LUYCLOUD_OWNER:-luolu1}"
GITHUB_REPO_NAME="${LUYCLOUD_REPO:-luycloud}"
# 指定要更新到的 Release 版本标签；留空表示使用 latest。
RELEASE_TAG="${LUYCLOUD_RELEASE_TAG:-latest}"

INSTALL_DIR="/opt/kvm-console"
SERVICE_NAME="kvm-console"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"

# CDN / 代理前缀（可为空 = 直连）。可用环境变量 LUYCLOUD_MIRROR 预置。
MIRROR_PREFIX="${LUYCLOUD_MIRROR:-}"

# 内置常用 GitHub 代理镜像（供交互菜单选择；不保证长期可用，可自行输入）
BUILTIN_MIRRORS=(
    "https://ghfast.top"
    "https://gh-proxy.com"
    "https://ghproxy.net"
    "https://mirror.ghproxy.com"
)

ARCH=""
ASSET_NAME=""
DOWNLOADER=""
BACKUP_DIR=""

# ---------- 前置检查 ----------
check_root() {
    if [ "$(id -u)" -ne 0 ]; then
        error "请使用 root 运行（sudo bash update.sh）"
        exit 1
    fi
}

detect_downloader() {
    if command -v curl >/dev/null 2>&1; then
        DOWNLOADER="curl"
    elif command -v wget >/dev/null 2>&1; then
        DOWNLOADER="wget"
    else
        error "需要 curl 或 wget 才能下载发行包，请先安装其一"
        exit 1
    fi
}

detect_arch() {
    local m
    m=$(uname -m)
    case "$m" in
        x86_64|amd64)  ARCH="amd64"; ASSET_NAME="luycloud-linux-amd64.tar.gz" ;;
        aarch64|arm64) ARCH="arm64"; ASSET_NAME="luycloud-linux-arm64.tar.gz" ;;
        *)
            error "不支持的 CPU 架构: ${m}（仅支持 x86_64 / aarch64）"
            exit 1
            ;;
    esac
    info "检测到 CPU 架构: ${ARCH}"
}

detect_existing_install() {
    [ -x "${INSTALL_DIR}/kvm-console" ] || [ -f "$SERVICE_FILE" ]
}

# ---------- 下载封装 ----------
# fetch_url URL OUTFILE  —— 下载到文件（进度条）
fetch_url() {
    local url="$1" out="$2"
    if [ "$DOWNLOADER" = "curl" ]; then
        curl -fL --connect-timeout 15 --progress-bar -o "$out" "$url"
    else
        wget -O "$out" "$url"
    fi
}

# fetch_stdout URL  —— 下载到标准输出（用于探测 / 读取 API）
fetch_stdout() {
    local url="$1"
    if [ "$DOWNLOADER" = "curl" ]; then
        curl -fsSL --connect-timeout 15 "$url"
    else
        wget -qO- "$url"
    fi
}

# apply_mirror URL  —— 若配置了镜像前缀，则拼接到 GitHub URL 前
apply_mirror() {
    local url="$1"
    if [ -n "$MIRROR_PREFIX" ]; then
        echo "${MIRROR_PREFIX%/}/${url}"
    else
        echo "$url"
    fi
}

# ---------- CDN / 代理前缀配置 ----------
configure_mirror() {
    # 已通过环境变量指定则直接沿用，不再交互
    if [ -n "$MIRROR_PREFIX" ]; then
        info "使用环境变量指定的 CDN 代理前缀: ${MIRROR_PREFIX}"
        return
    fi
    # 非交互式（无 TTY）时保持直连
    if [ ! -t 0 ]; then
        info "非交互模式，未配置 CDN 代理，直连 GitHub"
        return
    fi

    echo ""
    echo -e "${CYAN}是否为 GitHub 下载配置 CDN 代理加速？（国内访问 GitHub 较慢时推荐）${NC}"
    echo -e "  ${CYAN}0.${NC} 直连 GitHub（默认，海外/已有代理）"
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
        0)
            MIRROR_PREFIX=""
            info "将直连 GitHub"
            ;;
        c|C)
            read -rp "请输入代理前缀（形如 https://ghfast.top）: " MIRROR_PREFIX
            MIRROR_PREFIX="$(echo "$MIRROR_PREFIX" | tr -d '[:space:]')"
            if [ -z "$MIRROR_PREFIX" ]; then
                warn "未输入前缀，改为直连 GitHub"
            else
                info "使用自定义代理前缀: ${MIRROR_PREFIX}"
            fi
            ;;
        *)
            if [[ "$choice" =~ ^[0-9]+$ ]] && [ "$choice" -ge 1 ] && [ "$choice" -le "${#BUILTIN_MIRRORS[@]}" ]; then
                MIRROR_PREFIX="${BUILTIN_MIRRORS[$((choice - 1))]}"
                info "使用内置代理前缀: ${MIRROR_PREFIX}"
            else
                warn "无效选择，改为直连 GitHub"
                MIRROR_PREFIX=""
            fi
            ;;
    esac
}

# ---------- 解析 Release 下载地址 ----------
resolve_download_url() {
    local base="https://github.com/${GITHUB_OWNER}/${GITHUB_REPO_NAME}/releases"
    if [ "$RELEASE_TAG" = "latest" ]; then
        # /releases/latest/download/<asset> 会自动 302 到最新 release 对应资产
        echo "${base}/latest/download/${ASSET_NAME}"
    else
        echo "${base}/download/${RELEASE_TAG}/${ASSET_NAME}"
    fi
}

# ---------- 下载并校验发行包 ----------
TMP_DIR=""
RELEASE_SOURCE_DIR=""
download_release() {
    TMP_DIR=$(mktemp -d)
    local tarball="${TMP_DIR}/${ASSET_NAME}"
    local raw_url final_url
    raw_url="$(resolve_download_url)"
    final_url="$(apply_mirror "$raw_url")"

    info "下载发行包（版本: ${RELEASE_TAG}，架构: ${ARCH}）..."
    info "下载地址: ${final_url}"
    if ! fetch_url "$final_url" "$tarball"; then
        error "发行包下载失败。"
        if [ -n "$MIRROR_PREFIX" ]; then
            warn "如为代理前缀不可用，可换用其它镜像或直连后重试。"
        else
            warn "国内网络可重试并选择 CDN 代理加速。"
        fi
        exit 1
    fi

    # 基本完整性校验：非空且为 gzip
    if [ ! -s "$tarball" ]; then
        error "下载的发行包为空，可能是代理返回了错误页面"
        exit 1
    fi
    if ! tar -tzf "$tarball" >/dev/null 2>&1; then
        error "发行包不是有效的 tar.gz（可能下载到了 HTML 错误页），请检查代理前缀或网络"
        exit 1
    fi

    info "解压发行包..."
    tar -xzf "$tarball" -C "$TMP_DIR"

    local found_bin
    found_bin=$(find "$TMP_DIR" -maxdepth 3 -name "kvm-console" -type f 2>/dev/null | sed -n '1p') || true
    if [ -z "$found_bin" ]; then
        error "发行包中未找到 kvm-console 可执行文件"
        exit 1
    fi
    RELEASE_SOURCE_DIR=$(dirname "$found_bin")
    if [ ! -d "${RELEASE_SOURCE_DIR}/web-dist" ]; then
        error "发行包中未找到 web-dist 前端文件"
        exit 1
    fi
    success "发行包就绪: ${RELEASE_SOURCE_DIR}"
}

# ---------- 选择最合适的后端二进制（复用 install.sh 的判定逻辑） ----------
select_backend_binary() {
    # 若发行包仅含单一 kvm-console（如 native 或 compat 单构建），直接使用。
    if [ ! -f "${RELEASE_SOURCE_DIR}/kvm-console-native" ]; then
        return
    fi

    info "发行包包含宿主机原生版，检测运行环境以选择最合适的二进制..."
    local glibc_ver
    glibc_ver=$(ldd --version 2>&1 | sed -n '1 s/.* //p') || true
    if [ -z "$glibc_ver" ] || ! echo "$glibc_ver" | grep -qE '^[0-9]+\.[0-9]+$'; then
        glibc_ver=$(getconf GNU_LIBC_VERSION 2>/dev/null | awk '{print $2}' || echo "0")
    fi
    info "检测到宿主机 GLIBC 版本: ${glibc_ver}"

    local need_compat=true major minor
    IFS=. read -r major minor <<< "$glibc_ver"
    minor=${minor:-0}
    if [ "$major" -gt 2 ] || { [ "$major" -eq 2 ] && [ "$minor" -ge 34 ]; }; then
        need_compat=false
    fi

    if [ "$need_compat" = false ]; then
        if [ "$ARCH" = "amd64" ] && ! grep -q 'avx2' /proc/cpuinfo 2>/dev/null; then
            warn "CPU 不支持 AVX2/FMA 指令集，保留 zig 兼容版作为主程序（原生版可能崩溃）"
        else
            info "GLIBC ≥ 2.34 且 CPU 支持 AVX2，选用宿主机原生版为主程序"
            mv -f "${RELEASE_SOURCE_DIR}/kvm-console" "${RELEASE_SOURCE_DIR}/kvm-console-compat"
            mv -f "${RELEASE_SOURCE_DIR}/kvm-console-native" "${RELEASE_SOURCE_DIR}/kvm-console"
        fi
    else
        info "GLIBC < 2.34，继续使用 zig 兼容版作为主程序"
    fi
}

# ---------- 备份当前版本（便于回滚） ----------
backup_current() {
    BACKUP_DIR="${INSTALL_DIR}/.backup/update-$(date +%Y%m%d-%H%M%S)"
    mkdir -p "$BACKUP_DIR"
    info "备份当前版本到: ${BACKUP_DIR}"
    [ -f "${INSTALL_DIR}/kvm-console" ] && cp -f "${INSTALL_DIR}/kvm-console" "${BACKUP_DIR}/kvm-console" || true
    [ -d "${INSTALL_DIR}/web-dist" ] && cp -r "${INSTALL_DIR}/web-dist" "${BACKUP_DIR}/web-dist" || true
    success "备份完成（如需回滚可从该目录恢复）"
}

# ---------- 热替换二进制 + 前端并重启 ----------
apply_update() {
    if ! detect_existing_install; then
        error "未检测到已安装的 ${APP_NAME}（缺少 ${INSTALL_DIR}/kvm-console 或服务单元）。"
        warn  "首次安装请使用 luycloud-deploy.sh 或发行包内 install.sh。"
        exit 1
    fi

    select_backend_binary
    backup_current

    info "停止 ${APP_NAME} 服务..."
    systemctl stop "$SERVICE_NAME" 2>/dev/null || true

    info "更新后端二进制..."
    cp -f "${RELEASE_SOURCE_DIR}/kvm-console" "${INSTALL_DIR}/kvm-console"
    chmod +x "${INSTALL_DIR}/kvm-console"
    if [ -f "${RELEASE_SOURCE_DIR}/kvm-console-compat" ]; then
        cp -f "${RELEASE_SOURCE_DIR}/kvm-console-compat" "${INSTALL_DIR}/kvm-console-compat"
        chmod +x "${INSTALL_DIR}/kvm-console-compat"
    fi

    info "更新前端静态资源..."
    rm -rf "${INSTALL_DIR}/web-dist"
    cp -r "${RELEASE_SOURCE_DIR}/web-dist" "${INSTALL_DIR}/web-dist"

    # 同步随包脚本（管理脚本 / 兼容性脚本），存在则更新，不覆盖数据与配置
    if [ -f "${RELEASE_SOURCE_DIR}/qvmc-manage.sh" ]; then
        cp -f "${RELEASE_SOURCE_DIR}/qvmc-manage.sh" "${INSTALL_DIR}/qvmc-manage.sh"
        chmod +x "${INSTALL_DIR}/qvmc-manage.sh"
    fi
    if [ -f "${RELEASE_SOURCE_DIR}/check-system-compatibility.sh" ]; then
        mkdir -p "${INSTALL_DIR}/scripts"
        cp -f "${RELEASE_SOURCE_DIR}/check-system-compatibility.sh" "${INSTALL_DIR}/scripts/check-system-compatibility.sh"
        chmod +x "${INSTALL_DIR}/scripts/check-system-compatibility.sh"
    fi

    success "文件更新完成"
}

# ---------- 启动并校验（失败自动回滚） ----------
restart_and_verify() {
    info "启动 ${APP_NAME} 服务..."
    systemctl daemon-reload 2>/dev/null || true
    systemctl restart "$SERVICE_NAME"

    # 后端启动较慢（初始化命令较多），最多等待约 30 秒
    local waited=0
    while [ "$waited" -lt 30 ]; do
        if systemctl is-active --quiet "$SERVICE_NAME"; then
            success "${APP_NAME} 服务启动成功"
            return 0
        fi
        sleep 2
        waited=$((waited + 2))
    done

    error "服务启动失败或超时，尝试回滚到更新前版本..."
    rollback
    return 1
}

rollback() {
    if [ -z "$BACKUP_DIR" ] || [ ! -d "$BACKUP_DIR" ]; then
        error "未找到备份目录，无法自动回滚，请手动排查: journalctl -u ${SERVICE_NAME} -e"
        exit 1
    fi
    warn "回滚二进制与前端..."
    [ -f "${BACKUP_DIR}/kvm-console" ] && cp -f "${BACKUP_DIR}/kvm-console" "${INSTALL_DIR}/kvm-console" && chmod +x "${INSTALL_DIR}/kvm-console"
    if [ -d "${BACKUP_DIR}/web-dist" ]; then
        rm -rf "${INSTALL_DIR}/web-dist"
        cp -r "${BACKUP_DIR}/web-dist" "${INSTALL_DIR}/web-dist"
    fi
    systemctl restart "$SERVICE_NAME" 2>/dev/null || true
    sleep 3
    if systemctl is-active --quiet "$SERVICE_NAME"; then
        warn "已回滚到更新前版本并恢复运行。请查看日志确认新版失败原因: journalctl -u ${SERVICE_NAME} -e"
    else
        error "回滚后服务仍未启动，请手动排查: journalctl -u ${SERVICE_NAME} -e"
    fi
    exit 1
}

cleanup() {
    [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ] && rm -rf "$TMP_DIR" || true
}
trap cleanup EXIT

# ---------- 主流程 ----------
show_banner() {
    echo ""
    echo -e "${CYAN}╔══════════════════════════════════════════════════╗${NC}"
    echo -e "${CYAN}║      ${APP_NAME} 一键更新（预编译发行包 · CDN 加速）      ║${NC}"
    echo -e "${CYAN}╚══════════════════════════════════════════════════╝${NC}"
    echo ""
}

main() {
    check_root
    detect_downloader
    detect_arch
    show_banner

    if ! detect_existing_install; then
        warn "未检测到已安装的 ${APP_NAME}。"
        warn "本脚本用于【更新】现有安装；首次安装请使用："
        echo -e "  ${CYAN}bash <(curl -fsSL https://raw.githubusercontent.com/${GITHUB_OWNER}/${GITHUB_REPO_NAME}/main/luycloud-deploy.sh)${NC}"
        exit 1
    fi

    configure_mirror
    download_release
    apply_update
    restart_and_verify

    echo ""
    success "${APP_NAME} 已更新完成（版本: ${RELEASE_TAG}）"
    info "访问地址：http://<服务器IP>:8080"
    info "如遇异常，可查看日志：journalctl -u ${SERVICE_NAME} -f"
}

main "$@"

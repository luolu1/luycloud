package recycle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kvm_console/config"
	"kvm_console/logger"
	"kvm_console/service/vm_xml"
)

const recycleDirName = ".recycle"

// StoreRoot 回收站元数据根目录：<clone_dir>/.recycle
func StoreRoot() string {
	base := ""
	if config.GlobalConfig != nil {
		base = config.GlobalConfig.CloneDir
	}
	if base == "" {
		base = "/var/lib/libvirt/images"
	}
	return filepath.Join(base, recycleDirName)
}

// RetentionDays 读取系统设置；未设置时回退默认 7。<=0 表示不自动清除。
func RetentionDays() int {
	if config.GlobalConfig != nil {
		return config.GlobalConfig.VMRecycleRetentionDays
	}
	return 7
}

// ensureStoreDir 创建回收站侧车目录 <root>/<name>-<unixts>（0700）。
func ensureStoreDir(vmName string) (string, error) {
	name := strings.TrimSpace(vmName)
	name = vm_xml.VMXMLTempNameSanitizer.ReplaceAllString(name, "_")
	dir := filepath.Join(StoreRoot(), name+"-"+time.Now().Format("20060102150405"))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

// writeSidecar 将元数据写入 <dir>/meta.json（0600）。
func writeSidecar(dir string, meta *VMRecycleMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "meta.json"), data, 0600)
}

// ReadSidecar 读取 <dir>/meta.json。
func ReadSidecar(dir string) (*VMRecycleMeta, error) {
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return nil, err
	}
	var meta VMRecycleMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// removeStoreDir 删除回收站侧车目录，带 StoreRoot 前缀校验，拒绝 root 外路径。
func removeStoreDir(dir string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	absRoot := filepath.Clean(StoreRoot())
	if !isPathUnderDir(absDir, absRoot) {
		logger.App.Error("拒绝删除回收站目录：路径越界", "dir", absDir, "root", absRoot)
		return os.ErrInvalid
	}
	if err := os.RemoveAll(absDir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// isPathUnderDir 检查给定路径是否在指定目录下（含目录本身）。
// 移植自 service/clone/delete.go 的同名工具函数。
func isPathUnderDir(path, dir string) bool {
	path = filepath.Clean(path)
	dir = filepath.Clean(dir)
	if path == dir {
		return true
	}
	dirWithSep := dir + string(filepath.Separator)
	return strings.HasPrefix(path, dirWithSep)
}

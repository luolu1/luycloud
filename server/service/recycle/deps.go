package recycle

// Deps 承载 recycle 子包对外部模块的所有依赖，避免循环 import。
// 根包 service 不 import 本包；所有依赖在 main.go 的 initRecycleDeps() 中注入。
type Deps struct {
	// ---- 用户 / 归属 ----
	FindVMOwner         func(vmName string) string
	AddVMToUser         func(username, vmName string) error
	RemoveVMFromUser    func(username, vmName string) error
	GetUserCloudType    func(username string) string // 查不到用户时返回 "elastic"
	IsLightweightCloudType func(cloudType string) bool

	// ---- VM 缓存 / 清理 ----
	RefreshVMCacheByName        func(name string) error
	MarkVMCacheMissing          func(name string) error
	DeleteVMStatsRecords        func(name string)
	DeleteVMRuntimeRecord       func(name string)
	CleanupVMVPCBinding         func(vmName string)
	CleanupLightweightVMResources func(vmName string)
	DeleteVMSchedules          func(vmName string) error
	DeleteVMCredential         func(vmName string) error

	// ---- 网络绑定 ----
	BindVMToVPCAsAdmin  func(vmName string, switchID, securityGroupID uint) error
	EnsureLightweightVMNetwork func(owner, vmName string) error

	// ---- 迁移互斥 ----
	EnsureVMNotMigrating func(vmName, action string) error
}

// D 依赖注入对象（未注入时各方法通过 nil-safe 包装降级处理）。
var D *Deps

func InitDeps(d *Deps) { D = d }

// 以下 nil-safe 包装统一在 D 未初始化或某依赖为 nil 时安全返回。

func findVMOwner(name string) string {
	if D == nil || D.FindVMOwner == nil {
		return ""
	}
	return D.FindVMOwner(name)
}

func getUserCloudType(username string) string {
	if D == nil || D.GetUserCloudType == nil {
		return "elastic"
	}
	return D.GetUserCloudType(username)
}

func isLightweightCloudType(cloudType string) bool {
	if D == nil || D.IsLightweightCloudType == nil {
		return false
	}
	return D.IsLightweightCloudType(cloudType)
}

func addVMToUser(username, vmName string) error {
	if D == nil || D.AddVMToUser == nil {
		return nil
	}
	return D.AddVMToUser(username, vmName)
}

func removeVMFromUser(username, vmName string) error {
	if D == nil || D.RemoveVMFromUser == nil {
		return nil
	}
	return D.RemoveVMFromUser(username, vmName)
}

func refreshVMCacheByName(name string) error {
	if D == nil || D.RefreshVMCacheByName == nil {
		return nil
	}
	return D.RefreshVMCacheByName(name)
}

func markVMCacheMissing(name string) error {
	if D == nil || D.MarkVMCacheMissing == nil {
		return nil
	}
	return D.MarkVMCacheMissing(name)
}

func deleteVMStatsRecords(name string) {
	if D != nil && D.DeleteVMStatsRecords != nil {
		D.DeleteVMStatsRecords(name)
	}
}

func deleteVMRuntimeRecord(name string) {
	if D != nil && D.DeleteVMRuntimeRecord != nil {
		D.DeleteVMRuntimeRecord(name)
	}
}

func cleanupVMVPCBinding(vmName string) {
	if D != nil && D.CleanupVMVPCBinding != nil {
		D.CleanupVMVPCBinding(vmName)
	}
}

func cleanupLightweightVMResources(vmName string) {
	if D == nil || D.CleanupLightweightVMResources == nil {
		return
	}
	D.CleanupLightweightVMResources(vmName)
}

func deleteVMSchedules(vmName string) error {
	if D == nil || D.DeleteVMSchedules == nil {
		return nil
	}
	return D.DeleteVMSchedules(vmName)
}

func deleteVMCredential(vmName string) error {
	if D == nil || D.DeleteVMCredential == nil {
		return nil
	}
	return D.DeleteVMCredential(vmName)
}

func bindVMToVPCAsAdmin(vmName string, switchID, securityGroupID uint) error {
	if D == nil || D.BindVMToVPCAsAdmin == nil {
		return nil
	}
	return D.BindVMToVPCAsAdmin(vmName, switchID, securityGroupID)
}

func ensureLightweightVMNetwork(owner, vmName string) error {
	if D == nil || D.EnsureLightweightVMNetwork == nil {
		return nil
	}
	return D.EnsureLightweightVMNetwork(owner, vmName)
}

func ensureVMNotMigrating(vmName, action string) error {
	if D == nil || D.EnsureVMNotMigrating == nil {
		return nil
	}
	return D.EnsureVMNotMigrating(vmName, action)
}

package vm_xml

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"kvm_console/logger"
	"kvm_console/service/arch"
	"kvm_console/utils"
)

const (
	VMBootTypeBIOS       = "bios"
	VMBootTypeUEFI       = "uefi"
	VMBootTypeUEFISecure = "uefi-secure"
)

var (
	vmBootTypeOSBlockRegexp       = regexp.MustCompile(`(?s)<os\b[^>]*>.*?</os>`)
	vmBootTypeOSOpenTagRegexp     = regexp.MustCompile(`(?m)^(\s*)<os\b([^>]*)>`)
	vmBootTypeFirmwareAttrRegexp  = regexp.MustCompile(`\s+firmware=['"][^'"]+['"]`)
	vmBootTypeFirmwareBlockRegexp = regexp.MustCompile(`(?s)\n?\s*<firmware\b[^>]*>.*?</firmware>`)
	vmBootTypeLoaderBlockRegexp   = regexp.MustCompile(`(?s)\n?\s*<loader\b[^>]*(?:/>|>.*?</loader>)`)
	vmBootTypeNVRAMBlockRegexp    = regexp.MustCompile(`(?s)\n?\s*<nvram\b[^>]*(?:/>|>.*?</nvram>)`)
	vmBootTypeSecureAttrRegexp    = regexp.MustCompile(`\s+secure=['"][^'"]+['"]`)
	vmBootTypeSecureFeatureRegexp = regexp.MustCompile(`(?is)<feature\b[^>]*name=['"]secure-boot['"][^>]*enabled=['"]yes['"][^>]*/?>|<feature\b[^>]*enabled=['"]yes['"][^>]*name=['"]secure-boot['"][^>]*/?>`)
	vmBootTypeArchRegexp          = regexp.MustCompile(`<type\b[^>]*\barch=['"]([^'"]+)['"]`)
	vmBootTypeMachineRegexp       = regexp.MustCompile(`<type\b[^>]*\bmachine=['"]([^'"]+)['"]`)
	vmBootTypeSMMRegexp           = regexp.MustCompile(`(?s)\n?\s*<smm\b[^>]*/>`)
	vmBootTypeFeaturesRegexp      = regexp.MustCompile(`(?s)<features\b[^>]*>.*?</features>`)
	vmBootTypeTypeCloseRegexp     = regexp.MustCompile(`</type>`)
)

// NormalizeVMBootType 规范化引导方式。
func NormalizeVMBootType(bootType string) string {
	switch strings.ToLower(strings.TrimSpace(bootType)) {
	case VMBootTypeBIOS:
		return VMBootTypeBIOS
	case VMBootTypeUEFI:
		return VMBootTypeUEFI
	case VMBootTypeUEFISecure:
		return VMBootTypeUEFISecure
	default:
		return ""
	}
}

// ParseVMBootTypeFromDomainXML 从 domain XML 中解析当前引导方式。
// 支持两种 UEFI 标识：firmware='efi' 自动选择（旧模式）和显式 pflash loader（新模式）。
func ParseVMBootTypeFromDomainXML(xmlContent string) string {
	xmlContent = strings.TrimSpace(xmlContent)
	if xmlContent == "" {
		return ""
	}
	isUEFI := strings.Contains(xmlContent, "firmware='efi'") ||
		strings.Contains(xmlContent, `firmware="efi"`) ||
		DomainUsesPflashNVRAM(xmlContent)
	if !isUEFI {
		return VMBootTypeBIOS
	}
	if vmBootTypeSecureFeatureRegexp.MatchString(xmlContent) || vmBootTypeSecureAttrRegexp.MatchString(xmlContent) {
		return VMBootTypeUEFISecure
	}
	return VMBootTypeUEFI
}

// ParseVMArchFromDomainXML 从 domain XML 中解析架构。
func ParseVMArchFromDomainXML(xmlContent string) string {
	matches := vmBootTypeArchRegexp.FindStringSubmatch(xmlContent)
	if len(matches) < 2 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(matches[1]))
}

// ParseVMMachineTypeFromDomainXML 从 domain XML 中解析并归一化机器类型。
func ParseVMMachineTypeFromDomainXML(xmlContent string) string {
	matches := vmBootTypeMachineRegexp.FindStringSubmatch(xmlContent)
	if len(matches) < 2 {
		return ""
	}
	return normalizeVMMachineType(matches[1])
}

func normalizeVMMachineType(machine string) string {
	value := strings.ToLower(strings.TrimSpace(machine))
	switch {
	case strings.Contains(value, "q35"):
		return "q35"
	case strings.Contains(value, "i440fx"):
		return "i440fx"
	case strings.HasPrefix(value, "virt"):
		return "virt"
	default:
		return value
	}
}

func resolveVMNVRAMPath(name, xmlContent string) string {
	if path := strings.TrimSpace(ExtractDomainNVRAMPath(xmlContent)); path != "" {
		return path
	}
	cleanName := strings.TrimSpace(name)
	if cleanName == "" {
		cleanName = "vm"
	}
	return fmt.Sprintf("/var/lib/libvirt/qemu/nvram/%s_VARS.fd", cleanName)
}

// GetVMNVRAMPath 获取虚拟机的NVRAM文件路径（公共函数）
func GetVMNVRAMPath(name string) string {
	return resolveVMNVRAMPath(name, "")
}

// ResolveOVMFLoaderPath 根据是否启用安全引导选择相应的 OVMF Code 固件路径。
func ResolveOVMFLoaderPath(secure bool) string {
	candidates := []string{
		"/usr/share/OVMF/OVMF_CODE_4M.fd",
		"/usr/share/OVMF/OVMF_CODE.fd",
	}
	fallback := "/usr/share/OVMF/OVMF_CODE_4M.fd"
	if secure {
		candidates = []string{
			"/usr/share/OVMF/OVMF_CODE_4M.ms.fd",
			"/usr/share/OVMF/OVMF_CODE_4M.secboot.fd",
			"/usr/share/OVMF/OVMF_CODE.secboot.fd",
		}
		fallback = "/usr/share/OVMF/OVMF_CODE_4M.ms.fd"
	}
	return pickFirstExistingPath(candidates, fallback)
}

func ResolveOVMFVarsTemplatePath(secure bool) string {
	candidates := []string{
		"/usr/share/OVMF/OVMF_VARS_4M.fd",
		"/usr/share/OVMF/OVMF_VARS.fd",
	}
	fallback := "/usr/share/OVMF/OVMF_VARS_4M.fd"
	if secure {
		candidates = []string{
			"/usr/share/OVMF/OVMF_VARS_4M.ms.fd",
			"/usr/share/OVMF/OVMF_VARS.ms.fd",
			"/usr/share/OVMF/OVMF_VARS_4M.secboot.fd",
		}
		fallback = "/usr/share/OVMF/OVMF_VARS_4M.ms.fd"
	}
	return pickFirstExistingPath(candidates, fallback)
}

func pickFirstExistingPath(candidates []string, fallback string) string {
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return fallback
}

func replaceOSOpenTagFirmware(osBlock string, useEFI bool) string {
	return vmBootTypeOSOpenTagRegexp.ReplaceAllStringFunc(osBlock, func(tag string) string {
		matches := vmBootTypeOSOpenTagRegexp.FindStringSubmatch(tag)
		if len(matches) < 3 {
			return tag
		}
		indent := matches[1]
		attrs := vmBootTypeFirmwareAttrRegexp.ReplaceAllString(matches[2], "")
		attrs = strings.TrimSpace(attrs)
		if useEFI {
			if attrs == "" {
				attrs = "firmware='efi'"
			} else {
				attrs += " firmware='efi'"
			}
		}
		if attrs == "" {
			return indent + "<os>"
		}
		return indent + "<os " + attrs + ">"
	})
}

// buildUEFIFirmwareFeatureXML 生成 UEFI 固件特性块（仅 <firmware> 特性声明）。
// 注意：此函数仅在使用 firmware='efi' 自动选择模式时有意义（已废弃）。
// 当前推荐方案为显式 loader/nvram，通过 loader 的 secure='yes' 属性来启用安全引导。
func buildUEFIFirmwareFeatureXML(secure bool) string {
	if !secure {
		return ""
	}
	return `    <firmware>
      <feature enabled='yes' name='enrolled-keys'/>
      <feature enabled='yes' name='secure-boot'/>
    </firmware>`
}

// buildUEFILoaderNVRAMXML 生成显式 loader 和 nvram 元素（不使用 firmware='efi' 自动选择时使用）。
func buildUEFILoaderNVRAMXML(secure bool, loaderPath, varsTemplate, nvramPath string) string {
	loaderAttrs := " readonly='yes' type='pflash'"
	if secure {
		loaderAttrs = " readonly='yes' secure='yes' type='pflash'"
	}
	return fmt.Sprintf("    <loader%s>%s</loader>\n%s",
		loaderAttrs, loaderPath, BuildNVRAMElementXML(varsTemplate, nvramPath))
}

// BuildNVRAMElementXML 按当前 libvirt 版本能力生成 <nvram> 元素。
//
// - libvirt < 9.2.0：不声明 format（libvirt 会忽略并按 raw 加载），磁盘文件必须是 raw；
// - libvirt >= 9.2.0：显式声明 format=PreferredNVRAMFormat()，便于 dumpxml 自解释；
// - libvirt >= 10.10.0：额外声明 templateFormat='raw'（OVMF VARS 模板始终是 raw）。
//
// 该函数是所有创建/克隆/导入路径写 <nvram> 的唯一入口，避免各处手写 format 字面量再次引入格式错配。
func BuildNVRAMElementXML(templatePath, nvramPath string) string {
	attrs := fmt.Sprintf(" template='%s'", templatePath)
	if SupportsNVRAMTemplateFormatAttr() {
		attrs += " templateFormat='raw'" // OVMF VARS 模板始终为 raw
	}
	if SupportsNVRAMFormatAttr() {
		attrs += fmt.Sprintf(" format='%s'", PreferredNVRAMFormat())
	}
	return fmt.Sprintf("    <nvram%s>%s</nvram>", attrs, nvramPath)
}

func insertUEFIFirmwareXML(osBlock, firmwareXML string) string {
	if strings.TrimSpace(firmwareXML) == "" {
		return osBlock
	}
	if vmBootTypeTypeCloseRegexp.MatchString(osBlock) {
		return vmBootTypeTypeCloseRegexp.ReplaceAllString(osBlock, "</type>\n"+firmwareXML)
	}
	if strings.Contains(osBlock, "</os>") {
		return strings.Replace(osBlock, "</os>", firmwareXML+"\n  </os>", 1)
	}
	return osBlock
}

func ensureVMSecureBootSMM(xmlContent string) string {
	if vmBootTypeSMMRegexp.MatchString(xmlContent) {
		return vmBootTypeSMMRegexp.ReplaceAllStringFunc(xmlContent, func(node string) string {
			if strings.Contains(node, "state='on'") || strings.Contains(node, `state="on"`) {
				return node
			}
			node = strings.ReplaceAll(node, `state="off"`, `state="on"`)
			node = strings.ReplaceAll(node, `state='off'`, `state="on"`)
			if !strings.Contains(node, "state='") && !strings.Contains(node, `state="`) {
				node = strings.Replace(node, "/>", " state='on'/>", 1)
			}
			return node
		})
	}
	if vmBootTypeFeaturesRegexp.MatchString(xmlContent) {
		return vmBootTypeFeaturesRegexp.ReplaceAllStringFunc(xmlContent, func(block string) string {
			return strings.Replace(block, "</features>", "    <smm state='on'/>\n  </features>", 1)
		})
	}
	featuresXML := "  <features>\n    <smm state='on'/>\n  </features>\n"
	switch {
	case strings.Contains(xmlContent, "<clock "):
		return strings.Replace(xmlContent, "<clock ", featuresXML+"  <clock ", 1)
	case strings.Contains(xmlContent, "<clock>"):
		return strings.Replace(xmlContent, "<clock>", featuresXML+"  <clock>", 1)
	case strings.Contains(xmlContent, "<devices/>"):
		return strings.Replace(xmlContent, "<devices/>", featuresXML+"  <devices/>", 1)
	case strings.Contains(xmlContent, "<devices />"):
		return strings.Replace(xmlContent, "<devices />", featuresXML+"  <devices />", 1)
	case strings.Contains(xmlContent, "<devices>"):
		return strings.Replace(xmlContent, "<devices>", featuresXML+"  <devices>", 1)
	case strings.Contains(xmlContent, "<on_poweroff>"):
		return strings.Replace(xmlContent, "<on_poweroff>", featuresXML+"  <on_poweroff>", 1)
	default:
		return xmlContent
	}
}

// ApplyVMBootTypeToDomainXML 将引导方式写入 domain XML。
// 使用显式 <loader> + <nvram> 模式（不使用 firmware='efi' 自动选择），
// <nvram> 的 format/templateFormat 属性由 BuildNVRAMElementXML 按 libvirt 版本能力决定，
// 确保磁盘上的 NVRAM 真实格式与 libvirt 实际加载的 pflash 格式一致（否则 OVMF 读到错误变量存储会黑屏），
// 同时避免不同环境缺少 firmware descriptor 导致 "Unable to find 'efi' firmware" 错误。
func ApplyVMBootTypeToDomainXML(name, xmlContent, bootType string) (string, error) {
	normalized := NormalizeVMBootType(bootType)
	if normalized == "" {
		return "", fmt.Errorf("不支持的引导方式: %s", bootType)
	}

	vmArch := ParseVMArchFromDomainXML(xmlContent)
	machineType := ParseVMMachineTypeFromDomainXML(xmlContent)
	if normalized == VMBootTypeBIOS && vmArch == "aarch64" {
		return "", fmt.Errorf("ARM 架构虚拟机不支持 BIOS 引导")
	}
	if normalized == VMBootTypeUEFISecure {
		if vmArch == "aarch64" || vmArch == "riscv64" {
			return "", fmt.Errorf("当前架构暂不支持 UEFI 安全引导")
		}
		if machineType == "i440fx" {
			return "", fmt.Errorf("i440fx 机型不支持 UEFI 安全引导")
		}
	}

	osBlock := vmBootTypeOSBlockRegexp.FindString(xmlContent)
	if strings.TrimSpace(osBlock) == "" {
		return "", fmt.Errorf("未找到虚拟机的 <os> 配置段")
	}

	// 清除所有 UEFI 相关元素：firmware 属性、firmware 特性块、loader、nvram
	cleanedOS := vmBootTypeFirmwareBlockRegexp.ReplaceAllString(osBlock, "")
	cleanedOS = vmBootTypeLoaderBlockRegexp.ReplaceAllString(cleanedOS, "")
	cleanedOS = vmBootTypeNVRAMBlockRegexp.ReplaceAllString(cleanedOS, "")
	// 始终移除 firmware='efi' 属性，改用显式 loader/nvram
	cleanedOS = replaceOSOpenTagFirmware(cleanedOS, false)

	if normalized != VMBootTypeBIOS {
		secure := normalized == VMBootTypeUEFISecure
		if vmArch == "" {
			vmArch = "x86_64"
		}
		profile := arch.GetProfile(vmArch)
		loaderPath := profile.UEFIFirmwarePath(secure)
		varsTemplate := profile.UEFIVarsTemplatePath(secure)
		nvramPath := resolveVMNVRAMPath(name, xmlContent)
		loaderNVRAMXML := buildUEFILoaderNVRAMXML(secure, loaderPath, varsTemplate, nvramPath)
		cleanedOS = insertUEFIFirmwareXML(cleanedOS, loaderNVRAMXML)
	}

	updated := strings.Replace(xmlContent, osBlock, cleanedOS, 1)
	if normalized == VMBootTypeUEFISecure {
		updated = ensureVMSecureBootSMM(updated)
	}
	return updated, nil
}

// EnsureVMUEFINVRAMFile 确保 UEFI NVRAM 文件存在，且其磁盘格式与当前 libvirt 版本策略一致。
//
// 该函数是各生命周期路径（创建/导入/克隆/迁移接管/引导方式切换）统一的 NVRAM 自愈入口：
//   - 文件不存在 → 按 PreferredNVRAMFormat() 从模板生成；
//   - 文件存在但格式与策略不符 → 仅在虚拟机“关机”状态下转换（运行/暂停时 QEMU 持有 pflash，
//     就地替换会导致未定义行为甚至变量存储损坏，因此仅告警并跳过，待下次关机生命周期操作自愈）。
//
// vmName 传空表示无法核实运行状态（例如尚未定义的临时 XML），此时按“非运行”处理以允许生成/转换。
func EnsureVMUEFINVRAMFile(name, xmlContent, bootType string) error {
	normalized := NormalizeVMBootType(bootType)
	if normalized != VMBootTypeUEFI && normalized != VMBootTypeUEFISecure {
		return nil
	}

	nvramPath := resolveVMNVRAMPath(name, xmlContent)
	if nvramPath == "" {
		return fmt.Errorf("未找到可用的 UEFI NVRAM 路径")
	}

	wantFormat := PreferredNVRAMFormat()

	if _, err := os.Stat(nvramPath); err == nil {
		actual := DetectQemuImageFormat(nvramPath)
		if actual == "" {
			// 无法判定格式（文件损坏/权限/qemu-img 异常）——不要当作“没问题”，明确报错。
			return fmt.Errorf("无法识别 NVRAM 文件格式: %s", nvramPath)
		}
		if actual == wantFormat {
			return nil
		}
		// 格式不符，需要修复：仅在关机状态下就地转换。
		if name != "" && domainIsActive(name) {
			logger.App.Warn("NVRAM 格式与 libvirt 策略不符，但虚拟机正在运行，暂不转换（关机后将自动修复）",
				"vm", name, "path", nvramPath, "actual", actual, "want", wantFormat)
			return nil
		}
		if err := ConvertNVRAMFormat(nvramPath, actual, wantFormat); err != nil {
			return fmt.Errorf("转换 UEFI NVRAM 格式失败: %w", err)
		}
		return nil
	}

	vmArch := ParseVMArchFromDomainXML(xmlContent)
	if vmArch == "" {
		vmArch = "x86_64"
	}
	profile := arch.GetProfile(vmArch)
	templatePath := profile.UEFIVarsTemplatePath(normalized == VMBootTypeUEFISecure)
	if err := CreateNVRAMFromTemplate(templatePath, nvramPath); err != nil {
		return fmt.Errorf("创建 UEFI NVRAM 文件失败: %w", err)
	}
	return nil
}

// domainIsActive 通过 virsh domstate 判断虚拟机是否处于运行/暂停等活动状态。
// 无法判定时保守返回 true（宁可跳过转换也不要在可能运行的虚拟机上就地替换 NVRAM）。
func domainIsActive(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	result := utils.ExecCommand("virsh", "domstate", name)
	if result.Error != nil {
		// 域不存在时 virsh 报错——此时并非“运行中”，允许对临时/未定义域的文件进行操作。
		if strings.Contains(result.Stderr, "not found") ||
			strings.Contains(result.Stderr, "Domain not found") ||
			strings.Contains(result.Stderr, "failed to get domain") {
			return false
		}
		return true
	}
	state := strings.ToLower(strings.TrimSpace(result.Stdout))
	return state != "shut off" && state != "shutoff" && state != ""
}

func DetectQemuImageFormat(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	result := utils.ExecCommand("qemu-img", "info", "-U", "--output=json", path)
	if result.Error != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(parseQemuInfoStr(result.Stdout, "format")))
}

// CreateNVRAMFromTemplate 从模板（通常是 OVMF VARS，也可能是源虚拟机的 NVRAM）生成目标 NVRAM 文件，
// 目标格式由当前 libvirt 版本策略（PreferredNVRAMFormat）决定。
// 当源格式与目标格式一致时直接按字节复制（更快且对变量存储保持位精确）；否则用 qemu-img 转换。
func CreateNVRAMFromTemplate(templatePath, nvramPath string) error {
	return createNVRAMFromTemplate(templatePath, nvramPath, PreferredNVRAMFormat())
}

// CreateQCOW2NVRAMFromTemplate 已废弃：保留以兼容历史调用，实际按当前版本策略生成。
//
// Deprecated: 使用 CreateNVRAMFromTemplate。名称中的 "QCOW2" 已不再准确——
// 目标格式随 libvirt 版本而定（旧版本必须使用 raw）。
func CreateQCOW2NVRAMFromTemplate(templatePath, nvramPath string) error {
	return CreateNVRAMFromTemplate(templatePath, nvramPath)
}

func createNVRAMFromTemplate(templatePath, nvramPath, targetFormat string) error {
	templatePath = strings.TrimSpace(templatePath)
	nvramPath = strings.TrimSpace(nvramPath)
	targetFormat = NormalizeQemuImgFormat(targetFormat)
	if targetFormat == "" {
		targetFormat = "raw"
	}
	if templatePath == "" || nvramPath == "" {
		return fmt.Errorf("NVRAM 模板路径或目标路径为空")
	}
	// 检查模板文件是否存在
	if _, err := os.Stat(templatePath); os.IsNotExist(err) {
		return fmt.Errorf("OVMF模板文件不存在: %s, 请确认已安装OVMF固件 ( Debian/Ubuntu: apt install ovmf, CentOS/RHEL: yum install edk2-ovmf )", templatePath)
	}
	if err := os.MkdirAll(filepath.Dir(nvramPath), 0755); err != nil {
		return fmt.Errorf("创建 UEFI NVRAM 目录失败: %w", err)
	}
	sourceFormat := DetectQemuImageFormat(templatePath)
	if sourceFormat == "" {
		sourceFormat = "raw"
	}
	_ = os.Remove(nvramPath)
	if sourceFormat == targetFormat {
		// 格式一致：直接按字节复制，保持变量存储位精确。
		if err := copyFileContents(templatePath, nvramPath); err != nil {
			return fmt.Errorf("复制 NVRAM 模板失败: %w", err)
		}
	} else {
		result := utils.ExecCommand("qemu-img", "convert", "-f", sourceFormat, "-O", targetFormat, templatePath, nvramPath)
		if result.Error != nil {
			return fmt.Errorf("转换 NVRAM 为 %s 失败: %s", targetFormat, firstNonEmpty(result.Stderr, result.Error.Error()))
		}
	}
	return applyNVRAMFilePerms(nvramPath)
}

func applyNVRAMFilePerms(nvramPath string) error {
	if err := os.Chmod(nvramPath, 0600); err != nil {
		return fmt.Errorf("设置 NVRAM 文件权限失败: %w", err)
	}
	if err := utils.ChownLibvirtQEMU(nvramPath); err != nil {
		return fmt.Errorf("设置 NVRAM 文件属主失败: %w", err)
	}
	return nil
}

func copyFileContents(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0600)
}

// SetShimFallbackNoReboot 预置 shim fallback 的连续引导标记。
// 首次启动仍会自动登记发行版启动项，但不会显示倒计时并执行冷复位。
//
// 注意：virt-fw-vars 只能正确处理 raw edk2 变量存储；直接喂 qcow2 会让它把 qcow2 头部
// 当作变量存储扫描（历史 bug，导致静默损坏）。因此当 NVRAM 为 qcow2 时，
// 先转成 raw 临时文件、写入标记、再转回原格式，最后原子替换。
func SetShimFallbackNoReboot(nvramPath string) error {
	nvramPath = strings.TrimSpace(nvramPath)
	if nvramPath == "" {
		return fmt.Errorf("NVRAM 路径为空")
	}
	if _, err := os.Stat(nvramPath); err != nil {
		return fmt.Errorf("读取 NVRAM 文件失败: %w", err)
	}

	toolPath, err := exec.LookPath("virt-fw-vars")
	if err != nil {
		return fmt.Errorf("未找到 virt-fw-vars，请安装 python3-virt-firmware")
	}

	origFormat := DetectQemuImageFormat(nvramPath)
	if origFormat == "" {
		return fmt.Errorf("无法识别 NVRAM 文件格式: %s", nvramPath)
	}

	// virt-fw-vars 的输入/输出都用 raw：若原文件是 qcow2 先转 raw。
	rawInput := nvramPath
	var rawInputTmp string
	if origFormat != "raw" {
		rawInputTmp = nvramPath + ".shim-in.raw"
		_ = os.Remove(rawInputTmp)
		if r := utils.ExecCommand("qemu-img", "convert", "-f", origFormat, "-O", "raw", nvramPath, rawInputTmp); r.Error != nil {
			return fmt.Errorf("转换 NVRAM 为 raw 以写入 shim 标记失败: %s", firstNonEmpty(r.Stderr, r.Error.Error()))
		}
		defer os.Remove(rawInputTmp)
		rawInput = rawInputTmp
	}

	rawOutput := nvramPath + ".shim-out.raw"
	_ = os.Remove(rawOutput)
	defer os.Remove(rawOutput)

	result := utils.ExecCommand(toolPath,
		"--input", rawInput,
		"--output", rawOutput,
		"--set-fallback-no-reboot",
	)
	if result.Error != nil {
		return fmt.Errorf("写入 shim 连续引导标记失败: %s", firstNonEmpty(result.Stderr, result.Error.Error()))
	}
	// 校验变量存储有效性：virt-fw-vars --print 能成功解析即认为是合法的 edk2 varstore。
	if r := utils.ExecCommand(toolPath, "--input", rawOutput, "--print"); r.Error != nil {
		return fmt.Errorf("写入后的 NVRAM 不是有效的 edk2 变量存储: %s", firstNonEmpty(r.Stderr, r.Error.Error()))
	}

	// 转回原格式（若需要）。
	finalTmp := nvramPath + ".shim.tmp"
	_ = os.Remove(finalTmp)
	defer os.Remove(finalTmp)
	if origFormat == "raw" {
		if err := copyFileContents(rawOutput, finalTmp); err != nil {
			return fmt.Errorf("准备 NVRAM 结果文件失败: %w", err)
		}
	} else {
		if r := utils.ExecCommand("qemu-img", "convert", "-f", "raw", "-O", origFormat, rawOutput, finalTmp); r.Error != nil {
			return fmt.Errorf("将 NVRAM 转回 %s 失败: %s", origFormat, firstNonEmpty(r.Stderr, r.Error.Error()))
		}
	}
	if err := applyNVRAMFilePerms(finalTmp); err != nil {
		return err
	}
	if err := os.Rename(finalTmp, nvramPath); err != nil {
		return fmt.Errorf("替换 NVRAM 文件失败: %w", err)
	}
	return nil
}

// EnsureNVRAMFormatMatches 确保磁盘上的 NVRAM 文件为 wantFormat，不符则转换（带备份与回滚）。
func EnsureNVRAMFormatMatches(nvramPath, wantFormat string) error {
	nvramPath = strings.TrimSpace(nvramPath)
	wantFormat = NormalizeQemuImgFormat(wantFormat)
	if nvramPath == "" {
		return fmt.Errorf("NVRAM 路径为空")
	}
	if wantFormat == "" {
		wantFormat = PreferredNVRAMFormat()
	}
	actual := DetectQemuImageFormat(nvramPath)
	if actual == "" {
		return fmt.Errorf("无法识别 NVRAM 文件格式: %s", nvramPath)
	}
	if actual == wantFormat {
		return nil
	}
	return ConvertNVRAMFormat(nvramPath, actual, wantFormat)
}

// ConvertNVRAMFormat 就地转换 NVRAM 文件格式（fromFormat→toFormat），保留原文件为带格式后缀的备份，失败自动回滚。
// 调用方需自行保证虚拟机处于关机状态（QEMU 运行时持有 pflash，就地替换会导致变量存储损坏）。
func ConvertNVRAMFormat(nvramPath, fromFormat, toFormat string) error {
	nvramPath = strings.TrimSpace(nvramPath)
	fromFormat = NormalizeQemuImgFormat(fromFormat)
	toFormat = NormalizeQemuImgFormat(toFormat)
	if nvramPath == "" {
		return fmt.Errorf("NVRAM 路径为空")
	}
	if fromFormat == "" {
		if fromFormat = DetectQemuImageFormat(nvramPath); fromFormat == "" {
			return fmt.Errorf("无法识别 NVRAM 源格式: %s", nvramPath)
		}
	}
	if toFormat == "" {
		toFormat = PreferredNVRAMFormat()
	}
	if fromFormat == toFormat {
		return nil
	}
	tmpPath := nvramPath + "." + toFormat + ".tmp"
	backupPath := nvramPath + "." + fromFormat + ".bak"
	for i := 1; ; i++ {
		if _, err := os.Stat(backupPath); os.IsNotExist(err) {
			break
		}
		backupPath = fmt.Sprintf("%s.%s.bak.%d", nvramPath, fromFormat, i)
	}
	_ = os.Remove(tmpPath)
	result := utils.ExecCommand("qemu-img", "convert", "-f", fromFormat, "-O", toFormat, nvramPath, tmpPath)
	if result.Error != nil {
		return fmt.Errorf("转换 NVRAM %s→%s 失败: %s", fromFormat, toFormat, firstNonEmpty(result.Stderr, result.Error.Error()))
	}
	if err := os.Rename(nvramPath, backupPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("备份原 NVRAM 文件失败: %w", err)
	}
	if err := os.Rename(tmpPath, nvramPath); err != nil {
		_ = os.Rename(backupPath, nvramPath)
		_ = os.Remove(tmpPath)
		return fmt.Errorf("替换 NVRAM 文件失败: %w", err)
	}
	logger.App.Info("已转换 NVRAM 格式", "path", nvramPath, "from", fromFormat, "to", toFormat, "backup", backupPath)
	return applyNVRAMFilePerms(nvramPath)
}

// ConvertExistingNVRAMToQCOW2 已废弃：保留以兼容历史调用。
//
// Deprecated: 使用 EnsureNVRAMFormatMatches / ConvertNVRAMFormat。旧行为强制 qcow2，
// 在 libvirt < 9.2.0 上会导致黑屏。
func ConvertExistingNVRAMToQCOW2(nvramPath string) error {
	return EnsureNVRAMFormatMatches(nvramPath, "qcow2")
}

func DomainUsesPflashNVRAM(xmlContent string) bool {
	return strings.Contains(xmlContent, "type='pflash'") ||
		strings.Contains(xmlContent, `type="pflash"`)
}

func ExtractDomainNVRAMFormat(xmlContent string) string {
	matches := regexp.MustCompile(`(?s)<nvram\b([^>]*)>`).FindStringSubmatch(xmlContent)
	if len(matches) < 2 {
		return ""
	}
	attrMatches := regexp.MustCompile(`\bformat=['"]([^'"]+)['"]`).FindStringSubmatch(matches[1])
	if len(attrMatches) < 2 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(attrMatches[1]))
}

func ExtractDomainNVRAMPath(xmlContent string) string {
	matches := regexp.MustCompile(`(?s)<nvram[^>]*>\s*([^<]+?)\s*</nvram>`).FindStringSubmatch(xmlContent)
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

var (
	nvramTagRegexp        = regexp.MustCompile(`(?s)<nvram\b([^>]*)>`)
	nvramFormatAttrRegexp = regexp.MustCompile(`\s*\bformat=['"][^'"]+['"]`)
)

func SetDomainNVRAMFormat(xmlContent, format string) string {
	format = strings.TrimSpace(format)
	if strings.TrimSpace(xmlContent) == "" || format == "" {
		return xmlContent
	}
	return nvramTagRegexp.ReplaceAllStringFunc(xmlContent, func(tag string) string {
		if nvramFormatAttrRegexp.MatchString(tag) {
			return nvramFormatAttrRegexp.ReplaceAllString(tag, " format='"+format+"'")
		}
		return strings.Replace(tag, "<nvram", "<nvram format='"+format+"'", 1)
	})
}

// RemoveDomainNVRAMFormat 移除 <nvram> 的 format 属性（用于 libvirt 不支持该属性时避免误导性配置）。
func RemoveDomainNVRAMFormat(xmlContent string) string {
	if strings.TrimSpace(xmlContent) == "" {
		return xmlContent
	}
	return nvramTagRegexp.ReplaceAllStringFunc(xmlContent, func(tag string) string {
		return nvramFormatAttrRegexp.ReplaceAllString(tag, "")
	})
}

// ApplyDomainNVRAMFormatPolicy 按当前 libvirt 能力统一处理 <nvram> 的 format 属性：
// 支持 format 属性时注入指定格式（默认 PreferredNVRAMFormat），否则移除该属性以免 dumpxml 与实际不符。
func ApplyDomainNVRAMFormatPolicy(xmlContent, format string) string {
	format = strings.TrimSpace(format)
	if format == "" {
		format = PreferredNVRAMFormat()
	}
	if SupportsNVRAMFormatAttr() {
		return SetDomainNVRAMFormat(xmlContent, format)
	}
	return RemoveDomainNVRAMFormat(xmlContent)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

// parseQemuInfoStr 从 qemu-img info JSON 中解析字符串值（仅读取顶层字段）
func parseQemuInfoStr(output, key string) string {
	var data map[string]json.RawMessage
	if err := json.Unmarshal([]byte(output), &data); err != nil {
		return ""
	}
	raw, ok := data[key]
	if !ok {
		return ""
	}
	var val string
	if err := json.Unmarshal(raw, &val); err != nil {
		return ""
	}
	return val
}

// ==================== UEFI 固件兼容模式 ====================

// ApplyFirmwareCompatToDomainXML 将固件兼容模式应用到 domain XML。
// 仅对 aarch64 架构有效，将 UEFI 固件替换为旧版 EDK2。
func ApplyFirmwareCompatToDomainXML(name, xmlContent string, enabled *bool) (string, error) {
	if enabled == nil || !*enabled {
		return xmlContent, nil
	}
	vmArch := ParseVMArchFromDomainXML(xmlContent)
	if vmArch != "aarch64" {
		return xmlContent, nil // 仅 ARM 架构支持
	}

	profile := arch.GetProfile(vmArch)
	type legacyProvider interface {
		UEFILegacyFirmwarePath() string
		UEFILegacyVarsTemplatePath() string
	}
	lp, ok := profile.(legacyProvider)
	if !ok {
		return xmlContent, nil
	}

	legacyLoader := lp.UEFILegacyFirmwarePath()
	legacyVars := lp.UEFILegacyVarsTemplatePath()
	if legacyLoader == "" || legacyVars == "" {
		return xmlContent, fmt.Errorf("旧版兼容固件未安装，请检查 /opt/project/QVMConsole/firmware/ 目录")
	}

	// 替换 loader 路径
	osBlock := vmBootTypeOSBlockRegexp.FindString(xmlContent)
	if osBlock == "" {
		return xmlContent, nil
	}

	// 先移除 firmware='efi' 属性和 firmware 子元素（避免 libvirt 自动匹配固件）
	newOS := replaceOSOpenTagFirmware(osBlock, false)
	newOS = vmBootTypeFirmwareBlockRegexp.ReplaceAllString(newOS, "")

	// 替换 loader 内容
	loaderRegexp := regexp.MustCompile(`(<loader[^>]*>)[^<]*(</loader>)`)
	newOS = loaderRegexp.ReplaceAllString(newOS, "${1}"+legacyLoader+"${2}")

	// 替换 nvram template
	nvramTemplateRegexp := regexp.MustCompile(`template='[^']*'`)
	newOS = nvramTemplateRegexp.ReplaceAllString(newOS, "template='"+legacyVars+"'")

	return strings.Replace(xmlContent, osBlock, newOS, 1), nil
}

// ==================== 直接内核引导 ====================

var (
	vmKernelRegexp  = regexp.MustCompile(`(?s)\n?\s*<kernel\b[^>]*>.*?</kernel>`)
	vmInitrdRegexp  = regexp.MustCompile(`(?s)\n?\s*<initrd\b[^>]*>.*?</initrd>`)
	vmCmdlineRegexp = regexp.MustCompile(`(?s)\n?\s*<cmdline\b[^>]*>.*?</cmdline>`)
)

// DirectBootConfig 直接内核引导配置。
type DirectBootConfig struct {
	Enabled bool   `json:"enabled"`
	Kernel  string `json:"kernel,omitempty"`
	Initrd  string `json:"initrd,omitempty"`
	Cmdline string `json:"cmdline,omitempty"`
}

// ApplyDirectBootToDomainXML 将直接内核引导配置应用到 domain XML。
func ApplyDirectBootToDomainXML(xmlContent string, cfg *DirectBootConfig) (string, error) {
	if cfg == nil || !cfg.Enabled {
		return xmlContent, nil
	}
	if cfg.Kernel == "" {
		return xmlContent, fmt.Errorf("直接内核引导需要指定 kernel 路径")
	}

	osBlock := vmBootTypeOSBlockRegexp.FindString(xmlContent)
	if osBlock == "" {
		return xmlContent, fmt.Errorf("未找到 <os> 配置段")
	}

	// 清除已有的 kernel/initrd/cmdline
	newOS := vmKernelRegexp.ReplaceAllString(osBlock, "")
	newOS = vmInitrdRegexp.ReplaceAllString(newOS, "")
	newOS = vmCmdlineRegexp.ReplaceAllString(newOS, "")

	// 在 </os> 前插入 kernel/initrd/cmdline
	var directBootXML string
	directBootXML += fmt.Sprintf("    <kernel>%s</kernel>\n", cfg.Kernel)
	if cfg.Initrd != "" {
		directBootXML += fmt.Sprintf("    <initrd>%s</initrd>\n", cfg.Initrd)
	}
	if cfg.Cmdline != "" {
		directBootXML += fmt.Sprintf("    <cmdline>%s</cmdline>\n", cfg.Cmdline)
	}

	newOS = strings.Replace(newOS, "</os>", directBootXML+"  </os>", 1)
	return strings.Replace(xmlContent, osBlock, newOS, 1), nil
}

// RemoveDirectBootFromDomainXML 从 domain XML 中移除直接内核引导配置。
func RemoveDirectBootFromDomainXML(xmlContent string) string {
	osBlock := vmBootTypeOSBlockRegexp.FindString(xmlContent)
	if osBlock == "" {
		return xmlContent
	}
	newOS := vmKernelRegexp.ReplaceAllString(osBlock, "")
	newOS = vmInitrdRegexp.ReplaceAllString(newOS, "")
	newOS = vmCmdlineRegexp.ReplaceAllString(newOS, "")
	return strings.Replace(xmlContent, osBlock, newOS, 1)
}

// DetectFirmwareCompatFromDomainXML 从 domain XML 中检测是否启用了固件兼容模式。
// 通过检查 loader 路径是否包含 "legacy" 关键字或是否指向旧版固件来判断。
func DetectFirmwareCompatFromDomainXML(xmlContent string) bool {
	if ParseVMArchFromDomainXML(xmlContent) != "aarch64" {
		return false
	}
	loaderRegexp := regexp.MustCompile(`<loader[^>]*>([^<]*)</loader>`)
	matches := loaderRegexp.FindStringSubmatch(xmlContent)
	if len(matches) < 2 {
		return false
	}
	loaderPath := strings.ToLower(matches[1])
	return strings.Contains(loaderPath, "legacy") || strings.Contains(loaderPath, "2024")
}

// DetectDirectBootFromDomainXML 从 domain XML 中检测直接内核引导配置。
func DetectDirectBootFromDomainXML(xmlContent string) *DirectBootConfig {
	kernelRegexp := regexp.MustCompile(`<kernel>([^<]*)</kernel>`)
	initrdRegexp := regexp.MustCompile(`<initrd>([^<]*)</initrd>`)
	cmdlineRegexp := regexp.MustCompile(`<cmdline>([^<]*)</cmdline>`)

	kernelMatch := kernelRegexp.FindStringSubmatch(xmlContent)
	if len(kernelMatch) < 2 || strings.TrimSpace(kernelMatch[1]) == "" {
		return nil
	}

	cfg := &DirectBootConfig{
		Enabled: true,
		Kernel:  strings.TrimSpace(kernelMatch[1]),
	}
	if m := initrdRegexp.FindStringSubmatch(xmlContent); len(m) >= 2 {
		cfg.Initrd = strings.TrimSpace(m[1])
	}
	if m := cmdlineRegexp.FindStringSubmatch(xmlContent); len(m) >= 2 {
		cfg.Cmdline = strings.TrimSpace(m[1])
	}
	return cfg
}

// ExtractKernelFromISO 从 ARM64 ISO 中提取内核和 initrd。
// 返回 kernel 和 initrd 的路径（提取到 /var/lib/libvirt/boot/<vmname>/ 目录）。
func ExtractKernelFromISO(vmName, isoPath string) (kernel, initrd string, err error) {
	if isoPath == "" {
		return "", "", fmt.Errorf("未指定 ISO 路径")
	}

	// 创建提取目录
	extractDir := fmt.Sprintf("/var/lib/libvirt/boot/%s", vmName)
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return "", "", fmt.Errorf("创建内核提取目录失败: %w", err)
	}

	// 挂载 ISO（读取 ISO 属于 IO 操作，不设置自动超时）
	mountPoint := fmt.Sprintf("/tmp/iso-mount-%s", vmName)
	_ = os.MkdirAll(mountPoint, 0755)
	result := utils.ExecCommandNoTimeout("mount", "-o", "loop,ro", isoPath, mountPoint)
	if result.Error != nil {
		return "", "", fmt.Errorf("挂载 ISO 失败: %s", firstNonEmpty(result.Stderr, result.Error.Error()))
	}
	defer func() {
		_ = utils.ExecCommandNoTimeout("umount", mountPoint).Error
		_ = os.Remove(mountPoint)
	}()

	// 搜索 vmlinuz 和 initrd.img
	kernelCandidates := []string{
		mountPoint + "/images/pxeboot/vmlinuz",
		mountPoint + "/boot/vmlinuz",
		mountPoint + "/vmlinuz",
		mountPoint + "/casper/vmlinuz",
	}
	initrdCandidates := []string{
		mountPoint + "/images/pxeboot/initrd.img",
		mountPoint + "/boot/initrd.img",
		mountPoint + "/initrd.img",
		mountPoint + "/casper/initrd",
	}

	kernelSrc := pickFirstExistingPath(kernelCandidates, "")
	initrdSrc := pickFirstExistingPath(initrdCandidates, "")

	if kernelSrc == "" {
		return "", "", fmt.Errorf("在 ISO 中未找到 vmlinuz")
	}

	// 复制内核（从 ISO 读取并写入属于 IO 操作，不设置自动超时）
	kernel = filepath.Join(extractDir, "vmlinuz")
	cpResult := utils.ExecCommandNoTimeout("cp", kernelSrc, kernel)
	if cpResult.Error != nil {
		return "", "", fmt.Errorf("复制内核失败: %s", firstNonEmpty(cpResult.Stderr, cpResult.Error.Error()))
	}

	// 复制 initrd
	if initrdSrc != "" {
		initrd = filepath.Join(extractDir, "initrd.img")
		cpResult = utils.ExecCommandNoTimeout("cp", initrdSrc, initrd)
		if cpResult.Error != nil {
			return "", "", fmt.Errorf("复制 initrd 失败: %s", firstNonEmpty(cpResult.Stderr, cpResult.Error.Error()))
		}
	}

	return kernel, initrd, nil
}

// ParseFirstCDROMISOPath 从 domain XML 中提取第一个 CDROM 的 ISO 路径。
func ParseFirstCDROMISOPath(xmlContent string) string {
	cdromRegexp := regexp.MustCompile(`(?s)<disk[^>]*device=['"]cdrom['"][^>]*>.*?</disk>`)
	sourceRegexp := regexp.MustCompile(`<source[^>]*file=['"]([^'"]+)['"]`)

	matches := cdromRegexp.FindAllString(xmlContent, -1)
	for _, diskBlock := range matches {
		if m := sourceRegexp.FindStringSubmatch(diskBlock); len(m) >= 2 {
			path := strings.TrimSpace(m[1])
			if path != "" {
				return path
			}
		}
	}
	return ""
}

package template

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	libvirt "github.com/digitalocean/go-libvirt"

	"kvm_console/service/guestfs"
	"kvm_console/service/libvirt_rpc"
	"kvm_console/utils"
)

// NormalizeTemplateBootType exports normalizeTemplateBootType for clone Deps.
func NormalizeTemplateBootType(bootType string) string {
	return normalizeTemplateBootType(bootType)
}

func normalizeTemplateBootType(bootType string) string {
	switch strings.ToLower(strings.TrimSpace(bootType)) {
	case "uefi":
		return "uefi"
	case "bios":
		return "bios"
	default:
		return ""
	}
}

func shouldDetectTemplateBootType(templateType, bootType string, bootVerified bool) bool {
	normalizedType := strings.ToLower(strings.TrimSpace(templateType))
	normalizedBootType := normalizeTemplateBootType(bootType)
	if normalizedBootType != "" && bootVerified {
		return false
	}
	if normalizedBootType == "" {
		return true
	}
	return normalizedType == "windows" && normalizedBootType == "bios" && !bootVerified
}

// ResolveTemplateBootType resolves the effective boot type for a template.
// Exported for use by linked_clone.go in service root.
func ResolveTemplateBootType(templatePath, templateType, bootType string, bootVerified bool, detector func(string) string) (string, bool) {
	return resolveTemplateBootType(templatePath, templateType, bootType, bootVerified, detector)
}

func resolveTemplateBootType(templatePath, templateType, bootType string, bootVerified bool, detector func(string) string) (string, bool) {
	normalized := normalizeTemplateBootType(bootType)
	if !shouldDetectTemplateBootType(templateType, normalized, bootVerified) {
		return normalized, normalized != ""
	}
	if strings.TrimSpace(templatePath) == "" || detector == nil {
		return normalized, bootVerified && normalized != ""
	}
	detected := normalizeTemplateBootType(detector(templatePath))
	if detected != "" {
		return detected, true
	}
	return normalized, bootVerified && normalized != ""
}

// DetectTemplateBootType detects the boot type of a template/disk image.
//
// virt-filesystems 属于 libguestfs 系列命令，在嵌套虚拟化宿主机上会因 appliance
// 内部 qemu 设置 MSR 0x345 而 SIGABRT（signal 6）。故通过 guestfs 包执行以注入
// pmu=off 规避并复用崩溃自愈；否则裸调用崩溃后 result.Error != nil 会误落到 bios，
// 导致 UEFI 磁盘被错判为 BIOS 引导。
func DetectTemplateBootType(templatePath string) string {
	result := guestfs.ExecShellNoTimeout(fmt.Sprintf(
		"virt-filesystems -a %s --filesystems --long 2>/dev/null | awk 'tolower($0) ~ /(^|[[:space:]])vfat([[:space:]]|$)|efi/ {found=1} END {if (found) print \"uefi\"; else print \"bios\"}'",
		utils.ShellSingleQuote(templatePath),
	))
	if result.Error == nil {
		bootType := normalizeTemplateBootType(result.Stdout)
		if bootType != "" {
			return bootType
		}
	}
	return "bios"
}

func detectBootTypeFromDomainXML(xmlContent string) string {
	xmlContent = strings.TrimSpace(xmlContent)
	if xmlContent == "" {
		return ""
	}
	if strings.Contains(xmlContent, "firmware='efi'") || strings.Contains(xmlContent, `firmware="efi"`) {
		return "uefi"
	}
	return "bios"
}

// DetectVMBootType detects the boot type of a running/shutoff VM.
func DetectVMBootType(vmName string) string {
	for _, flags := range []uint32{2, 0} {
		xmlStr, err := libvirt_rpc.GetDomainXMLRPC(vmName, libvirt.DomainXMLFlags(flags))
		if err != nil {
			continue
		}
		bootType := detectBootTypeFromDomainXML(xmlStr)
		if bootType != "" {
			return bootType
		}
	}
	return ""
}

// DetectVMNVRAMPath detects the NVRAM path of a VM.
func DetectVMNVRAMPath(vmName string) string {
	for _, flags := range []uint32{2, 0} {
		xmlStr, err := libvirt_rpc.GetDomainXMLRPC(vmName, libvirt.DomainXMLFlags(flags))
		if err != nil {
			continue
		}
		if path := extractDomainNVRAMPath(xmlStr); path != "" {
			return path
		}
	}
	return ""
}

// ExtractDomainNVRAMPath extracts the NVRAM path from domain XML.
// Exported for use by snapshot subpackage via delegate.
func ExtractDomainNVRAMPath(xmlContent string) string {
	return extractDomainNVRAMPath(xmlContent)
}

func extractDomainNVRAMPath(xmlContent string) string {
	matches := regexp.MustCompile(`(?s)<nvram[^>]*>\s*([^<]+?)\s*</nvram>`).FindStringSubmatch(xmlContent)
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

func copyTemplateNVRAMFromVM(vmName, templatePath string) string {
	sourcePath := DetectVMNVRAMPath(vmName)
	if sourcePath == "" {
		return ""
	}
	if _, err := os.Stat(sourcePath); err != nil {
		return ""
	}
	targetPath := getTemplateNVRAMPath(templatePath)
	src, err := os.Open(sourcePath)
	if err != nil {
		return ""
	}
	defer src.Close()
	dst, err := os.Create(targetPath)
	if err != nil {
		return ""
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		_ = os.Remove(targetPath)
		return ""
	}
	_ = utils.ChownLibvirtQEMU(targetPath)
	_ = os.Chmod(targetPath, 0o600)
	return targetPath
}

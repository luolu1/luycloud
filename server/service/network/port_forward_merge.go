package network

import (
	"fmt"
	"net"
	"strings"

	"kvm_console/model"
)

// PortForwardAccessAddress 端口转发的一个对外访问地址（来自目标所属交换机的访问地址配置）。
type PortForwardAccessAddress struct {
	Label   string `json:"label"`
	Address string `json:"address"`
}

func portForwardProtocolOf(rule PortForwardRule) string {
	return strings.ToLower(strings.TrimSpace(rule.Protocol))
}

func samePortForwardTarget(a, b PortForwardRule) bool {
	return strings.TrimSpace(a.HostPort) == strings.TrimSpace(b.HostPort) &&
		strings.TrimSpace(a.DestIP) == strings.TrimSpace(b.DestIP) &&
		strings.TrimSpace(a.DestPort) == strings.TrimSpace(b.DestPort) &&
		strings.TrimSpace(a.SourceIP) == strings.TrimSpace(b.SourceIP)
}

func mergePortForwardPair(tcp, udp PortForwardRule) PortForwardRule {
	merged := tcp
	merged.Protocol = "both"
	merged.ID = "both|" + strings.TrimSpace(tcp.HostPort)
	merged.FirewallKeys = []string{tcp.FirewallKey, udp.FirewallKey}
	merged.RegionFilterEnabled = tcp.RegionFilterEnabled && udp.RegionFilterEnabled
	merged.RegionFilterInherited = tcp.RegionFilterInherited && udp.RegionFilterInherited
	return merged
}

// MergePortForwardRules 把同一宿主机端口、目标与入站白名单完全一致的 TCP/UDP 规则合并为一条 both 规则，
// 合并后的标识为 "both|<宿主机端口>"，编辑和删除会同时作用于底层两条规则。
func MergePortForwardRules(rules []PortForwardRule) []PortForwardRule {
	udpByPort := make(map[string]int)
	for i, rule := range rules {
		if portForwardProtocolOf(rule) == "udp" {
			udpByPort[strings.TrimSpace(rule.HostPort)] = i
		}
	}
	used := make(map[int]bool)
	result := make([]PortForwardRule, 0, len(rules))
	for i, rule := range rules {
		if used[i] {
			continue
		}
		if portForwardProtocolOf(rule) == "tcp" {
			if j, ok := udpByPort[strings.TrimSpace(rule.HostPort)]; ok && !used[j] && samePortForwardTarget(rule, rules[j]) {
				used[i], used[j] = true, true
				result = append(result, mergePortForwardPair(rule, rules[j]))
				continue
			}
		}
		used[i] = true
		result = append(result, rule)
	}
	return result
}

func getMergedPortForwardRule(hostPort string) (*PortForwardRule, error) {
	rules, err := listLivePortForwardsFromIPTables()
	if err != nil {
		return nil, err
	}
	id := "both|" + strings.TrimSpace(hostPort)
	for _, rule := range MergePortForwardRules(rules) {
		if rule.ID == id {
			merged := rule
			return &merged, nil
		}
	}
	return nil, fmt.Errorf("规则标识 %s 不存在", id)
}

// FillPortForwardAccessAddresses 按目标 IP 所属交换机的访问地址配置，为规则生成带描述的完整访问地址。
func FillPortForwardAccessAddresses(rules []PortForwardRule) {
	if model.DB == nil || len(rules) == 0 {
		return
	}
	var switches []model.VPCSwitch
	if err := model.DB.Select("id", "cidr", "access_addresses").Find(&switches).Error; err != nil {
		return
	}
	for i := range rules {
		destIP := strings.TrimSpace(rules[i].DestIP)
		hostPort := strings.TrimSpace(rules[i].HostPort)
		if destIP == "" || hostPort == "" {
			continue
		}
		for _, sw := range switches {
			if len(sw.AccessAddresses) == 0 || strings.TrimSpace(sw.CIDR) == "" || !ipInCIDR(destIP, sw.CIDR) {
				continue
			}
			for _, item := range sw.AccessAddresses {
				rules[i].AccessAddresses = append(rules[i].AccessAddresses, PortForwardAccessAddress{
					Label:   item.Label,
					Address: net.JoinHostPort(item.Host, hostPort),
				})
			}
			break
		}
	}
}

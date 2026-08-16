package account

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"strings"
)

// ResidentialSubnetInfo 描述一个预设的真实住宅宽带网段信息。
type ResidentialSubnetInfo struct {
	ID       string `json:"id"`
	CIDR     string `json:"cidr"`
	ISP      string `json:"isp"`
	Location string `json:"location"`
	Country  string `json:"country"`
	Flag     string `json:"flag"`
}

// DefaultResidentialSubnets 内置全球顶级住宅 ISP 真实网段列表。
var DefaultResidentialSubnets = []ResidentialSubnetInfo{
	// 北美 (US)
	{ID: "us-comcast-ca", CIDR: "73.168.0.0/16", ISP: "Comcast Xfinity", Location: "加利福尼亚", Country: "美国", Flag: "🇺🇸"},
	{ID: "us-comcast-wa", CIDR: "73.220.0.0/16", ISP: "Comcast Xfinity", Location: "华盛顿", Country: "美国", Flag: "🇺🇸"},
	{ID: "us-comcast-co", CIDR: "98.210.0.0/16", ISP: "Comcast Xfinity", Location: "科罗拉多", Country: "美国", Flag: "🇺🇸"},
	{ID: "us-comcast-il", CIDR: "67.180.0.0/16", ISP: "Comcast Xfinity", Location: "伊利诺伊", Country: "美国", Flag: "🇺🇸"},
	{ID: "us-comcast-or", CIDR: "98.248.0.0/16", ISP: "Comcast Xfinity", Location: "俄勒冈", Country: "美国", Flag: "🇺🇸"},
	{ID: "us-att-tx", CIDR: "108.85.0.0/16", ISP: "AT&T Internet", Location: "德克萨斯", Country: "美国", Flag: "🇺🇸"},
	{ID: "us-att-ca", CIDR: "76.218.0.0/16", ISP: "AT&T Internet", Location: "加利福尼亚", Country: "美国", Flag: "🇺🇸"},
	{ID: "us-att-fl", CIDR: "99.100.0.0/16", ISP: "AT&T Internet", Location: "佛罗里达", Country: "美国", Flag: "🇺🇸"},
	{ID: "us-att-ga", CIDR: "108.196.0.0/16", ISP: "AT&T Internet", Location: "乔治亚", Country: "美国", Flag: "🇺🇸"},
	{ID: "us-verizon-ny", CIDR: "100.36.0.0/16", ISP: "Verizon Fios", Location: "纽约", Country: "美国", Flag: "🇺🇸"},
	{ID: "us-verizon-ma", CIDR: "71.182.0.0/16", ISP: "Verizon Fios", Location: "马萨诸塞", Country: "美国", Flag: "🇺🇸"},
	{ID: "us-verizon-nj", CIDR: "108.16.0.0/16", ISP: "Verizon Fios", Location: "新泽西", Country: "美国", Flag: "🇺🇸"},
	{ID: "us-verizon-va", CIDR: "100.15.0.0/16", ISP: "Verizon Fios", Location: "弗吉尼亚", Country: "美国", Flag: "🇺🇸"},
	{ID: "us-spectrum-nc", CIDR: "142.129.0.0/16", ISP: "Charter Spectrum", Location: "北卡罗来纳", Country: "美国", Flag: "🇺🇸"},
	{ID: "us-spectrum-oh", CIDR: "75.130.0.0/16", ISP: "Charter Spectrum", Location: "俄亥俄", Country: "美国", Flag: "🇺🇸"},
	{ID: "us-spectrum-ny", CIDR: "24.180.0.0/16", ISP: "Charter Spectrum", Location: "纽约", Country: "美国", Flag: "🇺🇸"},
	{ID: "us-centurylink-az", CIDR: "71.222.0.0/16", ISP: "CenturyLink", Location: "亚利桑那", Country: "美国", Flag: "🇺🇸"},

	// 欧洲 (Europe)
	{ID: "uk-bt-london", CIDR: "86.154.0.0/16", ISP: "British Telecom", Location: "伦敦", Country: "英国", Flag: "🇬🇧"},
	{ID: "uk-bt-manchester", CIDR: "81.152.0.0/16", ISP: "British Telecom", Location: "曼彻斯特", Country: "英国", Flag: "🇬🇧"},
	{ID: "de-telekom-frankfurt", CIDR: "84.175.0.0/16", ISP: "Deutsche Telekom", Location: "法兰克福", Country: "德国", Flag: "🇩🇪"},
	{ID: "de-telekom-munich", CIDR: "80.136.0.0/16", ISP: "Deutsche Telekom", Location: "慕尼黑", Country: "德国", Flag: "🇩🇪"},

	// 亚太 (Asia-Pacific)
	{ID: "sg-singtel", CIDR: "116.14.0.0/16", ISP: "Singtel", Location: "新加坡", Country: "新加坡", Flag: "🇸🇬"},
	{ID: "sg-singtel-2", CIDR: "175.156.0.0/16", ISP: "Singtel", Location: "新加坡", Country: "新加坡", Flag: "🇸🇬"},
	{ID: "tw-cht-taipei", CIDR: "114.34.0.0/16", ISP: "中华电信", Location: "台北", Country: "中国台湾", Flag: "🇹🇼"},
	{ID: "tw-cht-hsinchu", CIDR: "220.135.0.0/16", ISP: "中华电信", Location: "新竹", Country: "中国台湾", Flag: "🇹🇼"},
	{ID: "jp-ocn-tokyo", CIDR: "122.215.0.0/16", ISP: "NTT OCN", Location: "东京", Country: "日本", Flag: "🇯🇵"},
	{ID: "jp-ocn-osaka", CIDR: "124.35.0.0/16", ISP: "NTT OCN", Location: "大阪", Country: "日本", Flag: "🇯🇵"},
}

// GetResidentialSubnets 返回系统支持的预设住宅网段列表。
func GetResidentialSubnets() []ResidentialSubnetInfo {
	return DefaultResidentialSubnets
}

// BatchAssignIPOptions 批量分配住宅 IP 选项。
type BatchAssignIPOptions struct {
	Mode              string   `json:"mode"`              // "unique" (默认独立打散) | "single" (统一分配同一 IP)
	SingleIP          string   `json:"singleIp"`          // single 模式下指定的 IP（若未传则从选定网段生成）
	SelectedSubnetIDs []string `json:"selectedSubnetIds"` // unique 模式下选中的网段列表，或 single 模式下单选的网段
	OverwriteAll      bool     `json:"overwriteAll"`      // true: 覆盖所有账号; false: 仅为空白账号分配
}

// ParseBatchAssignIPOptions 从各种 IPC 参数形态中解析 BatchAssignIPOptions。
func ParseBatchAssignIPOptions(args []interface{}) BatchAssignIPOptions {
	opts := BatchAssignIPOptions{OverwriteAll: true}
	if len(args) == 0 {
		return opts
	}
	if jsonStr, ok := args[0].(string); ok && strings.HasPrefix(strings.TrimSpace(jsonStr), "{") {
		_ = json.Unmarshal([]byte(jsonStr), &opts)
		return opts
	}
	if m, ok := args[0].(map[string]interface{}); ok {
		b, _ := json.Marshal(m)
		_ = json.Unmarshal(b, &opts)
		return opts
	}
	if subnetList, ok := args[0].([]interface{}); ok {
		for _, item := range subnetList {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				opts.SelectedSubnetIDs = append(opts.SelectedSubnetIDs, strings.TrimSpace(s))
			}
		}
	}
	if len(args) > 1 {
		if b, ok := args[1].(bool); ok {
			opts.OverwriteAll = b
		}
	}
	return opts
}

// GenerateRandomIPFromSubnet 从指定的住宅网段 ID 中随机生成一个合法公网 IP。
// 若 subnetID 为空或找不到，则从预设网段中随机挑选一个网段生成。
func GenerateRandomIPFromSubnet(subnetID string) (string, error) {
	var targetSubnet *ResidentialSubnetInfo
	if trimmed := strings.TrimSpace(subnetID); trimmed != "" {
		for i := range DefaultResidentialSubnets {
			if DefaultResidentialSubnets[i].ID == trimmed {
				targetSubnet = &DefaultResidentialSubnets[i]
				break
			}
		}
	}
	if targetSubnet == nil {
		if len(DefaultResidentialSubnets) == 0 {
			return "", fmt.Errorf("no default subnets available")
		}
		nBig, err := rand.Int(rand.Reader, big.NewInt(int64(len(DefaultResidentialSubnets))))
		if err != nil {
			targetSubnet = &DefaultResidentialSubnets[0]
		} else {
			targetSubnet = &DefaultResidentialSubnets[nBig.Int64()]
		}
	}
	return generateUniqueIPFromCIDR(targetSubnet.CIDR, nil)
}

// BatchAssignNvidiaEgressIP 为号池中的 NVIDIA 账号批量分配独立无重复或统一相同的住宅 IP。
// 返回成功分配/更新的账号数量与可能的错误。
func (m *Manager) BatchAssignNvidiaEgressIP(opts BatchAssignIPOptions) (int, error) {
	m.Lock()

	// 1. 统一分配单 IP 模式 (Single Mode)
	if strings.ToLower(strings.TrimSpace(opts.Mode)) == "single" {
		targetIP := strings.TrimSpace(opts.SingleIP)
		if targetIP != "" {
			parsed := net.ParseIP(targetIP)
			if parsed == nil || parsed.To4() == nil {
				m.Unlock()
				return 0, fmt.Errorf("invalid IPv4 address: %s", targetIP)
			}
		} else {
			selectedID := ""
			if len(opts.SelectedSubnetIDs) > 0 {
				selectedID = opts.SelectedSubnetIDs[0]
			}
			genIP, err := GenerateRandomIPFromSubnet(selectedID)
			if err != nil {
				m.Unlock()
				return 0, fmt.Errorf("failed to generate random IP: %w", err)
			}
			targetIP = genIP
		}

		updatedCount := 0
		for _, a := range m.accounts {
			if a.Provider != "nvidia" {
				continue
			}
			if opts.OverwriteAll || strings.TrimSpace(a.EgressIP) == "" {
				a.EgressIP = targetIP
				updatedCount++
			}
		}

		m.Unlock()
		if err := m.SaveAccountsFor(false, "nvidia"); err != nil {
			return updatedCount, fmt.Errorf("save nvidia accounts failed: %w", err)
		}
		return updatedCount, nil
	}

	// 2. 独立打散分配模式 (Unique Mode)
	// 筛选出有效的目标网段
	subnets := filterSubnets(opts.SelectedSubnetIDs)
	if len(subnets) == 0 {
		subnets = DefaultResidentialSubnets
	}

	// 收集已使用的 IP（如果是保留模式）
	usedIPs := make(map[string]bool)
	var targets []*Account

	for _, a := range m.accounts {
		if a.Provider != "nvidia" {
			continue
		}
		trimmedIP := strings.TrimSpace(a.EgressIP)
		if !opts.OverwriteAll && trimmedIP != "" {
			usedIPs[trimmedIP] = true
		} else {
			targets = append(targets, a)
		}
	}

	if len(targets) == 0 {
		m.Unlock()
		return 0, nil
	}

	// 为目标账号轮流从所选网段中随机选取唯一 IP
	updatedCount := 0
	for i, acc := range targets {
		subnet := subnets[i%len(subnets)]
		ip, err := generateUniqueIPFromCIDR(subnet.CIDR, usedIPs)
		if err != nil {
			return updatedCount, fmt.Errorf("account %s generate IP failed: %w", acc.Email, err)
		}
		acc.EgressIP = ip
		usedIPs[ip] = true
		updatedCount++
	}

	// 释放写锁后执行定向落盘
	m.Unlock()
	if err := m.SaveAccountsFor(false, "nvidia"); err != nil {
		return updatedCount, fmt.Errorf("save nvidia accounts failed: %w", err)
	}

	return updatedCount, nil
}

// filterSubnets 根据 ID 筛选网段
func filterSubnets(selectedIDs []string) []ResidentialSubnetInfo {
	if len(selectedIDs) == 0 {
		return DefaultResidentialSubnets
	}
	idMap := make(map[string]bool, len(selectedIDs))
	for _, id := range selectedIDs {
		idMap[strings.TrimSpace(id)] = true
	}
	var res []ResidentialSubnetInfo
	for _, s := range DefaultResidentialSubnets {
		if idMap[s.ID] {
			res = append(res, s)
		}
	}
	return res
}

// generateUniqueIPFromCIDR 从指定 CIDR 中随机选取一个未被占用的有效主机 IP
func generateUniqueIPFromCIDR(cidr string, used map[string]bool) (string, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("invalid CIDR %s: %w", cidr, err)
	}

	ip4 := ipnet.IP.To4()
	if ip4 == nil {
		return "", fmt.Errorf("CIDR %s is not IPv4", cidr)
	}

	mask := ipnet.Mask
	networkUint := binary.BigEndian.Uint32(ip4)
	maskUint := binary.BigEndian.Uint32(mask)
	broadcastUint := networkUint | (^maskUint)

	// 有效主机范围：网关 + 5 到 广播 - 5
	startHost := networkUint + 5
	endHost := broadcastUint - 5
	if endHost <= startHost {
		startHost = networkUint + 1
		endHost = broadcastUint - 1
	}
	hostRange := int64(endHost - startHost + 1)
	if hostRange <= 0 {
		return "", fmt.Errorf("CIDR %s has no usable hosts", cidr)
	}

	// 尝试最多 200 次安全随机采样
	for attempt := 0; attempt < 200; attempt++ {
		nBig, err := rand.Int(rand.Reader, big.NewInt(hostRange))
		if err != nil {
			return "", err
		}
		candidateUint := startHost + uint32(nBig.Int64())
		var candidateBytes [4]byte
		binary.BigEndian.PutUint32(candidateBytes[:], candidateUint)
		candidateIP := net.IP(candidateBytes[:]).String()

		if !used[candidateIP] {
			return candidateIP, nil
		}
	}

	// 若随机冲突，走确定性线性扫描
	for cur := startHost; cur <= endHost; cur++ {
		var candidateBytes [4]byte
		binary.BigEndian.PutUint32(candidateBytes[:], cur)
		candidateIP := net.IP(candidateBytes[:]).String()
		if !used[candidateIP] {
			return candidateIP, nil
		}
	}

	return "", fmt.Errorf("subnet %s exhausted", cidr)
}

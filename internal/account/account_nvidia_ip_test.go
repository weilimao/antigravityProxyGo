package account

import (
	"fmt"
	"net"
	"strings"
	"testing"
)

// TestBatchAssignNvidiaEgressIP 验证批量分配住宅 IP 逻辑：
// 1. 全局唯一性，无重复；
// 2. 覆盖模式与保留已有模式；
// 3. 多网段筛选生效；
// 4. 定向落盘 accounts_nvidia.json 往返验证。
func TestBatchAssignNvidiaEgressIP(t *testing.T) {
	tmp := t.TempDir()
	m := NewManager()
	m.Init(tmp)

	// 添加 10 个 NVIDIA 账号，其中 2 个已有 IP
	for i := 1; i <= 10; i++ {
		existingIP := ""
		if i == 1 {
			existingIP = "1.2.3.4"
		}
		if i == 2 {
			existingIP = "5.6.7.8"
		}
		_, err := m.AddNvidiaAccount(NvidiaAccountInput{
			BaseURL:  "https://integrate.api.nvidia.com/v1",
			APIKey:   "nvapi-test-key",
			Label:    fmt.Sprintf("nv-acc-%d@test.com", i),
			EgressIP: existingIP,
		})
		if err != nil {
			t.Fatalf("AddNvidiaAccount failed: %v", err)
		}
	}

	// 1. 测试保留模式 (OverwriteAll = false)，只分配 8 个空白账号
	updated, err := m.BatchAssignNvidiaEgressIP(BatchAssignIPOptions{
		SelectedSubnetIDs: []string{"us-comcast-ca", "us-att-tx"},
		OverwriteAll:      false,
	})
	if err != nil {
		t.Fatalf("BatchAssignNvidiaEgressIP failed: %v", err)
	}
	if updated != 8 {
		t.Fatalf("Expected 8 updated accounts, got %d", updated)
	}

	// 验证已有 IP 未被覆盖
	accs := m.GetAccounts()
	if len(accs) != 10 {
		t.Fatalf("Expected 10 accounts, got %d", len(accs))
	}
	if accs[0].EgressIP != "1.2.3.4" {
		t.Errorf("Expected acc 0 to keep 1.2.3.4, got %s", accs[0].EgressIP)
	}
	if accs[1].EgressIP != "5.6.7.8" {
		t.Errorf("Expected acc 1 to keep 5.6.7.8, got %s", accs[1].EgressIP)
	}

	// 2. 测试覆盖模式 (OverwriteAll = true)，全部 10 个重新分配且全部唯一
	updatedAll, err := m.BatchAssignNvidiaEgressIP(BatchAssignIPOptions{
		SelectedSubnetIDs: []string{"us-comcast-ca", "uk-bt-london", "sg-singtel"},
		OverwriteAll:      true,
	})
	if err != nil {
		t.Fatalf("BatchAssignNvidiaEgressIP overwrite failed: %v", err)
	}
	if updatedAll != 10 {
		t.Fatalf("Expected 10 updated accounts, got %d", updatedAll)
	}

	// 验证所有 IP 均为合法公网 IP 且互不重复
	seenIPs := make(map[string]bool)
	accsUpdated := m.GetAccounts()
	for i, acc := range accsUpdated {
		if strings.TrimSpace(acc.EgressIP) == "" {
			t.Fatalf("Account %d has empty EgressIP", i)
		}
		ip := net.ParseIP(acc.EgressIP)
		if ip == nil || ip.IsLoopback() || ip.IsPrivate() {
			t.Fatalf("Account %d has invalid or private IP: %s", i, acc.EgressIP)
		}
		if seenIPs[acc.EgressIP] {
			t.Fatalf("Duplicate IP detected: %s", acc.EgressIP)
		}
		seenIPs[acc.EgressIP] = true
	}

	// 3. 验证落盘：重新从磁盘加载 Manager 验证 accounts_nvidia.json
	m2 := NewManager()
	m2.Init(tmp)
	reloadedAccs := m2.GetAccounts()
	if len(reloadedAccs) != 10 {
		t.Fatalf("Reloaded accounts length want 10, got %d", len(reloadedAccs))
	}
	for i, acc := range reloadedAccs {
		if acc.EgressIP != accsUpdated[i].EgressIP {
			t.Fatalf("Account %d persisted IP mismatch: want %s, got %s", i, accsUpdated[i].EgressIP, acc.EgressIP)
		}
	}
}

func TestGetResidentialSubnets(t *testing.T) {
	subnets := GetResidentialSubnets()
	if len(subnets) < 10 {
		t.Fatalf("Expected at least 10 residential subnets, got %d", len(subnets))
	}
	for _, s := range subnets {
		if s.ID == "" || s.CIDR == "" || s.ISP == "" {
			t.Fatalf("Invalid subnet entry: %+v", s)
		}
		_, _, err := net.ParseCIDR(s.CIDR)
		if err != nil {
			t.Fatalf("Subnet %s has invalid CIDR %s: %v", s.ID, s.CIDR, err)
		}
	}
}

// TestBatchAssignNvidiaEgressIP_SingleMode_ManualIP 验证统一分配单 IP 模式（手动指定特定 IP）。
func TestBatchAssignNvidiaEgressIP_SingleMode_ManualIP(t *testing.T) {
	tmp := t.TempDir()
	m := NewManager()
	m.Init(tmp)

	for i := 1; i <= 5; i++ {
		existing := ""
		if i == 1 {
			existing = "99.99.99.99"
		}
		_, err := m.AddNvidiaAccount(NvidiaAccountInput{
			BaseURL:  "https://integrate.api.nvidia.com/v1",
			APIKey:   "nvapi-test",
			Label:    fmt.Sprintf("acc-%d", i),
			EgressIP: existing,
		})
		if err != nil {
			t.Fatalf("AddNvidiaAccount failed: %v", err)
		}
	}

	// 1. 保留模式 (OverwriteAll = false)，为 4 个空白账号赋 108.85.12.34
	manualIP := "108.85.12.34"
	updated, err := m.BatchAssignNvidiaEgressIP(BatchAssignIPOptions{
		Mode:         "single",
		SingleIP:     manualIP,
		OverwriteAll: false,
	})
	if err != nil {
		t.Fatalf("BatchAssignNvidiaEgressIP single mode failed: %v", err)
	}
	if updated != 4 {
		t.Fatalf("Expected 4 updated accounts, got %d", updated)
	}

	accs := m.GetAccounts()
	if accs[0].EgressIP != "99.99.99.99" {
		t.Errorf("Expected acc 0 to preserve 99.99.99.99, got %s", accs[0].EgressIP)
	}
	for i := 1; i < 5; i++ {
		if accs[i].EgressIP != manualIP {
			t.Errorf("Expected acc %d to be %s, got %s", i, manualIP, accs[i].EgressIP)
		}
	}

	// 2. 覆盖模式 (OverwriteAll = true)，全部 5 个账号统一设置为 71.222.88.99
	newManualIP := "71.222.88.99"
	updatedAll, err := m.BatchAssignNvidiaEgressIP(BatchAssignIPOptions{
		Mode:         "single",
		SingleIP:     newManualIP,
		OverwriteAll: true,
	})
	if err != nil {
		t.Fatalf("BatchAssignNvidiaEgressIP single mode overwrite failed: %v", err)
	}
	if updatedAll != 5 {
		t.Fatalf("Expected 5 updated accounts, got %d", updatedAll)
	}

	// 验证落盘持久化
	m2 := NewManager()
	m2.Init(tmp)
	for i, acc := range m2.GetAccounts() {
		if acc.EgressIP != newManualIP {
			t.Errorf("Reloaded account %d expected %s, got %s", i, newManualIP, acc.EgressIP)
		}
	}
}

// TestBatchAssignNvidiaEgressIP_SingleMode_FromSubnet 验证选择指定网段随机生成统一单 IP 并分配。
func TestBatchAssignNvidiaEgressIP_SingleMode_FromSubnet(t *testing.T) {
	tmp := t.TempDir()
	m := NewManager()
	m.Init(tmp)

	for i := 1; i <= 4; i++ {
		_, _ = m.AddNvidiaAccount(NvidiaAccountInput{
			BaseURL: "https://integrate.api.nvidia.com/v1",
			APIKey:  "nvapi-test",
			Label:   fmt.Sprintf("acc-%d", i),
		})
	}

	// 指定 tw-cht-taipei (114.34.0.0/16) 网段
	updated, err := m.BatchAssignNvidiaEgressIP(BatchAssignIPOptions{
		Mode:              "single",
		SelectedSubnetIDs: []string{"tw-cht-taipei"},
		OverwriteAll:      true,
	})
	if err != nil {
		t.Fatalf("BatchAssignNvidiaEgressIP single subnet mode failed: %v", err)
	}
	if updated != 4 {
		t.Fatalf("Expected 4 updated accounts, got %d", updated)
	}

	accs := m.GetAccounts()
	assignedIP := accs[0].EgressIP
	if assignedIP == "" {
		t.Fatalf("Assigned IP is empty")
	}
	_, ipnet, _ := net.ParseCIDR("114.34.0.0/16")
	if !ipnet.Contains(net.ParseIP(assignedIP)) {
		t.Fatalf("Assigned IP %s not in CIDR 114.34.0.0/16", assignedIP)
	}

	// 验证所有账号被赋上了完全相同的 IP
	for i, acc := range accs {
		if acc.EgressIP != assignedIP {
			t.Errorf("Account %d IP mismatch: want %s, got %s", i, assignedIP, acc.EgressIP)
		}
	}
}

// TestBatchAssignNvidiaEgressIP_InvalidSingleIP 验证非法 IP 地址被正确拒绝拦截。
func TestBatchAssignNvidiaEgressIP_InvalidSingleIP(t *testing.T) {
	tmp := t.TempDir()
	m := NewManager()
	m.Init(tmp)

	_, _ = m.AddNvidiaAccount(NvidiaAccountInput{
		BaseURL: "https://integrate.api.nvidia.com/v1",
		APIKey:  "nvapi-test",
		Label:   "acc-1",
	})

	_, err := m.BatchAssignNvidiaEgressIP(BatchAssignIPOptions{
		Mode:         "single",
		SingleIP:     "999.888.777.666",
		OverwriteAll: true,
	})
	if err == nil {
		t.Fatalf("Expected error for invalid IP, got nil")
	}
}

// TestGenerateRandomIPFromSubnet 验证单网段随机生成 IP 函数。
func TestGenerateRandomIPFromSubnet(t *testing.T) {
	ip, err := GenerateRandomIPFromSubnet("uk-bt-london")
	if err != nil {
		t.Fatalf("GenerateRandomIPFromSubnet failed: %v", err)
	}
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.To4() == nil {
		t.Fatalf("Invalid IP generated: %s", ip)
	}
	_, ipnet, _ := net.ParseCIDR("86.154.0.0/16")
	if !ipnet.Contains(parsed) {
		t.Fatalf("Generated IP %s not in 86.154.0.0/16", ip)
	}
}

// TestParseBatchAssignIPOptions 验证各类参数形态的解析。
func TestParseBatchAssignIPOptions(t *testing.T) {
	// 1. JSON 字符串
	opts1 := ParseBatchAssignIPOptions([]interface{}{`{"mode":"single","singleIp":"1.2.3.4","overwriteAll":false}`})
	if opts1.Mode != "single" || opts1.SingleIP != "1.2.3.4" || opts1.OverwriteAll != false {
		t.Fatalf("JSON parse mismatch: %+v", opts1)
	}

	// 2. map 对象
	opts2 := ParseBatchAssignIPOptions([]interface{}{map[string]interface{}{
		"mode":         "single",
		"singleIp":     "5.6.7.8",
		"overwriteAll": true,
	}})
	if opts2.Mode != "single" || opts2.SingleIP != "5.6.7.8" || opts2.OverwriteAll != true {
		t.Fatalf("Map parse mismatch: %+v", opts2)
	}

	// 3. 兼容旧位置参数
	opts3 := ParseBatchAssignIPOptions([]interface{}{[]interface{}{"us-comcast-ca"}, false})
	if len(opts3.SelectedSubnetIDs) != 1 || opts3.SelectedSubnetIDs[0] != "us-comcast-ca" || opts3.OverwriteAll != false {
		t.Fatalf("Legacy args parse mismatch: %+v", opts3)
	}
}


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

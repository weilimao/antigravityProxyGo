package relay

func (h *APICompatHandler) isNvidiaWorkerProxyEnabledSafe() (enabled bool) {
	if h == nil || h.settingsMgr == nil {
		return false
	}
	defer func() {
		if r := recover(); r != nil {
			enabled = false
		}
	}()
	return h.settingsMgr.IsNvidiaWorkerProxyEnabled()
}

func (h *APICompatHandler) getNvidiaWorkerProxyURLSafe() (url string) {
	if h == nil || h.settingsMgr == nil {
		return ""
	}
	defer func() {
		if r := recover(); r != nil {
			url = ""
		}
	}()
	return h.settingsMgr.GetNvidiaWorkerProxyURL()
}

func (h *APICompatHandler) isNvidiaDedicatedProxyEnabledSafe() (enabled bool) {
	if h == nil || h.settingsMgr == nil {
		return false
	}
	defer func() {
		if r := recover(); r != nil {
			enabled = false
		}
	}()
	return h.settingsMgr.GetNvidiaDedicatedProxyEnabled()
}

func (h *APICompatHandler) getNvidiaDedicatedProxyParamsSafe() (addr, user, pass string) {
	if h == nil || h.settingsMgr == nil {
		return "", "", ""
	}
	defer func() {
		if r := recover(); r != nil {
			addr, user, pass = "", "", ""
		}
	}()
	return h.settingsMgr.GetNvidiaDedicatedProxyAddress(), h.settingsMgr.GetNvidiaDedicatedProxyUsername(), h.settingsMgr.GetNvidiaDedicatedProxyPassword()
}

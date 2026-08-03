package settings

// 复用点 1: Manager 配置项的泛型读写封装。
//
// settings.go 历史上曾有 97 个样板 Get/Set,全部是「加锁 → 读/改字段 → SaveConfig」
// 的同构切片,仅字段名不同。本文件用 Go 1.18+ 泛型收口该样板,使未来加锁/日志/校验
// 只需改此一处,且保证与原实现逐段等价的锁语义。
//
// 三种语义对应三类原 setter:
//   - getSetting:           纯读,原 `m.RLock(); defer m.RUnlock(); return m.config.X`
//   - setSetting:           纯写(SaveConfig 持锁),原 `m.Lock(); defer m.Unlock(); set; SaveConfig`
//   - setSettingWithPost:   写+后置动作(如 updateNetutilConfig),原
//                            `m.Lock(); set; SaveConfig(); m.Unlock(); post()`
//                            锁必须在 post 之前释放,因 post(如 updateNetutilConfig)
//                            内部要 RLock,持写锁调用会自死锁。

// getSetting 泛型读:持读锁读取 Config 任意字段,返回回调结果(含零值或默认值兜底)。
// 回调在持锁期间执行,可安全解引用 *Config。
func getSetting[T any](m *Manager, get func(*Config) T) T {
	m.RLock()
	defer m.RUnlock()
	return get(&m.config)
}

// setSetting 泛型写:持写锁改 Config 任意字段 + SaveConfig 落盘。
// 「Lock → set → SaveConfig → Unlock」与原 simple setter 语义一致;
// SaveConfig 在持锁期间执行,保证字段修改与落盘的原子性。
// 适用于无后置动作(无 updateNetutilConfig/无 OS 自启/无加密)的简单配置项。
func setSetting[T any](m *Manager, set func(*Config, T), val T) error {
	m.Lock()
	defer m.Unlock()
	set(&m.config, val)
	return m.SaveConfig()
}

// setSettingWithPost 泛型写:改字段 + SaveConfig,解锁后执行后置动作 post。
// 锁在 post 之前释放(原因见文件头注释)。与原 updateNetutilConfig 系列 setter
// 逐段等价:Lock → set → SaveConfig → Unlock → post(仅 err==nil 时执行 post)。
func setSettingWithPost[T any](m *Manager, set func(*Config, T), val T, post func()) error {
	m.Lock()
	set(&m.config, val)
	err := m.SaveConfig()
	m.Unlock()
	if err == nil && post != nil {
		post()
	}
	return err
}

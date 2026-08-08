package dialogs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// MemoryKeyFile 是目录记忆持久化文件名（置于应用数据根目录，而非数据目录，
// 避免随数据目录迁移被复制/丢失）。
const MemoryKeyFile = "dialogs_memory.json"

// SerializableEventMemory 是 dialogs_memory.json 的磁盘格式。
type SerializableEventMemory struct {
	Memory map[DirMemoryKey]string `json:"memory"`
}

// FileDirMemory 是基于 JSON 文件的 DirMemory 持久化实现：
//   - 文件位于应用数据根目录（defaultUserDataPath），由 rootDir 指定。
//   - 修改即落盘（原子写），启动时读入内存缓存。
//   - 线程安全：sync.RWMutex。
type FileDirMemory struct {
	mu      sync.RWMutex
	rootDir string
	cache   map[DirMemoryKey]string
}

// NewFileDirMemory 构造并尝试从 rootDir/dialogs_memory.json 载入既有记忆。
// 文件缺失或解析失败时静默降级为空记忆，不阻断对话框功能。
func NewFileDirMemory(rootDir string) *FileDirMemory {
	m := &FileDirMemory{
		rootDir: rootDir,
		cache:   map[DirMemoryKey]string{},
	}
	m.load()
	return m
}

func (m *FileDirMemory) path() string {
	return filepath.Join(m.rootDir, MemoryKeyFile)
}

// load 启动时载入缓存。失败不返回错误，保持空记忆兜底。
// 过滤掉已不存在的目录，避免记忆指向已删除路径。
func (m *FileDirMemory) load() {
	data, err := os.ReadFile(m.path())
	if err != nil {
		return
	}
	var raw SerializableEventMemory
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}
	cache := map[DirMemoryKey]string{}
	for k, v := range raw.Memory {
		if v == "" {
			continue
		}
		if info, err := os.Stat(v); err == nil && info.IsDir() {
			cache[k] = v
		}
	}
	m.cache = cache
}

// Get 返回指定键的记忆目录；无记忆返回 ""。
func (m *FileDirMemory) Get(key DirMemoryKey) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cache[key]
}

// Set 写记忆并尝试原子落盘。落盘失败仅影响持久化，不阻断内存记忆。
func (m *FileDirMemory) Set(key DirMemoryKey, dir string) {
	m.mu.Lock()
	m.cache[key] = dir
	raw := SerializableEventMemory{
		Memory: m.cache,
	}
	m.mu.Unlock()

	m.persist(raw)
}

// persist 原子写入（临时文件 + rename）。
func (m *FileDirMemory) persist(raw SerializableEventMemory) {
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return
	}
	if m.rootDir != "" {
		_ = os.MkdirAll(m.rootDir, 0755)
	}
	tmp := m.path() + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return
	}
	_ = os.Rename(tmp, m.path())
}
package antigravitybg

// WallpaperConfig 定义壁纸与外观调谐配置。
type WallpaperConfig struct {
	Enabled          bool    `json:"enabled"`                    // 是否启用自定义壁纸
	ImageSource      string  `json:"imageSource"`                // 图片来源: "base64" | "url" | "file"
	ImageData        string  `json:"imageData"`                  // Base64 数据或 URL 字符串
	FilePath         string  `json:"filePath"`                   // 原图/视频本地文件路径 (可选)
	MediaType        string  `json:"mediaType,omitempty"`        // 媒体类型: "image" | "video" | "gif" (默认 "image")
	PlaybackRate     float64 `json:"playbackRate,omitempty"`     // 视频播放速度 (0.25 ~ 3.0，默认 1.0)
	Muted            bool    `json:"muted"`                      // 视频是否静音播放 (默认 true)
	Loop             bool    `json:"loop"`                       // 视频是否循环播放 (默认 true)
	Opacity          float64 `json:"opacity"`                    // 壁纸不透明度 (0.05 ~ 1.0)
	Blur             int     `json:"blur"`                       // 毛玻璃模糊半径 (0 ~ 40px)
	DarkOverlay      float64 `json:"darkOverlay"`                // 暗色遮罩浓度 (0.0 ~ 0.9)
	GlassAlpha       float64 `json:"glassAlpha"`                 // 界面卡片底色通透度 (0.5 ~ 1.0)
	BackgroundFit    string  `json:"backgroundFit"`              // 填充模式: "cover" | "contain" | "fill"
	ThemeMode        string  `json:"themeMode"`                  // 通透主题: "dark" | "light"
	SidebarTextColor   string  `json:"sidebarTextColor,omitempty"`   // 侧边栏文字颜色 (如 "#f1f5f9")
	ContentTextColor   string  `json:"contentTextColor,omitempty"`   // 正文/对话文字颜色 (如 "#ffffff")
	UserMessageBgColor string  `json:"userMessageBgColor,omitempty"`  // 用户发送消息气泡底色 (如 "rgba(255,255,255,0.08)")
	AgentMessageBgColor string `json:"agentMessageBgColor,omitempty"` // Agent 助手回复卡片底色 (如 "rgba(15,18,28,0.75)")
	ComposerBgColor    string  `json:"composerBgColor,omitempty"`    // 底部输入框底色 (如 "rgba(18,22,34,0.88)")
	SidebarBgColor     string  `json:"sidebarBgColor,omitempty"`     // 左侧边栏底色 (如 "rgba(12,15,24,0.85)")
	ModalBgColor       string  `json:"modalBgColor,omitempty"`       // 设置中心与弹窗底色 (如 "rgba(18,22,34,0.94)")
	CodeBlockBgColor   string  `json:"codeBlockBgColor,omitempty"`   // 代码块与编辑器底色 (如 "rgba(10,12,20,0.92)")
	AutoDarkTheme      bool    `json:"autoDarkTheme"`                // 是否自动将 Antigravity 切换为深色主题 (默认 true)
	ColorTheme         string  `json:"colorTheme,omitempty"`         // 深色主题名称 (默认 "Default Dark Modern")
}

// SavedWallpaper 描述保存在本地壁纸库中的单张壁纸条目。
type SavedWallpaper struct {
	ID               string   `json:"id"`                         // 唯一标识 (如时间戳/UUID)
	Name             string   `json:"name"`                       // 显示名称
	Type             string   `json:"type"`                       // "base64" | "url"
	MediaType        string   `json:"mediaType,omitempty"`        // 媒体类型: "image" | "video" | "gif"
	Data             string   `json:"data"`                       // Base64 数据或 URL
	Thumbnail        string   `json:"thumbnail,omitempty"`        // 极速轻量缩略图 (JPEG Base64)
	FilePath         string   `json:"filePath,omitempty"`         // 本地物理原图/视频路径
	CreatedAt        int64    `json:"createdAt"`                  // 创建时间毫秒戳
	PlaybackRate     *float64 `json:"playbackRate,omitempty"`     // 记忆的播放速度
	Muted            *bool    `json:"muted,omitempty"`            // 记忆的静音设置
	Loop             *bool    `json:"loop,omitempty"`             // 记忆的循环设置
	Opacity          *float64 `json:"opacity,omitempty"`          // 记忆的专属不透明度
	Blur             *int     `json:"blur,omitempty"`             // 记忆的专属模糊半径
	DarkOverlay      *float64 `json:"darkOverlay,omitempty"`      // 记忆的专属暗度
	GlassAlpha       *float64 `json:"glassAlpha,omitempty"`       // 记忆的专属卡片通透度
	ThemeMode        *string  `json:"themeMode,omitempty"`        // 记忆的主题模式
	SidebarTextColor   *string  `json:"sidebarTextColor,omitempty"`   // 记忆的侧边栏文字颜色
	ContentTextColor   *string  `json:"contentTextColor,omitempty"`   // 记忆的正文文字颜色
	UserMessageBgColor  *string `json:"userMessageBgColor,omitempty"`  // 记忆的用户发送消息气泡底色
	AgentMessageBgColor *string `json:"agentMessageBgColor,omitempty"` // 记忆的 Agent 助手回复卡片底色
	ComposerBgColor     *string `json:"composerBgColor,omitempty"`     // 记忆的底部输入框底色
	SidebarBgColor      *string `json:"sidebarBgColor,omitempty"`      // 记忆的左侧边栏底色
	ModalBgColor        *string `json:"modalBgColor,omitempty"`        // 记忆的设置中心与弹窗底色
	CodeBlockBgColor    *string `json:"codeBlockBgColor,omitempty"`    // 记忆的代码块与编辑器底色
	AutoDarkTheme      *bool    `json:"autoDarkTheme,omitempty"`      // 记忆的自动深色主题开关
	ColorTheme         *string  `json:"colorTheme,omitempty"`         // 记忆的主题名称
}

// StatusResponse 描述当前 Antigravity 桌面端的检测与注入状态。
type StatusResponse struct {
	Installed bool             `json:"installed"` // 是否已安装 Antigravity 桌面端
	Patched   bool             `json:"patched"`   // 是否已注入壁纸补丁
	HasBackup bool             `json:"hasBackup"` // 是否存在原版备份
	AppPath   string           `json:"appPath"`   // Antigravity 安装根路径
	AsarPath  string           `json:"asarPath"`  // app.asar 实际物理路径
	IsRunning bool             `json:"isRunning"` // Antigravity 当前是否正在运行
	OS        string           `json:"os"`        // 操作系统类型 ("darwin" | "windows" | "linux")
	Config    WallpaperConfig  `json:"config"`    // 当前生效的壁纸配置
	Gallery   []SavedWallpaper `json:"gallery"`   // 收藏的本地壁纸库列表
	Error     string           `json:"error,omitempty"`
}

// ApplyResult 描述应用壁纸补丁的执行结果。
type ApplyResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

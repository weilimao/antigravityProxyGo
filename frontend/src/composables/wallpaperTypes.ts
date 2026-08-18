export interface SavedWallpaper {
  id: string;
  name: string;
  type: 'base64' | 'url' | 'file';
  mediaType?: 'image' | 'video' | 'gif';
  data: string;
  thumbnail?: string;
  filePath?: string;
  createdAt: number;
  playbackRate?: number;
  muted?: boolean;
  loop?: boolean;
  opacity?: number;
  blur?: number;
  darkOverlay?: number;
  glassAlpha?: number;
  themeMode?: string;
  sidebarTextColor?: string;
  contentTextColor?: string;
  userMessageBgColor?: string;
  agentMessageBgColor?: string;
  composerBgColor?: string;
  sidebarBgColor?: string;
  modalBgColor?: string;
  codeBlockBgColor?: string;
  autoDarkTheme?: boolean;
  colorTheme?: string;
}

export interface WallpaperConfig {
  enabled: boolean;
  imageSource: 'base64' | 'url' | 'file';
  mediaType?: 'image' | 'video' | 'gif';
  imageData: string;
  filePath?: string;
  playbackRate: number;
  muted: boolean;
  loop: boolean;
  opacity: number;
  blur: number;
  darkOverlay: number;
  glassAlpha: number;
  backgroundFit: 'cover' | 'contain' | 'fill';
  themeMode: string;
  sidebarTextColor: string;
  contentTextColor: string;
  userMessageBgColor: string;
  agentMessageBgColor: string;
  composerBgColor: string;
  sidebarBgColor: string;
  modalBgColor: string;
  codeBlockBgColor: string;
  autoDarkTheme: boolean;
  colorTheme: string;
}

export interface StatusResponse {
  installed: boolean;
  patched: boolean;
  hasBackup: boolean;
  appPath: string;
  asarPath: string;
  isRunning: boolean;
  os: string;
  config: WallpaperConfig;
  gallery?: SavedWallpaper[];
  error?: string;
}

export interface PresetItem {
  name: string;
  url: string;
  mediaType: 'image' | 'video';
  thumbnail?: string;
}

export const sidebarColorPresets = [
  { label: '纯白高亮', color: '#ffffff' },
  { label: '冰霜银灰', color: '#f1f5f9' },
  { label: '赛博浅青', color: '#67e8f9' },
  { label: '霓虹浅粉', color: '#f9a8d4' },
  { label: '清新荧绿', color: '#86efac' }
];

export const contentColorPresets = [
  { label: '经典纯白', color: '#ffffff' },
  { label: '温暖柔金', color: '#fde047' },
  { label: '天空浅蓝', color: '#93c5fd' },
  { label: '幻彩淡紫', color: '#d8b4fe' },
  { label: '极客明绿', color: '#4ade80' }
];

export const userMessageColorPresets = [
  { label: '通透暗晶 (推荐)', color: 'rgba(255, 255, 255, 0.08)' },
  { label: '极光淡蓝', color: 'rgba(59, 130, 246, 0.22)' },
  { label: '优雅幽紫', color: 'rgba(99, 102, 241, 0.22)' },
  { label: '清新翡翠', color: 'rgba(16, 185, 129, 0.22)' },
  { label: '温暖琥珀', color: 'rgba(245, 158, 11, 0.22)' },
  { label: '经典微黑', color: 'rgba(18, 22, 34, 0.65)' }
];

export const agentMessageColorPresets = [
  { label: '深色磨砂 (默认)', color: 'rgba(15, 18, 28, 0.75)' },
  { label: '极夜透黑', color: 'rgba(8, 10, 16, 0.85)' },
  { label: '星云深蓝', color: 'rgba(15, 23, 42, 0.82)' },
  { label: '暗紫迷雾', color: 'rgba(24, 18, 38, 0.82)' },
  { label: '石墨浅灰', color: 'rgba(30, 36, 50, 0.78)' }
];

export const sidebarBgColorPresets = [
  { label: '深邃毛玻璃 (默认)', color: 'rgba(12, 15, 24, 0.85)' },
  { label: '超通透微光', color: 'rgba(12, 15, 24, 0.60)' },
  { label: '纯黑沉浸', color: 'rgba(6, 8, 14, 0.95)' },
  { label: '深海湛蓝', color: 'rgba(10, 20, 38, 0.88)' },
  { label: '暗夜紫晶', color: 'rgba(20, 12, 32, 0.88)' }
];

export const composerBgColorPresets = [
  { label: '深色胶囊 (默认)', color: 'rgba(18, 22, 34, 0.88)' },
  { label: '通透高光', color: 'rgba(255, 255, 255, 0.12)' },
  { label: '暗黑悬浮', color: 'rgba(10, 12, 20, 0.95)' },
  { label: '极光幽蓝', color: 'rgba(16, 26, 48, 0.92)' }
];

export interface ColorThemePack {
  name: string;
  desc: string;
  sidebarTextColor: string;
  contentTextColor: string;
  userMessageBgColor: string;
  agentMessageBgColor: string;
  composerBgColor: string;
  sidebarBgColor: string;
  modalBgColor: string;
  codeBlockBgColor: string;
}

export const colorThemePacks: ColorThemePack[] = [
  {
    name: '💎 通透暗晶 (极简高透)',
    desc: '轻盈通透的水晶质感，全景动态壁纸完美流露',
    sidebarTextColor: '#f1f5f9',
    contentTextColor: '#ffffff',
    userMessageBgColor: 'rgba(255, 255, 255, 0.08)',
    agentMessageBgColor: 'rgba(15, 18, 28, 0.75)',
    composerBgColor: 'rgba(18, 22, 34, 0.88)',
    sidebarBgColor: 'rgba(12, 15, 24, 0.85)',
    modalBgColor: 'rgba(18, 22, 34, 0.94)',
    codeBlockBgColor: 'rgba(10, 12, 20, 0.92)'
  },
  {
    name: '🌌 极光深蓝 (Cyber Cyan)',
    desc: '冷调赛博微光，与科技感和雨夜壁纸相得益彰',
    sidebarTextColor: '#67e8f9',
    contentTextColor: '#ffffff',
    userMessageBgColor: 'rgba(59, 130, 246, 0.22)',
    agentMessageBgColor: 'rgba(15, 23, 42, 0.82)',
    composerBgColor: 'rgba(16, 26, 48, 0.92)',
    sidebarBgColor: 'rgba(10, 20, 38, 0.88)',
    modalBgColor: 'rgba(15, 23, 42, 0.95)',
    codeBlockBgColor: 'rgba(8, 15, 28, 0.94)'
  },
  {
    name: '🔮 优雅幽紫 (Nebula Violet)',
    desc: '梦幻暗紫迷雾，神秘深邃的高级感',
    sidebarTextColor: '#f9a8d4',
    contentTextColor: '#ffffff',
    userMessageBgColor: 'rgba(139, 92, 246, 0.22)',
    agentMessageBgColor: 'rgba(24, 18, 38, 0.82)',
    composerBgColor: 'rgba(28, 20, 44, 0.92)',
    sidebarBgColor: 'rgba(20, 12, 32, 0.88)',
    modalBgColor: 'rgba(24, 18, 38, 0.95)',
    codeBlockBgColor: 'rgba(18, 12, 28, 0.94)'
  },
  {
    name: '🍃 翡翠森林 (Emerald Mist)',
    desc: '沉稳松石墨绿，保护视力清新自然',
    sidebarTextColor: '#86efac',
    contentTextColor: '#ffffff',
    userMessageBgColor: 'rgba(16, 185, 129, 0.22)',
    agentMessageBgColor: 'rgba(12, 26, 22, 0.82)',
    composerBgColor: 'rgba(14, 30, 26, 0.92)',
    sidebarBgColor: 'rgba(10, 22, 18, 0.88)',
    modalBgColor: 'rgba(12, 26, 22, 0.95)',
    codeBlockBgColor: 'rgba(8, 18, 15, 0.94)'
  }
];

export const speedPresets = [
  { val: 0.25, label: '0.25x 慢放' },
  { val: 0.5, label: '0.5x' },
  { val: 0.75, label: '0.75x' },
  { val: 1.0, label: '1.0x 原速' },
  { val: 1.25, label: '1.25x' },
  { val: 1.5, label: '1.5x 动态' },
  { val: 2.0, label: '2.0x' },
  { val: 2.5, label: '2.5x 极速' }
];

export const speedTicks = [
  { val: 0.25, label: '0.25x 慢放' },
  { val: 0.75, label: '0.75x' },
  { val: 1.0, label: '1.0x (原速)' },
  { val: 1.5, label: '1.5x' },
  { val: 2.5, label: '2.5x 极速' }
];

export const presets: PresetItem[] = [
  {
    name: '🌌 赛博流光动态粒子 (MP4 动效)',
    url: 'https://assets.mixkit.co/videos/preview/mixkit-stars-in-space-1610-large.mp4',
    mediaType: 'video',
    thumbnail: 'https://images.unsplash.com/photo-1506703719100-a0f3a48c0f86?q=80&w=320&auto=format&fit=crop'
  },
  {
    name: '🪐 深空引力流光 (MP4 动效)',
    url: 'https://assets.mixkit.co/videos/preview/mixkit-curved-lines-of-light-flowing-in-darkness-41225-large.mp4',
    mediaType: 'video',
    thumbnail: 'https://images.unsplash.com/photo-1550745165-9bc0b252726f?q=80&w=320&auto=format&fit=crop'
  },
  {
    name: '深空极光 (超清静态)',
    url: 'https://images.unsplash.com/photo-1519681393784-d120267933ba?q=80&w=1000&auto=format&fit=crop',
    mediaType: 'image'
  },
  {
    name: '代码矩阵 (超清静态)',
    url: 'https://images.unsplash.com/photo-1526374965328-7f61d4dc18c5?q=80&w=1000&auto=format&fit=crop',
    mediaType: 'image'
  }
];

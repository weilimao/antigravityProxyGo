import { ref, shallowRef, reactive } from 'vue';
import { ipcRenderer } from '../shared/ipc';
import {
  SavedWallpaper,
  WallpaperConfig,
  StatusResponse,
  PresetItem,
  presets,
  sidebarColorPresets,
  contentColorPresets,
  userMessageColorPresets,
  agentMessageColorPresets,
  sidebarBgColorPresets,
  composerBgColorPresets,
  colorThemePacks,
  ColorThemePack,
  speedPresets,
  speedTicks
} from './wallpaperTypes';

export * from './wallpaperTypes';

export function isVideoMedia(mediaType?: string, urlOrData?: string): boolean {
  if (mediaType === 'video') return true;
  if (!urlOrData) return false;
  return urlOrData.startsWith('data:video/') ||
         urlOrData.endsWith('.mp4') ||
         urlOrData.endsWith('.webm') ||
         urlOrData.endsWith('.mov') ||
         urlOrData.includes('.mp4?') ||
         urlOrData.includes('.webm?');
}

export function getSliderTrackStyle(val: number, min: number, max: number, activeColor = '#6366f1') {
  const num = typeof val === 'number' && !isNaN(val) ? val : min;
  const clamped = Math.max(min, Math.min(max, num));
  const pct = Math.max(0, Math.min(100, ((clamped - min) / (max - min)) * 100));
  return {
    background: `linear-gradient(to right, ${activeColor} 0%, ${activeColor} ${pct}%, rgba(148, 163, 184, 0.2) ${pct}%, rgba(148, 163, 184, 0.2) 100%)`
  };
}

export function createVideoThumbnail(dataUrlOrFile: string, maxWidth = 320, quality = 0.75): Promise<string> {
  return new Promise((resolve) => {
    const video = document.createElement('video');
    video.crossOrigin = 'anonymous';
    video.muted = true;
    video.playsInline = true;
    video.src = dataUrlOrFile;
    video.currentTime = 0.5;
    video.onloadeddata = () => {
      try {
        const canvas = document.createElement('canvas');
        const scale = Math.min(1, maxWidth / (video.videoWidth || 640));
        canvas.width = Math.round((video.videoWidth || 640) * scale);
        canvas.height = Math.round((video.videoHeight || 360) * scale);
        const ctx = canvas.getContext('2d');
        if (ctx) {
          ctx.drawImage(video, 0, 0, canvas.width, canvas.height);
          resolve(canvas.toDataURL('image/jpeg', quality));
          return;
        }
      } catch (e) {
        console.warn('视频抽帧异常:', e);
      }
      resolve('');
    };
    video.onerror = () => resolve('');
    setTimeout(() => resolve(''), 3000);
  });
}

export async function createThumbnail(dataUrl: string, maxWidth = 320, quality = 0.75): Promise<string> {
  if (!dataUrl) return '';
  if (isVideoMedia(undefined, dataUrl)) {
    const vThumb = await createVideoThumbnail(dataUrl, maxWidth, quality);
    if (vThumb) return vThumb;
  }
  if (!dataUrl.startsWith('data:image')) {
    return Promise.resolve(dataUrl);
  }
  return new Promise((resolve) => {
    const img = new Image();
    img.crossOrigin = 'anonymous';
    img.onload = () => {
      try {
        const canvas = document.createElement('canvas');
        const scale = Math.min(1, maxWidth / img.width);
        canvas.width = Math.round(img.width * scale);
        canvas.height = Math.round(img.height * scale);
        const ctx = canvas.getContext('2d');
        if (ctx) {
          ctx.imageSmoothingEnabled = true;
          ctx.imageSmoothingQuality = 'medium';
          ctx.drawImage(img, 0, 0, canvas.width, canvas.height);
          resolve(canvas.toDataURL('image/jpeg', quality));
          return;
        }
      } catch (e) {
        console.warn('生成缩略图异常:', e);
      }
      resolve(dataUrl);
    };
    img.onerror = () => resolve(dataUrl);
    img.src = dataUrl;
  });
}

// 共享单例状态（确保各子组件保持同步）
const status = reactive<StatusResponse>({
  installed: false,
  patched: false,
  hasBackup: false,
  appPath: '',
  asarPath: '',
  isRunning: false,
  os: 'windows',
  config: {
    enabled: true,
    imageSource: 'base64',
    mediaType: 'image',
    imageData: '',
    playbackRate: 1.0,
    muted: true,
    loop: true,
    opacity: 0.45,
    blur: 8,
    darkOverlay: 0.35,
    glassAlpha: 0.85,
    backgroundFit: 'cover',
    themeMode: 'dark',
    sidebarTextColor: '#f1f5f9',
    contentTextColor: '#ffffff',
    userMessageBgColor: 'rgba(255, 255, 255, 0.08)',
    agentMessageBgColor: 'rgba(15, 18, 28, 0.75)',
    composerBgColor: 'rgba(18, 22, 34, 0.88)',
    sidebarBgColor: 'rgba(12, 15, 24, 0.85)',
    modalBgColor: 'rgba(18, 22, 34, 0.94)',
    codeBlockBgColor: 'rgba(10, 12, 20, 0.92)',
    autoDarkTheme: true,
    colorTheme: 'Default Dark Modern'
  }
});

const config = reactive<WallpaperConfig>({
  enabled: true,
  imageSource: 'base64',
  mediaType: 'image',
  imageData: '',
  playbackRate: 1.0,
  muted: true,
  loop: true,
  opacity: 0.45,
  blur: 8,
  darkOverlay: 0.35,
  glassAlpha: 0.85,
  backgroundFit: 'cover',
  themeMode: 'dark',
  sidebarTextColor: '#f1f5f9',
  contentTextColor: '#ffffff',
  userMessageBgColor: 'rgba(255, 255, 255, 0.08)',
  agentMessageBgColor: 'rgba(15, 18, 28, 0.75)',
  composerBgColor: 'rgba(18, 22, 34, 0.88)',
  sidebarBgColor: 'rgba(12, 15, 24, 0.85)',
  modalBgColor: 'rgba(18, 22, 34, 0.94)',
  codeBlockBgColor: 'rgba(10, 12, 20, 0.92)',
  autoDarkTheme: true,
  colorTheme: 'Default Dark Modern'
});

// 使用 shallowRef 避免对壁纸大对象做深度响应式 Proxy 劫持
const galleryList = shallowRef<SavedWallpaper[]>([]);
const currentActiveId = ref<string>('');
const customAppPath = ref('');
const customUrlInput = ref('');
const selectedFileName = ref('');
const isLoadingImage = ref(false);
const isApplying = ref(false);
const isRestoring = ref(false);
const opMessage = ref<{ success: boolean; text: string } | null>(null);
const isActiveTab = ref(false);

export function useWallpaper() {
  function resetTextColors() {
    config.sidebarTextColor = '#f1f5f9';
    config.contentTextColor = '#ffffff';
    config.userMessageBgColor = 'rgba(255, 255, 255, 0.08)';
    config.agentMessageBgColor = 'rgba(15, 18, 28, 0.75)';
    config.composerBgColor = 'rgba(18, 22, 34, 0.88)';
    config.sidebarBgColor = 'rgba(12, 15, 24, 0.85)';
    config.modalBgColor = 'rgba(18, 22, 34, 0.94)';
    config.codeBlockBgColor = 'rgba(10, 12, 20, 0.92)';
  }

  function applyColorThemePack(pack: ColorThemePack) {
    config.sidebarTextColor = pack.sidebarTextColor;
    config.contentTextColor = pack.contentTextColor;
    config.userMessageBgColor = pack.userMessageBgColor;
    config.agentMessageBgColor = pack.agentMessageBgColor;
    config.composerBgColor = pack.composerBgColor;
    config.sidebarBgColor = pack.sidebarBgColor;
    config.modalBgColor = pack.modalBgColor;
    config.codeBlockBgColor = pack.codeBlockBgColor;
  }

  function isCurrentSelected(item: SavedWallpaper): boolean {
    if (currentActiveId.value && item.id === currentActiveId.value) return true;
    if (config.imageData && item.data && config.imageData === item.data) return true;
    if (item.filePath && config.filePath && item.filePath === config.filePath) return true;
    return false;
  }

  async function selectGalleryItem(item: SavedWallpaper) {
    currentActiveId.value = item.id;
    config.imageSource = item.type;
    config.filePath = item.filePath;
    config.mediaType = item.mediaType || (isVideoMedia(undefined, item.data || item.filePath) ? 'video' : 'image');
    selectedFileName.value = item.name;

    // 恢复该壁纸保存时的调谐习惯与文字配色
    if (typeof item.playbackRate === 'number') config.playbackRate = item.playbackRate;
    if (typeof item.muted === 'boolean') config.muted = item.muted;
    if (typeof item.loop === 'boolean') config.loop = item.loop;
    if (typeof item.opacity === 'number') config.opacity = item.opacity;
    if (typeof item.blur === 'number') config.blur = item.blur;
    if (typeof item.darkOverlay === 'number') config.darkOverlay = item.darkOverlay;
    if (typeof item.glassAlpha === 'number') config.glassAlpha = item.glassAlpha;
    if (item.sidebarTextColor) config.sidebarTextColor = item.sidebarTextColor;
    if (item.contentTextColor) config.contentTextColor = item.contentTextColor;
    if (item.userMessageBgColor) config.userMessageBgColor = item.userMessageBgColor;
    if (item.agentMessageBgColor) config.agentMessageBgColor = item.agentMessageBgColor;
    if (item.composerBgColor) config.composerBgColor = item.composerBgColor;
    if (item.sidebarBgColor) config.sidebarBgColor = item.sidebarBgColor;
    if (item.modalBgColor) config.modalBgColor = item.modalBgColor;
    if (item.codeBlockBgColor) config.codeBlockBgColor = item.codeBlockBgColor;
    if (typeof item.autoDarkTheme === 'boolean') config.autoDarkTheme = item.autoDarkTheme;
    if (item.colorTheme) config.colorTheme = item.colorTheme;

    // 本地媒体优先使用轻量流式地址，0 Base64 内存占用
    if (item.filePath) {
      config.imageData = `/local-media?path=${encodeURIComponent(item.filePath)}`;
    } else if (item.data) {
      config.imageData = item.data;
    }
  }

  function applyPreset(preset: PresetItem) {
    currentActiveId.value = '';
    config.imageSource = 'url';
    config.imageData = preset.url;
    config.filePath = '';
    config.mediaType = preset.mediaType || (isVideoMedia(undefined, preset.url) ? 'video' : 'image');
    selectedFileName.value = preset.name;
  }

  async function refreshStatus() {
    try {
      const resRaw = await ipcRenderer.invoke('antigravitybg:get-status', customAppPath.value);
      const res: StatusResponse = typeof resRaw === 'string' ? JSON.parse(resRaw) : resRaw;
      Object.assign(status, res);

      if (res.gallery && Array.isArray(res.gallery)) {
        galleryList.value = res.gallery;
      }

      if (res.config && (res.config.imageData || res.config.filePath)) {
        if (res.config.filePath) {
          config.filePath = res.config.filePath;
          config.imageData = `/local-media?path=${encodeURIComponent(res.config.filePath)}`;
        } else if (res.config.imageData) {
          config.imageData = res.config.imageData;
        }
        if (res.config.mediaType) config.mediaType = res.config.mediaType;
        if (res.config.imageSource) config.imageSource = res.config.imageSource;
        if (typeof res.config.opacity === 'number') config.opacity = res.config.opacity;
        if (typeof res.config.blur === 'number') config.blur = res.config.blur;
        if (typeof res.config.darkOverlay === 'number') config.darkOverlay = res.config.darkOverlay;
        if (typeof res.config.glassAlpha === 'number') config.glassAlpha = res.config.glassAlpha;
        if (res.config.backgroundFit) config.backgroundFit = res.config.backgroundFit;
        if (res.config.sidebarTextColor) config.sidebarTextColor = res.config.sidebarTextColor;
        if (res.config.contentTextColor) config.contentTextColor = res.config.contentTextColor;
        if (res.config.userMessageBgColor) config.userMessageBgColor = res.config.userMessageBgColor;
        if (res.config.agentMessageBgColor) config.agentMessageBgColor = res.config.agentMessageBgColor;
        if (res.config.composerBgColor) config.composerBgColor = res.config.composerBgColor;
        if (res.config.sidebarBgColor) config.sidebarBgColor = res.config.sidebarBgColor;
        if (res.config.modalBgColor) config.modalBgColor = res.config.modalBgColor;
        if (res.config.codeBlockBgColor) config.codeBlockBgColor = res.config.codeBlockBgColor;
        if (typeof res.config.autoDarkTheme === 'boolean') config.autoDarkTheme = res.config.autoDarkTheme;
        if (res.config.colorTheme) config.colorTheme = res.config.colorTheme;
        if (typeof res.config.playbackRate === 'number') config.playbackRate = res.config.playbackRate;
        if (typeof res.config.muted === 'boolean') config.muted = res.config.muted;
        if (typeof res.config.loop === 'boolean') config.loop = res.config.loop;
        if (!config.sidebarTextColor) config.sidebarTextColor = '#f1f5f9';
        if (!config.contentTextColor) config.contentTextColor = '#ffffff';
        if (!config.userMessageBgColor) config.userMessageBgColor = 'rgba(255, 255, 255, 0.08)';
        if (!config.agentMessageBgColor) config.agentMessageBgColor = 'rgba(15, 18, 28, 0.75)';
        if (!config.composerBgColor) config.composerBgColor = 'rgba(18, 22, 34, 0.88)';
        if (!config.sidebarBgColor) config.sidebarBgColor = 'rgba(12, 15, 24, 0.85)';
        if (!config.modalBgColor) config.modalBgColor = 'rgba(18, 22, 34, 0.94)';
        if (!config.codeBlockBgColor) config.codeBlockBgColor = 'rgba(10, 12, 20, 0.92)';
        if (typeof config.autoDarkTheme === 'undefined') config.autoDarkTheme = true;
        if (!config.colorTheme) config.colorTheme = 'Default Dark Modern';
        if (!config.playbackRate) config.playbackRate = 1.0;
        if (typeof config.muted === 'undefined') config.muted = true;
        if (typeof config.loop === 'undefined') config.loop = true;
        selectedFileName.value = res.config.filePath ? res.config.filePath.split(/[\\/]/).pop() || '自定义壁纸' : '已配置壁纸';
      } else if (galleryList.value.length > 0) {
        selectGalleryItem(galleryList.value[0]);
      } else if (!config.imageData && presets.length > 0) {
        applyPreset(presets[0]);
      }
    } catch (e: any) {
      console.error('获取 Antigravity 状态失败:', e);
    }
  }

  async function openImagePicker() {
    isLoadingImage.value = true;
    try {
      const resRaw = await ipcRenderer.invoke('antigravitybg:select-image-dialog');
      const res = typeof resRaw === 'string' ? JSON.parse(resRaw) : resRaw;
      if (res && res.success && res.filePath) {
        const fileName = res.fileName || res.filePath.split(/[\\/]/).pop() || '本地媒体';
        const mediaUrl = res.mediaUrl || `/local-media?path=${encodeURIComponent(res.filePath)}`;
        const isVid = (res.mediaType === 'video') || isVideoMedia(undefined, res.filePath);
        
        // 生成 <15KB 轻量压缩缩略图
        const thumbnail = await createThumbnail(mediaUrl, 200, 0.6);

        const newWallpaper: SavedWallpaper = {
          id: `wp_${Date.now()}`,
          name: fileName,
          type: 'file',
          mediaType: isVid ? 'video' : 'image',
          data: mediaUrl,
          thumbnail: thumbnail,
          filePath: res.filePath,
          createdAt: Date.now(),
          playbackRate: config.playbackRate,
          muted: config.muted,
          loop: config.loop,
          opacity: config.opacity,
          blur: config.blur,
          darkOverlay: config.darkOverlay,
          glassAlpha: config.glassAlpha,
          sidebarTextColor: config.sidebarTextColor,
          contentTextColor: config.contentTextColor,
          userMessageBgColor: config.userMessageBgColor,
          agentMessageBgColor: config.agentMessageBgColor,
          composerBgColor: config.composerBgColor,
          sidebarBgColor: config.sidebarBgColor,
          modalBgColor: config.modalBgColor,
          codeBlockBgColor: config.codeBlockBgColor,
          autoDarkTheme: config.autoDarkTheme,
          colorTheme: config.colorTheme
        };

        const addResRaw = await ipcRenderer.invoke('antigravitybg:add-wallpaper', newWallpaper);
        const addRes = typeof addResRaw === 'string' ? JSON.parse(addResRaw) : addResRaw;
        if (addRes && addRes.gallery) {
          galleryList.value = addRes.gallery;
        }

        selectGalleryItem(newWallpaper);
      }
    } catch (e: any) {
      console.error('选择媒体失败:', e);
    } finally {
      isLoadingImage.value = false;
    }
  }

  function applyCustomUrl() {
    if (!customUrlInput.value) return;
    const url = customUrlInput.value.trim();
    const isVid = isVideoMedia(undefined, url);
    currentActiveId.value = '';
    config.imageSource = 'url';
    config.mediaType = isVid ? 'video' : 'image';
    config.imageData = url;
    config.filePath = '';
    selectedFileName.value = (isVid ? '动态视频: ' : '网络壁纸: ') + url.slice(0, 30) + '...';
  }

  async function saveUrlToGallery() {
    if (!customUrlInput.value) return;
    const url = customUrlInput.value.trim();
    const isVid = isVideoMedia(undefined, url);
    const newWallpaper: SavedWallpaper = {
      id: `wp_${Date.now()}`,
      name: (isVid ? '动态视频 ' : '网络壁纸 ') + (galleryList.value.length + 1),
      type: 'url',
      mediaType: isVid ? 'video' : 'image',
      data: url,
      createdAt: Date.now(),
      playbackRate: config.playbackRate,
      muted: config.muted,
      loop: config.loop,
      opacity: config.opacity,
      blur: config.blur,
      darkOverlay: config.darkOverlay,
      glassAlpha: config.glassAlpha,
      sidebarTextColor: config.sidebarTextColor,
      contentTextColor: config.contentTextColor,
      userMessageBgColor: config.userMessageBgColor,
      agentMessageBgColor: config.agentMessageBgColor,
      composerBgColor: config.composerBgColor,
      sidebarBgColor: config.sidebarBgColor,
      modalBgColor: config.modalBgColor,
      codeBlockBgColor: config.codeBlockBgColor,
      autoDarkTheme: config.autoDarkTheme,
      colorTheme: config.colorTheme
    };

    const addResRaw = await ipcRenderer.invoke('antigravitybg:add-wallpaper', newWallpaper);
    const addRes = typeof addResRaw === 'string' ? JSON.parse(addResRaw) : addResRaw;
    if (addRes && addRes.gallery) {
      galleryList.value = addRes.gallery;
    }
    selectGalleryItem(newWallpaper);
    customUrlInput.value = '';
  }

  async function deleteGalleryItem(id: string) {
    try {
      const resRaw = await ipcRenderer.invoke('antigravitybg:remove-wallpaper', id);
      const res = typeof resRaw === 'string' ? JSON.parse(resRaw) : resRaw;
      if (res && res.gallery) {
        galleryList.value = res.gallery;
      }
      if (currentActiveId.value === id) {
        if (galleryList.value.length > 0) {
          selectGalleryItem(galleryList.value[0]);
        } else {
          applyPreset(presets[0]);
        }
      }
    } catch (e: any) {
      console.error('删除壁纸失败:', e);
    }
  }

  async function handleApply() {
    if (!config.imageData) {
      opMessage.value = { success: false, text: '请先选择本地壁纸图片或输入网络图片 URL' };
      return;
    }
    isApplying.value = true;
    opMessage.value = null;
    try {
      config.enabled = true;
      const resRaw = await ipcRenderer.invoke('antigravitybg:apply', {
        customPath: customAppPath.value,
        config: JSON.parse(JSON.stringify(config))
      });
      const res = typeof resRaw === 'string' ? JSON.parse(resRaw) : resRaw;
      if (res && res.success) {
        opMessage.value = { success: true, text: res.message || '壁纸与文字配色已成功应用到 Antigravity 桌面端！' };
        await refreshStatus();
      } else {
        opMessage.value = { success: false, text: (res && res.error) || '应用壁纸配置失败' };
      }
    } catch (e: any) {
      opMessage.value = { success: false, text: `应用异常: ${e.message || e}` };
    } finally {
      isApplying.value = false;
    }
  }

  async function handleRestore() {
    isRestoring.value = true;
    opMessage.value = null;
    try {
      const resRaw = await ipcRenderer.invoke('antigravitybg:restore', customAppPath.value);
      const res = typeof resRaw === 'string' ? JSON.parse(resRaw) : resRaw;
      if (res && res.success) {
        opMessage.value = { success: true, text: res.message || '已成功恢复 Antigravity 出厂默认界面！' };
        await refreshStatus();
      } else {
        opMessage.value = { success: false, text: (res && res.error) || '恢复出厂失败' };
      }
    } catch (e: any) {
      opMessage.value = { success: false, text: `恢复异常: ${e.message || e}` };
    } finally {
      isRestoring.value = false;
    }
  }

  async function handleLaunch() {
    try {
      const resRaw = await ipcRenderer.invoke('antigravitybg:launch', customAppPath.value);
      const res = typeof resRaw === 'string' ? JSON.parse(resRaw) : resRaw;
      if (res && res.success) {
        opMessage.value = { success: true, text: '已拉起 Antigravity 桌面端' };
      } else {
        opMessage.value = { success: false, text: res.error || '拉起失败' };
      }
    } catch (e: any) {
      opMessage.value = { success: false, text: `启动异常: ${e.message || e}` };
    }
  }

  function handleTabChange(e: any) {
    const tab = e?.detail?.activePanel;
    isActiveTab.value = (tab === 'wallpaper');
  }

  function initWallpaperPanel() {
    isActiveTab.value = true;
    window.addEventListener('settings:tab-change', handleTabChange);
    refreshStatus();
  }

  function destroyWallpaperPanel() {
    isActiveTab.value = false;
    window.removeEventListener('settings:tab-change', handleTabChange);
    try {
      // 壁纸子页签切走时通知后端修剪 Go 进程及 WebView2 子进程工作集,
      // 配合 WallpaperLivePreview 已释放的 <video> 解码器, 使物理内存真实回落。
      ipcRenderer.send('antigravitybg:trim-memory');
    } catch (e) {
      console.warn('调用内存修剪异常:', e);
    }
  }

  return {
    status,
    config,
    galleryList,
    currentActiveId,
    customAppPath,
    customUrlInput,
    selectedFileName,
    isLoadingImage,
    isApplying,
    isRestoring,
    opMessage,
    isActiveTab,
    presets,
    sidebarColorPresets,
    contentColorPresets,
    userMessageColorPresets,
    agentMessageColorPresets,
    sidebarBgColorPresets,
    composerBgColorPresets,
    colorThemePacks,
    speedPresets,
    speedTicks,
    isVideoMedia,
    getSliderTrackStyle,
    isCurrentSelected,
    selectGalleryItem,
    applyPreset,
    applyColorThemePack,
    refreshStatus,
    openImagePicker,
    applyCustomUrl,
    saveUrlToGallery,
    deleteGalleryItem,
    handleApply,
    handleRestore,
    handleLaunch,
    resetTextColors,
    initWallpaperPanel,
    destroyWallpaperPanel
  };
}

/**
 * ocrSettings.ts: OCR 图片分析模型设置的响应式逻辑与状态管理模块。
 *
 * 遵循 KISS 与模块化原则，从 settingsController.ts 抽离：
 * - 提供响应式状态 ocrModel、ocrModelOptions，供 GeneralPanel 中的 ModelSearchSelect 直接绑定；
 * - 结合 modelSelectCore.ts 中的 buildOcrCandidates，提取开启 Expose 的中继模型并追加当前值回显；
 * - 支持自定义输入、打开下拉自刷新以及通过 IPC (settings:set-ocr-model) 实时持久化落盘。
 */
import { ref } from 'vue';
import { ipcRenderer } from '../shared/ipc';
import { buildOcrCandidates } from '../components/settings/agent-config/modelSelectCore';

export const ocrModel = ref<string>('');
export const ocrModelOptions = ref<string[]>([]);

let isOcrEventsInited = false;

/**
 * 读取当前已保存的 OCR 模型名称
 */
export function getSavedOcrModel(): string {
  let val = '';
  if ((window as any).wailsConfigCache && (window as any).wailsConfigCache['settings:get-ocr-model']) {
    val = (window as any).wailsConfigCache['settings:get-ocr-model'];
  } else {
    try {
      const m = ipcRenderer.sendSync('settings:get-ocr-model');
      if (m) {
        val = m;
      }
    } catch (_) {
      /* 异常时保持 val 为空 */
    }
  }
  return val;
}

/**
 * 刷新 OCR 模型候选列表
 * 从中继模型映射中提取 expose=true 的 ClientModel，并追加当前已保存的值
 */
export async function refreshOcrModelOptions(): Promise<void> {
  try {
    const mappings = await ipcRenderer.invoke('relay:get-model-mapping');
    ocrModelOptions.value = buildOcrCandidates(mappings || [], ocrModel.value);
  } catch (e) {
    console.error('[OcrSettings] Failed to fetch relay model mapping:', e);
    const fallback = ocrModel.value ? [ocrModel.value] : [];
    ocrModelOptions.value = fallback;
  }
}

/**
 * 更新并持久化 OCR 模型设置
 */
export function setOcrModel(val: string): void {
  const finalVal = (val || '').trim();
  ocrModel.value = finalVal;

  if ((window as any).wailsConfigCache) {
    (window as any).wailsConfigCache['settings:get-ocr-model'] = finalVal;
  }

  try {
    ipcRenderer.send('settings:set-ocr-model', finalVal);
  } catch (err) {
    console.error('[OcrSettings] Failed to save ocr model:', err);
  }

  // 保证当前选中的模型在选项列表中正常显示
  if (finalVal && !ocrModelOptions.value.includes(finalVal)) {
    ocrModelOptions.value = [...ocrModelOptions.value, finalVal].sort((a, b) => a.localeCompare(b));
  }
}

/**
 * 刷新 OCR 状态与候选列表（供外部 settingsController / refreshSettingsUI 统一调用）
 */
export async function refreshOcrModel(): Promise<void> {
  const saved = getSavedOcrModel();
  if (saved) {
    ocrModel.value = saved;
  }
  await refreshOcrModelOptions();
}

/**
 * 初始化 OCR 设置状态与 IPC 事件监听
 */
export function initOcrSettings(): void {
  const saved = getSavedOcrModel();
  if (saved) {
    ocrModel.value = saved;
  }
  void refreshOcrModelOptions();

  if (!isOcrEventsInited) {
    ipcRenderer.on('settings:ocr-model-res', (_event, model: string) => {
      if (typeof model === 'string' && model) {
        ocrModel.value = model;
        if ((window as any).wailsConfigCache) {
          (window as any).wailsConfigCache['settings:get-ocr-model'] = model;
        }
        if (!ocrModelOptions.value.includes(model)) {
          ocrModelOptions.value = [...ocrModelOptions.value, model].sort((a, b) => a.localeCompare(b));
        }
      }
    });
    isOcrEventsInited = true;
  }
}

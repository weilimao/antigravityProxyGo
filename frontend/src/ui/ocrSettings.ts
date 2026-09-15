/**
 * ocrSettings.ts: OCR 图片分析模型设置的响应式逻辑与状态管理模块。
 *
 * 遵循 KISS 与模块化原则，从 settingsController.ts 抽离：
 * - 提供响应式状态 ocrModels、ocrModel、ocrModelOptions，供 GeneralPanel 中的 ModelSearchSelect 直接绑定；
 * - 支持多选候选并发竞速模式（首包成功胜出，自动 cancel 其余候选）；
 * - 结合 modelSelectCore.ts 中的 buildOcrCandidates，提取开启 Expose 的中继模型并追加当前值回显；
 * - 支持自定义输入、打开下拉自刷新以及通过 IPC (settings:set-ocr-models) 实时持久化落盘。
 */
import { ref } from 'vue';
import { ipcRenderer } from '../shared/ipc';
import { buildOcrCandidates } from '../components/settings/agent-config/modelSelectCore';

export const ocrModel = ref<string>('');
export const ocrModels = ref<string[]>([]);
export const ocrModelOptions = ref<string[]>([]);

let isOcrEventsInited = false;

/**
 * 读取当前已保存的 OCR 模型名称（单模型兼容）
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
 * 读取当前已保存的 OCR 候选模型列表
 */
export function getSavedOcrModels(): string[] {
  let list: string[] = [];
  const cache = (window as any).wailsConfigCache;
  if (cache && Array.isArray(cache['settings:get-ocr-models']) && cache['settings:get-ocr-models'].length > 0) {
    list = cache['settings:get-ocr-models'];
  } else {
    try {
      const res = ipcRenderer.sendSync('settings:get-ocr-models');
      if (Array.isArray(res) && res.length > 0) {
        list = res;
      }
    } catch (_) {
      /* 忽略 IPC 异常 */
    }
  }

  // 若多选列表为空，则尝试用单选模型兜底
  if (list.length === 0) {
    const single = getSavedOcrModel();
    if (single) {
      list = [single];
    }
  }
  return list;
}

/**
 * 刷新 OCR 模型候选列表
 * 从中继模型映射中提取 expose=true 的 ClientModel，并追加当前已保存的值
 */
export async function refreshOcrModelOptions(): Promise<void> {
  try {
    const mappings = await ipcRenderer.invoke('relay:get-model-mapping');
    const allKnown = Array.from(new Set([...(ocrModels.value || []), ocrModel.value].filter(Boolean)));
    const primary = allKnown[0] || '';
    let options = buildOcrCandidates(mappings || [], primary);
    // 确保所有已选的候选模型都在选项列表中
    for (const m of allKnown) {
      if (m && !options.includes(m)) {
        options = [...options, m];
      }
    }
    ocrModelOptions.value = options.sort((a, b) => a.localeCompare(b));
  } catch (e) {
    console.error('[OcrSettings] Failed to fetch relay model mapping:', e);
    const fallback = Array.from(new Set([...(ocrModels.value || []), ocrModel.value].filter(Boolean)));
    ocrModelOptions.value = fallback;
  }
}

/**
 * 更新并持久化 OCR 候选模型设置（支持多选竞速）
 */
export function setOcrModels(val: string[]): void {
  const cleanList = Array.from(new Set((val || []).map((m) => (m || '').trim()).filter(Boolean)));
  ocrModels.value = cleanList;
  ocrModel.value = cleanList[0] || '';

  if ((window as any).wailsConfigCache) {
    (window as any).wailsConfigCache['settings:get-ocr-models'] = cleanList;
    (window as any).wailsConfigCache['settings:get-ocr-model'] = ocrModel.value;
  }

  try {
    ipcRenderer.send('settings:set-ocr-models', cleanList);
  } catch (err) {
    console.error('[OcrSettings] Failed to save ocr models:', err);
  }

  // 保证已选模型都在候选列表中
  for (const m of cleanList) {
    if (m && !ocrModelOptions.value.includes(m)) {
      ocrModelOptions.value = [...ocrModelOptions.value, m].sort((a, b) => a.localeCompare(b));
    }
  }
}

/**
 * 单模型更新兼容函数
 */
export function setOcrModel(val: string): void {
  const finalVal = (val || '').trim();
  if (finalVal) {
    setOcrModels([finalVal]);
  } else {
    setOcrModels([]);
  }
}

/**
 * 刷新 OCR 状态与候选列表（供外部 settingsController / refreshSettingsUI 统一调用）
 */
export async function refreshOcrModel(): Promise<void> {
  const savedList = getSavedOcrModels();
  if (savedList.length > 0) {
    ocrModels.value = savedList;
    ocrModel.value = savedList[0] || '';
  }
  await refreshOcrModelOptions();
}

/**
 * 初始化 OCR 设置状态与 IPC 事件监听
 */
export function initOcrSettings(): void {
  const savedList = getSavedOcrModels();
  if (savedList.length > 0) {
    ocrModels.value = savedList;
    ocrModel.value = savedList[0] || '';
  }
  void refreshOcrModelOptions();

  if (!isOcrEventsInited) {
    ipcRenderer.on('settings:ocr-models-res', (_event, models: string[]) => {
      if (Array.isArray(models)) {
        ocrModels.value = models;
        ocrModel.value = models[0] || '';
        if ((window as any).wailsConfigCache) {
          (window as any).wailsConfigCache['settings:get-ocr-models'] = models;
          (window as any).wailsConfigCache['settings:get-ocr-model'] = ocrModel.value;
        }
      }
    });

    ipcRenderer.on('settings:ocr-model-res', (_event, model: string) => {
      if (typeof model === 'string' && model) {
        if (ocrModels.value.length === 0) {
          ocrModels.value = [model];
        }
        ocrModel.value = model;
        if ((window as any).wailsConfigCache) {
          (window as any).wailsConfigCache['settings:get-ocr-model'] = model;
        }
      }
    });
    isOcrEventsInited = true;
  }
}

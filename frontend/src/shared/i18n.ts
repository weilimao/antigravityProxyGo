/**
 * Antigravity Proxy - Bilingual Dictionary (ZH / EN)
 *
 * 本文件现为 re-export 壳：实际翻译字典已按命名空间拆分到 shared/i18n/ 目录
 * (common / dashboard / settings / relay / nvidia / other / accounts / otp /
 *  packets / pricing / autoTrigger / usage / help / requestlog / aggregate)，
 * 由 shared/i18n/index.ts 聚合合并。
 *
 * 保留此壳文件是为了不破坏所有现有 `import i18n from '../shared/i18n'`
 * 与 `import translations from '../shared/i18n'` 调用点（零改动迁移）。
 *
 * 生成工具：scripts/split_i18n.py + scripts/gen_i18n_files.py
 */
export { default, default as translations } from './i18n/index';
export type { I18nDictionary, Translations } from './i18n/index';

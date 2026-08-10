/**
 * i18n 聚合入口：合并各命名空间为 {zh:..., en:...} 形态。
 * 原 shared/i18n.ts 现 re-export 此处，保持所有 `import i18n from '../shared/i18n'` 零改动。
 *
 * 类型契约与原文件一致：I18nDictionary / Translations。
 * dev 模式断言：聚合前检测跨命名空间 key 重复（后者会静默覆盖前者，丢失文案）。
 */
import { commonZh, commonEn } from './common';
import { dashboardZh, dashboardEn } from './dashboard';
import { settingsZh, settingsEn } from './settings';
import { relayZh, relayEn } from './relay';
import { nvidiaZh, nvidiaEn } from './nvidia';
import { otherZh, otherEn } from './other';
import { grokZh, grokEn } from './grok';
import { accountsZh, accountsEn } from './accounts';
import { otpZh, otpEn } from './otp';
import { packetsZh, packetsEn } from './packets';
import { pricingZh, pricingEn } from './pricing';
import { autoTriggerZh, autoTriggerEn } from './autoTrigger';
import { usageZh, usageEn } from './usage';
import { helpZh, helpEn } from './help';
import { requestlogZh, requestlogEn } from './requestlog';
import { aggregateZh, aggregateEn } from './aggregate';

export interface I18nDictionary {
    [key: string]: string;
}

export interface Translations {
    zh: I18nDictionary;
    en: I18nDictionary;
    [key: string]: I18nDictionary;
}

// dev 模式断言：检测聚合前跨命名空间 key 冲突
function assertNoKeyOverlap(namespaces: Array<{ name: string; dict: Record<string, string> }>, label: string): void {
    const seen = new Map<string, string>();
    const dupes: string[] = [];
    for (const nsEl of namespaces) {
        for (const k of Object.keys(nsEl.dict)) {
            const prev = seen.get(k);
            if (prev !== undefined) {
                dupes.push(`${k} (in ${prev} and ${nsEl.name})`);
            } else {
                seen.set(k, nsEl.name);
            }
        }
    }
    if (dupes.length > 0) {
        // 重复 key 在 spread 合并时会静默丢失，仅保留最后出现的值
        console.warn(`[i18n] ${label}: duplicate keys across namespaces:`, dupes.join('; '));
    }
}

export const translations: Translations = {
    zh: {
    ...commonZh,
    ...dashboardZh,
    ...settingsZh,
    ...relayZh,
    ...nvidiaZh,
    ...otherZh,
    ...grokZh,
    ...accountsZh,
    ...otpZh,
    ...packetsZh,
    ...pricingZh,
    ...autoTriggerZh,
    ...usageZh,
    ...helpZh,
    ...requestlogZh,
    ...aggregateZh,
    },
    en: {
    ...commonEn,
    ...dashboardEn,
    ...settingsEn,
    ...relayEn,
    ...nvidiaEn,
    ...otherEn,
    ...grokEn,
    ...accountsEn,
    ...otpEn,
    ...packetsEn,
    ...pricingEn,
    ...autoTriggerEn,
    ...usageEn,
    ...helpEn,
    ...requestlogEn,
    ...aggregateEn,
    },
};

assertNoKeyOverlap(
    [
        { name: 'common', dict: commonZh },
        { name: 'common', dict: commonEn },
        { name: 'dashboard', dict: dashboardZh },
        { name: 'dashboard', dict: dashboardEn },
        { name: 'settings', dict: settingsZh },
        { name: 'settings', dict: settingsEn },
        { name: 'relay', dict: relayZh },
        { name: 'relay', dict: relayEn },
        { name: 'nvidia', dict: nvidiaZh },
        { name: 'nvidia', dict: nvidiaEn },
        { name: 'other', dict: otherZh },
        { name: 'other', dict: otherEn },
        { name: 'grok', dict: grokZh },
        { name: 'grok', dict: grokEn },
        { name: 'accounts', dict: accountsZh },
        { name: 'accounts', dict: accountsEn },
        { name: 'otp', dict: otpZh },
        { name: 'otp', dict: otpEn },
        { name: 'packets', dict: packetsZh },
        { name: 'packets', dict: packetsEn },
        { name: 'pricing', dict: pricingZh },
        { name: 'pricing', dict: pricingEn },
        { name: 'autoTrigger', dict: autoTriggerZh },
        { name: 'autoTrigger', dict: autoTriggerEn },
        { name: 'usage', dict: usageZh },
        { name: 'usage', dict: usageEn },
        { name: 'help', dict: helpZh },
        { name: 'help', dict: helpEn },
        { name: 'requestlog', dict: requestlogZh },
        { name: 'requestlog', dict: requestlogEn },
        { name: 'aggregate', dict: aggregateZh },
        { name: 'aggregate', dict: aggregateEn },
    ],
    'translations'
);

export default translations;

/**
 * i18n 命名空间: aggregate
 * 由 scripts/split_i18n.py + scripts/gen_i18n_files.py 从 shared/i18n.ts 拆分生成，保持 zh 原始顺序与原始 value 字面量。
 */
export const aggregateZh: Record<string, string> = {
    aggregateQuotaInfo: "共 0 个账号",
    aggregateQuotaTitle: "账号池总额度汇总",
};

export const aggregateEn: Record<string, string> = {
    aggregateQuotaInfo: "Total {count} accounts",
    aggregateQuotaTitle: "Total Quota Summary",
};

/**
 * trendAggregator.ts: 趋势图时间插槽生成、粒度自适应判断与多维度数据精准聚合。
 *
 * 核心设计:
 * 1. 自适应时间粒度 (Granularity):
 *    - 今日 (today) / 近24小时 (24h) / 近三天 (3d): 按小时 (Hourly) 粒度保留实时与微观波动。
 *    - 七天 (7d) / 30天 (30d): 自动切换为按日 (Daily) 聚合，消除 720 个小时点的高频毛刺锯齿。
 *    - 自定义筛选 (custom): 跨度 <= 48 小时走小时粒度，> 48 小时走日粒度。
 * 2. 精准按日 Group By:
 *    - 累加每天 24 小时的 input, output, cached, requests, cost 及各项分项成本。
 *    - 数学严格守恒: 聚合前后各指标求和结果 100% 精确一致，保证顶部卡片数据绝对准确。
 */

export interface TrendItem {
    time: string;
    input: number;
    output: number;
    cached: number;
    requests: number;
    cost: number;
    inputCost?: number;
    outputCost?: number;
    cachedCost?: number;
}

export type TrendGranularity = 'hour' | 'day';

/**
 * 格式化日期为 "MM/DD HH:00"
 */
export function formatTrendsHourTime(date: Date): string {
    const m = String(date.getMonth() + 1).padStart(2, '0');
    const d = String(date.getDate()).padStart(2, '0');
    const hh = String(date.getHours()).padStart(2, '0');
    return `${m}/${d} ${hh}:00`;
}

/**
 * 格式化日期为 "MM/DD"
 */
export function formatTrendsDayTime(date: Date): string {
    const m = String(date.getMonth() + 1).padStart(2, '0');
    const d = String(date.getDate()).padStart(2, '0');
    return `${m}/${d}`;
}

/**
 * 解析时间字符串为 Date 对象 (支持 "MM/DD HH:00" 与 "MM/DD")
 */
export function parseTrendsTime(timeStr: string): Date {
    if (!timeStr) return new Date();
    const currentYear = new Date().getFullYear();
    const parts = timeStr.trim().split(' ');
    const dateParts = parts[0].split('/');
    const month = parseInt(dateParts[0], 10) - 1;
    const day = parseInt(dateParts[1], 10);

    if (parts.length >= 2) {
        const timeParts = parts[1].split(':');
        const hour = parseInt(timeParts[0], 10);
        const minute = parseInt(timeParts[1] || '0', 10);
        return new Date(currentYear, month, day, hour, minute);
    }

    return new Date(currentYear, month, day, 0, 0, 0);
}

/**
 * 判断指定时间范围对应的聚合粒度 ('hour' | 'day')
 */
export function getTrendGranularity(
    range: string,
    customStartDate?: number | null,
    customEndDate?: number | null
): TrendGranularity {
    if (range === 'today' || range === '24h' || range === '3d') {
        return 'hour';
    }
    if (range === '7d' || range === '30d') {
        return 'day';
    }
    if (range === 'custom') {
        if (!customStartDate || !customEndDate) {
            return 'day';
        }
        const diffMs = Math.abs(customEndDate - customStartDate);
        const diffHours = diffMs / (3600 * 1000);
        return diffHours <= 48 ? 'hour' : 'day';
    }
    return 'day';
}

/**
 * 生成整点小时插槽 (如最近 24 小时、72 小时)
 */
export function generateHourlySlots(hoursCount: number, anchorDate: Date = new Date()): string[] {
    const slots: string[] = [];
    const anchorMs = new Date(
        anchorDate.getFullYear(),
        anchorDate.getMonth(),
        anchorDate.getDate(),
        anchorDate.getHours(),
        0, 0, 0
    ).getTime();

    for (let i = hoursCount - 1; i >= 0; i--) {
        const t = new Date(anchorMs - i * 3600 * 1000);
        slots.push(formatTrendsHourTime(t));
    }
    return slots;
}

/**
 * 生成今日插槽 (00:00 到当前小时)
 */
export function generateTodaySlots(anchorDate: Date = new Date()): string[] {
    const slots: string[] = [];
    const currentHour = anchorDate.getHours();
    for (let h = 0; h <= currentHour; h++) {
        const t = new Date(anchorDate.getFullYear(), anchorDate.getMonth(), anchorDate.getDate(), h, 0, 0, 0);
        slots.push(formatTrendsHourTime(t));
    }
    return slots;
}

/**
 * 生成日级插槽 (如过去 7 天、30 天，格式 "MM/DD")
 */
export function generateDailySlots(daysCount: number, anchorDate: Date = new Date()): string[] {
    const slots: string[] = [];
    const anchorMidnight = new Date(
        anchorDate.getFullYear(),
        anchorDate.getMonth(),
        anchorDate.getDate(),
        0, 0, 0, 0
    ).getTime();

    for (let i = daysCount - 1; i >= 0; i--) {
        const t = new Date(anchorMidnight - i * 24 * 3600 * 1000);
        slots.push(formatTrendsDayTime(t));
    }
    return slots;
}

/**
 * 生成自定义区间的插槽 (自适应小时或日)
 */
export function generateCustomSlots(startObj: Date, endObj: Date): { slots: string[]; granularity: TrendGranularity } {
    const startMs = new Date(startObj.getFullYear(), startObj.getMonth(), startObj.getDate(), startObj.getHours(), 0, 0, 0).getTime();
    const endMs = new Date(endObj.getFullYear(), endObj.getMonth(), endObj.getDate(), endObj.getHours(), 0, 0, 0).getTime();
    const diffHours = Math.max(1, Math.ceil((endMs - startMs) / (3600 * 1000)));

    if (diffHours <= 48) {
        const slots: string[] = [];
        for (let i = 0; i <= diffHours; i++) {
            slots.push(formatTrendsHourTime(new Date(startMs + i * 3600 * 1000)));
        }
        return { slots, granularity: 'hour' };
    }

    // 超过 48 小时按日聚合
    const startDayMs = new Date(startObj.getFullYear(), startObj.getMonth(), startObj.getDate(), 0, 0, 0, 0).getTime();
    const endDayMs = new Date(endObj.getFullYear(), endObj.getMonth(), endObj.getDate(), 0, 0, 0, 0).getTime();
    const diffDays = Math.max(0, Math.round((endDayMs - startDayMs) / (24 * 3600 * 1000)));

    const slots: string[] = [];
    for (let i = 0; i <= diffDays; i++) {
        slots.push(formatTrendsDayTime(new Date(startDayMs + i * 24 * 3600 * 1000)));
    }
    return { slots, granularity: 'day' };
}

/**
 * 将原始趋势数据按目标时间范围进行过滤与聚合
 *
 * @param trends 后端下发的原始小时趋势数组 (每项 time 格式如 "08/16 18:00")
 * @param range 时间范围 ('today' | '24h' | '3d' | '7d' | '30d' | 'custom')
 * @param customStartDate 自定义开始时间戳 (可选)
 * @param customEndDate 自定义结束时间戳 (可选)
 * @returns 过滤或聚合后的趋势项数组
 */
export function getFilteredTrends(
    trends: TrendItem[] | undefined | null,
    range: string,
    customStartDate?: number | null,
    customEndDate?: number | null
): TrendItem[] {
    const rawTrends = trends || [];
    const granularity = getTrendGranularity(range, customStartDate, customEndDate);

    let slots: string[] = [];

    if (range === 'today') {
        slots = generateTodaySlots();
    } else if (range === '24h') {
        slots = generateHourlySlots(24);
    } else if (range === '3d') {
        slots = generateHourlySlots(72);
    } else if (range === '7d') {
        slots = generateDailySlots(7);
    } else if (range === '30d') {
        slots = generateDailySlots(30);
    } else if (range === 'custom') {
        if (!customStartDate || !customEndDate) {
            slots = generateDailySlots(7);
        } else {
            const customRes = generateCustomSlots(new Date(customStartDate), new Date(customEndDate));
            slots = customRes.slots;
        }
    } else {
        slots = generateDailySlots(7);
    }

    if (granularity === 'hour') {
        // 小时级直接通过 time key 做 O(1) 索引映射
        const trendsByTime = new Map<string, TrendItem>();
        for (const item of rawTrends) {
            trendsByTime.set(item.time, item);
        }

        return slots.map(slot => {
            const found = trendsByTime.get(slot);
            if (found) return found;
            return {
                time: slot,
                input: 0,
                output: 0,
                cached: 0,
                requests: 0,
                cost: 0,
                inputCost: 0,
                outputCost: 0,
                cachedCost: 0
            };
        });
    }

    // 日级聚合: 按 "MM/DD" 对 24 小时桶进行 Group By 累加汇总
    const dailyMap = new Map<string, TrendItem>();

    for (const item of rawTrends) {
        if (!item || !item.time) continue;
        const dayKey = item.time.split(' ')[0]; // 提取 "MM/DD"
        if (!dayKey) continue;

        let existing = dailyMap.get(dayKey);
        if (!existing) {
            existing = {
                time: dayKey,
                input: 0,
                output: 0,
                cached: 0,
                requests: 0,
                cost: 0,
                inputCost: 0,
                outputCost: 0,
                cachedCost: 0
            };
            dailyMap.set(dayKey, existing);
        }

        existing.input += item.input || 0;
        existing.output += item.output || 0;
        existing.cached += item.cached || 0;
        existing.requests += item.requests || 0;
        existing.cost = Math.round((existing.cost + (item.cost || 0)) * 1000000) / 1000000;
        existing.inputCost = Math.round(((existing.inputCost || 0) + (item.inputCost || 0)) * 1000000) / 1000000;
        existing.outputCost = Math.round(((existing.outputCost || 0) + (item.outputCost || 0)) * 1000000) / 1000000;
        existing.cachedCost = Math.round(((existing.cachedCost || 0) + (item.cachedCost || 0)) * 1000000) / 1000000;
    }

    return slots.map(slot => {
        const found = dailyMap.get(slot);
        if (found) return found;
        return {
            time: slot,
            input: 0,
            output: 0,
            cached: 0,
            requests: 0,
            cost: 0,
            inputCost: 0,
            outputCost: 0,
            cachedCost: 0
        };
    });
}

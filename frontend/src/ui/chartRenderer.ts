import state from './dashboardState';
import i18n from '../shared/i18n';
import * as trendAggregator from './trendAggregator';
import * as chartAnimation from './chartAnimation';

export { trendAggregator, chartAnimation };
export const parseTrendsTime = trendAggregator.parseTrendsTime;
export const formatTrendsTime = trendAggregator.formatTrendsHourTime;

// Format Numbers
export function formatCompactNumber(number: number): string {
    if (number >= 1000000) {
        return (number / 1000000).toFixed(2) + 'M';
    }
    if (number >= 1000) {
        return (number / 1000).toFixed(1) + 'k';
    }
    return number.toFixed(0);
}

export interface FormatTokenOptions {
    /** 语言环境，默认取当前系统语言 ('zh' | 'en') */
    lang?: 'zh' | 'en';
    /** 小数保留位数，默认为 2 */
    decimals?: number;
    /** 是否自动修剪末尾无效的 0 (如 1.50 -> 1.5, 1.00 -> 1)，默认为 true */
    trimZero?: boolean;
}

/**
 * formatTokenCount: 对 Token 计数做多阶梯智能格式化
 * 超过亿/Billion 级别自动换算为易读单位，支持中英双语自适应
 *
 * @param count 原始 Token 数量
 * @param options 配置项
 * @returns 格式化后的字符串 (如 "149.92 亿", "14.99 B", "8,923")
 */
export function formatTokenCount(count: number, options?: FormatTokenOptions): string {
    if (typeof count !== 'number' || isNaN(count) || count < 0) {
        return '0';
    }

    const lang = options?.lang || (state.currentLanguage === 'zh' ? 'zh' : 'en');
    const decimals = options?.decimals ?? 2;
    const trimZero = options?.trimZero ?? true;

    const formatNum = (num: number): string => {
        let str = num.toFixed(decimals);
        if (trimZero && str.includes('.')) {
            str = str.replace(/\.?0+$/, '');
        }
        return str;
    };

    if (lang === 'zh') {
        // >= 1 亿 (100,000,000)
        if (count >= 100_000_000) {
            return `${formatNum(count / 100_000_000)} 亿`;
        }
        // >= 1 万 (10,000)
        if (count >= 10_000) {
            return `${formatNum(count / 10_000)} 万`;
        }
        // 小于 1 万直接显示千分位
        return Math.floor(count).toLocaleString('zh-CN');
    } else {
        // 英文进位体系
        if (count >= 1_000_000_000) {
            return `${formatNum(count / 1_000_000_000)} B`;
        }
        if (count >= 1_000_000) {
            return `${formatNum(count / 1_000_000)} M`;
        }
        if (count >= 1_000) {
            return `${formatNum(count / 1_000)} k`;
        }
        return Math.floor(count).toLocaleString('en-US');
    }
}

// Helper for calculating smooth bezier curves
export function getBezierPath(points: { x: number; y: number }[]): string {
    if (points.length === 0) return '';
    if (points.length === 1) return `M ${points[0].x},${points[0].y}`;
    let d = `M ${points[0].x},${points[0].y}`;
    for (let i = 0; i < points.length - 1; i++) {
        const p0 = points[i];
        const p1 = points[i + 1];
        const cpX1 = p0.x + (p1.x - p0.x) / 2;
        const cpY1 = p0.y;
        const cpX2 = p0.x + (p1.x - p0.x) / 2;
        const cpY2 = p1.y;
        d += ` C ${cpX1.toFixed(1)},${cpY1.toFixed(1)} ${cpX2.toFixed(1)},${cpY2.toFixed(1)} ${p1.x.toFixed(1)},${p1.y.toFixed(1)}`;
    }
    return d;
}

// Render Memory Usage Chart
export function updateMemoryChart() {
    const svg = document.getElementById('memorySvg');
    const path = document.getElementById('memoryChartPath');
    const area = document.getElementById('memoryChartArea');
    const dot = document.getElementById('memoryChartDot');
    if (!svg || !path || !area || state.memoryHistory.length === 0) return;

    const width = 200;
    const height = 45;
    const padding = 4; // Padding to keep line and dot within bounds

    const N = state.memoryHistory.length;
    let minVal = Math.min(...state.memoryHistory);
    let maxVal = Math.max(...state.memoryHistory);

    // Dynamic scaling logic
    if (maxVal - minVal < 5.0) {
        const center = (maxVal + minVal) / 2;
        minVal = Math.max(0, center - 2.5);
        maxVal = center + 2.5;
    } else {
        const diff = maxVal - minVal;
        minVal = Math.max(0, minVal - diff * 0.1);
        maxVal = maxVal + diff * 0.1;
    }

    const points = state.memoryHistory.map((val, idx) => {
        const x = N > 1 ? (idx / (N - 1)) * width : width / 2;
        const y = height - padding - ((val - minVal) / (maxVal - minVal)) * (height - 2 * padding);
        return { x, y };
    });

    let d = '';
    if (points.length === 1) {
        d = `M 0,${points[0].y} L ${width},${points[0].y}`;
    } else {
        d = getBezierPath(points);
    }

    path.setAttribute('d', d);

    if (points.length > 0) {
        const areaD = `${d} L ${points[points.length - 1].x},${height} L ${points[0].x},${height} Z`;
        area.setAttribute('d', areaD);
    }

    if (dot && points.length > 0) {
        const lastPoint = points[points.length - 1];
        dot.setAttribute('cx', lastPoint.x.toFixed(1));
        dot.setAttribute('cy', lastPoint.y.toFixed(1));
    }
}

// Draw SVG Line Chart
// clearTrendChart: 将趋势折线图清空为空态 (5 条 path 置空 d, 两个 area 置空, 左/右/底
// 轴 label 清空, 成本与 Token 汇总值归零)。用于「使用趋势」在 currentTrendScope 切到
// NVIDIA 但 nvidiaTrendsData 仍为空时, 避免画面残留上一个 scope 的曲线造成误读。
// 元素缺失时静默跳过, 与 drawTrendChartSVG 的容错口径一致。
export function clearTrendChart() {
    const ids = ['chartPathCost', 'chartPathInput', 'chartPathOutput', 'chartPathCached', 'chartPathRequests', 'chartAreaInput', 'chartAreaCached'];
    for (const id of ids) {
        const el = document.getElementById(id);
        if (el) el.setAttribute('d', '');
    }
    const axisIds = ['chartLeftAxis', 'chartRightAxis', 'chartXAxis'];
    for (const id of axisIds) {
        const g = document.getElementById(id);
        if (g) g.textContent = '';
    }
    const valIds = ['valSummaryTotal', 'valSummaryInput', 'valSummaryOutput', 'valSummaryCached', 'valSummaryTotalRequests', 'valSummaryTotalTokens', 'valSummaryInputTokens', 'valSummaryOutputTokens', 'valSummaryCachedTokens', 'valSummaryHitRate'];
    // 成本类汇总(以 $ 前缀显示)归零为 '$0.0000'; 命中率类(百分比)归零为 '0.0%'; 计数类(请求数/Token)归零为 '0'。
    const costValIds = new Set(['valSummaryTotal', 'valSummaryInput', 'valSummaryOutput', 'valSummaryCached']);
    const percentValIds = new Set(['valSummaryHitRate']);
    for (const id of valIds) {
        const el = document.getElementById(id);
        if (!el) continue;
        if (costValIds.has(id)) el.textContent = '$0.0000';
        else if (percentValIds.has(id)) el.textContent = '0.0%';
        else el.textContent = '0';
    }
}

export function drawTrendChartSVG(trends: any[], range = '7d', animate = true) {
    const trendSvg = document.getElementById('trendSvg');
    const costPath = document.getElementById('chartPathCost');
    const inputPath = document.getElementById('chartPathInput');
    const outputPath = document.getElementById('chartPathOutput');
    const cachedPath = document.getElementById('chartPathCached');
    const requestsPath = document.getElementById('chartPathRequests');
    const inputArea = document.getElementById('chartAreaInput');
    const cachedArea = document.getElementById('chartAreaCached');
    const gridLinesGroup = document.getElementById('chartGridLines');
    const sensorRect = document.getElementById('chartSensor');

    const leftAxis = document.getElementById('chartLeftAxis');
    const rightAxis = document.getElementById('chartRightAxis');
    const xAxis = document.getElementById('chartXAxis');

    if (!trendSvg || !trends || trends.length === 0 || !costPath || !inputPath || !outputPath || !cachedPath || !requestsPath || !inputArea || !cachedArea || !gridLinesGroup || !sensorRect || !leftAxis || !rightAxis || !xAxis) return;

    // Calculate total summary stats for the filtered trends
    let totalCostVal = 0;
    let totalInputCostVal = 0;
    let totalOutputCostVal = 0;
    let totalCachedCostVal = 0;

    let totalInputTokensVal = 0;
    let totalOutputTokensVal = 0;
    let totalCachedTokensVal = 0;
    let totalRequestsVal = 0;

    trends.forEach(bin => {
        const binCost = bin.cost || 0;
        let binInputCost = bin.inputCost;
        let binOutputCost = bin.outputCost;
        let binCachedCost = bin.cachedCost;

        if (binInputCost === undefined || binOutputCost === undefined || binCachedCost === undefined) {
            // Estimate using default rates (Gemini 3.5 Flash)
            const inputTokens = bin.input || 0;
            const outputTokens = bin.output || 0;
            const cachedTokens = bin.cached || 0;
            const nonCachedIn = Math.max(0, inputTokens - cachedTokens);

            const estInput = nonCachedIn * 1.50 / 1000000;
            const estOutput = outputTokens * 9.00 / 1000000;
            const estCached = cachedTokens * 0.375 / 1000000;
            const estTotal = estInput + estOutput + estCached;

            if (estTotal > 0) {
                binInputCost = binCost * (estInput / estTotal);
                binOutputCost = binCost * (estOutput / estTotal);
                binCachedCost = binCost * (estCached / estTotal);
            } else {
                binInputCost = 0;
                binOutputCost = 0;
                binCachedCost = 0;
            }
        }

        totalCostVal += binCost;
        totalInputCostVal += binInputCost;
        totalOutputCostVal += binOutputCost;
        totalCachedCostVal += binCachedCost;

        totalInputTokensVal += bin.input || 0;
        totalOutputTokensVal += bin.output || 0;
        totalCachedTokensVal += bin.cached || 0;
        totalRequestsVal += bin.requests || 0;
    });

    const totalTokensVal = totalInputTokensVal + totalOutputTokensVal;

    const labelSummaryTotal = document.getElementById('labelSummaryTotal');
    const valSummaryTotal = document.getElementById('valSummaryTotal');
    const valSummaryInput = document.getElementById('valSummaryInput');
    const valSummaryOutput = document.getElementById('valSummaryOutput');
    const valSummaryCached = document.getElementById('valSummaryCached');

    const labelSummaryTotalRequests = document.getElementById('labelSummaryTotalRequests');
    const valSummaryTotalRequests = document.getElementById('valSummaryTotalRequests');
    const labelSummaryTotalTokens = document.getElementById('labelSummaryTotalTokens');
    const valSummaryTotalTokens = document.getElementById('valSummaryTotalTokens');
    const valSummaryInputTokens = document.getElementById('valSummaryInputTokens');
    const valSummaryOutputTokens = document.getElementById('valSummaryOutputTokens');
    const valSummaryCachedTokens = document.getElementById('valSummaryCachedTokens');

    const dict = i18n[state.currentLanguage] || {};

    if (labelSummaryTotal) {
        let labelKey = 'summaryTotalCostCustom';
        if (range === 'today') labelKey = 'summaryTotalCostToday';
        else if (range === '24h') labelKey = 'summaryTotalCost24h';
        else if (range === '3d') labelKey = 'summaryTotalCost3d';
        else if (range === '7d') labelKey = 'summaryTotalCost7d';
        else if (range === '30d') labelKey = 'summaryTotalCost30d';
        labelSummaryTotal.textContent = dict[labelKey] || '总成本:';
    }

    if (valSummaryTotal) valSummaryTotal.textContent = `$${totalCostVal.toFixed(4)}`;
    if (valSummaryInput) valSummaryInput.textContent = `$${totalInputCostVal.toFixed(4)}`;
    if (valSummaryOutput) valSummaryOutput.textContent = `$${totalOutputCostVal.toFixed(4)}`;
    if (valSummaryCached) valSummaryCached.textContent = `$${totalCachedCostVal.toFixed(4)}`;

    if (labelSummaryTotalRequests) {
        let labelKey = 'summaryTotalRequestsCustom';
        if (range === 'today') labelKey = 'summaryTotalRequestsToday';
        else if (range === '24h') labelKey = 'summaryTotalRequests24h';
        else if (range === '3d') labelKey = 'summaryTotalRequests3d';
        else if (range === '7d') labelKey = 'summaryTotalRequests7d';
        else if (range === '30d') labelKey = 'summaryTotalRequests30d';
        labelSummaryTotalRequests.textContent = dict[labelKey] || '总请求数:';
    }
    if (valSummaryTotalRequests) valSummaryTotalRequests.textContent = totalRequestsVal.toLocaleString();

    if (labelSummaryTotalTokens) {
        let labelKey = 'summaryTotalTokensCustom';
        if (range === 'today') labelKey = 'summaryTotalTokensToday';
        else if (range === '24h') labelKey = 'summaryTotalTokens24h';
        else if (range === '3d') labelKey = 'summaryTotalTokens3d';
        else if (range === '7d') labelKey = 'summaryTotalTokens7d';
        else if (range === '30d') labelKey = 'summaryTotalTokens30d';
        labelSummaryTotalTokens.textContent = dict[labelKey] || '总 Token:';
    }

    if (valSummaryTotalTokens) {
        valSummaryTotalTokens.textContent = formatTokenCount(totalTokensVal);
        valSummaryTotalTokens.title = totalTokensVal.toLocaleString();
    }
    if (valSummaryInputTokens) {
        valSummaryInputTokens.textContent = formatTokenCount(totalInputTokensVal);
        valSummaryInputTokens.title = totalInputTokensVal.toLocaleString();
    }
    if (valSummaryOutputTokens) {
        valSummaryOutputTokens.textContent = formatTokenCount(totalOutputTokensVal);
        valSummaryOutputTokens.title = totalOutputTokensVal.toLocaleString();
    }
    if (valSummaryCachedTokens) {
        valSummaryCachedTokens.textContent = formatTokenCount(totalCachedTokensVal);
        valSummaryCachedTokens.title = totalCachedTokensVal.toLocaleString();
    }

    // 缓存命中率汇总(口径: 缓存命中 Token / 输入总 Token, input 已包含 cached, 见 stats.go:239)。
    // 与「指标 3 缓存命中率」卡片互相独立: 卡片按池桶 cacheEligibleInputTokens 分母, 本汇总
    // 按「使用趋势」当前 trends 序列(综合/NVIDIA)与时间范围(24h/今日/3/7/30/筛选)实时汇总,
    // 切换时随 drawTrendChartSVG 自动刷新。
    const totalHitRateVal = totalInputTokensVal > 0 ? (totalCachedTokensVal / totalInputTokensVal * 100) : 0;
    const valSummaryHitRate = document.getElementById('valSummaryHitRate');
    if (valSummaryHitRate) valSummaryHitRate.textContent = totalHitRateVal.toFixed(1) + '%';

    const N = trends.length;
    const xMin = 0, xMax = 1000;
    const yMin = 20, yMax = 265;

    // Calculate maximum values
    let maxTokens = 1000;
    let maxCost = 0.01;
    let maxRequests = 10;
    trends.forEach(d => {
        const tokenMax = Math.max(d.input || 0, d.output || 0, d.cached || 0);
        if (tokenMax > maxTokens) maxTokens = tokenMax;
        if ((d.cost || 0) > maxCost) maxCost = d.cost;
        if ((d.requests || 0) > maxRequests) maxRequests = d.requests;
    });

    // Padding values
    maxTokens = Math.ceil(maxTokens * 1.15);
    maxCost = maxCost * 1.15;
    maxRequests = Math.ceil(maxRequests * 1.15);

    // Reset Axis Containers only if counts are incorrect to avoid complete DOM destruction
    if (gridLinesGroup.children.length !== 5) {
        gridLinesGroup.innerHTML = '';
    }
    if (leftAxis.children.length !== 5) {
        leftAxis.innerHTML = '';
    }
    if (rightAxis.children.length !== 5) {
        rightAxis.innerHTML = '';
    }

    const existingLines = gridLinesGroup.children;
    const existingLeftLabels = leftAxis.children;
    const existingRightLabels = rightAxis.children;

    // 1. Draw horizontal grid lines (SVG) & Y labels (HTML) with node reuse
    for (let i = 4; i >= 0; i--) {
        const ratio = i / 4;
        const y = yMax - ratio * (yMax - yMin);
        const idx = 4 - i;
        
        // 1a. Grid Line (Coordinate y is constant as yMin/yMax are constants, so we only append if missing)
        if (existingLines.length < 5) {
            const line = document.createElementNS('http://www.w3.org/2000/svg', 'line');
            line.setAttribute('x1', String(xMin));
            line.setAttribute('y1', y.toFixed(1));
            line.setAttribute('x2', String(xMax));
            line.setAttribute('y2', y.toFixed(1));
            line.setAttribute('stroke-width', '1');
            if (i > 0 && i < 4) {
                line.setAttribute('stroke-dasharray', '3,3');
            }
            gridLinesGroup.appendChild(line);
        }

        // 1b. Left HTML Token label
        const tokenVal = ratio * maxTokens;
        let leftLabel: HTMLElement;
        if (existingLeftLabels.length < 5) {
            leftLabel = document.createElement('div');
            leftLabel.className = 'absolute right-2 -translate-y-1/2 font-sans text-[10px] text-slate-400 dark:text-slate-500 whitespace-nowrap select-none';
            leftLabel.style.top = `${(y / 300) * 100}%`;
            leftAxis.appendChild(leftLabel);
        } else {
            leftLabel = existingLeftLabels[idx] as HTMLElement;
        }
        leftLabel.textContent = formatCompactNumber(tokenVal);

        // 1c. Right HTML Cost label
        const costVal = ratio * maxCost;
        let rightLabel: HTMLElement;
        if (existingRightLabels.length < 5) {
            rightLabel = document.createElement('div');
            rightLabel.className = 'absolute left-2 -translate-y-1/2 font-sans text-[10px] text-slate-400 dark:text-slate-500 whitespace-nowrap select-none';
            rightLabel.style.top = `${(y / 300) * 100}%`;
            rightAxis.appendChild(rightLabel);
        } else {
            rightLabel = existingRightLabels[idx] as HTMLElement;
        }
        rightLabel.textContent = costVal === 0 ? '$0' : `$${costVal.toFixed(costVal < 1 ? 4 : 2)}`;
    }

    // 2. Draw X Labels in HTML using absolute percentages with element reuse
    const granularity = trendAggregator.getTrendGranularity(range, state.customStartDate, state.customEndDate);
    let isSingleDay = range === 'today';
    if (granularity === 'hour' && trends.length > 0) {
        const firstDay = trends[0].time ? trends[0].time.split(' ')[0] : '';
        const lastDay = trends[trends.length - 1].time ? trends[trends.length - 1].time.split(' ')[0] : '';
        if (firstDay && firstDay === lastDay) {
            isSingleDay = true;
        }
    }

    const indices: number[] = [];
    if (N <= 7) {
        for (let i = 0; i < N; i++) indices.push(i);
    } else if (N <= 31) {
        // 日粒度 (如 30d 或 14d) 精选 6 个左右均匀分布的刻度标签
        indices.push(0);
        const steps = 5;
        for (let i = 1; i < steps; i++) {
            indices.push(Math.round((i / steps) * (N - 1)));
        }
        indices.push(N - 1);
    } else {
        indices.push(0);
        for (let i = 1; i < 6; i++) {
            indices.push(Math.round((i / 6) * (N - 1)));
        }
        indices.push(N - 1);
    }

    const existingXLabels = xAxis.children;
    const targetCount = indices.length;

    // Prune excess elements
    while (existingXLabels.length > targetCount) {
        xAxis.removeChild(xAxis.lastChild!);
    }

    indices.forEach((idx, i) => {
        const d = trends[idx];
        const percent = N > 1 ? (idx / (N - 1)) * 100 : 50;
        let label: HTMLElement;

        if (i < existingXLabels.length) {
            label = existingXLabels[i] as HTMLElement;
        } else {
            label = document.createElement('div');
            label.className = 'absolute text-[10px] text-slate-400 dark:text-slate-500 whitespace-nowrap font-sans select-none';
            xAxis.appendChild(label);
        }

        label.style.left = `${percent}%`;
        // 首尾刻度防溢出与居中微调
        if (percent <= 0) {
            label.style.transform = 'translateX(0%)';
        } else if (percent >= 100) {
            label.style.transform = 'translateX(-100%)';
        } else {
            label.style.transform = 'translateX(-50%)';
        }
        
        let textVal = '';
        if (granularity === 'day') {
            textVal = d.time ? d.time.split(' ')[0] : '';
        } else if (range === '24h') {
            if (idx === 0) {
                textVal = d.time || '';
            } else {
                const prevD = trends[idx - 1];
                const currentDay = d.time ? d.time.split(' ')[0] : '';
                const prevDay = prevD && prevD.time ? prevD.time.split(' ')[0] : '';
                if (currentDay && prevDay && currentDay !== prevDay) {
                    textVal = d.time || '';
                } else {
                    textVal = d.time ? (d.time.split(' ')[1] || d.time) : '';
                }
            }
        } else if (isSingleDay) {
            textVal = d.time ? (d.time.split(' ')[1] || d.time) : '';
        } else {
            textVal = d.time ? d.time.split(' ')[0] : '';
        }
        label.textContent = textVal;
    });

    // 3. Coordinate calculation helpers
    const getX = (idx: number) => xMin + (idx / Math.max(1, N - 1)) * (xMax - xMin);
    const getYToken = (val: number) => yMax - ((val || 0) / maxTokens) * (yMax - yMin);
    const getYCost = (val: number) => yMax - ((val || 0) / maxCost) * (yMax - yMin);
    const getYRequests = (val: number) => yMax - ((val || 0) / maxRequests) * (yMax - yMin);

    const costPoints = trends.map((d, idx) => ({ x: getX(idx), y: getYCost(d.cost) }));
    const inputPoints = trends.map((d, idx) => ({ x: getX(idx), y: getYToken(d.input) }));
    const outputPoints = trends.map((d, idx) => ({ x: getX(idx), y: getYToken(d.output) }));
    const cachedPoints = trends.map((d, idx) => ({ x: getX(idx), y: getYToken(d.cached) }));
    const requestsPoints = trends.map((d, idx) => ({ x: getX(idx), y: getYRequests(d.requests) }));

    // 4. Generate & apply smooth paths
    const costD = getBezierPath(costPoints);
    const inputD = getBezierPath(inputPoints);
    const outputD = getBezierPath(outputPoints);
    const cachedD = getBezierPath(cachedPoints);
    const requestsD = getBezierPath(requestsPoints);

    costPath.setAttribute('d', costD);
    inputPath.setAttribute('d', inputD);
    outputPath.setAttribute('d', outputD);
    cachedPath.setAttribute('d', cachedD);
    requestsPath.setAttribute('d', requestsD);

    // 5. Generate & apply areas
    if (N > 0) {
        const inputAreaD = inputD + ` L ${xMax},${yMax} L ${xMin},${yMax} Z`;
        inputArea.setAttribute('d', inputAreaD);

        const cachedAreaD = cachedD + ` L ${xMax},${yMax} L ${xMin},${yMax} Z`;
        cachedArea.setAttribute('d', cachedAreaD);
    }

    // 6. 左到右画线动画（类 ECharts line drawing）
    //    - 4 条实线：stroke-dashoffset 从路径总长过渡到 0，线条从左到右"画出"
    //    - Cost 虚线：不能改它的 stroke-dasharray="3,3"，改用 clipPath 从左到右擦出
    //    - 2 个面积块：线条画完后（1400ms）opacity 淡入
    //    （上游 getElementById 返回 HTMLElement|null，断言为 SVG 子类型以满足动画函数签名；
    //     L103 守门已保证走到此处的 path 非空。）
    //    animate=true  播左到右动画（进 app / 切范围 / 切回 dashboard view）；
    //    animate=false 静默复位为全显（轮询 stats-updated 增量刷新不动画，避免每次轮询都"重画抖动"）。
    const animOpts = {
        solidPaths: [requestsPath, cachedPath, inputPath, outputPath] as unknown as SVGPathElement[],
        clipPath: costPath as unknown as SVGPathElement,
        areas: [inputArea, cachedArea] as unknown as SVGPathElement[],
        clipViewWidth: xMax // viewBox 宽度 1000
    };
    if (animate) {
        animatePathsDrawIn(animOpts);
    } else {
        resetChartToStatic(animOpts);
    }

    // 6. Interactive Hover Tooltip & Points
    const hoverLine = document.getElementById('chartHoverLine');
    const hoverPointsGroup = document.getElementById('chartHoverPoints');
    const tooltip = document.getElementById('chartTooltip');

    const ptCost = document.getElementById('hoverPointCost');
    const ptRequests = document.getElementById('hoverPointRequests');
    const ptCached = document.getElementById('hoverPointCached');
    const ptInput = document.getElementById('hoverPointInput');
    const ptOutput = document.getElementById('hoverPointOutput');

    if (!hoverLine || !hoverPointsGroup || !tooltip || !ptCost || !ptRequests || !ptCached || !ptInput || !ptOutput) return;

    const showHover = (idx: number) => {
        if (idx < 0 || idx >= N) return;
        const d = trends[idx];
        const x = getX(idx);

        const yCost = getYCost(d.cost);
        const yRequests = getYRequests(d.requests);
        const yCached = getYToken(d.cached);
        const yInput = getYToken(d.input);
        const yOutput = getYToken(d.output);

        // Position vertical indicator line
        hoverLine.setAttribute('x1', x.toFixed(1));
        hoverLine.setAttribute('x2', x.toFixed(1));
        hoverLine.setAttribute('opacity', '1');

        // Position focus circles using CSS percentages
        const px = `${(x / 10).toFixed(2)}%`;
        ptCost.style.left = px; ptCost.style.top = `${(yCost / 3).toFixed(2)}%`;
        ptRequests.style.left = px; ptRequests.style.top = `${(yRequests / 3).toFixed(2)}%`;
        ptCached.style.left = px; ptCached.style.top = `${(yCached / 3).toFixed(2)}%`;
        ptInput.style.left = px; ptInput.style.top = `${(yInput / 3).toFixed(2)}%`;
        ptOutput.style.left = px; ptOutput.style.top = `${(yOutput / 3).toFixed(2)}%`;
        hoverPointsGroup.style.opacity = '1';

        // Update Tooltip contents
        const tDate = document.getElementById('tooltipDate');
        const tInput = document.getElementById('tooltipInput');
        const tOutput = document.getElementById('tooltipOutput');
        const tRequests = document.getElementById('tooltipRequests');
        const tCached = document.getElementById('tooltipCached');
        const tCost = document.getElementById('tooltipCost');

        if (tDate) {
            if (granularity === 'day') {
                const rawTime = d.time || '';
                const isZH = state.currentLanguage === 'zh';
                tDate.textContent = isZH ? `${rawTime} (当日汇总)` : `${rawTime} (Daily Total)`;
            } else {
                tDate.textContent = d.time || '';
            }
        }
        if (tInput) {
            tInput.textContent = formatTokenCount(d.input || 0);
            tInput.title = (d.input || 0).toLocaleString();
        }
        if (tOutput) {
            tOutput.textContent = formatTokenCount(d.output || 0);
            tOutput.title = (d.output || 0).toLocaleString();
        }
        if (tRequests) tRequests.textContent = (d.requests || 0).toLocaleString();
        if (tCached) {
            tCached.textContent = formatTokenCount(d.cached || 0);
            tCached.title = (d.cached || 0).toLocaleString();
        }
        if (tCost) tCost.textContent = `$${(d.cost || 0).toFixed(6)}`;
        if (tInput) {
            tInput.textContent = formatTokenCount(d.input || 0);
            tInput.title = (d.input || 0).toLocaleString();
        }
        if (tOutput) {
            tOutput.textContent = formatTokenCount(d.output || 0);
            tOutput.title = (d.output || 0).toLocaleString();
        }
        if (tRequests) tRequests.textContent = (d.requests || 0).toLocaleString();
        if (tCached) {
            tCached.textContent = formatTokenCount(d.cached || 0);
            tCached.title = (d.cached || 0).toLocaleString();
        }
        if (tCost) tCost.textContent = `$${(d.cost || 0).toFixed(6)}`;

        // Coordinate positioning for Tooltip
        const containerWidth = sensorRect.getBoundingClientRect().width;
        const scale = containerWidth / 1000;
        const tooltipX = x * scale;

        tooltip.style.opacity = '1';
        if (tooltipX > containerWidth * 0.7) {
            tooltip.style.left = `${tooltipX - 180 + 48}px`; // Compensate left HTML axis offset w-12 (48px)
        } else {
            tooltip.style.left = `${tooltipX + 15 + 48}px`;
        }
        tooltip.style.top = `15px`;
    };

    const hideHover = () => {
        hoverLine.setAttribute('opacity', '0');
        hoverPointsGroup.style.opacity = '0';
        tooltip.style.opacity = '0';
        tooltip.style.left = '-1000px';
    };

    sensorRect.onmousemove = (e: MouseEvent) => {
        const rect = sensorRect.getBoundingClientRect();
        const mouseX = e.clientX - rect.left;
        const width = rect.width;
        const ratio = mouseX / width;
        const idx = Math.min(N - 1, Math.max(0, Math.round(ratio * (N - 1))));
        showHover(idx);
    };

    sensorRect.onmouseleave = () => {
        hideHover();
    };
}

// Helper: 趋势数据过滤与自适应聚合 (支持小时与日级多粒度)
export function getFilteredTrends(trends: any[], range: string): any[] {
    return trendAggregator.getFilteredTrends(
        trends,
        range,
        state.customStartDate,
        state.customEndDate
    );
}

// Init chart Range selectors and Custom Filter Modal
export function initChartFilters() {
    const chartRangeSelector = document.getElementById('chartRangeSelector');
    const chartFilterPanel = document.getElementById('chartFilterPanel');
    const btnCancelFilter = document.getElementById('btnCancelFilter');
    const btnApplyFilter = document.getElementById('btnApplyFilter');
    
    const filterStartDate = document.getElementById('filterStartDate') as HTMLInputElement | null;
    const filterStartTime = document.getElementById('filterStartTime') as HTMLInputElement | null;
    const filterEndDate = document.getElementById('filterEndDate') as HTMLInputElement | null;
    const filterEndTime = document.getElementById('filterEndTime') as HTMLInputElement | null;
    
    if (!chartRangeSelector || !chartFilterPanel || !btnCancelFilter || !btnApplyFilter || !filterStartDate || !filterStartTime || !filterEndDate || !filterEndTime) return;

    // Default dates
    const now = new Date();
    const sevenDaysAgo = new Date(now.getTime() - 7 * 24 * 3600 * 1000);
    
    filterEndDate.value = now.toISOString().split('T')[0];
    filterEndTime.value = `${String(now.getHours()).padStart(2, '0')}:${String(now.getMinutes()).padStart(2, '0')}`;
    filterStartDate.value = sevenDaysAgo.toISOString().split('T')[0];
    filterStartTime.value = '00:00';
    
    const buttons = chartRangeSelector.querySelectorAll('button[data-range]');
    
    buttons.forEach(btn => {
        btn.addEventListener('click', (e: any) => {
            const range = btn.getAttribute('data-range');
            if (!range) return;
            
            if (range === 'filter') {
                chartFilterPanel.classList.toggle('hidden');
                return;
            }
            
            chartFilterPanel.classList.add('hidden');
            state.currentRange = range;
            
            buttons.forEach((b: any) => {
                if (b.getAttribute('data-range') === 'filter') {
                    b.className = 'px-2.5 py-0.5 text-[10px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md transition-all font-medium flex items-center gap-0.5';
                    return;
                }
                
                if (b === btn) {
                    b.className = 'px-2.5 py-0.5 text-[10px] bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim rounded-md shadow-sm font-semibold';
                } else {
                    b.className = 'px-2.5 py-0.5 text-[10px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md transition-all font-medium';
                }
            });
            
            // 按 currentTrendScope 取数据源: 切换时间范围时也要尊重当前 scope,
            // 否则 NVIDIA Tab 下换 7d/30d 会错误地画回综合全局桶数据。
            const src = state.currentTrendScope === 'nvidia' ? state.nvidiaTrendsData : state.trendsData;
            const filtered = getFilteredTrends(src, state.currentRange);
            drawTrendChartSVG(filtered, state.currentRange);
        });
    });
    
    btnCancelFilter.addEventListener('click', () => {
        chartFilterPanel.classList.add('hidden');
    });
    
    btnApplyFilter.addEventListener('click', () => {
        const startD = filterStartDate.value;
        const startT = filterStartTime.value || '00:00';
        const endD = filterEndDate.value;
        const endT = filterEndTime.value || '23:59';
        
        const isZH = state.currentLanguage === 'zh';
        if (!startD || !endD) {
            alert(isZH ? '请选择完整的开始与结束日期' : 'Please select both start and end dates');
            return;
        }
        
        state.customStartDate = new Date(`${startD}T${startT}`).getTime();
        state.customEndDate = new Date(`${endD}T${endT}`).getTime();
        
        if (state.customStartDate > state.customEndDate) {
            alert(isZH ? '开始时间不能晚于结束时间' : 'Start time cannot be later than end time');
            return;
        }
        
        state.currentRange = 'custom';
        
        buttons.forEach((b: any) => {
            if (b.getAttribute('data-range') === 'filter') {
                b.className = 'px-2.5 py-0.5 text-[10px] bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim rounded-md shadow-sm font-semibold flex items-center gap-0.5';
            } else {
                b.className = 'px-2.5 py-0.5 text-[10px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md transition-all font-medium';
            }
        });
        
        chartFilterPanel.classList.add('hidden');

        // 自定义时间区间同样需尊重当前 scope (与范围按钮同一回归点)。
        const srcForFilter = state.currentTrendScope === 'nvidia' ? state.nvidiaTrendsData : state.trendsData;
        const filtered = getFilteredTrends(srcForFilter, state.currentRange);
        drawTrendChartSVG(filtered, state.currentRange);
    });

    // ---- 趋势数据维度切换 (综合趋势 / NVIDIA) ----
    // trendScopeSelector 与时间范围选择器平级, 切换 currentTrendScope 后立即强制动画重画,
    // 既走 maybeDrawTrendChart 的 sig 感知路径外的"首切强制动画"兜底, 保证用户每次切换都看到动画。
    const trendScopeSelector = document.getElementById('trendScopeSelector');
    if (trendScopeSelector) {
        const scopeButtons = trendScopeSelector.querySelectorAll('button[data-trend-scope]');
        // 初始高亮与 state.currentTrendScope 对齐 (防止 DOM 初始 class 与 state 不同步)。
        scopeButtons.forEach((b: any) => {
            const s = b.getAttribute('data-trend-scope');
            if (s === state.currentTrendScope) {
                b.className = 'px-2.5 py-0.5 text-[10px] bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim rounded-md shadow-sm font-semibold';
            } else {
                b.className = 'px-2.5 py-0.5 text-[10px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md transition-all font-medium';
            }
        });
        scopeButtons.forEach((b: any) => {
            b.addEventListener('click', () => {
                const s = b.getAttribute('data-trend-scope');
                if (!s) return;
                state.currentTrendScope = s as 'all' | 'nvidia';
                scopeButtons.forEach((o: any) => {
                    if (o === b) {
                        o.className = 'px-2.5 py-0.5 text-[10px] bg-white dark:bg-[#1a1f30] text-primary dark:text-primary-fixed-dim rounded-md shadow-sm font-semibold';
                    } else {
                        o.className = 'px-2.5 py-0.5 text-[10px] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 rounded-md transition-all font-medium';
                    }
                });
                // scope 切换时强制带动画重画该 scope 的当前序列。
                // 此处内联重画而非回调 dashboard.ts 的函数, 避免 chartRenderer→dashboard 反向依赖形成循环。
                const scopeSrc = state.currentTrendScope === 'nvidia' ? state.nvidiaTrendsData : state.trendsData;
                if (!scopeSrc || scopeSrc.length === 0) {
                    clearTrendChart();
                } else {
                    const scopeFiltered = getFilteredTrends(scopeSrc, state.currentRange);
                    drawTrendChartSVG(scopeFiltered, state.currentRange, true);
                }
            });
        });
    }
}

// 趋势图动画系统已抽离至 chartAnimation.ts
export const animatePathsDrawIn = chartAnimation.animatePathsDrawIn;
export const resetChartToStatic = chartAnimation.resetChartToStatic;


import state from './dashboardState';
import i18n from '../shared/i18n';

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

    if (valSummaryTotalTokens) valSummaryTotalTokens.textContent = totalTokensVal.toLocaleString();
    if (valSummaryInputTokens) valSummaryInputTokens.textContent = totalInputTokensVal.toLocaleString();
    if (valSummaryOutputTokens) valSummaryOutputTokens.textContent = totalOutputTokensVal.toLocaleString();
    if (valSummaryCachedTokens) valSummaryCachedTokens.textContent = totalCachedTokensVal.toLocaleString();

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
    let isSingleDay = range === 'today';
    if (range === 'custom' && trends.length > 0) {
        const firstDay = trends[0].time.split(' ')[0];
        const lastDay = trends[trends.length - 1].time.split(' ')[0];
        if (firstDay === lastDay) {
            isSingleDay = true;
        }
    }

    const indices: number[] = [];
    if (N <= 7) {
        for (let i = 0; i < N; i++) indices.push(i);
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
            label.className = 'absolute -translate-x-1/2 text-[10px] text-slate-400 dark:text-slate-500 whitespace-nowrap font-sans';
            xAxis.appendChild(label);
        }

        label.style.left = `${percent}%`;
        
        let textVal = '';
        if (range === '24h') {
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

        if (tDate) tDate.textContent = d.time || '';
        if (tInput) tInput.textContent = (d.input || 0).toLocaleString();
        if (tOutput) tOutput.textContent = (d.output || 0).toLocaleString();
        if (tRequests) tRequests.textContent = (d.requests || 0).toLocaleString();
        if (tCached) tCached.textContent = (d.cached || 0).toLocaleString();
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

// Helper: parse time string
export function parseTrendsTime(timeStr: string): Date {
    if (!timeStr) return new Date();
    const currentYear = new Date().getFullYear();
    const parts = timeStr.split(' ');
    if (parts.length < 2) return new Date();
    const dateParts = parts[0].split('/');
    const timeParts = parts[1].split(':');
    return new Date(
        currentYear,
        parseInt(dateParts[0]) - 1,
        parseInt(dateParts[1]),
        parseInt(timeParts[0]),
        parseInt(timeParts[1] || '0')
    );
}

// Helper: format Date to "MM/DD HH:00"
export function formatTrendsTime(date: Date): string {
    const m = String(date.getMonth() + 1).padStart(2, '0');
    const d = String(date.getDate()).padStart(2, '0');
    const hh = String(date.getHours()).padStart(2, '0');
    return `${m}/${d} ${hh}:00`;
}

// Helper: generate hourly slots
function generateHourlySlots(hoursCount: number): string[] {
    const slots: string[] = [];
    const now = new Date();
    const nowMs = new Date(now.getFullYear(), now.getMonth(), now.getDate(), now.getHours(), 0, 0, 0).getTime();
    for (let i = hoursCount - 1; i >= 0; i--) {
        const t = new Date(nowMs - i * 3600 * 1000);
        slots.push(formatTrendsTime(t));
    }
    return slots;
}

// Helper: generate today slots
function generateTodaySlots(): string[] {
    const slots: string[] = [];
    const now = new Date();
    const currentHour = now.getHours();
    for (let h = 0; h <= currentHour; h++) {
        const t = new Date(now.getFullYear(), now.getMonth(), now.getDate(), h, 0, 0, 0);
        slots.push(formatTrendsTime(t));
    }
    return slots;
}

// Helper: generate custom slots
function generateCustomSlots(startObj: Date, endObj: Date): string[] {
    const slots: string[] = [];
    const startMs = new Date(startObj.getFullYear(), startObj.getMonth(), startObj.getDate(), startObj.getHours(), 0, 0, 0).getTime();
    const endMs = new Date(endObj.getFullYear(), endObj.getMonth(), endObj.getDate(), endObj.getHours(), 0, 0, 0).getTime();
    
    const hoursDiff = Math.min(720, Math.ceil((endMs - startMs) / (3600 * 1000)));
    for (let i = 0; i <= hoursDiff; i++) {
        const t = new Date(startMs + i * 3600 * 1000);
        slots.push(formatTrendsTime(t));
    }
    return slots;
}

// Helper: get filtered trends with slot auto-completion
export function getFilteredTrends(trends: any[], range: string): any[] {
    if (!trends) trends = [];
    
    let slots: string[] = [];
    if (range === 'today') {
        slots = generateTodaySlots();
    } else if (range === '24h') {
        slots = generateHourlySlots(24);
    } else if (range === '3d') {
        slots = generateHourlySlots(72);
    } else if (range === '7d') {
        slots = generateHourlySlots(168);
    } else if (range === '30d') {
        slots = generateHourlySlots(720);
    } else if (range === 'custom') {
        if (!state.customStartDate || !state.customEndDate) {
            slots = generateHourlySlots(168); // Fallback 7d
        } else {
            slots = generateCustomSlots(new Date(state.customStartDate), new Date(state.customEndDate));
        }
    } else {
        slots = generateHourlySlots(168); // Fallback 7d
    }
    
    // Index trends by their time key once (O(n)) so each slot lookup is O(1).
    // The previous slots.map(slot => trends.find(...)) was O(slots * trends),
    // i.e. up to 720 * 720 comparisons on the 30d range, every chart redraw.
    const trendsByTime = new Map<string, any>();
    for (const item of trends) {
        trendsByTime.set(item.time, item);
    }

    const result = slots.map(slot => {
        const found = trendsByTime.get(slot);
        if (found) {
            return found;
        } else {
            return {
                time: slot,
                input: 0,
                output: 0,
                cached: 0,
                requests: 0,
                cost: 0
            };
        }
    });

    return result;
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
            
            const filtered = getFilteredTrends(state.trendsData, state.currentRange);
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
        
        const filtered = getFilteredTrends(state.trendsData, state.currentRange);
        drawTrendChartSVG(filtered, state.currentRange);
    });
}

// ============================================================================
// 趋势图"左到右画线"动画（类 ECharts line drawing），纯 SVG/CSS，零依赖。
// 原理：SVG path.getTotalLength() 得路径总长 L；设 stroke-dasharray=L、
//   stroke-dashoffset=L（整条线被 dash 藏到看不见）→ 强制 reflow →
//   stroke-dashoffset=0，可见段从起点平移到全显，视觉即从左到右"画出"。
// Cost 虚线本身 break 成 3,3，不能动它的 dasharray，改走 clipPath：
//   在 trendSvg 动态挂一个 clipPath 包一个 rect，rect 宽度从 0 过渡到
//   viewBox 宽度，把虚线从左到右"擦出来"；动画结束卸载 clip-path 还原虚线。
// 面积块：动画启动置 opacity=0，线条画完（800ms）后加 area-shown 类淡入。
// ============================================================================

const CHART_DRAW_DURATION = 1400; // ms，与 dashboard.css 中 .chart-line-draw transition 一致（放慢，类 ECharts 舒展感）
const CHART_AREA_FADE_DURATION = 420; // ms，与 .chart-area-anim transition 一致

// 模块级持久 timer id：每次重画前清掉上一次未触发的面积淡入回调，避免快速切范围时
// 旧 timer 把已经清零的面积又拉亮，造成闪烁残留。
let chartAreaFadeTimer: ReturnType<typeof setTimeout> | null = null;
let chartCostClipCleanupTimer: ReturnType<typeof setTimeout> | null = null;

interface DrawInConfig {
    solidPaths: SVGPathElement[];   // 实线（走 dashoffset 方案）
    clipPath: SVGPathElement;       // Cost 虚线（走 clipPath 方案）
    areas: SVGPathElement[];        // 渐变面积块（画完后淡入）
    clipViewWidth: number;          // clipRect 目标宽度（viewBox 宽，1000）
}

export function animatePathsDrawIn(cfg: DrawInConfig) {
    // 1. 复位/清理上轮动画
    if (chartAreaFadeTimer !== null) {
        clearTimeout(chartAreaFadeTimer);
        chartAreaFadeTimer = null;
    }
    if (chartCostClipCleanupTimer !== null) {
        clearTimeout(chartCostClipCleanupTimer);
        chartCostClipCleanupTimer = null;
    }

    // 2. 实线：设 dasharray/dashoffset 初始隐藏，强制 reflow 后归零触发过渡。
    //    复用路径下 .chart-line-draw 的 transition 始终挂着，直接写 strokeDashoffset=len
    //    会触发 0→len 反向过渡（线先缩成隐藏），再写 0 又过渡回来，整条线先消失再画出 =
    //    闪屏。故：设初始隐藏值前临时关 transition，reflow 提交“静止隐藏”状态后再开
    //    transition 并设归零目标，保证只有这一次 len→0 的正向“画出”过渡。
    const solidInit: { path: SVGPathElement; len: number }[] = [];
    for (const path of cfg.solidPaths) {
        let len = 0;
        try {
            len = path.getTotalLength();
        } catch {
            len = 0;
        }
        if (len <= 0) {
            // 空路径或异常：直接显示，跳过动画（不破坏渲染）
            path.style.removeProperty('stroke-dasharray');
            path.style.removeProperty('stroke-dashoffset');
            continue;
        }
        const prevTransition = path.style.transition;
        path.style.transition = 'none';
        path.style.strokeDasharray = String(len);
        path.style.strokeDashoffset = String(len);
        void path.getBBox(); // 强制提交“静止隐藏”状态
        path.style.transition = prevTransition;
        solidInit.push({ path, len });
    }
    // 强制同步布局回流：读取几何属性迫使浏览器提交上面两步样式，否则
    // 后面的归零会被合并、过渡跳过、动画不出现。
    for (const { path, len } of solidInit) {
        if (len > 0) void path.getBBox();
    }
    for (const { path, len } of solidInit) {
        if (len > 0) path.style.strokeDashoffset = '0';
    }

    // 3. Cost 虚线：clipPath 从左到右擦出
    setupCostClip(cfg.clipPath, cfg.clipViewWidth);

    // 4. 面积块：先归无（CSS .chart-area-anim 已 opacity:0），画完后淡入。
    //    复用路径下 .chart-area-anim 的 opacity transition 始终挂着，直接 remove('area-shown')
    //    会触发 1→0 过渡（面积先淡出），800ms 后再 add 又过渡回来，视觉为“淡出再淡入”。
    //    故复位时临时关 transition，瞬时归零，确保只有结尾一次 0→1 的正向淡入。
    for (const area of cfg.areas) {
        const prevTransition = area.style.transition;
        area.style.transition = 'none';
        area.classList.remove('area-shown');
        void area.getBBox(); // 强制提交“静止隐藏”
        area.style.transition = prevTransition;
    }
    chartAreaFadeTimer = setTimeout(() => {
        for (const area of cfg.areas) {
            area.classList.add('area-shown');
        }
        chartAreaFadeTimer = null;
    }, CHART_DRAW_DURATION);

    // 5. 动画结束后清理实线 dash 内联样式（避免长期占用 stroke-dashoffset 导致
    //    后续 hover/重画时样式残留；虚线 clipPath 也在此时卸载还原 3,3 虚线）
    const cleanupTotal = Math.max(CHART_DRAW_DURATION, CHART_DRAW_DURATION + 50);
    chartCostClipCleanupTimer = setTimeout(() => {
        for (const { path, len } of solidInit) {
            if (len > 0) {
                path.style.removeProperty('stroke-dasharray');
                path.style.removeProperty('stroke-dashoffset');
            }
        }
        teardownCostClip(cfg.clipPath);
        chartCostClipCleanupTimer = null;
    }, cleanupTotal);
}

// 静默复位：轮询 stats-updated 增量刷新时（animate=false）不播左到右动画，
// 但必须把上次动画可能残留的"隐藏/dashoffset/clip/未淡入"状态瞬时清为全显，
// 否则会留半截隐藏线或一直不可见的面积。复位采用"临时关 transition → 改属性 →
// reflow 提交 → 开回 transition"手法，避免复位动作本身触发过渡抖动。
function resetChartToStatic(cfg: DrawInConfig) {
    // 取消可能正在排队的面积淡入回调，避免它把刚复位的面积又改一次
    if (chartAreaFadeTimer !== null) {
        clearTimeout(chartAreaFadeTimer);
        chartAreaFadeTimer = null;
    }
    if (chartCostClipCleanupTimer !== null) {
        clearTimeout(chartCostClipCleanupTimer);
        chartCostClipCleanupTimer = null;
    }

    // 4 条实线：清掉 dasharray/dashoffset 内联样式（回到 path 默认全显）
    for (const path of cfg.solidPaths) {
        const prevTransition = path.style.transition;
        path.style.transition = 'none';
        path.style.removeProperty('stroke-dasharray');
        path.style.removeProperty('stroke-dashoffset');
        void path.getBBox(); // 强制提交“静止全显”，避免开回 transition 时反向过渡
        path.style.transition = prevTransition;
    }

    // Cost 虚线：移除 clip-path，还原其 3,3 虚线
    teardownCostClip(cfg.clipPath);

    // 2 个面积块：直接置 opacity=1（加 area-shown），瞬时无过渡
    for (const area of cfg.areas) {
        const prevTransition = area.style.transition;
        area.style.transition = 'none';
        area.classList.add('area-shown');
        void area.getBBox();
        area.style.transition = prevTransition;
    }
}

// Cost 虚线 clipPath 动画：在 trendSvg 内动态创建/复用 clipPath + rect，
// rect 宽度从 0 过渡到 clipViewWidth。
function setupCostClip(costPath: SVGPathElement, clipViewWidth: number) {
    const svg = document.getElementById('trendSvg') as SVGSVGElement | null;
    if (!svg) return;

    const CLIP_ID = 'chartCostClipRuntime';
    let clip = svg.querySelector(`#${CLIP_ID}`) as SVGClipPathElement | null;
    let rect: SVGRectElement | null;

    if (!clip) {
        // 首次：创建 clipPath + rect，挂到 svg 顶部（无须 <defs>，SVG 规范允许
        // clipPath 定义在 svg 任意子树位置）。
        clip = document.createElementNS('http://www.w3.org/2000/svg', 'clipPath') as SVGClipPathElement;
        clip.setAttribute('id', CLIP_ID);
        rect = document.createElementNS('http://www.w3.org/2000/svg', 'rect') as SVGRectElement;
        rect.setAttribute('x', '0');
        rect.setAttribute('y', '0');
        rect.setAttribute('height', '300');
        rect.setAttribute('width', '0');
        // rect width 过渡：CSS 控制不到 SVG attribute 在所有引擎下的解析，
        // 这里直接对 rect 的 width 属性用 transition（现代浏览器支持 SVG
        // presentation attribute 的 CSS 过渡；为兼容性同时用 style.width 兜底无效，
        // 故只设 attribute + CSS transition）。
        rect.style.transition = `width ${CHART_DRAW_DURATION}ms cubic-bezier(0.25,0.46,0.45,0.94)`;
        clip.appendChild(rect);
        svg.appendChild(clip);
    } else {
        rect = clip.querySelector('rect');
        if (!rect) {
            rect = document.createElementNS('http://www.w3.org/2000/svg', 'rect') as SVGRectElement;
            rect.setAttribute('x', '0');
            rect.setAttribute('y', '0');
            rect.setAttribute('height', '300');
            rect.setAttribute('width', '0');
            rect.style.transition = `width ${CHART_DRAW_DURATION}ms cubic-bezier(0.25,0.46,0.45,0.94)`;
            clip.appendChild(rect);
        }
    }

    // 复位 width=0 → 强制 reflow → 设目标宽度触发过渡。
    // 注意：rect 上的 transition 在复用路径下会一直挂着。直接 setAttribute('width','0')
    // 会触发一次 1000→0 的反向过渡，紧接着设目标宽度时浏览器因中间 reflow 合并两次
    // 赋值，动画丢失。故复位前先关掉 transition，复位并 reflow 后再挂上 transition，
    // 最后设目标宽度，确保只有这一次 0→1000 的正向过渡。
    const prevTransition = rect!.style.transition;
    rect!.style.transition = 'none';
    rect!.setAttribute('width', '0');
    void (rect as any).getBBox();                                  // 强制提交复位
    rect!.style.transition = prevTransition || `width ${CHART_DRAW_DURATION}ms cubic-bezier(0.25,0.46,0.45,0.94)`;
    costPath.setAttribute('clip-path', `url(#${CLIP_ID})`);
    void (rect as any).getBBox();                                  // 强制提交"无过渡"状态，下一帧才设目标
    rect!.setAttribute('width', String(clipViewWidth));
}

// 动画结束后卸载 clip-path，还原 Cost 虚线本身的 3,3 效果（clipPath 节点留作下次复用）。
function teardownCostClip(costPath: SVGPathElement) {
    costPath.removeAttribute('clip-path');
}


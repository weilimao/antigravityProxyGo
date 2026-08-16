/**
 * chartAnimation.ts: 趋势图"从左到右画线"与面积淡入动画系统 (纯 SVG/CSS, 零外部依赖)
 *
 * 原理：
 * 1. 实线：SVG path.getTotalLength() 获取路径总长 L；设置 stroke-dasharray=L、
 *    stroke-dashoffset=L（整条线被 dash 隐藏）→ 强制 reflow → stroke-dashoffset=0，
 *    可见段从起点平移到全显，视觉即从左到右"画出"。
 * 2. Cost 虚线：不能改动其 stroke-dasharray="3,3"，改用 clipPath 从左到右擦出。
 * 3. 渐变面积块：动画启动时 opacity=0，线条画完后加 area-shown 类平滑淡入。
 */

export const CHART_DRAW_DURATION = 1400; // ms，与 dashboard.css 中 .chart-line-draw transition 一致
export const CHART_AREA_FADE_DURATION = 420; // ms，与 .chart-area-anim transition 一致

// 模块级持久 timer id：每次重画前清掉上一次未触发的回调，避免快速切换时闪烁残留
let chartAreaFadeTimer: ReturnType<typeof setTimeout> | null = null;
let chartCostClipCleanupTimer: ReturnType<typeof setTimeout> | null = null;

export interface DrawInConfig {
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

    // 2. 实线：设 dasharray/dashoffset 初始隐藏，强制 reflow 后归零触发过渡
    const solidInit: { path: SVGPathElement; len: number }[] = [];
    for (const path of cfg.solidPaths) {
        let len = 0;
        try {
            len = path.getTotalLength();
        } catch {
            len = 0;
        }
        if (len <= 0) {
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

    // 强制同步布局回流
    for (const { path, len } of solidInit) {
        if (len > 0) void path.getBBox();
    }
    for (const { path, len } of solidInit) {
        if (len > 0) path.style.strokeDashoffset = '0';
    }

    // 3. Cost 虚线：clipPath 从左到右擦出
    setupCostClip(cfg.clipPath, cfg.clipViewWidth);

    // 4. 面积块：先归无，画完后淡入
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

    // 5. 动画结束后清理实线 dash 内联样式
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

/**
 * 静默复位：轮询刷新时复位为静态全显
 */
export function resetChartToStatic(cfg: DrawInConfig) {
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
        void path.getBBox();
        path.style.transition = prevTransition;
    }

    // Cost 虚线：移除 clip-path，还原其 3,3 虚线
    teardownCostClip(cfg.clipPath);

    // 2 个面积块：直接置 opacity=1
    for (const area of cfg.areas) {
        const prevTransition = area.style.transition;
        area.style.transition = 'none';
        area.classList.add('area-shown');
        void area.getBBox();
        area.style.transition = prevTransition;
    }
}

/**
 * Cost 虚线 clipPath 动画：在 trendSvg 内动态创建/复用 clipPath + rect
 */
function setupCostClip(costPath: SVGPathElement, clipViewWidth: number) {
    const svg = document.getElementById('trendSvg') as SVGSVGElement | null;
    if (!svg) return;

    const CLIP_ID = 'chartCostClipRuntime';
    let clip = svg.querySelector(`#${CLIP_ID}`) as SVGClipPathElement | null;
    let rect: SVGRectElement | null;

    if (!clip) {
        clip = document.createElementNS('http://www.w3.org/2000/svg', 'clipPath') as SVGClipPathElement;
        clip.setAttribute('id', CLIP_ID);
        rect = document.createElementNS('http://www.w3.org/2000/svg', 'rect') as SVGRectElement;
        rect.setAttribute('x', '0');
        rect.setAttribute('y', '0');
        rect.setAttribute('height', '300');
        rect.setAttribute('width', '0');
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

    const prevTransition = rect!.style.transition;
    rect!.style.transition = 'none';
    rect!.setAttribute('width', '0');
    void (rect as any).getBBox();
    rect!.style.transition = prevTransition || `width ${CHART_DRAW_DURATION}ms cubic-bezier(0.25,0.46,0.45,0.94)`;
    costPath.setAttribute('clip-path', `url(#${CLIP_ID})`);
    void (rect as any).getBBox();
    rect!.setAttribute('width', String(clipViewWidth));
}

/**
 * 动画结束后卸载 clip-path
 */
function teardownCostClip(costPath: SVGPathElement) {
    costPath.removeAttribute('clip-path');
}

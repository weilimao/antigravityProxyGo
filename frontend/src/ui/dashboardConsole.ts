/**
 * dashboardConsole.ts: 系统控制台浮窗 + 日志攒批(自包含 console/drag/resize 20+ let + ring buffer consts)。
 *
 * 从 dashboard.ts 抽离(7 非连续段):console* let + MAX_CONSOLE_ENTRIES/consolePool/consolePoolIdx consts +
 * drag/resize 12 let + applyConsoleClasses(私有) + initDashboardEvents 体内 console 取值/事件/导出/logs:batch 编排段。
 * 全部 console/drag/resize let 仅在 initConsoleEvents 内再赋值(取值段 + 事件块 + logs:batch),自包含。
 * hub initDashboardEvents 改调 initConsoleEvents();行为等价于原 init 体内 console 编排(幂等重取 DOM + 重绑监听)。
 */
import { ipcRenderer } from '../shared/ipc';
import state from './dashboardState';
import { saveText } from '../shared/fileService';

// Console Log Panel
let consoleHeader: HTMLElement | null;
let systemConsole: HTMLElement | null;
let consoleBody: HTMLElement | null;
let isConsoleScrollScheduled = false;
// Console log ring buffer: a fixed pool of DOM nodes is reused instead of
// creating/removing a <div> per log line. Under heavy concurrent traffic the
// backend can flush hundreds of log lines per 1.5s tick; the old create+prune
// loop caused WebView memory pools to inflate without ever being returned to
// the OS. Reusing nodes caps the work at O(MAX_CONSOLE_ENTRIES) per tick.
const MAX_CONSOLE_ENTRIES = 80;
const consolePool: HTMLDivElement[] = [];
let consolePoolIdx = 0;
let consoleFloatBtn: HTMLElement | null = null;
let consoleToggleBtn: HTMLElement | null = null;
let consoleResizeHandle: HTMLElement | null = null;

let isConsoleDragging = false;
let consoleDragStartX = 0;
let consoleDragStartY = 0;
let consoleDragInitialLeft = 0;
let consoleDragInitialTop = 0;

let isConsoleResizing = false;
let consoleResizeStartX = 0;
let consoleResizeStartY = 0;
let consoleResizeInitialWidth = 0;
let consoleResizeInitialHeight = 0;

let isMouseOverConsole = false;

let btnExportLogs: HTMLButtonElement | null;

// Apply emoji-based severity classes to a console entry (reset each reuse).
function applyConsoleClasses(entry: HTMLElement, log: string) {
    if (log.includes('⚠️')) entry.classList.add('warn');
    if (log.includes('❌')) entry.classList.add('error');
    if (log.includes('✅') || log.includes('🚀')) entry.classList.add('info');
}

export function initConsoleEvents() {
consoleHeader = document.getElementById('consoleHeader');
systemConsole = document.getElementById('systemConsole');
consoleBody = document.getElementById('consoleBody');
consoleFloatBtn = document.getElementById('consoleFloatBtn');
consoleToggleBtn = document.getElementById('consoleToggleBtn');
consoleResizeHandle = document.getElementById('consoleResizeHandle');

btnExportLogs = document.getElementById('btnExportLogs') as HTMLButtonElement | null;

if (consoleHeader && systemConsole) {
    // Track mouse hover state
    systemConsole.addEventListener('mouseenter', () => {
        isMouseOverConsole = true;
    });
    systemConsole.addEventListener('mouseleave', () => {
        isMouseOverConsole = false;
    });

    // Toggle console log drawer (only when NOT floating)
    const toggleConsole = () => {
        if (systemConsole!.classList.contains('floating')) return;
        const isExpanded = systemConsole!.classList.contains('expanded');
        if (isExpanded) {
            systemConsole!.classList.remove('expanded');
            systemConsole!.style.height = '36px';
            if (consoleBody) consoleBody.style.display = 'none';
            if (consoleToggleBtn) consoleToggleBtn.textContent = 'keyboard_double_arrow_up';
        } else {
            systemConsole!.classList.add('expanded');
            systemConsole!.style.height = '30vh';
            if (consoleBody) consoleBody.style.display = 'block';
            if (consoleToggleBtn) consoleToggleBtn.textContent = 'keyboard_double_arrow_down';
            if (consoleBody) consoleBody.scrollTop = consoleBody.scrollHeight;
        }
    };

    consoleHeader.addEventListener('click', (e: MouseEvent) => {
        // Ignore click events on action buttons to prevent drawer folding/unfolding
        if ((e.target as HTMLElement).closest('#consoleFloatBtn') || (e.target as HTMLElement).closest('#consoleToggleBtn')) {
            return;
        }
        toggleConsole();
    });

    if (consoleToggleBtn) {
        consoleToggleBtn.addEventListener('click', (e: MouseEvent) => {
            e.stopPropagation();
            toggleConsole();
        });
    }

    // Handle Float/Dock mode switching
    if (consoleFloatBtn) {
        consoleFloatBtn.addEventListener('click', (e: MouseEvent) => {
            e.stopPropagation();
            const isFloating = systemConsole!.classList.contains('floating');
            if (!isFloating) {
                // Switch to FLOAT mode
                systemConsole!.classList.add('floating');
                systemConsole!.classList.remove('expanded');

                // Show resize handle, hide toggle drawer button
                if (consoleResizeHandle) consoleResizeHandle.classList.remove('hidden');
                if (consoleToggleBtn) consoleToggleBtn.style.display = 'none';

                // Recover dimensions/position from localStorage, fallback to responsive viewport-based sizes
                const defaultWidth = Math.min(900, Math.max(400, window.innerWidth * 0.7));
                const defaultHeight = Math.min(600, Math.max(300, window.innerHeight * 0.6));
                const savedWidth = localStorage.getItem('console_float_width') || String(defaultWidth);
                const savedHeight = localStorage.getItem('console_float_height') || String(defaultHeight);

                systemConsole!.style.width = `${savedWidth}px`;
                systemConsole!.style.height = `${savedHeight}px`;

                const savedLeft = localStorage.getItem('console_float_left');
                const savedTop = localStorage.getItem('console_float_top');
                if (savedLeft && savedTop) {
                    systemConsole!.style.left = `${savedLeft}px`;
                    systemConsole!.style.top = `${savedTop}px`;
                } else {
                    // Default position: center dynamically
                    systemConsole!.style.left = `${(window.innerWidth - parseFloat(savedWidth)) / 2}px`;
                    systemConsole!.style.top = `${(window.innerHeight - parseFloat(savedHeight)) / 2}px`;
                }
                systemConsole!.style.bottom = 'auto';
                systemConsole!.style.right = 'auto';

                if (consoleBody) {
                    consoleBody.style.display = 'block';
                    consoleBody.scrollTop = consoleBody.scrollHeight;
                }

                consoleFloatBtn!.textContent = 'vertical_align_bottom';
                consoleFloatBtn!.setAttribute('data-i18n-title', 'consoleDockTitle');
                consoleFloatBtn!.title = state.currentLanguage === 'zh' ? '贴回底部' : 'Dock to bottom';
            } else {
                // Switch back to DOCKED mode
                systemConsole!.classList.remove('floating');

                // Hide resize handle, show toggle drawer button
                if (consoleResizeHandle) consoleResizeHandle.classList.add('hidden');
                if (consoleToggleBtn) {
                    consoleToggleBtn.style.display = 'block';
                    consoleToggleBtn.textContent = 'keyboard_double_arrow_down';
                }

                // Reset styling
                systemConsole!.style.width = '';
                systemConsole!.style.left = '';
                systemConsole!.style.top = '';
                systemConsole!.style.bottom = '';
                systemConsole!.style.right = '';

                // Auto-expand in docked mode
                systemConsole!.classList.add('expanded');
                systemConsole!.style.height = '30vh';
                if (consoleBody) {
                    consoleBody.style.display = 'block';
                    consoleBody.scrollTop = consoleBody.scrollHeight;
                }

                consoleFloatBtn!.textContent = 'open_in_new';
                consoleFloatBtn!.setAttribute('data-i18n-title', 'consoleFloatTitle');
                consoleFloatBtn!.title = state.currentLanguage === 'zh' ? '脱离为浮窗' : 'Detach to floating window';
            }
        });
    }

    // Dragging Logic
    consoleHeader.addEventListener('mousedown', (e: MouseEvent) => {
        if (!systemConsole!.classList.contains('floating')) return;
        // Ignore button clicks
        if ((e.target as HTMLElement).closest('.material-symbols-outlined')) return;

        isConsoleDragging = true;
        consoleDragStartX = e.clientX;
        consoleDragStartY = e.clientY;

        const rect = systemConsole!.getBoundingClientRect();
        consoleDragInitialLeft = rect.left;
        consoleDragInitialTop = rect.top;

        document.body.style.userSelect = 'none';
        systemConsole!.style.transition = 'none';
    });

    // Resizing Logic
    if (consoleResizeHandle) {
        consoleResizeHandle.addEventListener('mousedown', (e: MouseEvent) => {
            e.stopPropagation();
            e.preventDefault();
            isConsoleResizing = true;
            consoleResizeStartX = e.clientX;
            consoleResizeStartY = e.clientY;

            const rect = systemConsole!.getBoundingClientRect();
            consoleResizeInitialWidth = rect.width;
            consoleResizeInitialHeight = rect.height;

            document.body.style.userSelect = 'none';
            systemConsole!.style.transition = 'none';
        });
    }

    // Document-level Mousemove & Mouseup
    document.addEventListener('mousemove', (e: MouseEvent) => {
        if (isConsoleDragging && systemConsole!.classList.contains('floating')) {
            const dx = e.clientX - consoleDragStartX;
            const dy = e.clientY - consoleDragStartY;

            let newLeft = consoleDragInitialLeft + dx;
            let newTop = consoleDragInitialTop + dy;

            const rect = systemConsole!.getBoundingClientRect();
            const maxLeft = window.innerWidth - rect.width;
            const maxTop = window.innerHeight - rect.height;

            // Boundary protection
            newLeft = Math.max(0, Math.min(newLeft, maxLeft));
            newTop = Math.max(0, Math.min(newTop, maxTop));

            systemConsole!.style.left = `${newLeft}px`;
            systemConsole!.style.top = `${newTop}px`;
        }

        if (isConsoleResizing && systemConsole!.classList.contains('floating')) {
            const dx = e.clientX - consoleResizeStartX;
            const dy = e.clientY - consoleResizeStartY;

            let newWidth = consoleResizeInitialWidth + dx;
            let newHeight = consoleResizeInitialHeight + dy;

            // Limit minimum sizes
            newWidth = Math.max(300, newWidth);
            newHeight = Math.max(150, newHeight);

            // Limit maximum sizes to screen
            newWidth = Math.min(window.innerWidth - 20, newWidth);
            newHeight = Math.min(window.innerHeight - 20, newHeight);

            systemConsole!.style.width = `${newWidth}px`;
            systemConsole!.style.height = `${newHeight}px`;
        }
    });

    document.addEventListener('mouseup', () => {
        if (isConsoleDragging) {
            isConsoleDragging = false;
            document.body.style.userSelect = '';
            systemConsole!.style.transition = '';

            // Persist coordinates
            const rect = systemConsole!.getBoundingClientRect();
            localStorage.setItem('console_float_left', String(rect.left));
            localStorage.setItem('console_float_top', String(rect.top));
        }

        if (isConsoleResizing) {
            isConsoleResizing = false;
            document.body.style.userSelect = '';
            systemConsole!.style.transition = '';

            // Persist size
            const rect = systemConsole!.getBoundingClientRect();
            localStorage.setItem('console_float_width', String(rect.width));
            localStorage.setItem('console_float_height', String(rect.height));
        }
    });

    // Window resize boundary self-correction
    window.addEventListener('resize', () => {
        if (systemConsole!.classList.contains('floating')) {
            const rect = systemConsole!.getBoundingClientRect();
            let left = rect.left;
            let top = rect.top;
            let width = rect.width;
            let height = rect.height;

            let sizeChanged = false;
            let posChanged = false;

            if (width > window.innerWidth - 20) {
                width = window.innerWidth - 20;
                systemConsole!.style.width = `${width}px`;
                sizeChanged = true;
            }
            if (height > window.innerHeight - 20) {
                height = window.innerHeight - 20;
                systemConsole!.style.height = `${height}px`;
                sizeChanged = true;
            }

            const maxLeft = window.innerWidth - width;
            const maxTop = window.innerHeight - height;

            if (left > maxLeft) {
                left = Math.max(0, maxLeft);
                systemConsole!.style.left = `${left}px`;
                posChanged = true;
            }
            if (top > maxTop) {
                top = Math.max(0, maxTop);
                systemConsole!.style.top = `${top}px`;
                posChanged = true;
            }

            if (posChanged) {
                localStorage.setItem('console_float_left', String(left));
                localStorage.setItem('console_float_top', String(top));
            }
            if (sizeChanged) {
                localStorage.setItem('console_float_width', String(width));
                localStorage.setItem('console_float_height', String(height));
            }
        }
    });
}

// Export Logs Button
if (btnExportLogs) {
    btnExportLogs.addEventListener('click', async () => {
        try {
            let text = '';
            if (consoleBody) {
                text = Array.from(consoleBody.children)
                    .map(child => child.textContent)
                    .join('\n');
            }
            if (!text) {
                text = '暂无日志内容 / No logs available';
            }
            const saved = await saveText(
                { channel: 'settings:export-logs', args: [text] },
                state.currentLanguage === 'zh' ? '系统日志已成功导出！' : 'System logs exported successfully!',
                state.currentLanguage === 'zh' ? '导出失败: ' : 'Export failed: ',
            );
            void saved;
        } catch (err) {
            console.error('Failed to export logs:', err);
        }
    });
}

// Appending raw logs batch to console tray to minimize DOM reflows
ipcRenderer.on('logs:batch', (event: any, logs: string[]) => {
    if (!consoleBody) {
        consoleBody = document.getElementById('consoleBody');
    }
    if (!consoleBody || !logs || logs.length === 0) return;

    const fragment = document.createDocumentFragment();

    if (consolePool.length < MAX_CONSOLE_ENTRIES) {
        // Warmup: pool not yet full — create new nodes only (preserves order).
        const capacity = MAX_CONSOLE_ENTRIES - consolePool.length;
        const slice = logs.length > capacity ? logs.slice(logs.length - capacity) : logs;
        for (const log of slice) {
            const entry = document.createElement('div');
            entry.className = 'console-entry';
            applyConsoleClasses(entry, log);
            entry.textContent = log;
            consolePool.push(entry);
            fragment.appendChild(entry);
        }
    } else {
        // Steady state: reuse the oldest pooled node. appendChild relocates
        // an existing node to the end, so a fixed pool naturally stays in
        // newest-last order with zero create/remove churn. Only the last
        // MAX_CONSOLE_ENTRIES lines of the batch are rendered; older ones
        // would have been pruned anyway.
        const slice = logs.length > MAX_CONSOLE_ENTRIES ? logs.slice(logs.length - MAX_CONSOLE_ENTRIES) : logs;
        for (const log of slice) {
            const entry = consolePool[consolePoolIdx];
            consolePoolIdx = (consolePoolIdx + 1) % MAX_CONSOLE_ENTRIES;
            entry.className = 'console-entry';
            applyConsoleClasses(entry, log);
            entry.textContent = log;
            fragment.appendChild(entry);
        }
    }
    consoleBody.appendChild(fragment);

    // Safety prune (only relevant during warmup; steady state is exactly MAX)
    if (consoleBody.children.length > MAX_CONSOLE_ENTRIES) {
        while (consoleBody.children.length > MAX_CONSOLE_ENTRIES) {
            if (consoleBody.firstChild) {
                consoleBody.removeChild(consoleBody.firstChild);
            }
        }
    }

    // Scroll to bottom only if console is expanded or floating, and mouse is NOT hovering over it
    const isVisible = systemConsole && (systemConsole.classList.contains('expanded') || systemConsole.classList.contains('floating'));
    if (isVisible && !isMouseOverConsole) {
        if (!isConsoleScrollScheduled) {
            isConsoleScrollScheduled = true;
            requestAnimationFrame(() => {
                if (consoleBody) {
                    consoleBody.scrollTop = consoleBody.scrollHeight;
                }
                isConsoleScrollScheduled = false;
            });
        }
    }
});
}

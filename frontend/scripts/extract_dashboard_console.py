# -*- coding: utf-8 -*-
"""
extract_dashboard_console.py — Task#6 step5: dashboard.ts 系统控制台浮窗 + 日志攒批簇抽离 → dashboardConsole.ts

自包含簇(7 非连续段):
  ① 声明段 L67-71: Console Log Panel 注释 + consoleHeader/systemConsole/consoleBody/isConsoleScrollScheduled
     (ring buffer consts MAX_CONSOLE_ENTRIES/consolePool/consolePoolIdx 单独 L79-81 一起搬)
  ② ring buffer consts L74-81: 注释 + MAX_CONSOLE_ENTRIES + consolePool + consolePoolIdx
  ③ float/toggle/resize/drag 声明 L84-100: consoleFloatBtn/consoleToggleBtn/consoleResizeHandle +
     isConsoleDragging/consoleDragStart*/consoleDragInitial* + isConsoleResizing/consoleResizeStart*/consoleResizeInitial* + isMouseOverConsole
  ④ applyConsoleClasses L110-115 (私有,仅 logs:batch 自用)
  ⑤ init 体内 console 取值段 L479-484 (consoleHeader..consoleResizeHandle) + L488 (btnExportLogs) —— 跳过 L485-487 toggleZH/EN/Theme (留 hub)
  ⑥ console 事件/浮窗/拖拽块 L577-844: if (consoleHeader && systemConsole) { ... }
  ⑦ export-logs 点击 handler L859-881: if (btnExportLogs) { ... }
  ⑧ logs:batch IPC 监听 L972-1032: ipcRenderer.on('logs:batch', ...)

自包含性:console/drag/resize 20+ let + 3 const + applyConsoleClasses 仅在 initConsoleEvents(=⑤取值 + ⑥事件 + ⑦export + ⑧logs:batch)再赋值。
initDashboardEvents 失去 ⑤⑥⑦⑧,改调 initConsoleEvents()。

hub 改造:
  - 删 ①②③④(sub-lets + consts + applyConsoleClasses)
  - 删 ⑤ L479-484 + L488(init 取值,跳过 L485-487)
  - 删 ⑥ L577-844(若行)
  - 删 ⑦ L859-881
  - 删 ⑧ L972-1032
  - 在 ⑤原位(L479 位置)插 `    initConsoleEvents();`
  - 插 `import { initConsoleEvents } from './dashboardConsole';`

initConsoleEvents 组装:export function 内含 ⑤deindent(取值)+ ⑥deindent(事件)+ ⑦deindent(export)+ ⑧deindent(logs:batch)。
"""
import io
import os
import sys

HUB_PATH = os.path.join('frontend', 'src', 'ui', 'dashboard.ts')
SIB_PATH = os.path.join('frontend', 'src', 'ui', 'dashboardConsole.ts')


def E(s):
    return s.encode('utf-8')


def deindent4(line_bytes):
    if line_bytes.strip(b' \t') == b'':
        return b''
    if line_bytes.startswith(b'    '):
        return line_bytes[4:]
    return line_bytes


def trim_ends(block):
    while block and block[0].strip(b' \t') == b'' and block[0] != b'':
        block.pop(0)
    while block and block[-1].strip(b' \t') == b'' and block[-1] != b'':
        block.pop()
    return block


# 锚点
A1_HEAD = b'// Console Log Panel'
A1_LAST = b'let isConsoleScrollScheduled = false;'
A2_HEAD = b'// Console log ring buffer: a fixed pool of DOM nodes is reused instead of'
A2_LAST = b'let consolePoolIdx = 0;'
A3_HEAD = b'let consoleFloatBtn: HTMLElement | null = null;'
A3_LAST = b'let isMouseOverConsole = false;'
A4_HEAD = b'// Apply emoji-based severity classes to a console entry (reset each reuse).'
A4_LAST = b'}'
# ⑨ btnExportLogs 声明(L107,位于 Toggles 区,但所有权属 console 簇:仅 init 取值 L488 + handler L859 再赋值,均随簇迁出)
A9_BTN_DECL = b'let btnExportLogs: HTMLButtonElement | null;'
A5_HEAD = b"    consoleHeader = document.getElementById('consoleHeader');"
A5_LAST = b"    consoleResizeHandle = document.getElementById('consoleResizeHandle');"
A5_BTN = b"    btnExportLogs = document.getElementById('btnExportLogs') as HTMLButtonElement | null;"
A6_HEAD = b'    if (consoleHeader && systemConsole) {'
A6_LAST = b'    }'   # console event block close (indented 4)
A7_HEAD = b'    // Export Logs Button'
A7_LAST = b'    }'   # export logs if-close
A8_HEAD = b"    // Appending raw logs batch to console tray to minimize DOM reflows"
A8_LAST = b'    });' # logs:batch close


def main():
    dry = '--apply' not in sys.argv
    raw = io.open(HUB_PATH, 'rb').read()
    lines = raw.split(b'\r\n')
    n = len(lines)
    print(f"[info] hub total CRLF lines = {n}")

    # ---- 定位各段(content-anchored find)----
    def find_line(needle, start, end):
        for i in range(start - 1, end):
            if lines[i] == needle:
                return i + 1
        raise AssertionError(f"not found [{start},{end}]: {needle!r}")

    # ①
    ln1 = find_line(A1_HEAD, 1, n)
    ln1_end = find_line(A1_LAST, ln1, ln1 + 20)
    # ②
    ln2 = find_line(A2_HEAD, ln1_end, ln1_end + 20)
    ln2_end = find_line(A2_LAST, ln2, ln2 + 15)
    # ③
    ln3 = find_line(A3_HEAD, ln2_end, ln2_end + 10)
    ln3_end = find_line(A3_LAST, ln3, ln3 + 30)
    # ④ applyConsoleClasses (L110-115: 注释 + function{ + 3 if + })
    ln4 = find_line(A4_HEAD, ln3_end, 200)
    ln4_end = ln4 + 5  # 6 行: comment + function + 3 ifs + closing }
    seg4 = b'\r\n'.join(lines[ln4 - 1:ln4_end])
    assert seg4.count(b'{') == seg4.count(b'}'), f"applyConsole brace mismatch at L{ln4}"

    # ⑨ btnExportLogs 声明(L107,位于 Toggles 区,需单独迁出)
    ln9 = find_line(A9_BTN_DECL, ln3_end, 200)
    # ⑤ init 取值: consoleHeader@479 .. consoleResizeHandle@484; btnExportLogs@488
    ln5a = find_line(A5_HEAD, 100, n)
    ln5a_end = find_line(A5_LAST, ln5a, ln5a + 10)
    ln5btn = find_line(A5_BTN, ln5a_end, ln5a_end + 30)
    # ⑥ console event block: if (consoleHeader && systemConsole) { ... }
    ln6 = find_line(A6_HEAD, ln5btn, n)
    # brace-balance to find } at indent 4 depth 0-relative-to-if-start
    # scan from ln6 finding matching `    }` at depth 1 (the if-body close)
    depth = 0
    started = False
    ln6_end = None
    for i in range(ln6 - 1, n):
        line = lines[i]
        # only count braces in the code (skip strings naive ok here, no braces in strings)
        op = line.count(b'{')
        cl = line.count(b'}')
        if op or cl or started:
            depth += op - cl
            started = True
        # we want the line where if-block closes: starts as `    if (...) {` depth after line = 1
        # close when depth returns to 0
        if started and depth == 0:
            ln6_end = i + 1
            break
    assert ln6_end is not None, f"console event block close not found from L{ln6}"
    assert lines[ln6_end - 1] == A6_LAST, f"console block close mismatch L{ln6_end}: {lines[ln6_end-1]!r}"
    # ⑦ export logs btn
    ln7 = find_line(A7_HEAD, ln6_end, n)
    # brace-balance for `    if (btnExportLogs) { ... }`
    depth = 0
    started = False
    ln7_end = None
    # the head is the comment line; the `if` starts at ln7+1
    ln7_if = ln7 + 1
    for i in range(ln7_if - 1, n):
        line = lines[i]
        op = line.count(b'{')
        cl = line.count(b'}')
        if op or cl or started:
            depth += op - cl
            started = True
        if started and depth == 0:
            ln7_end = i + 1
            break
    assert ln7_end is not None and lines[ln7_end - 1] == A7_LAST, f"export logs block close mismatch L{ln7_end}: {lines[ln7_end-1] if ln7_end else None!r}"
    # ⑧ logs:batch
    ln8 = find_line(A8_HEAD, ln7_end, n)
    # brace-balance for `    ipcRenderer.on('logs:batch', (event, logs) => { ... });`
    depth = 0
    started = False
    ln8_end = None
    for i in range(ln8 - 1, n):
        line = lines[i]
        op = line.count(b'{')
        cl = line.count(b'}')
        if op or cl or started:
            depth += op - cl
            started = True
        if started and depth == 0:
            ln8_end = i + 1
            break
    assert ln8_end is not None, f"logs:batch close not found from L{ln8}"
    assert lines[ln8_end - 1] == A8_LAST, f"logs:batch close mismatch L{ln8_end}: {lines[ln8_end-1]!r}"

    print(f"[anchor] ①decl L{ln1}-{ln1_end}")
    print(f"[anchor] ②ringbuf L{ln2}-{ln2_end}")
    print(f"[anchor] ③drag/resize L{ln3}-{ln3_end}")
    print(f"[anchor] ④applyConsole L{ln4}-{ln4_end}")
    print(f"[anchor] ⑨btnExportLogs-decl L{ln9}")
    print(f"[anchor] ⑤init-fetch L{ln5a}-{ln5a_end} + btnExportLogs L{ln5btn}")
    print(f"[anchor] ⑥console-event L{ln6}-{ln6_end}")
    print(f"[anchor] ⑦export-logs L{ln7}-{ln7_end}")
    print(f"[anchor] ⑧logs:batch L{ln8}-{ln8_end}")

    # ---- 切段 ----
    seg_decl = b'\r\n'.join(lines[ln1 - 1:ln1_end])            # ① 注释+4 let
    seg_ringbuf = b'\r\n'.join(lines[ln2 - 1:ln2_end])           # ② 注释+1 const+1 const+1 let
    seg_drag = b'\r\n'.join(lines[ln3 - 1:ln3_end])              # ③ +空行 分隔
    seg_apply = b'\r\n'.join(lines[ln4 - 1:ln4_end])            # ④ applyConsoleClasses
    seg_btn_decl = lines[ln9 - 1]                                 # ⑨ btnExportLogs 声明单行

    # ⑤ init 取值: consoleHeader..consoleResizeHandle + btnExportLogs(跳过 toggleZH/EN/Theme)
    block5_main = [deindent4(lines[i]) for i in range(ln5a - 1, ln5a_end)]
    block5_btn = [deindent4(lines[ln5btn - 1])]
    seg_init_fetch = b'\r\n'.join(block5_main + [b''] + block5_btn)

    # ⑥ console event: 去首层 4 空格
    block6 = [deindent4(lines[i]) for i in range(ln6 - 1, ln6_end)]
    block6 = trim_ends(block6)
    seg_console_event = b'\r\n'.join(block6)

    # ⑦ export logs
    block7 = [deindent4(lines[i]) for i in range(ln7 - 1, ln7_end)]
    block7 = trim_ends(block7)
    seg_export_logs = b'\r\n'.join(block7)

    # ⑧ logs:batch
    block8 = [deindent4(lines[i]) for i in range(ln8 - 1, ln8_end)]
    block8 = trim_ends(block8)
    seg_logs_batch = b'\r\n'.join(block8)

    # ---- sibling 组装 ----
    header = E(
        "/**\n"
        " * dashboardConsole.ts: 系统控制台浮窗 + 日志攒批(自包含 console/drag/resize 20+ let + ring buffer consts)。\n"
        " *\n"
        " * 从 dashboard.ts 抽离(7 非连续段):console* let + MAX_CONSOLE_ENTRIES/consolePool/consolePoolIdx consts +\n"
        " * drag/resize 12 let + applyConsoleClasses(私有) + initDashboardEvents 体内 console 取值/事件/导出/logs:batch 编排段。\n"
        " * 全部 console/drag/resize let 仅在 initConsoleEvents 内再赋值(取值段 + 事件块 + logs:batch),自包含。\n"
        " * hub initDashboardEvents 改调 initConsoleEvents();行为等价于原 init 体内 console 编排(幂等重取 DOM + 重绑监听)。\n"
        " */\n"
        "import { ipcRenderer } from '../shared/ipc';\n"
        "import state from './dashboardState';\n"
        "import { saveText } from '../shared/fileService';\n"
        "\n"
    ).replace(b'\n', b'\r\n')

    init_head = E("export function initConsoleEvents() {\n").replace(b'\n', b'\r\n')
    init_tail = b'}\r\n'

    parts = [header,
             seg_decl, b'\r\n',
             seg_ringbuf, b'\r\n',
             seg_drag, b'\r\n',
             b'\r\n',   # ⑨ btnExportLogs 单独声明前空行
             seg_btn_decl, b'\r\n',
             b'\r\n',
             seg_apply, b'\r\n',
             b'\r\n',  # before init wrapper
             init_head,
             seg_init_fetch, b'\r\n',
             b'\r\n',
             seg_console_event, b'\r\n',
             b'\r\n',
             seg_export_logs, b'\r\n',
             b'\r\n',
             seg_logs_batch, b'\r\n',
             init_tail]
    sibling_content = b''.join(parts)
    sib_lines = sibling_content.split(b'\r\n')
    print(f"[gen] sibling CRLF lines = {len(sib_lines)}")
    op = sibling_content.count(b'{')
    cl = sibling_content.count(b'}')
    print(f"[gen] braces open={op} close={cl}")
    assert op == cl, f"brace unbalanced: {op} vs {cl}"

    if dry:
        pv = SIB_PATH + '.preview'
        with io.open(pv, 'wb') as f:
            f.write(sibling_content)
        print(f"[dry] wrote sibling preview -> {pv}")
        print("[dry] NOT modifying hub. Re-run with --apply to commit.")
        return

    # ---- 落盘 sibling ----
    with io.open(SIB_PATH, 'wb') as f:
        f.write(sibling_content)
    print(f"[apply] wrote sibling -> {SIB_PATH} ({len(sib_lines)} CRLF lines)")

    # ---- 重写 hub:keep mask 8 段 False ----
    keep = [True] * n
    for i in range(ln1 - 1, ln1_end):
        keep[i] = False
    for i in range(ln2 - 1, ln2_end):
        keep[i] = False
    for i in range(ln3 - 1, ln3_end):
        keep[i] = False
    for i in range(ln4 - 1, ln4_end):
        keep[i] = False
    keep[ln9 - 1] = False  # ⑨ btnExportLogs 声明单行迁出
    for i in range(ln5a - 1, ln5a_end):
        keep[i] = False
    keep[ln5btn - 1] = False
    for i in range(ln6 - 1, ln6_end):
        keep[i] = False
    for i in range(ln7 - 1, ln7_end):
        keep[i] = False
    for i in range(ln8 - 1, ln8_end):
        keep[i] = False

    # 在 ⑤ 段首位置(ln5a 原位)插 initConsoleEvents(); ⑥⑦⑦⑧ 整段跳过
    new_hub_lines = []
    init_call_inserted = False
    for i in range(n):
        if not keep[i]:
            if not init_call_inserted and i == ln5a - 1:
                new_hub_lines.append(b'    initConsoleEvents();')
                init_call_inserted = True
            continue
        new_hub_lines.append(lines[i])

    joined = b'\r\n'.join(new_hub_lines)

    # 在 dashboardModal import 后插入 console import
    anchor = b"import { initModalDom, showModal, hideModal } from './dashboardModal';\r\n"
    assert joined.count(anchor) == 1, "dashboardModal import 锚点非唯一"
    insert_line = b"import { initConsoleEvents } from './dashboardConsole';\r\n"
    joined = joined.replace(anchor, anchor + insert_line, 1)

    with io.open(HUB_PATH, 'wb') as f:
        f.write(joined)
    final_hub_lines = joined.split(b'\r\n')
    print(f"[apply] rewrote hub -> {HUB_PATH} ({len(final_hub_lines)} CRLF lines)")


if __name__ == '__main__':
    main()

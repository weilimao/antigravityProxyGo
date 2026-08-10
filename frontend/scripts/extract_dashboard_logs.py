# -*- coding: utf-8 -*-
"""
extract_dashboard_logs.py — Task#6 step3: dashboard.ts 日志行池簇抽离 → dashboardLogs.ts

自包含簇(单一连续段 L137-327):
  - interface LogsRowSlot (L142-166)
  - const logsRowSlots: LogsRowSlot[] (L168)  ← hub renderLogsTable 跨模块 mutate (length=0/push/[i])
  - const viewBtnLogMap: WeakMap (L174)        ← hub renderLogsTable.set + init 点击委托 .get
  - function buildLogsRowSlot (L176-258)       ← 纯 DOM,无 state/i18n 依赖
  - function updateLogsRowSlot (L260-327)     ← 读 state.currentLanguage + 调 formatDuration

关键:const 数组/WeakMap 跨模块 mutation 合法(live binding),故 hub renderLogsTable 经 import 即可就地 mutate。
renderLogsTable 自身留 hub(再赋值 logsTableBody/valShowingText/paginationControls,跨函数共享必须留 hub)。

hub 改造:删 L137-327;插
  `import { LogsRowSlot, logsRowSlots, viewBtnLogMap, buildLogsRowSlot, updateLogsRowSlot } from './dashboardLogs';`
buildLogsRowSlot/updateLogsRowSlot 私有→export;interface/const → export。
"""
import io
import os
import sys

HUB_PATH = os.path.join('frontend', 'src', 'ui', 'dashboard.ts')
SIB_PATH = os.path.join('frontend', 'src', 'ui', 'dashboardLogs.ts')

# 簇起止(1-based CRLF 行号,含两端):L137-327
C_START, C_END = 137, 327


def E(s):
    return s.encode('utf-8')


# 锚点
C_HEAD = b'// --- Logs table row pool ---'
C_IFACE = b'interface LogsRowSlot {'
C_LOGS = b'const logsRowSlots: LogsRowSlot[] = [];'
C_WEAKMAP = b'const viewBtnLogMap = new WeakMap<HTMLButtonElement, any>();'
C_BUILD = b'function buildLogsRowSlot(): LogsRowSlot {'
C_UPDATE = b'function updateLogsRowSlot(slot: LogsRowSlot, log: any, dict: any) {'


def main():
    dry = '--apply' not in sys.argv
    raw = io.open(HUB_PATH, 'rb').read()
    lines = raw.split(b'\r\n')
    n = len(lines)
    print(f"[info] hub total CRLF lines = {n}")

    # 断言簇头/各锚点
    assert lines[C_START - 1] == C_HEAD, f"C_START L{C_START} mismatch: {lines[C_START-1]!r}"
    assert lines[C_END - 1] == b'}', f"C_END L{C_END} mismatch: {lines[C_END-1]!r}"
    assert lines[C_END] == b'', f"簇尾 L{C_END+1} 应空行, got {lines[C_END]!r}"

    def find_line(needle, start, end):
        for i in range(start - 1, end):
            if lines[i] == needle:
                return i + 1
        raise AssertionError(f"not found [{start},{end}]: {needle!r}")

    ln_iface = find_line(C_IFACE, C_START, C_END)
    ln_logs = find_line(C_LOGS, C_START, C_END)
    ln_weakmap = find_line(C_WEAKMAP, C_START, C_END)
    ln_build = find_line(C_BUILD, C_START, C_END)
    ln_update = find_line(C_UPDATE, C_START, C_END)
    print(f"[anchor] iface@L{ln_iface} logs@L{ln_logs} weakmap@L{ln_weakmap} build@L{ln_build} update@L{ln_update}")
    assert ln_iface < ln_logs < ln_weakmap < ln_build < ln_update, "簇内锚点顺序异常"

    # ---- 切簇(模块作用域 0 缩进,无需 deindent)----
    block = lines[C_START - 1:C_END]

    # interface/const/function → export
    out = []
    for ln in block:
        if ln == C_IFACE:
            out.append(b'export interface LogsRowSlot {')
        elif ln == C_LOGS:
            out.append(b'export const logsRowSlots: LogsRowSlot[] = [];')
        elif ln == C_WEAKMAP:
            out.append(b'export const viewBtnLogMap = new WeakMap<HTMLButtonElement, any>();')
        elif ln == C_BUILD:
            out.append(b'export function buildLogsRowSlot(): LogsRowSlot {')
        elif ln == C_UPDATE:
            out.append(b'export function updateLogsRowSlot(slot: LogsRowSlot, log: any, dict: any) {')
        else:
            out.append(ln)

    header = E(
        "/**\n"
        " * dashboardLogs.ts: 请求日志表格行池 builder + 行 slot 刷新(内存稳定 row-pool)。\n"
        " *\n"
        " * 从 dashboard.ts 抽离(原 L137-327 单一连续段):LogsRowSlot interface + logsRowSlots 数组 +\n"
        " * viewBtnLogMap WeakMap + buildLogsRowSlot(纯 DOM 建元素,无 state 依赖)+ updateLogsRowSlot(读 state + formatDuration)。\n"
        " * logsRowSlots/viewBtnLogMap 为 export const:hub renderLogsTable 跨模块就地 mutate(push/length=0/[i]读、set/get),\n"
        " * init 点击委托跨模块读 viewBtnLogMap.get(btn) —— const 的 live 引用支持跨模块 mutation,语义等价于原同模块 mutation。\n"
        " */\n"
        "import state from './dashboardState';\n"
        "import { formatDuration } from './dashboardUtils';\n"
        "\n"
    ).replace(b'\n', b'\r\n')

    sibling_content = header + b'\r\n'.join(out) + b'\r\n'
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

    # ---- 重写 hub:keep mask L137-327 False ----
    keep = [True] * n
    for i in range(C_START - 1, C_END):
        keep[i] = False
    new_hub_lines = [lines[i] for i in range(n) if keep[i]]
    joined = b'\r\n'.join(new_hub_lines)

    # 在 dashboardTrends import 后插入 logs import
    anchor = b"import { maybeDrawTrendChart, redrawTrendChartAnimated } from './dashboardTrends';\r\n"
    assert joined.count(anchor) == 1, "dashboardTrends import 锚点非唯一"
    insert_line = (b"import { LogsRowSlot, logsRowSlots, viewBtnLogMap, "
                   b"buildLogsRowSlot, updateLogsRowSlot } from './dashboardLogs';\r\n")
    joined = joined.replace(anchor, anchor + insert_line, 1)

    with io.open(HUB_PATH, 'wb') as f:
        f.write(joined)
    final_hub_lines = joined.split(b'\r\n')
    print(f"[apply] rewrote hub -> {HUB_PATH} ({len(final_hub_lines)} CRLF lines)")


if __name__ == '__main__':
    main()

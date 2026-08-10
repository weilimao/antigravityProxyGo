# -*- coding: utf-8 -*-
"""
extract_dashboard_trends.py — Task#6 step2: dashboard.ts 趋势图簇抽离 → dashboardTrends.ts

自包含簇:5 个模块级 let (lastTrendsSig/lastChartRange/lastChartScope/lastChartDrawTs/chartRedrawTimer)
+ const CHART_DRAW_MIN_INTERVAL + 私有 currentTrendsSource + maybeDrawTrendChart + redrawTrendChartAnimated。
这 5 let 仅在 maybeDrawTrendChart / redrawTrendChartAnimated 再赋值 → 两函数随簇迁出,自包含,无需 init-helper。

非连续两段:
  ① 声明段 L101-110(含上方 throttle 注释 L101-103)
  ② 函数段 L145-219(currentTrendsSource 注释+函数 / maybeDrawTrendChart 注释+函数 / redrawTrendChartAnimated 注释+export 函数)

hub 改造:删两段;maybeDrawTrendChart 私有→export(hub renderActiveView 调用 import);
  插 `import { maybeDrawTrendChart, redrawTrendChartAnimated } from './dashboardTrends';`。
  调用点 maybeDrawTrendChart()(renderActiveView)/redrawTrendChartAnimated()(switchView)同名经 import 解析。
chartRenderer 仍留 hub(formatCompactNumber@renderActiveView + updateMemoryChart@memory-stats-updated)。
"""
import io
import os
import sys

HUB_PATH = os.path.join('frontend', 'src', 'ui', 'dashboard.ts')
SIB_PATH = os.path.join('frontend', 'src', 'ui', 'dashboardTrends.ts')

# ① 声明段(1-based CRLF 行号,含两端)
DECL_START, DECL_END = 101, 110
# ② 函数段
FN_START, FN_END = 145, 219


def E(s):
    return s.encode('utf-8')


# 锚点(纯 ASCII)
DECL_HEAD = b'// Trend chart redraw throttle: the chart only needs to refresh every few'
DECL_LAST = b'const CHART_DRAW_MIN_INTERVAL = 3000;'
FN_HEAD_COMMENT = E('// currentTrendsSource: 按 currentTrendScope 返回当前应喂给趋势图的数据序列。')
REDCLOSE = b'}'  # L219 redrawTrendChartAnimated 闭合
MAYBE_DECL = b'function maybeDrawTrendChart() {'


def main():
    dry = '--apply' not in sys.argv
    raw = io.open(HUB_PATH, 'rb').read()
    lines = raw.split(b'\r\n')
    n = len(lines)
    print(f"[info] hub total CRLF lines = {n}")

    # ---- 断言 ① 声明段 ----
    assert lines[DECL_START - 1] == DECL_HEAD, f"DECL_START L{DECL_START} mismatch: {lines[DECL_START-1]!r}"
    assert lines[DECL_END - 1] == DECL_LAST, f"DECL_END L{DECL_END} mismatch: {lines[DECL_END-1]!r}"
    print(f"[anchor] 声明段 [L{DECL_START}, L{DECL_END}]")

    # ---- 断言 ② 函数段 ----
    assert lines[FN_START - 1] == FN_HEAD_COMMENT, f"FN_START L{FN_START} mismatch: {lines[FN_START-1]!r}"
    assert lines[FN_END - 1] == REDCLOSE, f"FN_END L{FN_END} mismatch: {lines[FN_END-1]!r}"
    # 段内含 maybeDrawTrendChart 私有声明(待改 export)
    found_maybe = False
    for i in range(FN_START - 1, FN_END):
        if lines[i] == MAYBE_DECL:
            found_maybe = True
            break
    assert found_maybe, "maybeDrawTrendChart 私有声明行未找到"
    print(f"[anchor] 函数段 [L{FN_START}, L{FN_END}]")

    # ---- 切两段(原样字节,模块作用域 0 缩进,无需 deindent)----
    decl_block = lines[DECL_START - 1:DECL_END]   # L101..L110
    fn_block = lines[FN_START - 1:FN_END]          # L145..L219

    # fn_block 内:maybeDrawTrendChart 私有 → export
    fn_block = [b'export function maybeDrawTrendChart() {' if ln == MAYBE_DECL else ln for ln in fn_block]

    # ---- sibling 组装 ----
    header = E(
        "/**\n"
        " * dashboardTrends.ts: 趋势图绘制与节流(自包含 5 个模块级 let + CHART_DRAW_MIN_INTERVAL const)。\n"
        " *\n"
        " * 从 dashboard.ts 抽离:lastTrendsSig/lastChartRange/lastChartScope/lastChartDrawTs/chartRedrawTimer\n"
        " * 5 个 let 仅在 maybeDrawTrendChart / redrawTrendChartAnimated 再赋值,两函数随簇迁出,自包含。\n"
        " * maybeDrawTrendChart 由 hub renderActiveView 调;redrawTrendChartAnimated 由 hub switchView 调(切回 dashboard 强制动画)。\n"
        " */\n"
        "import state from './dashboardState';\n"
        "import * as chartRenderer from './chartRenderer';\n"
        "\n"
    ).replace(b'\n', b'\r\n')

    parts = [header,
             b'\r\n'.join(decl_block),   # 已含上方 throttle 注释
             b'\r\n',                    # 段间 1 空行
             b'\r\n'.join(fn_block),
             b'\r\n']                    # 末尾换行
    sibling_content = b''.join(parts)
    sib_lines = sibling_content.split(b'\r\n')
    print(f"[gen] sibling CRLF lines = {len(sib_lines)}")

    # 平衡校验
    open_b = sibling_content.count(b'{')
    close_b = sibling_content.count(b'}')
    print(f"[gen] braces open={open_b} close={close_b}")
    assert open_b == close_b, f"brace unbalanced: {open_b} vs {close_b}"

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

    # ---- 重写 hub:keep mask 两段 False ----
    keep = [True] * n
    for i in range(DECL_START - 1, DECL_END):
        keep[i] = False
    for i in range(FN_START - 1, FN_END):
        keep[i] = False
    new_hub_lines = [lines[i] for i in range(n) if keep[i]]
    joined = b'\r\n'.join(new_hub_lines)

    # 在 dashboardUtils import 后插入 trends import
    anchor = b"import { formatDuration } from './dashboardUtils';\r\n"
    assert joined.count(anchor) == 1, "dashboardUtils import 锚点非唯一"
    insert_line = b"import { maybeDrawTrendChart, redrawTrendChartAnimated } from './dashboardTrends';\r\n"
    joined = joined.replace(anchor, anchor + insert_line, 1)

    with io.open(HUB_PATH, 'wb') as f:
        f.write(joined)
    final_hub_lines = joined.split(b'\r\n')
    print(f"[apply] rewrote hub -> {HUB_PATH} ({len(final_hub_lines)} CRLF lines)")


if __name__ == '__main__':
    main()

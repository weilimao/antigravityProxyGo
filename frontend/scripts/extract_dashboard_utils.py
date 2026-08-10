# -*- coding: utf-8 -*-
"""
extract_dashboard_utils.py — Task#6 step1: dashboard.ts formatDuration 抽离 → dashboardUtils.ts

formatDuration(L136-140)纯函数,读入参,无模块 let 依赖。
被 showModal(modal 簇)/updateLogsRowSlot(logs 簇)两处调用(future siblings import)。
单独成簇避免 sibling 互相耦合。

hub 改造:L136-140 删,L1 区插 `import { formatDuration } from './dashboardUtils';`。
"""
import io
import os
import sys

HUB_PATH = os.path.join('frontend', 'src', 'ui', 'dashboard.ts')
SIB_PATH = os.path.join('frontend', 'src', 'ui', 'dashboardUtils.ts')

# formatDuration 簇(1-based CRLF 行号,含两端):L136-140
FN_START, FN_END = 136, 140


def E(s):
    return s.encode('utf-8')


FN_HEAD = b'function formatDuration(ms: number | undefined): string {'
FN_LAST = b'    return `${(ms / 1000).toFixed(2)}s`;'
FN_CLOSE = b'}'


def main():
    dry = '--apply' not in sys.argv
    raw = io.open(HUB_PATH, 'rb').read()
    lines = raw.split(b'\r\n')
    n = len(lines)
    print(f"[info] hub total CRLF lines = {n}")

    # 断言簇头/体/尾
    assert lines[FN_START - 1] == FN_HEAD, f"L{FN_START} mismatch: {lines[FN_START-1]!r}"
    assert lines[FN_END - 2] == FN_LAST, f"L{FN_END-1} mismatch: {lines[FN_END-2]!r}"
    assert lines[FN_END - 1] == FN_CLOSE, f"L{FN_END} mismatch: {lines[FN_END-1]!r}"
    # 簇前应是空行(L135);btnExportLogs 声明在 L134
    assert lines[FN_START - 2] == b'', f"L{FN_START-1} 应为空行, got {lines[FN_START-2]!r}"
    assert lines[FN_START - 3].startswith(b'let btnExportLogs'), f"L{FN_START-2} prev mismatch: {lines[FN_START-3]!r}"
    print(f"[anchor] formatDuration 簇 [L{FN_START}, L{FN_END}]")

    # sibling 内容:header + export function(簇体直接复用,函数基缩进已是 0→4,无需 deindent)
    header = E(
        "/**\n"
        " * dashboardUtils.ts: 仪表盘小工具(纯函数,无 DOM/模块状态依赖)。\n"
        " *\n"
        " * formatDuration 从 dashboard.ts 抽离(原 L136-140),被详情弹窗(dashboardModal)与\n"
        " * 日志行池(dashboardLogs)复用,作为叶子 util 避免 sibling 互引。\n"
        " */\n"
        "export function formatDuration(ms: number | undefined): string {\n"
    ).replace(b'\n', b'\r\n')
    # 簇体:L137-139(三行函数体),再加 export function 行由 header 提供,故 body = L138-140(含闭合)
    body = b'\r\n'.join(lines[FN_START:FN_END])  # L137..L140
    sibling_content = header + body + b'\r\n'
    sib_lines = sibling_content.split(b'\r\n')
    print(f"[gen] sibling CRLF lines = {len(sib_lines)}")

    if dry:
        pv = SIB_PATH + '.preview'
        with io.open(pv, 'wb') as f:
            f.write(sibling_content)
        print(f"[dry] wrote sibling preview -> {pv}")
        print("[dry] NOT modifying hub. Re-run with --apply to commit.")
        return

    # 落盘 sibling
    with io.open(SIB_PATH, 'wb') as f:
        f.write(sibling_content)
    print(f"[apply] wrote sibling -> {SIB_PATH} ({len(sib_lines)} CRLF lines)")

    # 重写 hub:keep mask 置 L136-140 为 False
    keep = [True] * n
    for i in range(FN_START - 1, FN_END):
        keep[i] = False
    new_hub_lines = [lines[i] for i in range(n) if keep[i]]
    joined = b'\r\n'.join(new_hub_lines)

    # 在 L1 `import { ipcRenderer } from '../shared/ipc';` 后插入 utils import
    anchor = b"import { ipcRenderer } from '../shared/ipc';\r\n"
    assert joined.count(anchor) == 1, "ipc import 锚点非唯一"
    insert_line = b"import { formatDuration } from './dashboardUtils';\r\n"
    joined = joined.replace(anchor, anchor + insert_line, 1)

    with io.open(HUB_PATH, 'wb') as f:
        f.write(joined)
    final_hub_lines = joined.split(b'\r\n')
    print(f"[apply] rewrote hub -> {HUB_PATH} ({len(final_hub_lines)} CRLF lines)")


if __name__ == '__main__':
    main()

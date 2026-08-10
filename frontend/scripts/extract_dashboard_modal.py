# -*- coding: utf-8 -*-
"""
extract_dashboard_modal.py — Task#6 step4: dashboard.ts 详情弹窗簇抽离 → dashboardModal.ts

自包含簇(3 非连续段):
  ① 声明段 L61-81: `// Details Modal Elements` 注释 + 18 个 `modal*` let
  ② init 段 L490-536: initDashboardEvents 体内 modal 取值(18 个 getElementById)+ 关闭/复制 addEventListener(基缩进 4)
  ③ 函数段 L1220-1337: export hideModal + `let modalDetailsToken` + formatRequestBody(私有)+ formatRequestHeaders(私有)+ export showModal

自包含性:18 modal* let + modalDetailsToken 仅在 initModalDom(=②取出deindent)、showModal 懒取(③内)、
hideModal、showModal/hideModal 的 ++modalDetailsToken 再赋值 —— 全随簇迁出。
initDashboardEvents 删 ②段 → 调 initModalDom();hub 调用点 showModal/hideModal 经 import。
点击委托(L582-602,用 viewBtnLogMap.get + showModal)留 hub,改 import 调用。
(window as any).showModal/hideModal 赋值留 hub(引用 import 的 live 绑定)。

hub 改造:删 3 段;插
  `import { initModalDom, showModal, hideModal } from './dashboardModal';`
② deindent 4→0,包进 `export function initModalDom() { ... }`(取值+复制 addEventListener)。
"""
import io
import os
import sys

HUB_PATH = os.path.join('frontend', 'src', 'ui', 'dashboard.ts')
SIB_PATH = os.path.join('frontend', 'src', 'ui', 'dashboardModal.ts')

# ① 声明段
DECL_START, DECL_END = 61, 81
# ② init 段(基缩进 4)
INIT_START, INIT_END = 490, 536
# ③ 函数段(基缩进 0)
FN_START, FN_END = 1220, 1337


def E(s):
    return s.encode('utf-8')


# 锚点
DECL_HEAD = b'// Details Modal Elements'
DECL_LAST = b'let modalHeaderArea: HTMLElement | null = null;'
INIT_HEAD = b"    detailsModal = document.getElementById('detailsModal');"
INIT_LAST = b'    }'  # L536 modalCopyBtn addEventListener 闭合
FN_HEAD = b'export function hideModal() {'
FN_LAST = b'}'  # L1337 showModal 闭合


def deindent4(line_bytes):
    if line_bytes.strip(b' \t') == b'':
        return b''
    if line_bytes.startswith(b'    '):
        return line_bytes[4:]
    return line_bytes


def main():
    dry = '--apply' not in sys.argv
    raw = io.open(HUB_PATH, 'rb').read()
    lines = raw.split(b'\r\n')
    n = len(lines)
    print(f"[info] hub total CRLF lines = {n}")

    # ---- 断言 ① ----
    assert lines[DECL_START - 1] == DECL_HEAD, f"DECL_START L{DECL_START} mismatch: {lines[DECL_START-1]!r}"
    assert lines[DECL_END - 1] == DECL_LAST, f"DECL_END L{DECL_END} mismatch: {lines[DECL_END-1]!r}"
    print(f"[anchor] 声明段 [L{DECL_START}, L{DECL_END}]")

    # ---- 断言 ② ----
    assert lines[INIT_START - 1] == INIT_HEAD, f"INIT_START L{INIT_START} mismatch: {lines[INIT_START-1]!r}"
    assert lines[INIT_END - 1] == INIT_LAST, f"INIT_END L{INIT_END} mismatch: {lines[INIT_END-1]!r}"
    print(f"[anchor] init 段 [L{INIT_START}, L{INIT_END}]")

    # ---- 断言 ③ ----
    assert lines[FN_START - 1] == FN_HEAD, f"FN_START L{FN_START} mismatch: {lines[FN_START-1]!r}"
    assert lines[FN_END - 1] == FN_LAST, f"FN_END L{FN_END} mismatch: {lines[FN_END-1]!r}"
    print(f"[anchor] 函数段 [L{FN_START}, L{FN_END}]")

    # ---- 切段 ----
    decl_block = lines[DECL_START - 1:DECL_END]            # L61..L81(原样)
    init_block = [deindent4(lines[i]) for i in range(INIT_START - 1, INIT_END)]  # L490..L536 deindent 4→0
    fn_block = lines[FN_START - 1:FN_END]                   # L1220..L1337(原样)

    # 去段内首尾冗余空行(init_block 内有 L496/L509/L524 内部空行,保留相对结构;仅去首/尾空行)
    def trim_ends(block):
        while block and block[0].strip(b' \t') == b'':
            block.pop(0)
        while block and block[-1].strip(b' \t') == b'':
            block.pop()
        return block
    init_block = trim_ends(init_block)

    # ---- sibling 组装 ----
    header = E(
        "/**\n"
        " * dashboardModal.ts: 请求详情弹窗(自包含 18 个 modal* let + modalDetailsToken)。\n"
        " *\n"
        " * 从 dashboard.ts 抽离:声明段(原 L61-81)+ initDashboardEvents 体内 modal 取值/复制监听段(原 L490-536,deindent 4→0,\n"
        " * 包进 initModalDom)+ hideModal/formatRequestBody/formatRequestHeaders/showModal 函数段(原 L1220-1337)。\n"
        " * 18 modal* let + modalDetailsToken 仅在 initModalDom / showModal(懒取)/ hideModal / ++modalDetailsToken 再赋值,全随簇迁出,自包含。\n"
        " * hub initDashboardEvents 调 initModalDom();点击委托 + (window).showModal/hideModal 赋值留 hub,经 import 解析。\n"
        " */\n"
        "import { ipcRenderer } from '../shared/ipc';\n"
        "import state from './dashboardState';\n"
        "import { formatDuration } from './dashboardUtils';\n"
        "\n"
    ).replace(b'\n', b'\r\n')

    # initModalDom 包裹
    init_fn_head = E("export function initModalDom() {\n").replace(b'\n', b'\r\n')
    init_fn_tail = b'}\r\n'

    parts = [header,
             b'\r\n'.join(decl_block),                     # L61-L81(含 Details Modal 注释 + 18 let)
             b'\r\n',
             b'\r\n',                                      # init 段前 1 空行
             init_fn_head,
             b'\r\n'.join(init_block),                     # deindent 后的 init 体
             init_fn_tail,
             b'\r\n',                                      # 段间
             b'\r\n'.join(fn_block),                       # L1220-L1337(已含 hideModal + let modalDetailsToken + formatX + showModal)
             b'\r\n']
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

    # ---- 重写 hub:keep mask 3 段 False;②段插入 initModalDom() 调用 ----
    keep = [True] * n
    for i in range(DECL_START - 1, DECL_END):
        keep[i] = False
    for i in range(INIT_START - 1, INIT_END):
        keep[i] = False
    for i in range(FN_START - 1, FN_END):
        keep[i] = False

    new_hub_lines = []
    for i in range(n):
        if not keep[i]:
            # ②段首位置插入 initModalDom() 调用,整段跳过
            if i == INIT_START - 1:
                new_hub_lines.append(b'    initModalDom();')
            continue
        new_hub_lines.append(lines[i])

    joined = b'\r\n'.join(new_hub_lines)

    # 在 dashboardLogs import 后插入 modal import
    anchor_logs = b"import { LogsRowSlot, logsRowSlots, viewBtnLogMap, "
    assert joined.count(anchor_logs) == 1, "dashboardLogs import 锚点非唯一"
    insert_line = b"import { initModalDom, showModal, hideModal } from './dashboardModal';\r\n"
    # 找完整 logs import 行尾部 `from './dashboardLogs';\r\n`
    logs_full_tail = b" } from './dashboardLogs';\r\n"
    assert joined.count(logs_full_tail) == 1, "dashboardLogs import 尾部锚点非唯一"
    joined = joined.replace(logs_full_tail, logs_full_tail + insert_line, 1)

    with io.open(HUB_PATH, 'wb') as f:
        f.write(joined)
    final_hub_lines = joined.split(b'\r\n')
    print(f"[apply] rewrote hub -> {HUB_PATH} ({len(final_hub_lines)} CRLF lines)")


if __name__ == '__main__':
    main()

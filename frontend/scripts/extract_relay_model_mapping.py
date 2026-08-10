# -*- coding: utf-8 -*-
"""
extract_relay_model_mapping.py — Task #9: relayController.ts D 簇抽离 → relayModelMapping.ts

D 簇 = L220-1054(动态号池 Tab + 模型映射配置交互),原位于 `initRelayEvents` 闭包内(4 空格基缩进)。
迁出为模块顶层:整体减 4 空格基缩进,5 个闭包内 `let` 提升为模块作用域(需 initRelayModelMapping 显式重置),
5 个 `window._relay*` 赋值移入 `initRelayModelMapping()` 体内(保留注册时机),`loadModelMappings` 作 export。

协议(沿用已验证 byte-level extractor 流程):
  1. rb 读 relayController.ts → b'\r\n' split → 逐锚点断言 D 簇头/尾 + 5 个 window 赋值行。
  2. 切 L220-1054,逐行去 4 空格基缩进(保留相对缩进;空行置空)。
  3. 拆分:5 个 `window._relay*` 赋值块从主序列抽出,纳入 initRelayModelMapping 体内(再加 4 空格);
     其余 function/interface/let 留模块顶层;5 个带初始值的 `let` 上提到 header 下并从主序列删除避免重复声明。
  4. gated 写新文件 frontend/src/ui/relayModelMapping.ts(wb, CRLF)。
  5. gated 重写 hub:keep mask 置 L220-1054 为 False,join 回 + 在 security 闭合空行后插 initRelayModelMapping() 调用;
     3 处精确 import 替换(删 i18n、插 relayModelMapping import)。
"""
import io
import os
import sys

HUB_PATH = os.path.join('frontend', 'src', 'ui', 'relayController.ts')
SIB_PATH = os.path.join('frontend', 'src', 'ui', 'relayModelMapping.ts')

# D 簇起止(1-based CRLF 行号,含两端)
D_START = 220
D_END = 1054


def E(s):
    """UTF-8 编码一个 unicode 字符串为 bytes(中文安全,避免手写 \\xNN 误码)。"""
    return s.encode('utf-8')


# ===== 锚点断言用(用 E() 避免手写 \\x 误码)=====
H_DSTART = E('    // ========== 动态号池 Tab 与模型映射配置交互 ==========')
H_INTERFACE = b'    interface PoolTabInfo {'
H_FETCH = b'    (window as any)._relayFetchChannelModels = async () => {'
H_ADDTAB = b'    (window as any)._relayAddTab = () => {'
H_DELTAB = b'    (window as any)._relayDeleteCurrentTab = () => {'
H_ADDMAP = b'    (window as any)._relayAddModelMapping = () => {'
H_SAVEMAP = b'    (window as any)._relaySaveModelMapping = async () => {'

INIT_AvailChannels = "['antigravity', 'google', 'gcp', 'nvidia']"


def main():
    dry = '--apply' not in sys.argv
    raw = io.open(HUB_PATH, 'rb').read()
    lines = raw.split(b'\r\n')
    n = len(lines)
    print(f"[info] hub total CRLF lines = {n}")

    # ---- 断言(不靠缓存行号)----
    assert lines[D_START - 1] == H_DSTART, (
        f"D_START L{D_START} mismatch: got {lines[D_START-1]!r}")
    assert lines[D_START] == H_INTERFACE, (
        f"L{D_START + 1} mismatch: got {lines[D_START]!r}")

    def find_line(needle, start, end):
        for i in range(start - 1, end):
            if lines[i] == needle:
                return i + 1
        raise AssertionError(f"not found in [{start},{end}]: {needle!r}")

    ln_fetch = find_line(H_FETCH, D_START, D_END)
    ln_addtab = find_line(H_ADDTAB, D_START, D_END)
    ln_deltab = find_line(H_DELTAB, D_START, D_END)
    ln_addmap = find_line(H_ADDMAP, D_START, D_END)
    ln_savemap = find_line(H_SAVEMAP, D_START, D_END)
    print(f"[anchor] _relayFetchChannelModels @ L{ln_fetch}")
    print(f"[anchor] _relayAddTab           @ L{ln_addtab}")
    print(f"[anchor] _relayDeleteCurrentTab @ L{ln_deltab}")
    print(f"[anchor] _relayAddModelMapping  @ L{ln_addmap}")
    print(f"[anchor] _relaySaveModelMapping @ L{ln_savemap}")
    assert ln_fetch < ln_addtab < ln_deltab < ln_addmap < ln_savemap, "window 赋值顺序异常"

    def match_block_end(start_line):
        """从 start_line 的 `(window ...) = ... => {` 起,brace 平衡定位配对 `};` 闭合行(1-based,含)。"""
        depth = 0
        started = False
        for i in range(start_line - 1, D_END):
            line = lines[i]
            for ch in line:
                if ch == ord('{'):
                    depth += 1
                    started = True
                elif ch == ord('}'):
                    depth -= 1
            if started and depth == 0:
                return i + 1
        raise AssertionError(f"brace balance not found from L{start_line}")

    end_fetch = match_block_end(ln_fetch)
    end_addtab = match_block_end(ln_addtab)
    end_deltab = match_block_end(ln_deltab)
    end_addmap = match_block_end(ln_addmap)
    end_savemap = match_block_end(ln_savemap)
    print(f"[brace] end_fetch={end_fetch} end_addtab={end_addtab} "
          f"end_deltab={end_deltab} end_addmap={end_addmap} end_savemap={end_savemap}")
    assert end_savemap == D_END, f"end_savemap={end_savemap} != D_END={D_END} —— D 簇尾错位"

    # ---- 切 D 簇 L220..1054,逐行去 4 空格基缩进 ----
    def deindent(line_bytes):
        if line_bytes.strip(b' \t') == b'':
            return b''
        if line_bytes.startswith(b'    '):
            return line_bytes[4:]
        return line_bytes

    d_block = [deindent(lines[i]) for i in range(D_START - 1, D_END)]

    def to_block_idx(abs_line):
        return abs_line - D_START

    b_fetch = (to_block_idx(ln_fetch), to_block_idx(end_fetch))
    b_addtab = (to_block_idx(ln_addtab), to_block_idx(end_addtab))
    b_deltab = (to_block_idx(ln_deltab), to_block_idx(end_deltab))
    b_addmap = (to_block_idx(ln_addmap), to_block_idx(end_addmap))
    b_savemap = (to_block_idx(ln_savemap), to_block_idx(end_savemap))
    window_blocks = [b_fetch, b_addtab, b_deltab, b_addmap, b_savemap]

    def in_any_window(idx):
        return any(s <= idx <= e for (s, e) in window_blocks)

    # 模块顶层序列:去掉 window 赋值块行
    top_lines = [line for idx, line in enumerate(d_block) if not in_any_window(idx)]

    # window 赋值块去缩进版(已 deindent)原文
    win_blobs = [b'\r\n'.join(d_block[s:e + 1]) for (s, e) in window_blocks]

    # ---- 头部与模块级 let 初始化(中文用 E(),避免手写 \\x 误码)----
    header = E(
        "/**\n"
        " * relayModelMapping.ts: Relay 模型映射配置子面板动态号池 Tab 与模型映射表格交互。\n"
        " *\n"
        " * 从 relayController.ts initRelayEvents 闭包内 D 簇抽离(原 L220-1054)。原闭包内 function 声明迁为模块私有,\n"
        " * 5 个闭包内 let(allMappings/poolTabs/activeTabId/availableChannels/channelModelsCache)提升为模块作用域,\n"
        " * initRelayModelMapping() 顶部显式重置保持「每次 re-mount 重置」闭包语义(单一语义敏感点)。\n"
        " * 5 个 window._relay* 赋值移入 initRelayModelMapping 体内,经 hub initRelayEvents 在同一 onMounted 时机注册。\n"
        " */\n"
        "import { ipcRenderer } from '../shared/ipc';\n"
        "import state from './dashboardState';\n"
        "import i18n from '../shared/i18n';\n"
        "\n"
        "// ===== 模块作用域状态(每次 initRelayModelMapping 显式重置,保持闭包语义) =====\n"
    ).replace(b'\n', b'\r\n')

    let_init = E(
        "let allMappings: any[] = [];\n"
        "let poolTabs: PoolTabInfo[] = [];\n"
        "let activeTabId: string = 'google';\n"
        f"let availableChannels: string[] = {INIT_AvailChannels};\n"
        "let channelModelsCache: Record<string, string[]> = {};\n"
        "\n"
    ).replace(b'\n', b'\r\n')

    # 删除主序列里 5 个带初始值的 let(已上提到 header 下方,避免重复声明)
    def looks_like_module_let(line):
        return (
            line == b'let allMappings: any[] = [];'
            or line == b'let poolTabs: PoolTabInfo[] = [];'
            or line == b"let activeTabId: string = 'google';"
            or (line.startswith(b'let availableChannels: string[] = ')
                and line.rstrip(b'\r\n').endswith(b';'))
            or line == b'let channelModelsCache: Record<string, string[]> = {};'
        )

    filtered_top = [line for line in top_lines if not looks_like_module_let(line)]

    # loadModelMappings 原 `async function loadModelMappings() {` → 改 export
    new_top = []
    for line in filtered_top:
        if line == b'async function loadModelMappings() {':
            new_top.append(b'export async function loadModelMappings() {')
        else:
            new_top.append(line)

    # init wrapper 头(重置)+ 尾。
    # 去除 new_top 末尾残留的连续空行(原 D 簇里 window 块之间的分隔空行,移除 5 个 window 块后悬空成多余尾空行)。
    while new_top and new_top[-1].strip(b' \t') == b'':
        new_top.pop()
    wrapper_head = E(
        "\n\n"
        "export function initRelayModelMapping() {\n"
        "    // 重置 5 个模块级 let 到初始值(模拟原闭包每次 initRelayEvents 重建语义),避免跨 mount 状态残留。\n"
        "    allMappings = [];\n"
        "    poolTabs = [];\n"
        "    activeTabId = 'google';\n"
        f"    availableChannels = {INIT_AvailChannels};\n"
        "    channelModelsCache = {};\n"
        "\n"
    ).replace(b'\n', b'\r\n')
    wrapper_tail = b'}\r\n'

    # 拼装 sibling:header + let_init + 主序列(去缩进) + wrapper_head + 5 window 块(+4 缩进) + wrapper_tail
    parts = [header, let_init, b'\r\n'.join(new_top), wrapper_head]
    for k, blob in enumerate(win_blobs):
        # window 块在 init 体内,再加 4 空格基缩进(空行不加)
        reindented = b'\r\n'.join(
            (b'    ' + ln if ln.strip(b' \t') != b'' else b'') for ln in blob.split(b'\r\n'))
        parts.append(reindented)
        # blob 之间留 1 空行分隔;末块用单换行接 wrapper 闭合 `(换行})`。
        if k != len(win_blobs) - 1:
            parts.append(b'\r\n\r\n')
        else:
            parts.append(b'\r\n')
    parts.append(wrapper_tail)

    sibling_content = b''.join(parts)
    sib_lines = sibling_content.split(b'\r\n')
    print(f"[gen] sibling CRLF lines = {len(sib_lines)}")
    print(f"[gen] sibling first line = {sib_lines[0]!r}")

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

    # ---- 重写 hub:keep mask 置 L220..1054 为 False ----
    keep = [True] * n
    for i in range(D_START - 1, D_END):
        keep[i] = False
    new_hub_lines = [lines[i] for i in range(n) if keep[i]]

    # 在 security 块闭合 `    }`(L218)后的第一个空行后插入 `    initRelayModelMapping();`
    insert_idx = None
    for i in range(len(new_hub_lines) - 1, 0, -1):
        if new_hub_lines[i] == b'' and new_hub_lines[i - 1] == b'    }':
            insert_idx = i + 1
            break
    assert insert_idx is not None, "未找到 security 块闭合后的空行插入点"
    new_hub_lines = (new_hub_lines[:insert_idx] + [b'    initRelayModelMapping();']
                     + new_hub_lines[insert_idx:])

    joined = b'\r\n'.join(new_hub_lines)

    # (1) 删 i18n import
    assert b"import i18n from '../shared/i18n';\r\n" in joined, "i18n import 锚点缺失"
    joined = joined.replace(b"import i18n from '../shared/i18n';\r\n", b"", 1)

    # (2) 在 `import './relayUserStats';` 后插入 relayModelMapping import
    anchor_userstats = b"import './relayUserStats';\r\n"
    assert joined.count(anchor_userstats) == 1, "relayUserStats import 锚点非唯一"
    insert_line = (b"import { initRelayModelMapping, loadModelMappings } "
                   b"from './relayModelMapping';\r\n")
    joined = joined.replace(anchor_userstats, anchor_userstats + insert_line, 1)

    with io.open(HUB_PATH, 'wb') as f:
        f.write(joined)

    final_hub_lines = joined.split(b'\r\n')
    print(f"[apply] rewrote hub -> {HUB_PATH} ({len(final_hub_lines)} CRLF lines)")


if __name__ == '__main__':
    main()

# -*- coding: utf-8 -*-
"""
extract_settings_proxy.py — Task #10 PR1: settingsController.ts SOCK5 兜底代理 mirror 簇抽离 → settingsProxy.ts

抽离一对镜像 set/get 子簇:
  SET 侧 [322, 439](位于 initSettings 体内,基缩进 8 空格):自定义 SOCKS5 + NVIDIA 断流兜底代理的 change 监听 → ipcRenderer.send('settings:set-*')。
  GET 侧 [705, 786](位于 refreshSettingsUI 体内,基缩进 8 空格):同套 DOM id 的 sendSync 回填。

迁出为同级兄弟 settingsProxy.ts 的两个 standalone export:
  export function bindProxySettings(): void { ... }  ← SET 簇整体减 4 缩进(8→4)
  export function loadProxyState(): void     { ... }  ← GET 簇整体减 4 缩进(8→4)
唯一外部依赖 ipcRenderer(两簇均不读 state/i18n/shell)。

hub 改造:
  - 删 L3 `import i18n from '../shared/i18n';`(dead,全文零引用)。
  - initSettings 体内 [322,439] → 单行 `        bindProxySettings();`(8 空格)。
  - refreshSettingsUI 体内 [705,786] → 单行 `        loadProxyState();`(8 空格)。
  - L1 区插入 `import { bindProxySettings, loadProxyState } from './settingsProxy';`。

协议(沿用 Task#9 已验证 byte-level extractor 流程):
  1. rb 读 hub → b'\\r\\n' split → 逐锚点断言两簇头/尾。
  2. 切簇,逐行去 4 空格基缩进(保留相对缩进;空行置空),分别包进 standalone export function。
  3. gated 写 sibling settingsProxy.ts(wb, CRLF)。dry-run 写 .preview。
  4. gated 重写 hub:keep mask 置两簇区间 False,删簇 + 在 SET 簇原位插 bindProxySettings() 调用、GET 簇原位插 loadProxyState() 调用;
     删 i18n import;插 settingsProxy import。--apply 落盘,默认 dry-run。
"""
import io
import os
import sys

HUB_PATH = os.path.join('frontend', 'src', 'ui', 'settingsController.ts')
SIB_PATH = os.path.join('frontend', 'src', 'ui', 'settingsProxy.ts')

# 簇起止(1-based CRLF 行号,含两端)
SET_START, SET_END = 322, 439
GET_START, GET_END = 705, 786


def E(s):
    """UTF-8 编码 unicode 字符串为 bytes(中文安全,避免手写 \\xNN 误码)。"""
    return s.encode('utf-8')


# ===== 锚点断言(纯 ASCII;簇头是 `        const chkCustomSocks5Enabled = ...`)=====
SET_HEAD_PREFIX = b"        const chkCustomSocks5Enabled = document.getElementById('chkCustomSocks5Enabled')"
SET_MARKER_PREFIX = b'        // ===== NVIDIA'
SET_TAIL = b'        }'
GET_HEAD_PREFIX = SET_HEAD_PREFIX  # GET 簇头同名 const
GET_TAIL = SET_TAIL


def deindent4(line_bytes):
    """去前 4 空格基缩进(8→4);空行置空。不减以非空格开头的行(保守)。"""
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

    # ---- 断言 SET 簇 ----
    assert lines[SET_START - 1].startswith(SET_HEAD_PREFIX), (
        f"SET_START L{SET_START} mismatch: got {lines[SET_START-1]!r}")
    assert lines[376 - 1].startswith(SET_MARKER_PREFIX), (
        f"SET marker L376 mismatch: got {lines[376-1]!r}")
    assert lines[SET_END - 1] == SET_TAIL, (
        f"SET_END L{SET_END} mismatch: got {lines[SET_END-1]!r}")
    # 簇尾后随空行(L440)
    assert lines[SET_END] == b'', f"SET 簇尾 L{SET_END + 1} 应为空行, got {lines[SET_END]!r}"

    # ---- 断言 GET 簇 ----
    assert lines[GET_START - 1].startswith(GET_HEAD_PREFIX), (
        f"GET_START L{GET_START} mismatch: got {lines[GET_START-1]!r}")
    assert lines[743 - 1].startswith(SET_MARKER_PREFIX), (
        f"GET marker L743 mismatch: got {lines[743-1]!r}")
    assert lines[GET_END - 1] == GET_TAIL, (
        f"GET_END L{GET_END} mismatch: got {lines[GET_END-1]!r}")
    assert lines[GET_END] == b'', f"GET 簇尾 L{GET_END + 1} 应为空行, got {lines[GET_END]!r}"

    print(f"[anchor] SET 簇 [L{SET_START}, L{SET_END}] ({SET_END - SET_START + 1} 行)")
    print(f"[anchor] GET 簇 [L{GET_START}, L{GET_END}] ({GET_END - GET_START + 1} 行)")

    # ---- 切簇 + 去缩进 ----
    set_body = [deindent4(lines[i]) for i in range(SET_START - 1, SET_END)]
    get_body = [deindent4(lines[i]) for i in range(GET_START - 1, GET_END)]

    # 去簇内首行后的尾随空行(若有)
    def trim_trailing_blank(body):
        while len(body) > 1 and body[-1].strip(b' \t') == b'':
            body.pop()
        return body

    set_body = trim_trailing_blank(set_body)
    get_body = trim_trailing_blank(get_body)

    # ---- sibling 头部 ----
    header = E(
        "/**\n"
        " * settingsProxy.ts: 自定义 SOCKS5 + NVIDIA 断流兜底出站代理的表单绑定(SET)与回填(GET)。\n"
        " *\n"
        " * 从 settingsController.ts initSettings(L322-439)/ refreshSettingsUI(L705-786) 闭包体抽离,\n"
        " * 两簇字面镜像同一套 DOM id 与 settings:set-*/get-* IPC 通道,唯一外部依赖 ipcRenderer。\n"
        " * 各整体减 4 空格基缩进(函数体基缩进 8→4),无 let 可变状态,幂等重绑/重读,无跨 mount 残留。\n"
        " */\n"
        "import { ipcRenderer } from '../shared/ipc';\n"
        "\n"
    ).replace(b'\n', b'\r\n')

    # SET 函数:头部一行注释 + export function + 去缩进的簇体(无末尾空行)+ 闭合 }
    set_fn = E(
        "// 自定义 SOCKS5 + NVIDIA 兜底代理:逐字段绑 change → ipcRenderer.send('settings:set-*')。\n"
        "export function bindProxySettings(): void {\n"
    ).replace(b'\n', b'\r\n')
    set_fn += b'\r\n'.join(set_body)
    set_fn += b'\r\n}\r\n'

    # GET 函数
    get_fn = E(
        "\n"
        "// 自定义 SOCKS5 + NVIDIA 兜底代理:逐字段 ipcRenderer.sendSync('settings:get-*') 回填。\n"
        "export function loadProxyState(): void {\n"
    ).replace(b'\n', b'\r\n')
    get_fn += b'\r\n'.join(get_body)
    get_fn += b'\r\n}\r\n'

    sibling_content = header + set_fn + get_fn
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

    # ---- 重写 hub:keep mask 置两簇区间 False ----
    keep = [True] * n
    for i in range(SET_START - 1, SET_END):
        keep[i] = False
    for i in range(GET_START - 1, GET_END):
        keep[i] = False

    # 在删簇的同时,在原簇位置插入单行调用(8 空格)。按原行顺序遍历,遇簇头行时插入调用行,簇区间整体跳过。
    new_hub_lines = []
    for i in range(n):
        if not keep[i]:
            # 簇首行位置插入调用;整簇跳过(只插一次:当 i == SET_START-1 或 i == GET_START-1)
            if i == SET_START - 1:
                new_hub_lines.append(b'        bindProxySettings();')
            elif i == GET_START - 1:
                new_hub_lines.append(b'        loadProxyState();')
            continue
        new_hub_lines.append(lines[i])

    joined = b'\r\n'.join(new_hub_lines)

    # (1) 删 i18n import
    i18n_import = b"import i18n from '../shared/i18n';\r\n"
    assert i18n_import in joined, "i18n import 锚点缺失"
    joined = joined.replace(i18n_import, b"", 1)

    # (2) 在 L1 `import { ipcRenderer, shell } from '../shared/ipc';` 后插入 settingsProxy import
    anchor_ipc = b"import { ipcRenderer, shell } from '../shared/ipc';\r\n"
    assert joined.count(anchor_ipc) == 1, "ipc import 锚点非唯一"
    insert_line = b"import { bindProxySettings, loadProxyState } from './settingsProxy';\r\n"
    joined = joined.replace(anchor_ipc, anchor_ipc + insert_line, 1)

    with io.open(HUB_PATH, 'wb') as f:
        f.write(joined)

    final_hub_lines = joined.split(b'\r\n')
    print(f"[apply] rewrote hub -> {HUB_PATH} ({len(final_hub_lines)} CRLF lines)")


if __name__ == '__main__':
    main()

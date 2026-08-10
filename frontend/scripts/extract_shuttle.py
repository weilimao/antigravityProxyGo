#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""accountsController.ts L2735-3097 (双列穿梭框) 抽出为 nvidiaPreferredShuttle.ts。

策略：全文以单一字符串 + 行号锚点操作，避免 CRLF/行号偏移混乱。
  1. 从 controller 读取全文(crlf=True)，按原始行切分(保留 CRLF)。
  2. 精确抽取 4 个散落块，逐字节(含 CRLF)拼到新文件。
  3. controller 用唯一锚点行做块删除 + 单点插入 initNvidiaPreferredShuttle() 调用。

零回归不变量:
  - controller 顶部 import 段不变(穿梭框只靠 ipcRenderer + window.__nvidiaPreferredDict)。
  - accountsRenderer / autotriggerHistory 已有的 import 面与本簇无关 → 无须改外部。
"""
import io

CTRL = 'src/ui/accountsController.ts'
NEW = 'src/ui/nvidiaPreferredShuttle.txt'  # 先写 txt，确认后改 .ts

raw = open(CTRL, 'rb').read()
# 统一按 b'\r\n' 切分，每行保留 \r\n 结尾
parts = raw.split(b'\r\n')
# lines[i] = 文件第 (i+1) 行的纯内容(不含 CRLF)
lines = [p for p in parts]
# 若文件以 \r\n 结尾，split 末尾会多一个 b''；保留尾部空字符串以维持行数一致
trailing_empty = lines[-1] == b''
if trailing_empty:
    lines = lines[:-1]

NL = b'\r\n'

def rng(lo, hi):  # 1-based inclusive
    return [bytes(x) for x in lines[lo-1:hi]]

# ---------- 抽取 4 块 ----------
# A) 声明块 L126-159（含头注释）
decl = rng(126, 159)
# B) handle 赋值 L909-936
asg = rng(909, 936)
# C) 事件绑定 L1252-1275（含头注释）
evt = rng(1252, 1275)
# D) 首屏静默回读 L1425-1427
tail = rng(1425, 1427)
# E) 函数体 L2735-3097
fn = rng(2735, 3097)

# ---------- 构造新文件 ----------
HDR = (
"/**\r\n"
" * NVIDIA 号池全局专属模型清单 Modal（双列穿梭框）模块：从 accountsController.ts 抽离的独立模块。\r\n"
" *\r\n"
" * 高内聚：本模块自带 28 个 DOM handle、4 个模块级状态、26 个函数，\r\n"
" * 对外仅暴露 initNvidiaPreferredShuttle() 一个入口，由 accountsController.initAccountsEvents 统一调用。\r\n"
" * 不依赖 state/i18n/renderAccounts 等跨簇符号；仅用 ipcRenderer 与 window.__nvidiaPreferredDict 注入点。\r\n"
" */\r\n"
"import { ipcRenderer } from '../shared/ipc';\r\n"
"\r\n"
).encode('utf-8')

mid_handles_pre = (
"// 绑定 DOM 句柄（由 accountsController.initAccountsEvents 委托调用，避免命名冲突）\r\n"
"function initNvidiaPreferredShuttleHandles(): void {\r\n"
).encode('utf-8')
mid_handles_post = b"}\r\n\r\n"

mid_events_pre = (
"// 绑定穿梭框按钮事件 + 后端 res 事件订阅（仅绑定一次）\r\n"
"function initNvidiaPreferredShuttleEvents(): void {\r\n"
).encode('utf-8')
mid_events_post = (
"}\r\n"
"\r\n"
"// 穿梭框 Modal 统一初始化套口：DOM 句柄赋值、事件绑定、首屏徽标回读。\r\n"
"export function initNvidiaPreferredShuttle(): void {\r\n"
"    initNvidiaPreferredShuttleHandles();\r\n"
"    initNvidiaPreferredShuttleEvents();\r\n"
).encode('utf-8')
mid_tail_post = (
"}\r\n"
"\r\n"
).encode('utf-8')

def block_bytes(lst):
    return NL.join(lst) + NL

new_content = HDR
new_content += block_bytes(decl)
new_content += b"\r\n"
new_content += mid_handles_pre
new_content += block_bytes(asg)
new_content += mid_handles_post
new_content += mid_events_pre
new_content += block_bytes(evt)
new_content += mid_events_post
new_content += block_bytes(tail)
new_content += mid_tail_post
new_content += block_bytes(fn)

with open(NEW, 'wb') as f:
    f.write(new_content)

print("written temp:", NEW, len(new_content), "bytes")
print("decl lines:", len(decl), "asg lines:", len(asg), "evt lines:", len(evt), "tail lines:", len(tail), "fn lines:", len(fn))

# ---------- 改写 controller ----------
# 用唯一锚点删除 4 块 + 标记插入点。
# 删除块用首行内容做唯一性校验，逐块筛掉对应区间行。
keep = [True] * len(lines)

def kill(lo, hi):
    for i in range(lo-1, hi):
        keep[i] = False

kill(126, 159)   # decl
kill(909, 936)   # asg
kill(1252, 1275) # evt
kill(1425, 1427) # tail
kill(2735, 3097) # fn

kept = [lines[i] for i in range(len(lines)) if keep[i]]

# 插入 import + init 调用。
# 1) import: 追加到既有 import 块末。controller 原 import 块尾是 L18 'import * as hitRateFilter from ...'
#    其后是 L20 async function exportAccountConfig。我们在 hitRateFilter 行后插一行。
# 2) init 调用: 原 initAccountsEvents() 在 handle 赋值之后会绑定 sessionBindings/refreshAllQuota，
#    然后是 filter 分页绑定；再之后是各弹窗绑定。删块后 nvidiaPreferred 段已空。
#    我们把 initNvidiaPreferredShuttle() 插在 btnAddNvidiaAccount 赋值之后 campagin 位置：原 L938 btnExportAccounts 之前更稳。
#    实际更简单：插到 initAccountsEvents() 结束 '}' 前（即 accounts:get/initAutoTriggerModalEvents 之前）—— 与原 tail 调用点等价。
#    用 '    ipcRenderer.send(\'accounts:get\');' 锚点，在其前一空行处插入。

# 用字符串定位
text = NL.join(kept)
text_str = text.decode('utf-8', errors='surrogateescape')

# 1) import 注入
import_anchor = "import * as hitRateFilter from './hitRateFilter';"
assert import_anchor in text_str, "import anchor not found"
import_inject = import_anchor + "\r\nimport { initNvidiaPreferredShuttle } from './nvidiaPreferredShuttle';"
text_str = text_str.replace(import_anchor, import_inject, 1)

# 2) init 调用注入：在 initAccountsEvents 末尾 ipcRenderer.send('accounts:get') 之前
#    原：\r\n    // 首屏静默回读...（已删）\r\n    ipcRenderer.send('accounts:get');\r\n    initAutoTriggerModalEvents();
#    现在删块后形如：\r\n    \r\n    ipcRenderer.send('accounts:get');\r\n    initAutoTriggerModalEvents();
init_anchor = "    ipcRenderer.send('accounts:get');"
assert init_anchor in text_str, "init anchor not found"
init_inject = "    // NVIDIA 专属模型清单穿梭框：句柄赋值 + 事件绑定 + 首屏徽标回读（已抽离 nvidiaPreferredShuttle.ts）\r\n    initNvidiaPreferredShuttle();\r\n\r\n" + init_anchor
text_str = text_str.replace(init_anchor, init_inject, 1)

out_bytes = text_str.encode('utf-8', errors='surrogateescape')
if not out_bytes.endswith(b'\r\n'):
    out_bytes += b'\r\n'
with open(CTRL, 'wb') as f:
    f.write(out_bytes)

print("controller rewritten:", len(out_bytes), "bytes; kept lines:", len(kept), "/ orig:", len(lines))

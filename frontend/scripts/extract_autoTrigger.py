#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""accountsController.ts 自动化任务包 Modal (autoTrigger) 簇抽出为 autoTrigger.ts。

3 块:
  B1 声明池      L149..177   (// 自动化任务包 Modal 变量定义 .. btnSaveTask)
  B2 常量+init   L1976..2083 (section注释 + AUTO_MODELS* + initAutoTriggerModalEvents)
  B3 函数体      L2654..2960 (openAutoTriggerModal..saveAutoTriggerTask,EOF)

依赖: state/ipcRenderer/$confirm(全局)/alert(window). 不依赖跨簇 handle。
外部 contract: autotriggerHistoryController.ts 从本 controller 导入 switchAutoTriggerPanel
  → 抽离后 controller 必须 re-export switchAutoTriggerPanel(从 ./autoTrigger)保持零改动。
controller 内 initAccountsEvents 末尾 initAutoTriggerModalEvents() 调用改为新文件 export。
"""
CTRL = 'src/ui/accountsController.ts'
NEW = 'src/ui/autoTrigger.ts'
NL = b'\r\n'

raw = open(CTRL, 'rb').read()
parts = raw.split(b'\r\n')
lines = [p for p in parts]
if lines[-1] == b'':
    lines = lines[:-1]

def rng(lo, hi):
    return [bytes(x) for x in lines[lo-1:hi]]

b1 = rng(149, 177)   # // 自动化任务包 Modal 变量定义 .. let btnSaveTask
b2 = rng(1976, 2083) # // == 自动化定时... ==  .. initAutoTriggerModalEvents 结束 }
b3 = rng(2654, 2960) # openAutoTriggerModal .. saveAutoTriggerTask 结束 }

# 校验锚点
assert b1[0].decode('utf-8').startswith('// 自动化任务包 Modal'), "B1 anchor mismatch"
assert '自动化定时与刷新任务 Modal 控制逻辑' in b2[0].decode('utf-8'), "B2 anchor mismatch"
assert b'function openAutoTriggerModal' in b3[0], "B3 anchor mismatch"
assert any(b'function saveAutoTriggerTask' in x for x in b3), "B3 tail anchor mismatch"

HDR = (
"/**\r\n"
" * 自动化定时与刷新任务包 Modal 控制逻辑：从 accountsController.ts 抽离的独立模块。\r\n"
" *\r\n"
" * 高内聚：本模块承担自动化触发任务包的列表渲染、新建/编辑表单填充、保存与状态切换；\r\n"
" * 自带 27 个 DOM handle、3 个模型常量与 9 个函数，由 accountsController.initAccountsEvents\r\n"
" * 委托调用 initAutoTriggerModalEvents()；switchAutoTriggerPanel 被 controller re-export，\r\n"
" * 保持 autotriggerHistoryController.ts 既有的 import 面零改动。\r\n"
" * 依赖：state（语言+账号列表）、ipcRenderer、全局 $confirm / alert；无跨簇 handle 引用。\r\n"
" */\r\n"
"import { ipcRenderer } from '../shared/ipc';\r\n"
"import state from './dashboardState';\r\n"
"\r\n"
).encode('utf-8')

new = HDR
new += NL.join(b1) + NL      # 声明池
new += b"\r\n"
# B2 块首是 section 注释 '== 自动化... ==',逐字节
new += NL.join(b2) + NL
new += b"\r\n"
new += NL.join(b3) + NL
if not new.endswith(NL):
    new += NL

with open(NEW, 'wb') as f:
    f.write(new)
print("written", NEW, len(new), "bytes; b1=%d b2=%d b3=%d" % (len(b1), len(b2), len(b3)))

# ---------- 改写 controller ----------
keep = [True] * len(lines)
def kill(lo, hi):
    for i in range(lo-1, hi):
        keep[i] = False
kill(149, 177)
kill(1976, 2083)
kill(2654, 2960)
kept = [lines[i] for i in range(len(lines)) if keep[i]]
text = NL.join(kept).decode('utf-8', errors='surrogateescape')

# 1) import 注入:autoTrigger + re-export switchAutoTriggerPanel。挂在既有 nvidiaPreferredShuttle import 之后。
anchor_imp = "import { initNvidiaPreferredShuttle, setNvidiaPreferredModelsButtonVisible } from './nvidiaPreferredShuttle';"
assert anchor_imp in text, "import anchor not found"
inject_imp = anchor_imp + "\r\nimport { initAutoTriggerModalEvents, switchAutoTriggerPanel } from './autoTrigger';\r\nexport { switchAutoTriggerPanel } from './autoTrigger';"
text = text.replace(anchor_imp, inject_imp, 1)

# 2) initAutoTriggerModalEvents() 调用:旧定义在 controller 已删,initAccountsEvents 末尾调用语句保留("    initAutoTriggerModalEvents();" 行原本就在 initAccountsEvents 内,未删)。
#    删 B2 时删的是“定义体”(L1976-2083),initAccountsEvents 内的“调用行”(原 L1349 "    initAutoTriggerModalEvents();") 在 L1349 仍在 keeper 中,且名字相同 → 自动命中新 import。无需改。

# 校验调用行保留
assert "    initAutoTriggerModalEvents();" in text, "init call missing"

out = text.encode('utf-8', errors='surrogateescape')
if not out.endswith(NL):
    out += NL
with open(CTRL, 'wb') as f:
    f.write(out)
print("controller rewritten:", len(out), "bytes; kept lines:", len(kept), "/ orig:", len(lines))

#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""accountsController.ts 会话绑定 Modal (sessionBindings) 簇抽出为 sessionBindingsModal.ts。

块:
  B0 handle声明池  L139..145  (btnShowSessionBindings .. sessionBindingsCount)
  B1 事件绑定块   L842..861  (句柄赋值 + 4 个 addEventListener)
  B2 函数体       L1389..1585 (loadSessionBindings/showSessionBindings/hideSessionBindings/clearAllSessionBindings)

依赖: state/ipcRenderer/i18n/$confirm(全局). 无跨簇函数依赖。
window.startLogin/startProjectLogin 注册行(L1588-1590)保留在 controller(与弹窗无关)。
"""
CTRL = 'src/ui/accountsController.ts'
NEW = 'src/ui/sessionBindingsModal.ts'
NL = b'\r\n'

raw = open(CTRL, 'rb').read()
parts = raw.split(b'\r\n')
lines = [p for p in parts]
if lines[-1] == b'':
    lines = lines[:-1]

def rng(lo, hi):
    return [bytes(x) for x in lines[lo-1:hi]]

b0 = rng(139, 145)
b1 = rng(842, 861)
b2 = rng(1389, 1585)

# 锚点校验
assert b0[0].strip(b' ').startswith(b'let btnShowSessionBindings'), "B0 anchor: %r" % b0[0]
assert b"btnShowSessionBindings = document.getElementById" in b1[0], "B1 head anchor: %r" % b1[0]
assert b1[-1].strip() == b'}', "B1 tail anchor: %r" % b1[-1]
assert b'async function loadSessionBindings' in b2[0], "B2 head anchor: %r" % b2[0]
assert b2[-1].strip() == b'}', "B2 tail anchor: %r" % b2[-1]

HDR = (
"/**\r\n"
" * 会话路由绑定 Modal 控制逻辑：从 accountsController.ts 抽离的独立模块。\r\n"
" *\r\n"
" * 高内聚：承担会话路由绑定关系的列表加载、弹窗显隐、批量清空；\r\n"
" * 自带 7 个 DOM handle、4 个函数，由 accountsController.initAccountsEvents 委托\r\n"
" * 调用 initSessionBindingsModalEvents() 完成句柄赋值与事件绑定。\r\n"
" * 依赖：state（当前通道）、ipcRenderer、i18n、全局 $confirm；无跨簇函数依赖。\r\n"
" */\r\n"
"import { ipcRenderer } from '../shared/ipc';\r\n"
"import state from './dashboardState';\r\n"
"import i18n from '../shared/i18n';\r\n"
"\r\n"
).encode('utf-8')

new = HDR
new += NL.join(b0) + NL
new += b"\r\n"
pre_init = ('// 句柄赋值 + 事件绑定（由 accountsController.initAccountsEvents 委托调用）\r\n'
           'export function initSessionBindingsModalEvents(): void {\r\n')
post_init = '}\r\n\r\n'
new += pre_init.encode('utf-8')
new += NL.join(b1) + NL
new += post_init.encode('utf-8')
new += NL.join(b2) + NL
if not new.endswith(NL):
    new += NL

with open(NEW, 'wb') as f:
    f.write(new)
print("written", NEW, len(new), "bytes; b0=%d b1=%d b2=%d" % (len(b0), len(b1), len(b2)))

# ---------- 改写 controller ----------
keep = [True] * len(lines)
def kill(lo, hi):
    for i in range(lo-1, hi):
        keep[i] = False
kill(139, 145)
kill(842, 861)
kill(1389, 1585)
kept = [lines[i] for i in range(len(lines)) if keep[i]]
text = NL.join(kept).decode('utf-8', errors='surrogateescape')

# import 注入
anchor_imp = "import { initTriggerTestModalEvents, appendTriggerTestLog } from './triggerTestModal';"
assert anchor_imp in text, "import anchor not found"
inject_imp = anchor_imp + "\r\nimport { initSessionBindingsModalEvents } from './sessionBindingsModal';"
text = text.replace(anchor_imp, inject_imp, 1)

# init 调用注入:在 initAccountsEvents 内原 B1 之后,refreshAllQuota 绑定之前。
# B1 删除后,原位置(在 btnImportAccounts 赋值之后 L840)留下空行 + btnRefreshAllQuota 绑定。
# 我们在 "    if (btnRefreshAllQuota) {" 之前插入 init 调用。
anchor_call = "    if (btnRefreshAllQuota) {"
assert text.count(anchor_call) >= 1, "call anchor not found"
inject_call = ('    // 会话绑定 Modal：句柄赋值 + 事件绑定（已抽离 sessionBindingsModal.ts）\r\n'
               '    initSessionBindingsModalEvents();\r\n\r\n' + anchor_call)
text = text.replace(anchor_call, inject_call, 1)

out = text.encode('utf-8', errors='surrogateescape')
if not out.endswith(NL):
    out += NL
with open(CTRL, 'wb') as f:
    f.write(out)
print("controller rewritten:", len(out), "bytes; kept lines:", len(kept), "/ orig:", len(lines))

#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""accountsController.ts 触发测试回复 Modal (triggerTest) 簇抽出为 triggerTestModal.ts。

5 块:
  B0 handle声明池  L133..146  (// 触发测试回复 Modal 变量定义 .. let triggerModalAccountCount)
  B1 事件绑定块   L1260..1302 (// 触发测试回复按钮绑定 .. btnStartTriggerTest绑定)
  B2 函数体       L1697..1945 (triggerTestResponse .. renderTriggerResultsTable 结束})

依赖: state/ipcRenderer/i18n/loadAccountQuota/updateBatchActionBarUI.
跨簇: loadAccountQuota 来自 accountsRenderer; updateBatchActionBarUI 来自本 controller(仍 export)。
  → init 入口由本模块自含,init 内 attach 按钮调用 triggerTestResponse/inc 路径。
外部调用: triggerTestResponse 仅被 controller initAccountsEvents 中 btnTrigger(idx按原 L1264)
  调用 → init 入口导出 initTriggerTestModalEvents 委托绑定即可,函数本身不必导出。
"""
CTRL = 'src/ui/accountsController.ts'
NEW = 'src/ui/triggerTestModal.ts'
NL = b'\r\n'

raw = open(CTRL, 'rb').read()
parts = raw.split(b'\r\n')
lines = [p for p in parts]
if lines[-1] == b'':
    lines = lines[:-1]

def rng(lo, hi):
    return [bytes(x) for x in lines[lo-1:hi]]

b0 = rng(134, 147)   # // 触发测试回复 Modal 变量定义 .. triggerModalAccountCount
b1 = rng(1261, 1302) # // 触发测试回复按钮绑定 .. btnStartTriggerTest 绑定尾 }
b2 = rng(1697, 1943) # triggerTestResponse() .. renderTriggerResultsTable() 结束 }

# 校验锚点
assert b0[0].decode('utf-8').startswith('// 触发测试回复 Modal'), "B0 anchor: %r" % b0[0]
assert '触发测试回复按钮绑定' in b1[0].decode('utf-8'), "B1 anchor: %r" % b1[0]
assert b'function triggerTestResponse' in b2[0], "B2 head anchor"
# B2 尾:renderTriggerResultsTable 函数体最后一行应为 '}'。我们取 b2 末元素应为 '}'。
assert b2[-1].strip() == b'}', "B2 tail anchor: %r" % b2[-1]

HDR = (
"/**\r\n"
" * 触发测试回复 Modal 控制逻辑：从 accountsController.ts 抽离的独立模块。\r\n"
" *\r\n"
" * 高内聚：承担批量选号 → 触发上游模型回复测试 → 渲染结果表的弹窗生命周期；\r\n"
" * 自带 14 个 DOM handle、5 个函数，由 accountsController.initAccountsEvents 委托\r\n"
" * 调用 initTriggerTestModalEvents() 完成 DOM 句柄赋值与事件绑定。\r\n"
" * 依赖：state（选中账号）、ipcRenderer、i18n；跨模块回调 loadAccountQuota（accountsRenderer）\r\n"
" * 与 updateBatchActionBarUI（accountsController 仍在用）由 init 时注入,避免循环 import。\r\n"
" */\r\n"
"import { ipcRenderer } from '../shared/ipc';\r\n"
"import state from './dashboardState';\r\n"
"import i18n from '../shared/i18n';\r\n"
"import { loadAccountQuota } from './accountsRenderer';\r\n"
"\r\n"
).encode('utf-8')

new = HDR
new += NL.join(b0) + NL
new += b"\r\n"
# B1 绑定块原为 initAccountsEvents 内的内联块,改造为 init 函数体;B1 已 4 空格缩进,直接包进 init 函数。
pre_init = '// 句柄赋值 + 事件绑定（由 accountsController.initAccountsEvents 委托调用）\r\nexport function initTriggerTestModalEvents(): void {\r\n'
post_init = '}\r\n\r\n'
new += pre_init.encode('utf-8')
new += NL.join(b1) + NL
new += post_init.encode('utf-8')
# B2 函数体:逐字节搬(triggerTestResponse 是 local init 内被引用,无需导出)。
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
kill(134, 147)
kill(1261, 1302)
kill(1697, 1943)
kept = [lines[i] for i in range(len(lines)) if keep[i]]
text = NL.join(kept).decode('utf-8', errors='surrogateescape')

# 1) import 注入
anchor_imp = "export { switchAutoTriggerPanel } from './autoTrigger';"
assert anchor_imp in text, "import anchor not found"
inject_imp = anchor_imp + "\r\nimport { initTriggerTestModalEvents } from './triggerTestModal';"
text = text.replace(anchor_imp, inject_imp, 1)

# 2) 删 B1 后,initAccountsEvents 内原 B1 块位置(在 btnTrigger .. btnStartTriggerTest 绑定尾)} 处后续紧跟
#   "// 当 DOM 挂载时..."。我们在删除处插入一次 init 调用替代被删的内联块。
# 删 B1 块后,B1 上一行是 updateBatchActionBarUI 的 '});' (L1258 区);B1 块尾 b1 最后一行 '}' (btnStartTriggerTest 绑定)。
# 删整个块后 initAccountsEvents 内会缺这段绑定。我们在 initAccountsEvents 在 "// 当 DOM 挂载时" 之前插入一次调用。
anchor_call = "    // 当 DOM 挂载时，根据已经同步过的 accounts 数据对 DOM 状态进行一轮初始化"
assert anchor_call in text, "call anchor not found"
inject_call = ('    // 触发测试回复 Modal：句柄赋值 + 事件绑定（已抽离 triggerTestModal.ts）\r\n'
               '    initTriggerTestModalEvents();\r\n\r\n' + anchor_call)
text = text.replace(anchor_call, inject_call, 1)

out = text.encode('utf-8', errors='surrogateescape')
if not out.endswith(NL):
    out += NL
with open(CTRL, 'wb') as f:
    f.write(out)
print("controller rewritten:", len(out), "bytes; kept lines:", len(kept), "/ orig:", len(lines))

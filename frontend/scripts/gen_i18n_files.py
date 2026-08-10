"""逐行原样复制拆分 i18n.ts → shared/i18n/<ns>.ts + index.ts。
不重新构造 value 字面量，整行复制原始行（避免双层引号/转义破坏）。
依赖 scripts/split_i18n.py 产出的 /tmp/ns.json（key 顺序归类）。"""
import json
import re
from pathlib import Path

src_lines = open('src/shared/i18n.ts', encoding='utf-8').read().splitlines()
ns = json.loads(Path('/tmp/ns.json').read_text(encoding='utf-8'))

# 把上一轮 UNCLASSIFIED 的两个补进 accounts
for extra in ('editAccountTitle', 'manageKeys'):
    if extra not in ns['accounts']:
        ns['accounts'].append(extra)


def parse_block(lo, hi):
    """返回 {key: 去前导空格后的整行(含尾逗号)}，并保留每条顺序。"""
    items = []  # (key, raw_line)
    for i in range(lo - 1, hi):
        line = src_lines[i]
        s = line.strip()
        if not s or s.startswith('//') or s in ('{', '}', '},'):
            continue
        m = re.match(r'^([a-zA-Z_][a-zA-Z0-9_]*):', s)
        if m:
            # 统一改为 4 空格缩进，保留原始 value 段（: 之后整段）
            key = m.group(1)
            items.append((key, '    ' + s))
    return items


zh_items = parse_block(17, 858)
en_items = parse_block(860, 1705)
zh_line_map = dict(zh_items)
en_line_map = dict(en_items)

# en 块首行是 `en: {`,parse_block 已正确跳过,但首 token `en:` 的 key 误判为 'en'。这里在 parse 后过滤。
en_items = [(k, l) for k, l in en_items if k not in ('zh', 'en')]
en_keys = [k for k, _ in en_items]
en_line_map = dict(en_items)

# 校验
zh_keys = [k for k, _ in zh_items]
en_keys = [k for k, _ in en_items]
assert set(zh_keys) == set(en_keys), ("key 集合不一致",
                                      set(zh_keys) ^ set(en_keys))

OUT = Path('src/shared/i18n')
OUT.mkdir(parents=True, exist_ok=True)

files_spec = [
    ('common', 'common'),
    ('dashboard', 'dashboard'),
    ('settings', 'settings'),
    ('relay', 'relay'),
    ('nvidia', 'nvidia'),
    ('other', 'other'),
    ('accounts', 'accounts'),
    ('otp', 'otp'),
    ('packets', 'packets'),
    ('pricing', 'pricing'),
    ('autoTrigger', 'autoTrigger'),
    ('usage', 'usage'),
    ('help', 'help'),
    ('requestlog', 'requestlog'),
    ('aggregate', 'aggregate'),
]


def emit_ns(keys, line_map, var_name):
    lines = [f'export const {var_name}: Record<string, string> = {{']
    for k in keys:
        if k in line_map:
            lines.append(line_map[k])
    lines.append('};')
    return lines


# 跨命名空间 key 冲突检测（dev 期）：用计数器
zh_key_count = {}
for k in zh_keys:
    zh_key_count[k] = zh_key_count.get(k, 0) + 1


exports_list = []
total_zh = 0
for ns_name, fname in files_spec:
    keys = ns[ns_name]
    zh_lines = emit_ns(keys, zh_line_map, f'{fname}Zh')
    en_lines = emit_ns(keys, en_line_map, f'{fname}En')
    content = (
        '/**\n'
        f' * i18n 命名空间: {ns_name}\n'
        ' * 由 scripts/split_i18n.py + scripts/gen_i18n_files.py 从 shared/i18n.ts 拆分生成，保持 zh 原始顺序与原始 value 字面量。\n'
        ' */\n'
        + '\n'.join(zh_lines) + '\n\n'
        + '\n'.join(en_lines) + '\n'
    )
    (OUT / f'{fname}.ts').write_text(content, encoding='utf-8', newline='\r\n')
    exports_list.append(fname)
    total_zh += len(keys)
    print(f"written {fname}.ts  zh/en:{len(keys)}")

# index.ts：用 replace 注入，避免 f-string 花括号冲突
import_str = '\n'.join(
    f"import {{ {f}Zh, {f}En }} from './{f}';" for f in exports_list)
spread_zh = '\n'.join(f"    ...{f}Zh," for f in exports_list)
spread_en = '\n'.join(f"    ...{f}En," for f in exports_list)

index_template = """/**
 * i18n 聚合入口：合并各命名空间为 {zh:..., en:...} 形态。
 * 原 shared/i18n.ts 现 re-export 此处，保持所有 `import i18n from '../shared/i18n'` 零改动。
 *
 * 类型契约与原文件一致：I18nDictionary / Translations。
 * dev 模式断言：聚合前检测跨命名空间 key 重复（后者会静默覆盖前者，丢失文案）。
 */
__IMPORTS__

export interface I18nDictionary {
    [key: string]: string;
}

export interface Translations {
    zh: I18nDictionary;
    en: I18nDictionary;
    [key: string]: I18nDictionary;
}

// dev 模式断言：检测聚合前跨命名空间 key 冲突
function assertNoKeyOverlap(namespaces: Array<{ name: string; dict: Record<string, string> }>, label: string): void {
    const seen = new Map<string, string>();
    const dupes: string[] = [];
    for (const nsEl of namespaces) {
        for (const k of Object.keys(nsEl.dict)) {
            const prev = seen.get(k);
            if (prev !== undefined) {
                dupes.push(`${k} (in ${prev} and ${nsEl.name})`);
            } else {
                seen.set(k, nsEl.name);
            }
        }
    }
    if (dupes.length > 0) {
        // 重复 key 在 spread 合并时会静默丢失，仅保留最后出现的值
        console.warn(`[i18n] ${label}: duplicate keys across namespaces:`, dupes.join('; '));
    }
}

export const translations: Translations = {
    zh: {
__SPREAD_ZH__
    },
    en: {
__SPREAD_EN__
    },
};

assertNoKeyOverlap(
    [
__NAMESPACE_LIST__
    ],
    'translations'
);

export default translations;
export type { I18nDictionary, Translations };
"""

namespace_list = '\n'.join(
    f"        {{ name: '{f}', dict: {f}Zh }},\n        {{ name: '{f}', dict: {f}En }}," for f in exports_list
)

index_content = (index_template
                 .replace('__IMPORTS__', import_str)
                 .replace('__SPREAD_ZH__', spread_zh)
                 .replace('__SPREAD_EN__', spread_en)
                 .replace('__NAMESPACE_LIST__', namespace_list))

(OUT / 'index.ts').write_text(index_content, encoding='utf-8', newline='\r\n')
print(f"\nwritten index.ts")
print(f"total zh classified keys: {total_zh} / {len(zh_keys)}")

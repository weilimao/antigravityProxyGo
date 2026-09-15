export type DiffKind = 'new' | 'stale';

export interface MappingLike {
    clientModel?: string;
    targetModel?: string;
    [k: string]: any;
}

function normalizeLower(s: string): string {
    return (s || '').trim().toLowerCase();
}

export function computeRemoteAddedLower(current: string[], snapshot: string[]): Set<string> {
    const out = new Set<string>();
    if (!current || current.length === 0) return out;
    const snapSet = new Set<string>();
    for (const s of snapshot || []) {
        const k = normalizeLower(s);
        if (k) snapSet.add(k);
    }
    for (const c of current) {
        const k = normalizeLower(c);
        if (k && !snapSet.has(k)) out.add(k);
    }
    return out;
}

export function computeStaleMappings(mappings: MappingLike[], liveRemote: string[]): MappingLike[] {
    if (!liveRemote || liveRemote.length === 0) return [];
    const liveSet = new Set<string>();
    for (const r of liveRemote) {
        const k = normalizeLower(r);
        if (k) liveSet.add(k);
    }
    const stale: MappingLike[] = [];
    for (const m of mappings || []) {
        const tm = normalizeLower((m && m.targetModel) || '');
        if (!tm) continue; 
        if (!liveSet.has(tm)) stale.push(m);
    }
    return stale;
}

export function shouldMarkStale(mapping: MappingLike, liveSetLower: Set<string>): boolean {
    if (!liveSetLower || liveSetLower.size === 0) return false;
    const tm = normalizeLower((mapping && mapping.targetModel) || '');
    if (!tm) return false;
    return !liveSetLower.has(tm);
}

export function shouldMarkNew(targetModel: string, addedSetLower: Set<string>): boolean {
    if (!addedSetLower || addedSetLower.size === 0) return false;
    const tm = normalizeLower(targetModel || '');
    if (!tm) return false;
    return addedSetLower.has(tm);
}

export function buildLiveSetLower(liveRemote: string[]): Set<string> {
    const s = new Set<string>();
    if (!liveRemote) return s;
    for (const r of liveRemote) {
        const k = normalizeLower(r);
        if (k) s.add(k);
    }
    return s;
}

export function buildStaleConfirmPrompt(staleToRemove: MappingLike[]): string {
    const head = `确定要清理以下 ${staleToRemove.length} 个远端已失效的映射吗？\n(这说明该组/上游的模型列表里已不再包含它们)\n\n`;
    const lines = staleToRemove.slice(0, 10).map(m => ` • ${m.clientModel || ''} -> ${m.targetModel || ''}`);
    const tail = staleToRemove.length > 10 ? `\n...等共 ${staleToRemove.length} 项` : '';
    return head + lines.join('\n') + tail;
}

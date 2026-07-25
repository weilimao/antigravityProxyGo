/**
 * 浏览器环境最小存根：在 Node 中为 tsx/ESM 预加载。
 * 仅用于离线纯函数断言脚本，使 dashboardState 顶层对 localStorage 的访问无害。
 * 通过 `tsx --import scripts/verify-usage-tabs.preload.ts ...` 先于被测模块执行。
 */
type Store = Record<string, string>;
const mem: Store = {};
const StorageStub = {
    getItem: (k: string) => (k in mem ? mem[k] : null),
    setItem: (k: string, v: string) => { mem[k] = String(v); },
    removeItem: (k: string) => { delete mem[k]; },
    clear: () => { for (const k of Object.keys(mem)) delete mem[k]; },
    key: (_i: number) => null,
    get length() { return Object.keys(mem).length; },
};
const g = globalThis as any;
if (!g.localStorage) g.localStorage = StorageStub;
if (!g.sessionStorage) g.sessionStorage = StorageStub;
if (!g.document) {
    g.document = { getElementById: () => null };
}
if (!g.window) g.window = g;

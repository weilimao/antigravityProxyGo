/**
 * 广告位数据服务 (AdService)
 * 
 * 职责:
 * 1. 从 ProxySubForClash 后端拉取公开免鉴权广告列表 (无需 Token);
 * 2. 具备 3 秒超时隔离与多端口回退 (8090 -> 8000) 容错机制;
 * 3. 内存与本地轻量缓存, 服务不可达时提供优雅降级默认宣传项, 保证客户端界面流畅秒开。
 */

export interface AdvertisementItem {
  id: number;
  title: string;
  title_en: string;
  image_url: string;
  target_url: string;
  redirect_url: string;
  position: string;
  sort: number;
}

// 默认备选降级轮播项 (当后端离线或未就绪时平滑展示)
const FALLBACK_ADS: AdvertisementItem[] = [
  {
    id: 9001,
    title: "CrossLink Pro 全球超快智能节点上线",
    title_en: "CrossLink Pro Global Ultra-fast Nodes Online",
    image_url: "https://images.unsplash.com/photo-1558494949-ef010cbdcc31?w=1200&auto=format&fit=crop&q=80",
    target_url: "https://crosslinkdev.com",
    redirect_url: "https://crosslinkdev.com",
    position: "top_banner",
    sort: 100,
  },
  {
    id: 9002,
    title: "新用户首充专享特惠 · 极速大带宽低延迟",
    title_en: "Exclusive New User Special Offer · Ultra Low Latency",
    image_url: "https://images.unsplash.com/photo-1618005182384-a83a8bd57fbe?w=1200&auto=format&fit=crop&q=80",
    target_url: "https://crosslinkdev.com/#pricing",
    redirect_url: "https://crosslinkdev.com/#pricing",
    position: "top_banner",
    sort: 90,
  },
  {
    id: 9003,
    title: "独享纯净静态住宅 IP · 原生海外电商/社媒环境",
    title_en: "Dedicated Static Residential IPs for Global Business",
    image_url: "https://images.unsplash.com/photo-1526374965328-7f61d4dc18c5?w=1200&auto=format&fit=crop&q=80",
    target_url: "https://crosslinkdev.com/#residential",
    redirect_url: "https://crosslinkdev.com/#residential",
    position: "top_banner",
    sort: 80,
  },
];

let cachedAds: AdvertisementItem[] | null = null;
let lastFetchTime = 0;
const CACHE_TTL_MS = 60 * 1000; // 缓存有效周期 1 分钟

/**
 * 获取可候选的服务端基地址列表
 */
function getCandidateBaseUrls(): string[] {
  const customUrl = localStorage.getItem("proxysub_api_base");
  const candidates: string[] = [];
  if (customUrl && customUrl.trim()) {
    candidates.push(customUrl.trim().replace(/\/+$/, ""));
  }
  // 默认候选列表：优先 8090 (平台标准接入端口)，其次 8000 (FastAPI 本地开发直通端口)
  candidates.push("http://127.0.0.1:8090");
  candidates.push("http://localhost:8090");
  candidates.push("http://127.0.0.1:8000");
  candidates.push("http://localhost:8000");
  return Array.from(new Set(candidates));
}

/**
 * 单地址请求尝试 (带超时控制)
 */
async function fetchWithTimeout(url: string, timeoutMs = 3000): Promise<Response> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const res = await fetch(url, {
      method: "GET",
      headers: {
        "Accept": "application/json",
      },
      signal: controller.signal,
    });
    return res;
  } finally {
    clearTimeout(timer);
  }
}

/**
 * 拉取广告位列表 (无需 Token，自动降级与缓存)
 */
export async function fetchTopBannerAds(forceRefresh = false): Promise<AdvertisementItem[]> {
  const now = Date.now();
  if (!forceRefresh && cachedAds && cachedAds.length > 0 && now - lastFetchTime < CACHE_TTL_MS) {
    return cachedAds;
  }

  const baseUrls = getCandidateBaseUrls();

  for (const base of baseUrls) {
    try {
      const endpoint = `${base}/api/v1/advertisements?position=top_banner`;
      const resp = await fetchWithTimeout(endpoint, 2500);
      if (resp.ok) {
        const data = await resp.json();
        if (Array.isArray(data) && data.length > 0) {
          cachedAds = data;
          lastFetchTime = now;
          return data;
        }
      }
    } catch {
      // 网络超时或端口未开启，静默尝试下一个候选地址
    }
  }

  // 若后端均不可达但有历史缓存，使用历史缓存；否则使用内置默认降级广告
  if (cachedAds && cachedAds.length > 0) {
    return cachedAds;
  }
  return FALLBACK_ADS;
}

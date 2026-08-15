import state from './dashboardState';

export function formatJsonText(text: any): string {
    if (!text) return '';
    if (typeof text === 'object') return JSON.stringify(text, null, 2);
    try {
        return JSON.stringify(JSON.parse(text), null, 2);
    } catch (e) {
        return text;
    }
}

export function resolvePacketSource(p: any): string {
    if (!p) return '未知';
    let ua = '';
    if (p.reqHeaders && typeof p.reqHeaders === 'object') {
        for (const key of Object.keys(p.reqHeaders)) {
            if (key.toLowerCase() === 'user-agent') {
                const val = p.reqHeaders[key];
                ua = Array.isArray(val) ? val[0] : (typeof val === 'string' ? val : '');
                break;
            }
        }
    }
    const uaLower = ua.toLowerCase();
    if (uaLower) {
        // 1. Agent 优先匹配（防止带有 aidev_client 等通用附注的 Hub 流量误判为 CLI）
        if (uaLower.includes('antigravity/hub') || uaLower.includes('antigravityproxy-')) {
            return 'Agent';
        }
        // 2. IDE 识别（包含官方 IDE、VS Code 客户端、JetBrains 插件等）
        if (uaLower.includes('antigravity/ide') || uaLower.includes('vscode_client') ||
            uaLower.includes('cloudaicompanion') || uaLower.includes('google-api-nodejs-client') ||
            uaLower.includes('go-http-client')) {
            return 'IDE';
        }
        // 3. CLI 识别
        if (uaLower.includes('antigravity/cli')) {
            return 'CLI';
        }
    }
    // 兜底回退：如果 reqHeaders 缺失但有已存在的 source 字段
    if (p.source && p.source !== '客户端' && p.source !== '未知') {
        return p.source;
    }
    return '未知';
}

export function generateSinglePacketMarkdown(p: any): string {
    if (!p) return '';
    const isZH = state.currentLanguage === 'zh';
    const source = resolvePacketSource(p);
    const displaySource = source === '未知' ? (isZH ? '未知' : 'Unknown') : source;
    
    let md = `# ${isZH ? 'Antigravity Proxy 接口数据包日志' : 'Antigravity Proxy Packet Log'}\n\n`;
    md += `## ${isZH ? '基础信息 (Basic Info)' : 'Basic Info'}\n\n`;
    md += `- **URL**: ${p.url || ''}\n`;
    md += `- **${isZH ? '方法 (Method)' : 'Method'}**: \`${p.method || ''}\`\n`;
    md += `- **${isZH ? '路径 (Path)' : 'Path'}**: \`${p.path || ''}\`\n`;
    md += `- **${isZH ? '主机 (Host)' : 'Host'}**: \`${p.host || ''}\`\n`;
    md += `- **${isZH ? '来源 (Source)' : 'Source'}**: \`${displaySource}\`\n`;
    md += `- **${isZH ? '状态码 (Status)' : 'Status Code'}**: \`${p.statusCode || ''}\`\n`;
    md += `- **${isZH ? '捕获时间' : 'Captured Time'}**: *${p.timestamp || ''}*\n\n`;
    
    md += `---\n\n`;
    
    md += `## ${isZH ? '📤 请求报文 (Request)' : '📤 Request'}\n\n`;
    md += `### Headers\n`;
    if (p.reqHeaders) {
        md += `\`\`\`json\n${JSON.stringify(p.reqHeaders, null, 2)}\n\`\`\`\n\n`;
    } else {
        md += `*${isZH ? '无 Headers' : 'No Headers'}*\n\n`;
    }
    
    md += `### Body\n`;
    if (p.reqBody) {
        md += `\`\`\`json\n${formatJsonText(p.reqBody)}\n\`\`\`\n\n`;
    } else {
        md += `*${isZH ? '无 Body' : 'No Body'}*\n\n`;
    }
    
    md += `---\n\n`;
    
    md += `## ${isZH ? '📥 响应报文 (Response)' : '📥 Response'}\n\n`;
    md += `### Headers\n`;
    if (p.resHeaders) {
        md += `\`\`\`json\n${JSON.stringify(p.resHeaders, null, 2)}\n\`\`\`\n\n`;
    } else {
        md += `*${isZH ? '无 Headers' : 'No Headers'}*\n\n`;
    }
    
    md += `### Body\n`;
    if (p.resBody) {
        md += `\`\`\`json\n${formatJsonText(p.resBody)}\n\`\`\`\n\n`;
    } else {
        md += `*${isZH ? '无 Body' : 'No Body'}*\n\n`;
    }
    
    return md;
}


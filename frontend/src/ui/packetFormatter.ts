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

export function generateSinglePacketMarkdown(p: any): string {
    if (!p) return '';
    const isZH = state.currentLanguage === 'zh';
    const source = p._resolvedSource || p.source || '未知';
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


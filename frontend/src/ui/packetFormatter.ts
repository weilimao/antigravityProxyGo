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
    const source = p._resolvedSource || p.source || '未知';
    
    let md = `# Antigravity Proxy 接口数据包日志\n\n`;
    md += `## 基础信息 (Basic Info)\n\n`;
    md += `- **URL**: ${p.url || ''}\n`;
    md += `- **方法 (Method)**: \`${p.method || ''}\`\n`;
    md += `- **路径 (Path)**: \`${p.path || ''}\`\n`;
    md += `- **主机 (Host)**: \`${p.host || ''}\`\n`;
    md += `- **来源 (Source)**: \`${source}\`\n`;
    md += `- **状态码 (Status)**: \`${p.statusCode || ''}\`\n`;
    md += `- **捕获时间**: *${p.timestamp || ''}*\n\n`;
    
    md += `---\n\n`;
    
    md += `## 📤 请求报文 (Request)\n\n`;
    md += `### Headers\n`;
    if (p.reqHeaders) {
        md += `\`\`\`json\n${JSON.stringify(p.reqHeaders, null, 2)}\n\`\`\`\n\n`;
    } else {
        md += `*无 Headers*\n\n`;
    }
    
    md += `### Body\n`;
    if (p.reqBody) {
        md += `\`\`\`json\n${formatJsonText(p.reqBody)}\n\`\`\`\n\n`;
    } else {
        md += `*无 Body*\n\n`;
    }
    
    md += `---\n\n`;
    
    md += `## 📥 响应报文 (Response)\n\n`;
    md += `### Headers\n`;
    if (p.resHeaders) {
        md += `\`\`\`json\n${JSON.stringify(p.resHeaders, null, 2)}\n\`\`\`\n\n`;
    } else {
        md += `*无 Headers*\n\n`;
    }
    
    md += `### Body\n`;
    if (p.resBody) {
        md += `\`\`\`json\n${formatJsonText(p.resBody)}\n\`\`\`\n\n`;
    } else {
        md += `*无 Body*\n\n`;
    }
    
    return md;
}

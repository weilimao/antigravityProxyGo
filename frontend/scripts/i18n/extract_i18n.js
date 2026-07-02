const fs = require('fs');
const path = require('path');
const vueDir = 'b:\\GPT\\antigravityProxyGo\\frontend\\src\\views';
const files = fs.readdirSync(vueDir).filter(f => f.endsWith('.vue'));
const keys = new Map();

files.forEach(f => {
  const content = fs.readFileSync(path.join(vueDir, f), 'utf-8');
  // Match data-i18n="key", data-i18n-placeholder="key", data-i18n-title="key"
  const regex = /data-i18n(?:-placeholder|-title)?=\"([^\"]+)\"([^>]*>)([^<]*)/g;
  let match;
  while ((match = regex.exec(content)) !== null) {
    keys.set(match[1], { file: f, text: match[3].trim() });
  }
});

const output = Array.from(keys.entries()).map(([k, v]) => `${k}: ${v.text}`);
console.log(output.join('\n'));

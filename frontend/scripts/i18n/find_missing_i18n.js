const fs = require('fs');
const path = require('path');
const vueDir = 'b:\\GPT\\antigravityProxyGo\\frontend\\src\\views';
const i18nFile = 'b:\\GPT\\antigravityProxyGo\\frontend\\src\\shared\\i18n.ts';

const files = fs.readdirSync(vueDir).filter(f => f.endsWith('.vue'));
const keys = new Map();

files.forEach(f => {
  const content = fs.readFileSync(path.join(vueDir, f), 'utf-8');
  const regex = /data-i18n(?:-placeholder|-title)?=\"([^\"]+)\"([^>]*>)([^<]*)/g;
  let match;
  while ((match = regex.exec(content)) !== null) {
    keys.set(match[1], match[3].trim());
  }
});

const i18nContent = fs.readFileSync(i18nFile, 'utf-8');
// match keys in zh section
const zhSectionMatch = i18nContent.match(/zh:\s*\{([^}]*)\}/);
let existingKeys = [];
if (zhSectionMatch) {
  const zhContent = zhSectionMatch[1];
  const keyRegex = /([a-zA-Z0-9_]+)\s*:/g;
  let m;
  while ((m = keyRegex.exec(zhContent)) !== null) {
    existingKeys.push(m[1]);
  }
}

const missing = [];
for (const [k, v] of keys.entries()) {
  if (!existingKeys.includes(k)) {
    missing.push({ key: k, zh: v });
  }
}
console.log(JSON.stringify(missing, null, 2));

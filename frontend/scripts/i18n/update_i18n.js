const fs = require('fs');
const path = require('path');

const srcDir = 'b:\\GPT\\antigravityProxyGo\\frontend\\src';
const i18nFile = path.join(srcDir, 'shared', 'i18n.ts');

function getAllFiles(dir, extList, fileList = []) {
  const files = fs.readdirSync(dir);
  for (const file of files) {
    const fullPath = path.join(dir, file);
    if (fs.statSync(fullPath).isDirectory()) {
      getAllFiles(fullPath, extList, fileList);
    } else {
      if (extList.some(ext => fullPath.endsWith(ext))) {
        fileList.push(fullPath);
      }
    }
  }
  return fileList;
}

const allFiles = getAllFiles(srcDir, ['.vue', '.ts']);
const keysToAdd = new Map();

for (const file of allFiles) {
  if (file === i18nFile) continue;
  const content = fs.readFileSync(file, 'utf-8');
  
  // Vue data-i18n
  const regexVue = /data-i18n(?:-placeholder|-title)?=\"([^\"]+)\"([^>]*>)([^<]*)/g;
  let match;
  while ((match = regexVue.exec(content)) !== null) {
    const k = match[1];
    let text = match[3].trim();
    if (!text) {
      // maybe placeholder or title
      const placeholderMatch = match[0].match(/placeholder=\"([^\"]+)\"/);
      if (placeholderMatch) text = placeholderMatch[1];
      const titleMatch = match[0].match(/title=\"([^\"]+)\"/);
      if (titleMatch && !text) text = titleMatch[1];
    }
    if (k && !keysToAdd.has(k)) keysToAdd.set(k, { zh: text, en: text }); // en will be same as zh for now
  }

  // TS dict.xxx || '中文'
  const regexTs = /dict\.([a-zA-Z0-9_]+)\s*\|\|\s*[\'\"]([^\'\"]+)[\'\"]/g;
  while ((match = regexTs.exec(content)) !== null) {
    const k = match[1];
    const text = match[2];
    if (k && !keysToAdd.has(k)) keysToAdd.set(k, { zh: text, en: text });
  }
}

// Now parse i18n.ts
let i18nContent = fs.readFileSync(i18nFile, 'utf-8');

// Find existing keys (simple regex matching "key: " or key: )
const existingKeys = new Set();
const keyRegex = /^\s*([a-zA-Z0-9_]+)\s*:/gm;
let m;
while ((m = keyRegex.exec(i18nContent)) !== null) {
  existingKeys.add(m[1]);
}

const missingKeys = [];
for (const [k, v] of keysToAdd.entries()) {
  if (!existingKeys.has(k)) {
    missingKeys.push({ k, v });
  }
}

if (missingKeys.length === 0) {
  console.log("No missing keys.");
  process.exit(0);
}

// We will inject missing keys at the end of the `zh: { ... }` block and `en: { ... }` block
// Find the end of `zh: {`
const zhEndIndex = i18nContent.indexOf('en: {') - 8; // heuristics: just before en: {

let zhAdd = '';
let enAdd = '';
for (const item of missingKeys) {
  zhAdd += `        ${item.k}: "${item.v.zh}",\n`;
  enAdd += `        ${item.k}: "${item.v.en}",\n`;
}

// Insert into zh
const enStartIndex = i18nContent.indexOf('en: {');
// find the '},' before 'en: {'
const insertZhIndex = i18nContent.lastIndexOf('},', enStartIndex);

i18nContent = i18nContent.slice(0, insertZhIndex) + ',\n' + zhAdd + i18nContent.slice(insertZhIndex);

// Insert into en
// find the last '}' before '};' at the end of file
const insertEnIndex = i18nContent.lastIndexOf('    }\n};');
i18nContent = i18nContent.slice(0, insertEnIndex) + ',\n' + enAdd + i18nContent.slice(insertEnIndex);

fs.writeFileSync(i18nFile, i18nContent, 'utf-8');
console.log(`Added ${missingKeys.length} keys.`);

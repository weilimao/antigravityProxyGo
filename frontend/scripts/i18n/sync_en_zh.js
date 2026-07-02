const fs = require('fs');
const path = require('path');
const i18nFile = path.join(__dirname, 'src/shared/i18n.ts');

let content = fs.readFileSync(i18nFile, 'utf-8').replace(/\r\n/g, '\n');

// Extract zh and en blocks safely
const zhStart = content.indexOf('zh: {');
const zhEnd = content.indexOf('    },\n    en: {');
const enStart = content.indexOf('en: {');
const enEnd = content.indexOf('    }\n};');

if (zhStart === -1 || zhEnd === -1 || enStart === -1 || enEnd === -1) {
    console.error("Could not find sections");
    process.exit(1);
}

const zhBlock = content.slice(zhStart, zhEnd);
const enBlock = content.slice(enStart, enEnd);

const keyRegex = /^\s*([a-zA-Z0-9_]+)\s*:\s*"(.*)"\s*,?/gm;

const zhKeys = new Map();
let m;
while ((m = keyRegex.exec(zhBlock)) !== null) {
    zhKeys.set(m[1], m[2]);
}

const enKeys = new Map();
while ((m = keyRegex.exec(enBlock)) !== null) {
    enKeys.set(m[1], m[2]);
}

let missingInEn = [];
for (let [k, v] of zhKeys.entries()) {
    if (!enKeys.has(k)) {
        missingInEn.push(`        ${k}: "${k}",`);
    }
}

if (missingInEn.length > 0) {
    let newEntries = missingInEn.join('\n');
    content = content.slice(0, enEnd) + newEntries + '\n' + content.slice(enEnd);
    fs.writeFileSync(i18nFile, content, 'utf-8');
    console.log(`Synced ${missingInEn.length} keys to en.`);
} else {
    console.log("No missing keys in en.");
}

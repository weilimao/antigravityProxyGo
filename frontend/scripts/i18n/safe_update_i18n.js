const fs = require('fs');
const path = require('path');
const i18nFile = path.join(__dirname, 'src/shared/i18n.ts');

const vueDir = path.join(__dirname, 'src/views');
const uiDir = path.join(__dirname, 'src/ui');

const keys = new Map();

function scanDir(dir) {
    if (!fs.existsSync(dir)) return;
    fs.readdirSync(dir).forEach(f => {
        const fullPath = path.join(dir, f);
        if (fs.statSync(fullPath).isDirectory()) {
            scanDir(fullPath);
        } else if (f.endsWith('.vue') || f.endsWith('.ts')) {
            const content = fs.readFileSync(fullPath, 'utf-8');
            
            const regex1 = /data-i18n(?:-placeholder|-title)?=\"([^\"]+)\"([^>]*>)([^<]*)/g;
            let match;
            while ((match = regex1.exec(content)) !== null) {
                keys.set(match[1], match[3].trim());
            }
            
            const regex2 = /dict\.([a-zA-Z0-9_]+)/g;
            while ((match = regex2.exec(content)) !== null) {
                if (!keys.has(match[1])) keys.set(match[1], match[1]);
            }
        }
    });
}
scanDir(vueDir);
scanDir(uiDir);

let i18nContent = fs.readFileSync(i18nFile, 'utf-8').replace(/\r\n/g, '\n');
const zhSectionMatch = i18nContent.match(/zh:\s*\{([\s\S]*?)\},/);
let existingKeys = [];
if (zhSectionMatch) {
    const zhContent = zhSectionMatch[1];
    const keyRegex = /^\s*([a-zA-Z0-9_]+)\s*:/gm;
    let m;
    while ((m = keyRegex.exec(zhContent)) !== null) {
        existingKeys.push(m[1]);
    }
}

let missingKeys = [];
for (let [k, v] of keys.entries()) {
    if (!existingKeys.includes(k) && k !== 'length' && k !== 'map') {
        missingKeys.push({key: k, val: v || k});
    }
}

if (missingKeys.length === 0) {
    console.log("No missing keys.");
    process.exit(0);
}

let newEntries = missingKeys.map(k => {
    let text = k.val.replace(/"/g, '\\"');
    if (text === '') text = k.key;
    return `        ${k.key}: "${text}",`;
}).join('\n');

let zhEndIndex = i18nContent.indexOf('    },\n    en: {');
if (zhEndIndex !== -1) {
    i18nContent = i18nContent.slice(0, zhEndIndex) + newEntries + '\n' + i18nContent.slice(zhEndIndex);
} else {
    console.error("Could not find zh section end!");
}

let enEndIndex = i18nContent.indexOf('    }\n};\n\nexport default translations;');
if (enEndIndex !== -1) {
    i18nContent = i18nContent.slice(0, enEndIndex) + newEntries + '\n' + i18nContent.slice(enEndIndex);
} else {
    console.error("Could not find en section end!");
}

fs.writeFileSync(i18nFile, i18nContent, 'utf-8');
console.log(`Successfully added ${missingKeys.length} missing keys.`);

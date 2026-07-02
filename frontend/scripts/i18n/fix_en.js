const fs = require('fs');
let c = fs.readFileSync('src/shared/i18n.ts', 'utf-8');
const enStart = c.indexOf('en: {');
if (!c.includes('packetDetailTip: ', enStart)) {
    c = c.replace('packetSelectAccountPlaceholder:', 'packetDetailTip: "Click a packet to view details",\n        packetSelectAccountPlaceholder:');
    fs.writeFileSync('src/shared/i18n.ts', c, 'utf-8');
    console.log('Added packetDetailTip to en');
} else {
    console.log('Already in en');
}

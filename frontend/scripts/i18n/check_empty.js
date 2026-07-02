const fs = require('fs');
const lines = fs.readFileSync('src/shared/i18n.ts', 'utf-8').split('\n');
lines.forEach((l, i) => {
    if (l.match(/:\s*\"\"/)) {
        console.log(i + 1 + ':', l);
    }
});

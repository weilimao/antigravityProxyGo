const fs = require('fs');
let c = fs.readFileSync('src/shared/i18n.ts', 'utf-8');
const search = 'packetDetailTip: "<span class=\\"material-symbols-outlined text-[48px] mb-2 text-outline/30\\">info</span><br>点击左侧接口查看请求报文和响应报文的完整内容",';
const replace = 'packetDetailTip: \'<span class=\"material-symbols-outlined text-[48px] mb-2 text-outline/30\">info</span><br>点击左侧接口查看请求报文和响应报文的完整内容\',';
c = c.split(search).join(replace);
fs.writeFileSync('src/shared/i18n.ts', c, 'utf-8');

const fs = require('fs');
const content = fs.readFileSync('index.html', 'utf8');
const start = content.indexOf('<body');
const end = content.indexOf('</body>');
const before = content.substring(0, start);
const after = content.substring(end);
const newBody = `<body class="antialiased min-h-screen flex flex-col font-sans bg-slate-50 dark:bg-[#10131c]">
    <div id="app"></div>
    <script type="module" src="/src/main.ts"></script>
`;
fs.writeFileSync('index.html', before + newBody + after);

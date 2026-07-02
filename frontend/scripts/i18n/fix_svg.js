
const fs = require('fs');
const path = require('path');
function walk(dir) {
  let results = [];
  const list = fs.readdirSync(dir);
  list.forEach(function(file) {
    file = path.join(dir, file);
    const stat = fs.statSync(file);
    if (stat && stat.isDirectory()) { 
      results = results.concat(walk(file));
    } else { 
      if (file.endsWith('.vue')) results.push(file);
    }
  });
  return results;
}
const files = walk('./src/views');
files.forEach(file => {
  let content = fs.readFileSync(file, 'utf8');
  const original = content;
  content = content.replace(/lineargradient/g, 'linearGradient')
                 .replace(/preserveaspectratio/g, 'preserveAspectRatio')
                 .replace(/viewbox/g, 'viewBox')
                 .replace(/fegaussianblur/g, 'feGaussianBlur')
                 .replace(/femerge>/g, 'feMerge>')
                 .replace(/femerge/g, 'feMerge')
                 .replace(/femergenode/g, 'feMergeNode')
                 .replace(/stddeviation/g, 'stdDeviation');
  if (content !== original) {
    fs.writeFileSync(file, content, 'utf8');
    console.log('Fixed SVG in:', file);
  }
});


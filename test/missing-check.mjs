import Parser from 'tree-sitter';
import devnotes from '../bindings/node/index.js';
const p = new Parser();
p.setLanguage(devnotes);
const t1 = p.parse('@note #x observation : Example\n');
console.log('ONE NL:', t1.rootNode.toString());
const t2 = p.parse('@note #x observation : Example\n\n');
console.log('TWO NL:', t2.rootNode.toString());

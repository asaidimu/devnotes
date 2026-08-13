import Parser from 'tree-sitter';
import devnotes from '../bindings/node/index.js';

function parse(input) {
  const parser = new Parser();
  parser.setLanguage(devnotes);
  return parser.parse(input).rootNode.toString();
}

function normalize(s) {
  return s
    .replace(/\[[^\]]*\]/g, '')
    .replace(/\s+/g, ' ')
    .trim();
}

let failures = 0;
function check(name, actual, expected) {
  const act = normalize(actual);
  const exp = normalize(expected);
  if (act !== exp) {
    failures++;
    console.error(`FAIL: ${name}`);
    console.error(`--- actual ---\n${act}`);
    console.error(`--- expected ---\n${exp}`);
  } else {
    console.log(`ok: ${name}`);
  }
}

check('minimal note', parse('@note #x observation : Example\n\n'), `(source_file
  (note_block
    (header_line (id) (category) (title))
    (separator_line)))`);

check('full header', parse('@note #pool-ownership issue open P1 2026-08-10T16:29+03:00 #pooling,#memory : Nested documents can outlive their parent pool\n\n'), `(source_file
  (note_block
    (header_line
      (id) (category)
      (header_field (status))
      (header_field (priority))
      (header_field (timestamp))
      (header_field (tags (tag_name) (tag_name)))
      (title))
    (separator_line)))`);

check('directives and body', parse('@note #pool observation : Pool ownership\n@author jane\n@see #document-lifecycle\n\nbody text\n'), `(source_file
  (note_block
    (header_line (id) (category) (title))
    (directive_line (author_directive (author_value)))
    (directive_line (see_directive (reference (id))))
    (separator_line)
    (body_line (body_text))))`);

check('title with colons', parse('@note #x observation : Why A : B is necessary\n\n'), `(source_file
  (note_block
    (header_line (id) (category) (title))
    (separator_line)))`);

check('crlf and blank body lines', parse('@note #a observation : first\r\n\r\npara1\r\n\r\n@note #b todo : second\r\n\r\n'), `(source_file
  (note_block
    (header_line (id) (category) (title))
    (separator_line)
    (body_line (body_text))
    (body_line))
  (note_block
    (header_line (id) (category) (title))
    (separator_line)))`);

check('escape preserved in body', parse('@note #x observation : a\n\n\\@note is literal.\n'), `(source_file
  (note_block
    (header_line (id) (category) (title))
    (separator_line)
    (body_line (body_text))))`);

check('error recovery', parse('@note broken ...\n@note #b observation : B\n\nbody of b\n'), `(source_file
  (note_block
    (header_line (ERROR (UNEXPECTED '.')) (id) (category) (title))
    (separator_line)
    (body_line (body_text))))`);

if (failures > 0) {
  console.error(`${failures} test(s) failed`);
  process.exit(1);
}
console.log('all basic tests passed');

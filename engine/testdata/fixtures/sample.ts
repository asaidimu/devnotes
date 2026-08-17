// @note #pool-release ts : Duplicate pool release note across languages
function dupPool() {}

/*
 * @note #block-note ts : Block comment note
 */
function blockNote() {}

const url = "https://example.com/api/v2"; // trailing note after a URL string

// @note #ts-only ts : Only here
// @see #missing-note-ts
function tsOnly() {}

// @note #templates-gx observation : Host scanner must skip strings, regex, templates
// @see #ts-only
function grab(id: string): string {
  const re = /^\/\//.source; // regex delimiter is not a comment
  const tmpl = `prefix ${id ? "/x" : re}`; // interpolation is not a comment
  const div = 10 / 2; // division slash is not a comment
  const svc = "// not a comment " + url;
  return div > 2 ? "// still not a comment" : re;
}
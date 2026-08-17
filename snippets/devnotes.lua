local ls = require('luasnip')
local s = ls.snippet
local t = ls.text_node
local i = ls.insert_node

return {
  -- Full note header with id, category, fields, and title.
  s('note', {
    t('@note #'),
    i(1, 'id'),
    t(' '),
    i(2, 'observation'),
    t(' : '),
    i(3, 'title'),
    t({ '', '' }),
    i(0),
  }),

  -- Category variants (start with "note " then the category name).
  s('note-observation', {
    t('@note #'), i(1, 'id'), t(' observation : '), i(2, 'title'), t({ '', '' }), i(0),
  }),
  s('note-todo', {
    t('@note #'), i(1, 'id'), t(' todo : '), i(2, 'title'), t({ '', '' }), i(0),
  }),
  s('note-issue', {
    t('@note #'), i(1, 'id'), t(' issue : '), i(2, 'title'), t({ '', '' }), i(0),
  }),
  s('note-context', {
    t('@note #'), i(1, 'id'), t(' context : '), i(2, 'title'), t({ '', '' }), i(0),
  }),
  s('note-lesson', {
    t('@note #'), i(1, 'id'), t(' lesson : '), i(2, 'title'), t({ '', '' }), i(0),
  }),
  s('note-prompt', {
    t('@note #'), i(1, 'id'), t(' prompt : '), i(2, 'title'), t({ '', '' }), i(0),
  }),

  -- Directives.
  s('author', {
    t('@author '), i(1, 'name'), t({ '', '' }), i(0),
  }),
  s('see', {
    t('@see #'), i(1, 'id'), t({ '', '' }), i(0),
  }),

  -- Complete note with directives + body.
  s('note-full', {
    t('@note #'), i(1, 'id'), t(' '), i(2, 'observation'), t(' : '), i(3, 'title'),
    t({ '', '' }),
    t('@author '), i(4, 'name'),
    t({ '', '' }),
    t('@see #'), i(5, 'related'),
    t({ '', '' }),
    t(''),
    t({ '', '' }),
    i(0, 'body'),
  }),
}

-- Shared DevNotes snippet definitions.
--
-- Loaded for the `devnotes` filetype AND for host filetypes (go, typescript,
-- javascript, ...) via thin wrappers in the repo `snippets/` dir, so notes
-- can be written inside Go/TS/JS comments without ever opening a .dn file.
--
-- IDs are tooling concerns: they must be unique and stable, but humans think
-- in titles. So the note snippets auto-generate a short random UID for the
-- `#id` slot (editable if you really want to override it) and leave the
-- human-facing title as the primary insert.

local ls = require('luasnip')
local s = ls.snippet
local t = ls.text_node
local i = ls.insert_node
local d = ls.dynamic_node
local sn = ls.snippet_node

-- Short random UID: 6 chars from a-z0-9. math.random is seeded once per nvim
-- session by LuaJIT/Neovim; entropy is fine for a collision-resistant-enough
-- local id.
local ID_ALPHABET = 'abcdefghijklmnopqrstuvwxyz0123456789'
local function gen_uid(len)
  len = len or 6
  local out = {}
  for _ = 1, len do
    local k = math.random(1, #ID_ALPHABET)
    out[#out + 1] = ID_ALPHABET:sub(k, k)
  end
  return table.concat(out)
end

-- Editable insert node whose default is a freshly generated UID.
local function uid_node(pos)
  return d(pos, function()
    return sn(nil, { i(1, gen_uid()) })
  end)
end

local function note_header(category)
  return {
    t('@note #'),
    uid_node(1),
    t(' '),
    i(2, category),
    t(' : '),
    i(3, 'title'),
    t({ '', '' }),
    i(0),
  }
end

return {
  -- Generic note: auto UID, choose category, write title.
  s('note', note_header('observation')),
  s('note-observation', note_header('observation')),
  s('note-todo', note_header('todo')),
  s('note-issue', note_header('issue')),
  s('note-context', note_header('context')),
  s('note-lesson', note_header('lesson')),
  s('note-prompt', note_header('prompt')),

  -- Directives.
  s('author', {
    t('@author '), i(1, 'name'), t({ '', '' }), i(0),
  }),
  s('see', {
    t('@see #'), i(1, 'id'), t({ '', '' }), i(0),
  }),

  -- Complete note with directives + body.
  s('note-full', {
    t('@note #'),
    uid_node(1),
    t(' '),
    i(2, 'observation'),
    t(' : '),
    i(3, 'title'),
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

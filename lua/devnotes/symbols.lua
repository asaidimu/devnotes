-- DevNotes symbol window (spec §14.4, §6).
--
-- A dedicated, always-available window listing every note in the workspace —
-- the DevNotes equivalent of a symbols-outline. Backed by the `devnotes.list`
-- workspace command, so it shows the same data pickers do (id, category,
-- status, priority, tags, authors, reference count, body).
--
-- Usage:
--   :DevNotesSymbols            open with the configured layout
--   :DevNotesSymbols left       open as a left vertical split
--   :DevNotesSymbols right|top|bottom|float
--   :DevNotesSymbolsToggle / :DevNotesSymbolsClose
--
-- The panel is a triage list rather than a table:
--
--   ✖ Issues (4)
--       ▲ Critical problem                          ▸
--       ◆ Another issue                             ▸
--   ▶ Todos (1)
--       ▽ Low-priority task                         ▸
--
-- Each note is one row: priority glyph + title (truncated only while
-- collapsed). Expanding a row wraps the full title and body and adds a
-- footer with the note's #id, tags, file:line, priority, status, and
-- reference count:
--
--       ▲ Critical problem                          ▾
--         │ First body line…
--         │ Second body line…
--         └ #a  #pool #memory · a.dn:12 · critical · open · 2 refs
--
-- Window keymaps (buffer-local):
--   <CR>  jump to the note in the source file
--   j/k   move between note title rows (skipping expanded details)
--   Tab   expand/collapse details (za also works)
--   p/K   floating preview of the note body
--   r     refresh from the server
--   q     close the window
--
-- Layout is configurable via require('devnotes').setup{ symbols = {...} }:
--   style          'float' (default) | 'left' | 'right' | 'top' | 'bottom'
--   width          columns for vertical splits / float width (default 80)
--   height         rows for horizontal splits / float height (default 24)
--   group_by       'category' (default) | 'file' | 'none'
--   sort           passed to devnotes.list (default: 'priority')
--   show_resolved  include resolved/deprecated notes (default true)
--   margin         blank columns kept at the right edge (default 4)
--   close_on_jump  close the window after jumping (default: true for floats)
--   keymaps        table of { jump, preview, close, refresh } LHs

local M = {}

local DEFAULTS = {
  style = 'float',
  width = 80,
  height = 24,
  group_by = 'category',
  sort = 'priority',
  show_resolved = true,
  margin = 4,
  close_on_jump = nil, -- resolved per style at open time
  keymaps = {},
}

local KEYMAPS = {
  jump = '<CR>',
  preview = 'p',
  close = 'q',
  refresh = 'r',
}

local opts = vim.deepcopy(DEFAULTS)

-- One scratch buffer per session; module-level because vim.b cannot hold
-- Lua tables (line -> item lookup lives here, keyed by buffer).
local bufnr = nil
local winid = nil
local current_close_on_jump = nil -- resolved per open() call
local line_items = {} -- [bufnr] = { [row(1-based)] = item }
local fetch_seq = 0
local last_items = {}
local last_linemap = {}
local note_rows = {} -- [bufnr] = { [row(1-based)] = true }
local expanded = {} -- [item key] = true
local total_notes = 0
local ns = vim.api.nvim_create_namespace('devnotes.symbols')

-- ---------------------------------------------------------------------------
-- Highlighting
-- ---------------------------------------------------------------------------

local HI_GROUPS = {
  DevNotesHeader = { default = true, link = 'Title' },
  DevNotesId = { default = true, link = 'Special' },
  DevNotesPath = { default = true, link = 'Comment' },
  DevNotesGlyphOpen = { default = true, link = 'DiagnosticInfo' },
  DevNotesGlyphResolved = { default = true, link = 'DiagnosticOk' },
  DevNotesGlyphWontfix = { default = true, link = 'Comment' },
  DevNotesGlyphDeprecated = { default = true, link = 'Comment' },
  DevNotesCatIssue = { default = true, link = 'DiagnosticError' },
  DevNotesCatTodo = { default = true, link = 'DiagnosticWarn' },
  DevNotesCatObservation = { default = true, link = 'DiagnosticInfo' },
  DevNotesCatContext = { default = true, link = 'Comment' },
  DevNotesCatLesson = { default = true, link = 'Label' },
  DevNotesCatPrompt = { default = true, link = 'Character' },
  DevNotesP0 = { default = true, link = 'DiagnosticError' },
  DevNotesP1 = { default = true, link = 'DiagnosticWarn' },
  DevNotesP2 = { default = true, link = 'DiagnosticInfo' },
  DevNotesP3 = { default = true, link = 'Comment' },
}

local function define_highlights()
  for name, def in pairs(HI_GROUPS) do
    if def.link then
      vim.cmd(('hi default link %s %s'):format(name, def.link))
    end
  end
end

local CATEGORY_HL = {
  issue = 'DevNotesCatIssue',
  todo = 'DevNotesCatTodo',
  observation = 'DevNotesCatObservation',
  context = 'DevNotesCatContext',
  lesson = 'DevNotesCatLesson',
  prompt = 'DevNotesCatPrompt',
}

local PRIORITY_HL = {
  critical = 'DevNotesP0', high = 'DevNotesP1', medium = 'DevNotesP2', low = 'DevNotesP3',
  P0 = 'DevNotesP0', P1 = 'DevNotesP1', P2 = 'DevNotesP2', P3 = 'DevNotesP3',
}

local GLYPHS = { open = '○', resolved = '✔', wontfix = '⊘', deprecated = '◌' }
local GLYPH_HL = {
  open = 'DevNotesGlyphOpen',
  resolved = 'DevNotesGlyphResolved',
  wontfix = 'DevNotesGlyphWontfix',
  deprecated = 'DevNotesGlyphDeprecated',
}

-- ---------------------------------------------------------------------------
-- Server access
-- ---------------------------------------------------------------------------

---Request the devnotes client directly. The symbols buffer is a scratch
---buffer that the LSP client is *not* attached to, so vim.lsp.buf_request(0)
---would find no client; resolving the client by name avoids that.
local function get_client()
  local clients = vim.lsp.get_clients and vim.lsp.get_clients({ name = 'devnotes' })
    or vim.lsp.get_active_clients({ name = 'devnotes' })
  return clients and clients[1] or nil
end

local function fetch_items(cb)
  local client = get_client()
  if not client then
    vim.notify('devnotes: LSP client not attached — open a file first', vim.log.levels.WARN)
    return
  end
  fetch_seq = fetch_seq + 1
  local seq = fetch_seq
  local filter = {}
  if opts.show_resolved then
    filter.includeResolved = true
    filter.includeDeprecated = true
  end
  if next(filter) == nil then
    -- Lua encodes an empty table as [] over JSON-RPC; the server expects an
    -- object, so use vim.empty_dict() to force {}.
    filter = vim.empty_dict()
  end
  local params = { command = 'devnotes.list', arguments = { { filter = filter, sort = opts.sort, includeBody = true } } }
  local handler = function(err, result)
    if seq ~= fetch_seq then return end -- superseded by a newer refresh
    if err then
      vim.notify('devnotes: list failed: ' .. tostring(err.message or err), vim.log.levels.ERROR)
      return
    end
    cb(result and result.items or {})
  end
  -- Prefer the 0.11+ method form; fall back to the legacy function form on
  -- older clients.
  if vim.fn.has('nvim-0.11') == 1 then
    client:request('workspace/executeCommand', params, handler)
  else
    client.request('workspace/executeCommand', params, handler)
  end
end

-- ---------------------------------------------------------------------------
-- Rendering
-- ---------------------------------------------------------------------------

local CATEGORY_ORDER = { 'todo', 'issue', 'observation', 'context', 'lesson', 'prompt' }
local CATEGORY_LABEL = {
  todo = 'Todos', issue = 'Issues', observation = 'Observations',
  context = 'Context', lesson = 'Lessons', prompt = 'Prompts',
}

local function note_file(item)
  local uri = ((item.location or {}).uri)
  if not uri then return '' end
  local fname = vim.uri_to_fname(uri)
  return vim.fn.fnamemodify(fname, ':.')
end

local function note_line(item)
  local r = ((item.location or {}).range or {}).start
  return r and (r.line + 1) or 0
end

local function status_of(item)
  return item.status or 'open'
end

local function disp(s)
  return vim.fn.strdisplaywidth(s)
end

local function pad(s, n)
  local w = disp(s)
  if w >= n then return s end
  return s .. string.rep(' ', n - w)
end

local function trunc(s, n)
  if n <= 0 then return '' end
  if disp(s) <= n then return s end
  if n <= 1 then return '…' end
  local out, width = {}, 0
  for _, ch in ipairs(vim.fn.split(s, '\\zs')) do
    local cw = vim.fn.strdisplaywidth(ch)
    if width + cw > n - 1 then break end
    out[#out + 1] = ch
    width = width + cw
  end
  return table.concat(out) .. '…'
end

---Wrap text to `width` display columns. Words stay intact where possible;
---a word longer than the line is split on character boundaries. Used for
---expanded titles and body excerpts so nothing is hidden behind an ellipsis.
local function wrap(text, width)
  width = math.max(4, width)
  local out = {}
  for _, para in ipairs(vim.split(text, '\n', { plain = true })) do
    para = vim.trim((para:gsub('\r$', '')))
    if para == '' then
      out[#out + 1] = ''
    else
      local line = ''
      for _, word in ipairs(vim.split(para, '%s+', { trimempty = true })) do
        local cand = line == '' and word or (line .. ' ' .. word)
        if disp(cand) <= width then
          line = cand
        else
          if line ~= '' then out[#out + 1] = line end
          line = ''
          if disp(word) > width then
            local chunk = ''
            for _, ch in ipairs(vim.fn.split(word, '\\zs')) do
              if disp(chunk .. ch) > width then
                out[#out + 1] = chunk
                chunk = ch
              else
                chunk = chunk .. ch
              end
            end
            line = chunk
          else
            line = word
          end
        end
      end
      if line ~= '' then out[#out + 1] = line end
    end
  end
  if #out == 0 then out[1] = '' end
  return out
end

---Shorten a path from the left, keeping the basename and as much of the
---directory suffix as fits. The trailing "file:line" location survives.
local function shorten_location(item, max)
  local file = note_file(item)
  local loc = ('%s:%d'):format(file, note_line(item))
  if max <= 0 or disp(loc) <= max then return loc end
  local parts = vim.split(file, '/', { plain = true })
  local base = parts[#parts] or file
  local tail = ('%s:%d'):format(base, note_line(item))
  if disp(tail) >= max then return trunc(tail, max) end
  local out = base
  for i = #parts - 1, 1, -1 do
    local candidate = parts[i] .. '/' .. out
    if disp('…/' .. candidate .. ':' .. note_line(item)) > max then break end
    out = candidate
  end
  return ('…/%s:%d'):format(out, note_line(item))
end

local PRIORITY_RANK = {
  critical = 0, high = 1, medium = 2, low = 3,
  P0 = 0, P1 = 1, P2 = 2, P3 = 3,
}
local STATUS_RANK = { open = 0, wontfix = 1, resolved = 2, deprecated = 3 }

local CATEGORY_GLYPH = {
  todo = '▶', issue = '✖', observation = '●', context = '◈',
  lesson = '✦', prompt = '⟡',
}

local function priority_rank(item)
  return PRIORITY_RANK[item.priority or ''] or 9
end

local function file_key_cmp(a, c)
  local fa, fc = note_file(a), note_file(c)
  if fa ~= fc then return fa < fc end
  local la, lc = note_line(a), note_line(c)
  if la ~= lc then return la < lc end
  return (a.id or '') < (c.id or '')
end

local function compare_items(a, c)
  local mode = opts.sort or 'priority'
  if mode == 'id' then
    if (a.id or '') ~= (c.id or '') then return (a.id or '') < (c.id or '') end
    return file_key_cmp(a, c)
  end
  if mode == 'category' then
    if (a.category or '') ~= (c.category or '') then
      return (a.category or '') < (c.category or '')
    end
    local ra, rc = priority_rank(a), priority_rank(c)
    if ra ~= rc then return ra < rc end
    return file_key_cmp(a, c)
  end
  if mode == 'timestamp' then
    local ta, tc = a.timestamp or '', c.timestamp or ''
    if ta ~= tc then
      if ta == '' then return false end
      if tc == '' then return true end
      return ta > tc
    end
  end
  local ra, rc = priority_rank(a), priority_rank(c)
  if ra ~= rc then return ra < rc end
  local sa, sc = STATUS_RANK[status_of(a)] or 9, STATUS_RANK[status_of(c)] or 9
  if sa ~= sc then return sa < sc end
  return file_key_cmp(a, c)
end

---Group items into { { key =, label =, items = {} } } buckets.
local function group_items(items)
  local buckets, index = {}, {}
  for _, item in ipairs(items) do
    local key
    if opts.group_by == 'file' then
      key = note_file(item)
    else
      key = item.category or 'prompt'
    end
    if not buckets[key] then
      buckets[key] = { key = key, items = {} }
      index[#index + 1] = key
    end
    table.insert(buckets[key].items, item)
  end
  for _, b in pairs(buckets) do
    table.sort(b.items, compare_items)
  end
  if opts.group_by == 'file' then
    table.sort(index)
  else
    table.sort(index, function(a, c)
      local ra, rc = {}, {}
      for i, name in ipairs(CATEGORY_ORDER) do ra[name], rc[name] = i, i end
      return (ra[a] or 99) < (rc[c] or 99)
    end)
  end
  local groups = {}
  for _, key in ipairs(index) do
    local b = buckets[key]
    b.label = ('%s (%d)'):format(opts.group_by == 'file' and b.key or (CATEGORY_LABEL[b.key] or b.key), #b.items)
    groups[#groups + 1] = b
  end
  if opts.group_by ~= 'file' and (opts.sort == 'priority' or opts.sort == 'default') then
    local function best_rank(b)
      local best = 9
      for _, item in ipairs(b.items) do
        local r = priority_rank(item)
        if r < best then best = r end
      end
      return best
    end
    local order = {}
    for i, name in ipairs(CATEGORY_ORDER) do order[name] = i end
    table.sort(groups, function(a, c)
      local ra, rc = best_rank(a), best_rank(c)
      if ra ~= rc then return ra < rc end
      return (order[a.key] or 99) < (order[c.key] or 99)
    end)
  end
  return groups
end

---Concatenate {text, hl} segments into a line, returning the line plus
---0-based byte-range highlight specs. `#text` is byte length, which is
---exactly what extmark columns expect (glyphs are multibyte-safe).
local function assemble(segments)
  local parts, marks, col = {}, {}, 0
  for _, seg in ipairs(segments) do
    local text, hl = seg[1], seg[2]
    if text ~= '' then
      if hl then
        marks[#marks + 1] = { col = col, end_col = col + #text, hl = hl }
      end
      col = col + #text
      parts[#parts + 1] = text
    end
  end
  return table.concat(parts), marks
end

local PRIORITY_GLYPH = {
  critical = '▲', high = '◆', medium = '●', low = '▽',
}
local PRIORITY_GLYPH_HL = {
  critical = 'DevNotesP0', high = 'DevNotesP1', medium = 'DevNotesP2', low = 'DevNotesP3',
}

local function item_key(item)
  return ('%s|%s:%d'):format(item.id or '', note_file(item), note_line(item))
end

local function layout_for(width)
  width = math.max(24, width or opts.width)
  -- Keep a right margin so wrapped text never runs into the window edge.
  -- Layout math uses this reduced width for wrapping, padding and the
  -- expander marker; the winbar/statusline still use the physical width.
  local margin = math.max(0, opts.margin or 0)
  return {
    width = math.max(20, width - margin),
    indent = opts.group_by == 'none' and 2 or 4,
    show_category = opts.group_by == 'none' and width >= 40,
  }
end

---A body line that starts a new logical block (list item, heading, quote)
---instead of continuing the current paragraph.
local function starts_block(line)
  return line:match('^%d+[.)]%s') ~= nil
    or line:match('^[-*+•]%s') ~= nil
    or line:match('^#+%s') ~= nil
    or line:match('^>%s') ~= nil
end

---Split a block into its list marker (if any) and the remaining text, so
---wrapped list items can hang-indent under the marker.
local function split_marker(text)
  local marker, rest = text:match('^(%d+[.)]%s+)(.*)$')
  if marker then return marker, rest end
  marker, rest = text:match('^([-*+•]%s+)(.*)$')
  return marker, rest
end

---Expanded body rows: source notes are often hard-wrapped, so joining each
---paragraph before wrapping produces even, readable lines instead of a
---choppy staircase. List items, headings and quotes each start their own
---block; blank lines separate blocks. `""` marks a paragraph gap.
local function body_rows(item, width)
  local out = {}
  if not item.body or item.body == '' then return out end

  local blocks, cur, gap = {}, {}, false
  local function flush()
    if #cur == 0 then return end
    blocks[#blocks + 1] = { text = table.concat(cur, ' '), gap = gap }
    cur = {}
    gap = false
  end

  for _, raw in ipairs(vim.split(item.body, '\n', { plain = true })) do
    local line = vim.trim((raw:gsub('\r$', '')))
    if line == '' then
      flush()
      gap = #blocks > 0
    elseif starts_block(line) then
      flush()
      cur = { line }
    else
      cur[#cur + 1] = line
    end
  end
  flush()

  for _, block in ipairs(blocks) do
    if block.gap and #out > 0 then out[#out + 1] = '' end
    local marker, rest = split_marker(block.text)
    if marker then
      local hang = string.rep(' ', disp(marker))
      local parts = wrap(rest, width - disp(marker))
      for i, part in ipairs(parts) do
        out[#out + 1] = (i == 1 and marker or hang) .. part
      end
    else
      for _, part in ipairs(wrap(block.text, width)) do out[#out + 1] = part end
    end
  end
  return out
end

---Title row(s). Collapsed notes truncate to a single row; expanded notes
---wrap the full title, with continuation rows aligned under the title text.
local function title_rows(item, L, show_category, is_expanded)
  local prio = item.priority or ''
  local glyph = PRIORITY_GLYPH[prio] or '·'
  local ghl = PRIORITY_GLYPH_HL[prio] or 'DevNotesP3'
  local prefix = L.indent + 2 + (show_category and 2 or 0) + 2
  local avail = math.max(6, L.width - prefix - 2)
  local title = item.title or '(untitled)'
  local parts = is_expanded and wrap(title, avail) or { trunc(title, avail) }

  local rows = {}
  for i, text in ipairs(parts) do
    local segs
    if i == 1 then
      segs = { { string.rep(' ', L.indent) } }
      if show_category then
        segs[#segs + 1] = {
          CATEGORY_GLYPH[item.category] or '◆',
          CATEGORY_HL[item.category] or 'DevNotesCatContext',
        }
        segs[#segs + 1] = { ' ' }
      end
      segs[#segs + 1] = { glyph, ghl }
      segs[#segs + 1] = { ' ' }
    else
      segs = { { string.rep(' ', prefix) } }
    end
    segs[#segs + 1] = { text }
    if i == 1 then
      local used = prefix + disp(text)
      local pad = L.width - 1 - used
      if pad > 0 then segs[#segs + 1] = { string.rep(' ', pad) } end
      segs[#segs + 1] = { is_expanded and '▾' or '▸', 'DevNotesPath' }
    end
    rows[#rows + 1] = segs
  end
  return rows
end

---Footer at the end of an expanded note: #id, tags, location, status, refs.
---Pieces wrap onto continuation rows instead of being clipped, and long
---pieces (a deep path) are split so every character stays visible.
local function footer_rows(item, L, indent)
  local avail = L.width - indent - 2
  if avail < 4 then return {} end

  local pieces = {}
  local function add(text, hl, sep)
    if text == '' then return end
    local chunks = wrap(text, avail)
    for i, chunk in ipairs(chunks) do
      pieces[#pieces + 1] = { text = chunk, hl = hl, sep = i == 1 and sep or '' }
    end
  end

  local id = item.id or ''
  if id ~= '' and id:sub(1, 1) ~= '#' then id = '#' .. id end
  add(id, 'DevNotesId', '')
  for _, t in ipairs(item.tags or {}) do
    add((t:sub(1, 1) == '#') and t or ('#' .. t), 'DevNotesPath', '  ')
  end
  add(('%s:%d'):format(note_file(item), note_line(item)), 'DevNotesPath', ' · ')
  if item.priority and item.priority ~= '' then
    add(item.priority, PRIORITY_HL[item.priority] or 'DevNotesP3', ' · ')
  end
  if status_of(item) ~= '' then
    add(status_of(item), 'DevNotesGlyphOpen', ' · ')
  end
  if (item.referenceCount or 0) > 0 then
    add(('%d refs'):format(item.referenceCount), 'DevNotesPath', ' · ')
  end

  local rows = {}
  local segs = { { string.rep(' ', indent) }, { '└ ', 'DevNotesPath' } }
  local used = indent + 2
  for _, p in ipairs(pieces) do
    local sep = p.sep or ''
    if used > indent + 2 and used + disp(sep) + disp(p.text) > L.width then
      rows[#rows + 1] = segs
      segs = { { string.rep(' ', indent + 2) } }
      used = indent + 2
      sep = ''
    end
    if sep ~= '' then
      segs[#segs + 1] = { sep, 'DevNotesPath' }
      used = used + disp(sep)
    end
    segs[#segs + 1] = { p.text, p.hl }
    used = used + disp(p.text)
  end
  rows[#rows + 1] = segs
  return rows
end

---Build the display lines, extmark highlights, row->item map and the set of
---title rows (used for note-level j/k navigation).
---@param items table[] devnotes.list items
---@return string[] lines, table[] marks, table linemap, table titles
local function render(items)
  define_highlights()
  local width = (winid and vim.api.nvim_win_is_valid(winid))
    and vim.api.nvim_win_get_width(winid) or opts.width
  local L = layout_for(width)
  local lines, marks, linemap, titles = {}, {}, {}, {}
  local function cat_hl(item) return CATEGORY_HL[item.category] or 'DevNotesCatContext' end

  local function append(item, segs, is_title)
    local line, ms = assemble(segs)
    local row = #lines + 1
    lines[#lines + 1] = line
    if item then linemap[row] = item end
    if is_title then titles[row] = true end
    for _, m in ipairs(ms) do
      marks[#marks + 1] = { row = row - 1, col = m.col, end_col = m.end_col, hl = m.hl }
    end
    return row
  end

  local function add_item(item, show_category)
    local key = item_key(item)
    local is_expanded = expanded[key] == true
    for i, segs in ipairs(title_rows(item, L, show_category, is_expanded)) do
      append(item, segs, i == 1)
    end
    if not is_expanded then return end

    local detail_indent = L.indent + 2
    local text_w = L.width - detail_indent - 2
    for _, text in ipairs(body_rows(item, text_w)) do
      append(item, {
        { string.rep(' ', detail_indent) },
        { '│ ', 'DevNotesPath' },
        { text },
      }, false)
    end
    for _, segs in ipairs(footer_rows(item, L, detail_indent)) do
      append(item, segs, false)
    end
  end

  if opts.group_by == 'none' then
    table.sort(items, compare_items)
    for _, item in ipairs(items) do
      add_item(item, L.show_category)
    end
    return lines, marks, linemap, titles
  end

  for _, group in ipairs(group_items(items)) do
    local glyph = opts.group_by == 'file' and '▸' or (CATEGORY_GLYPH[group.key] or '◆')
    local ghl = opts.group_by == 'file' and 'DevNotesHeader' or (CATEGORY_HL[group.key] or 'DevNotesHeader')
    local header = ('  %s %s'):format(glyph, group.label)
    local row = #lines + 1
    lines[#lines + 1] = header
    marks[#marks + 1] = { row = row - 1, col = 2, end_col = #header, hl = ghl }
    for _, item in ipairs(group.items) do
      add_item(item, false)
    end
  end
  return lines, marks, linemap, titles
end

local function escape_statusline(s)
  return (s:gsub('%%', '%%%%'))
end

---Keep the focused note's identity in the winbar while the list is collapsed.
local function update_winbar()
  if not (winid and vim.api.nvim_win_is_valid(winid)) then return end
  local row = vim.api.nvim_win_get_cursor(winid)[1]
  local item = (line_items[bufnr] or {})[row]
  if item then
    local id = item.id or '?'
    if id:sub(1, 1) ~= '#' then id = '#' .. id end
    local bits = { id }
    if item.priority and item.priority ~= '' then bits[#bits + 1] = item.priority end
    if item.status and item.status ~= '' then bits[#bits + 1] = item.status end
    local loc = ('%s:%d'):format(note_file(item), note_line(item))
    -- Drop the location first on narrow panels; the list already shows
    -- groups and the expanded footer carries it when there is room.
    local avail = vim.api.nvim_win_get_width(winid) - 14
    local text = table.concat(bits, ' · ')
    if disp(text) + 3 + disp(loc) <= avail then
      bits[#bits + 1] = loc
    elseif disp(text) > avail then
      bits = { id }
      if item.priority and item.priority ~= '' and item.priority ~= 'medium' then
        bits[#bits + 1] = item.priority
      end
    end
    pcall(function()
      vim.wo[winid].winbar = ('%%#DevNotesHeader# DevNotes  %s '):format(
        escape_statusline(table.concat(bits, ' · ')))
    end)
  else
    pcall(function()
      vim.wo[winid].winbar = ('%%#DevNotesHeader# DevNotes %%=%d notes '):format(total_notes)
    end)
  end
end

local function paint(items)
  if bufnr == nil or not vim.api.nvim_buf_is_valid(bufnr) then return end
  last_items = items or {}
  local lines, marks, linemap, titles = render(last_items)
  line_items[bufnr] = linemap
  note_rows[bufnr] = titles
  last_linemap = linemap
  vim.bo[bufnr].modifiable = true
  vim.api.nvim_buf_set_lines(bufnr, 0, -1, false, lines)
  vim.bo[bufnr].modifiable = false
  vim.api.nvim_buf_clear_namespace(bufnr, ns, 0, -1)
  for _, m in ipairs(marks) do
    pcall(vim.api.nvim_buf_set_extmark, bufnr, ns, m.row, m.col, {
      end_col = m.end_col, hl_group = m.hl, priority = 120,
    })
  end
  total_notes = 0
  for _ in pairs(titles) do total_notes = total_notes + 1 end
  if winid and vim.api.nvim_win_is_valid(winid) then
    pcall(vim.api.nvim_win_set_var, winid, 'devnotes_symbols_count', total_notes)
    update_winbar()
  end
end

-- ---------------------------------------------------------------------------
-- Actions
-- ---------------------------------------------------------------------------

local function item_at_cursor()
  if bufnr == nil then return nil end
  local row = vim.api.nvim_win_get_cursor(0)[1]
  local map = line_items[bufnr] or {}
  return map[row]
end

local function note_row_list()
  local rows = {}
  for row in pairs(note_rows[bufnr] or {}) do rows[#rows + 1] = row end
  table.sort(rows)
  return rows
end

---Move to the previous/next note title row, skipping expanded detail lines.
function M.move(delta)
  local rows = note_row_list()
  if #rows == 0 then return end
  local cur = vim.api.nvim_win_get_cursor(0)[1]
  local target
  if delta > 0 then
    for _, row in ipairs(rows) do
      if row > cur then target = row; break end
    end
  else
    for i = #rows, 1, -1 do
      if rows[i] < cur then target = rows[i]; break end
    end
  end
  if target then
    vim.api.nvim_win_set_cursor(0, { target, 0 })
    update_winbar()
  end
end

---Expand/collapse the note under the cursor.
function M.toggle_details()
  local item = item_at_cursor()
  if not item then return end
  local key = item_key(item)
  if expanded[key] then
    expanded[key] = nil
  else
    -- Accordion: only one note is expanded at a time.
    expanded = {}
    expanded[key] = true
  end
  paint(last_items)
  for row, it in pairs(last_linemap) do
    if it == item and (note_rows[bufnr] or {})[row] then
      vim.api.nvim_win_set_cursor(0, { row, 0 })
      break
    end
  end
  update_winbar()
end

function M.jump()
  local item = item_at_cursor()
  if not item then return end
  local uri = (item.location or {}).uri
  if not uri then return end

  local panel = winid
  local target = vim.uri_to_bufnr(uri)
  vim.fn.bufload(target)

  if current_close_on_jump and panel and vim.api.nvim_win_is_valid(panel) then
    -- Floating panel: closing it returns focus to the window underneath.
    M.close()
  elseif panel and vim.api.nvim_win_is_valid(panel)
    and vim.api.nvim_get_current_win() == panel then
    -- Docked panel: never replace the panel's own buffer. Move to the
    -- previous window (or create one if the panel is somehow alone).
    local alt = vim.fn.win_getid(vim.fn.winnr('#'))
    if alt ~= 0 and alt ~= panel and vim.api.nvim_win_is_valid(alt) then
      vim.api.nvim_set_current_win(alt)
    else
      vim.cmd('vsplit')
    end
  end

  vim.api.nvim_set_current_buf(target)
  local s = ((item.location or {}).range or {}).start or {}
  vim.api.nvim_win_set_cursor(0, { (s.line or 0) + 1, s.character or 0 })
end

function M.preview()
  local item = item_at_cursor()
  if not item then return end
  local status = status_of(item)
  -- Server IDs include the leading '#' (grammar token); normalize anyway.
  local id = item.id or '?'
  if id:sub(1, 1) ~= '#' then id = '#' .. id end
  local meta = {}
  if item.category then meta[#meta + 1] = '`' .. item.category .. '`' end
  meta[#meta + 1] = '`' .. status .. '`'
  if item.priority and item.priority ~= '' then meta[#meta + 1] = '`' .. item.priority .. '`' end
  for _, t in ipairs(item.tags or {}) do meta[#meta + 1] = t end
  for _, a in ipairs(item.authors or {}) do meta[#meta + 1] = '@' .. a end
  if (item.referenceCount or 0) > 0 then
    meta[#meta + 1] = ('%d refs'):format(item.referenceCount)
  end
  local lines = {
    ('### %s · %s'):format(id, item.title or '(untitled)'),
    '',
    table.concat(meta, ' · '),
  }
  if item.body and item.body ~= '' then
    lines[#lines + 1] = ''
    for _, l in ipairs(vim.split(item.body, '\n', { plain = true })) do
      lines[#lines + 1] = l
    end
  end
  lines[#lines + 1] = ''
  lines[#lines + 1] = ('**file:** %s:%d'):format(note_file(item), note_line(item))
  vim.lsp.util.open_floating_preview(lines, 'markdown', { border = 'rounded', max_width = math.max(opts.width, 80) })
end

function M.refresh()
  fetch_items(paint)
end

function M.close()
  if winid and vim.api.nvim_win_is_valid(winid) then
    vim.api.nvim_win_close(winid, true)
  end
  winid = nil
end

-- ---------------------------------------------------------------------------
-- Window management
-- ---------------------------------------------------------------------------

local function ensure_buffer()
  if bufnr and vim.api.nvim_buf_is_valid(bufnr) then return bufnr end
  bufnr = vim.api.nvim_create_buf(false, true)
  vim.bo[bufnr].bufhidden = 'hide'
  vim.bo[bufnr].buftype = 'nofile'
  vim.bo[bufnr].swapfile = false
  vim.bo[bufnr].buflisted = false
  vim.bo[bufnr].filetype = 'devnotes-symbols'
  line_items[bufnr] = {}
  local km = vim.tbl_extend('force', KEYMAPS, opts.keymaps or {})
  local map = vim.keymap.set
  local o = { buffer = bufnr, nowait = true, silent = true }
  map('n', km.jump, function() M.jump() end, vim.tbl_extend('force', o, { desc = 'DevNotes symbols: jump to note' }))
  map('n', km.preview, function() M.preview() end, vim.tbl_extend('force', o, { desc = 'DevNotes symbols: preview note' }))
  map('n', 'K', function() M.preview() end, vim.tbl_extend('force', o, { desc = 'DevNotes symbols: preview note' }))
  map('n', km.close, function() M.close() end, vim.tbl_extend('force', o, { desc = 'DevNotes symbols: close' }))
  map('n', km.refresh, function() M.refresh() end, vim.tbl_extend('force', o, { desc = 'DevNotes symbols: refresh' }))
  map('n', 'j', function() M.move(1) end, vim.tbl_extend('force', o, { desc = 'DevNotes symbols: next note' }))
  map('n', 'k', function() M.move(-1) end, vim.tbl_extend('force', o, { desc = 'DevNotes symbols: previous note' }))
  map('n', '<Tab>', function() M.toggle_details() end, vim.tbl_extend('force', o, { desc = 'DevNotes symbols: toggle details' }))
  map('n', 'za', function() M.toggle_details() end, vim.tbl_extend('force', o, { desc = 'DevNotes symbols: toggle details' }))
  vim.api.nvim_create_autocmd('CursorMoved', {
    buffer = bufnr,
    callback = function() update_winbar() end,
  })
  return bufnr
end

local function open_float(b)
  local width = math.min(opts.width, vim.o.columns - 4)
  local height = math.min(opts.height, vim.o.lines - 6)
  local cfg = {
    relative = 'editor',
    width = width,
    height = height,
    col = math.floor((vim.o.columns - width) / 2),
    row = math.floor((vim.o.lines - height) / 2) - 1,
    border = 'rounded',
    style = 'minimal',
  }
  -- `title` arrived in nvim 0.10; retry without it on older versions.
  local ok, win = pcall(vim.api.nvim_open_win, b, true,
    vim.tbl_extend('force', cfg, { title = ' DevNotes ', title_pos = 'center' }))
  if not ok then
    _, win = pcall(vim.api.nvim_open_win, b, true, cfg)
  end
  return win
end

local function open_split(b, direction)
  local cmds = {
    left = 'topleft vsplit',
    right = 'botright vsplit',
    top = 'topleft split',
    bottom = 'botright split',
  }
  vim.cmd(cmds[direction] or cmds.right)
  vim.api.nvim_win_set_buf(0, b)
  local vertical = direction == 'left' or direction == 'right'
  local size = vertical and opts.width or opts.height
  pcall(vim.cmd, (vertical and 'vertical resize ' or 'resize ') .. size)
  return vim.api.nvim_get_current_win()
end

---Open (or focus) the symbol window.
---@param style string|nil 'float'|'left'|'right'|'top'|'bottom' override
function M.open(style)
  define_highlights()
  style = style or opts.style
  if winid and vim.api.nvim_win_is_valid(winid) then
    vim.api.nvim_set_current_win(winid)
    M.refresh()
    return
  end
  local b = ensure_buffer()
  -- Per-open: unset option means "close for floats, stay open for splits".
  local close_on_jump = opts.close_on_jump == nil and (style == 'float') or opts.close_on_jump
  if style == 'float' then
    winid = open_float(b)
  else
    winid = open_split(b, style)
  end
  current_close_on_jump = close_on_jump
  vim.wo[winid].wrap = false
  vim.wo[winid].number = false
  vim.wo[winid].relativenumber = false
  vim.wo[winid].signcolumn = 'no'
  vim.wo[winid].foldcolumn = '0'
  vim.wo[winid].spell = false
  vim.wo[winid].cursorline = true
  M.refresh()
end

function M.toggle()
  if winid and vim.api.nvim_win_is_valid(winid) then
    M.close()
  else
    M.open()
  end
end

---Merge user configuration; called from require('devnotes').setup().
function M.setup(user)
  user = user or {}
  opts = vim.tbl_deep_extend('force', DEFAULTS, user)
  -- Auto-refresh while the window is visible: re-query on entering the
  -- window and after saving any file (notes may have changed).
  vim.api.nvim_create_autocmd({ 'BufEnter', 'WinEnter' }, {
    group = vim.api.nvim_create_augroup('DevNotesSymbolsRefresh', { clear = true }),
    callback = function(args)
      if winid and vim.api.nvim_win_is_valid(winid) and args.buf == bufnr then
        M.refresh()
      end
    end,
  })
  vim.api.nvim_create_autocmd('BufWritePost', {
    group = vim.api.nvim_create_augroup('DevNotesSymbolsSave', { clear = true }),
    callback = function()
      if winid and vim.api.nvim_win_is_valid(winid) then
        M.refresh()
      end
    end,
  })
  -- Re-render on resize without a server round-trip so truncation and the
  -- column layout adapt to the panel's current width.
  vim.api.nvim_create_autocmd({ 'WinResized', 'VimResized' }, {
    group = vim.api.nvim_create_augroup('DevNotesSymbolsResize', { clear = true }),
    callback = function()
      if winid and vim.api.nvim_win_is_valid(winid) then
        paint(last_items)
      end
    end,
  })
  return M
end

return M

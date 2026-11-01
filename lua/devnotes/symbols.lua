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
-- Window keymaps (buffer-local):
--   <CR>  jump to the note in the source file
--   p/K   floating preview of the note body
--   r     refresh from the server
--   q     close the window
--
-- Layout is configurable via require('devnotes').setup{ symbols = {...} }:
--   style          'float' (default) | 'left' | 'right' | 'top' | 'bottom'
--   width          columns for vertical splits / float width (default 62)
--   height         rows for horizontal splits / float height (default 24)
--   group_by       'category' (default) | 'file' | 'none'
--   sort           passed to devnotes.list (default: server default)
--   show_resolved  include resolved/deprecated notes (default true)
--   close_on_jump  close the window after jumping (default: true for floats)
--   keymaps        table of { jump, preview, close, refresh } LHs

local M = {}

local DEFAULTS = {
  style = 'float',
  width = 62,
  height = 24,
  group_by = 'category',
  sort = 'default',
  show_resolved = true,
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

local PRIORITY_HL = { P0 = 'DevNotesP0', P1 = 'DevNotesP1', P2 = 'DevNotesP2', P3 = 'DevNotesP3' }

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
  local params = { command = 'devnotes.list', arguments = { { filter = filter, sort = opts.sort, includeBody = true } } }
  local handler = function(err, result)
    if seq ~= fetch_seq then return end -- superseded by a newer refresh
    if err then
      vim.notify('devnotes: list failed: ' .. tostring(err.message or err), vim.log.levels.ERROR)
      return
    end
    cb(result and result.items or {})
  end
  -- client.request(method, params, handler) works on 0.8+; client:request is
  -- 0.11-only, so prefer the long form and fall back if removed.
  if client.request then
    client.request('workspace/executeCommand', params, handler)
  else
    client:request('workspace/executeCommand', params, handler)
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

local function pad(s, n)
  if #s >= n then return s end
  return s .. string.rep(' ', n - #s)
end

local function sort_key(item)
  local f, l = note_file(item), note_line(item)
  return ('%s:%06d:%s'):format(f, l, item.id or '')
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
    table.sort(b.items, function(a, c) return sort_key(a) < sort_key(c) end)
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
  return groups
end

---Concatenate {text, hl} segments into a line, returning the line plus
---0-based byte-range highlight specs. `#text` is byte length, which is
---exactly what extmark columns expect (glyphs are multibyte-safe).
local function assemble(segments)
  local parts, marks, col = {}, {}, 0
  for _, seg in ipairs(segments) do
    local text, hl = seg[1], seg[2]
    if hl and text ~= '' then
      marks[#marks + 1] = { col = col, end_col = col + #text, hl = hl }
    end
    col = col + #text
    parts[#parts + 1] = text
  end
  return table.concat(parts), marks
end

---Build the display lines, extmark highlights and line->item map.
---@param items table[] devnotes.list items
---@return string[] lines, table[] marks, table linemap
local function render(items)
  local lines, marks, linemap = {}, {}, {}
  local function cat_hl(item) return CATEGORY_HL[item.category] or 'DevNotesCatContext' end

  if opts.group_by == 'none' then
    table.sort(items, function(a, c) return sort_key(a) < sort_key(c) end)
    for _, item in ipairs(items) do
      local status = status_of(item)
      local prio = item.priority or ''
      local id = item.id or ''
      local path = ('%s:%d'):format(note_file(item), note_line(item))
      local line, ms = assemble({
        { '  ' },
        { GLYPHS[status] or GLYPHS.open, GLYPH_HL[status] or 'DevNotesGlyphOpen' },
        { ' ' },
        { pad(prio, 2), PRIORITY_HL[prio] or 'DevNotesP3' },
        { ' ' },
        { pad(item.category or '', 11), cat_hl(item) },
        { '  ' },
        { id, 'DevNotesId' },
        { '  ' },
        { item.title or '(untitled)' },
        { '  ' },
        { path, 'DevNotesPath' },
      })
      local row = #lines + 1
      lines[#lines + 1] = line
      linemap[row] = item
      for _, m in ipairs(ms) do
        marks[#marks + 1] = { row = row - 1, col = m.col, end_col = m.end_col, hl = m.hl }
      end
    end
    return lines, marks, linemap
  end

  for _, group in ipairs(group_items(items)) do
    local header = ('  ── %s'):format(group.label)
    local row = #lines + 1
    lines[#lines + 1] = header
    marks[#marks + 1] = { row = row - 1, col = 2, end_col = #header, hl = 'DevNotesHeader' }
    for _, item in ipairs(group.items) do
      local status = status_of(item)
      local prio = item.priority or ''
      local id = item.id or ''
      local path = ('%s:%d'):format(note_file(item), note_line(item))
      local line, ms = assemble({
        { '    ' },
        { GLYPHS[status] or GLYPHS.open, GLYPH_HL[status] or 'DevNotesGlyphOpen' },
        { ' ' },
        { pad(prio, 2), PRIORITY_HL[prio] or 'DevNotesP3' },
        { '  ' },
        { id, 'DevNotesId' },
        { '  ' },
        { item.title or '(untitled)' },
        { '  ' },
        { path, 'DevNotesPath' },
      })
      row = #lines + 1
      lines[#lines + 1] = line
      linemap[row] = item
      for _, m in ipairs(ms) do
        marks[#marks + 1] = { row = row - 1, col = m.col, end_col = m.end_col, hl = m.hl }
      end
    end
  end
  return lines, marks, linemap
end

local function paint(items)
  if bufnr == nil or not vim.api.nvim_buf_is_valid(bufnr) then return end
  local lines, marks, linemap = render(items)
  line_items[bufnr] = linemap
  vim.bo[bufnr].modifiable = true
  vim.api.nvim_buf_set_lines(bufnr, 0, -1, false, lines)
  vim.bo[bufnr].modifiable = false
  vim.api.nvim_buf_clear_namespace(bufnr, 0, 0, -1)
  for _, m in ipairs(marks) do
    pcall(vim.api.nvim_buf_set_extmark, bufnr, 0, m.row, m.col, {
      end_col = m.end_col, hl_group = m.hl, priority = 120,
    })
  end
  local total = 0
  for _ in pairs(linemap) do total = total + 1 end
  if winid and vim.api.nvim_win_is_valid(winid) then
    pcall(vim.api.nvim_win_set_var, winid, 'devnotes_symbols_count', total)
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

function M.jump()
  local item = item_at_cursor()
  if not item then return end
  local uri = (item.location or {}).uri
  if not uri then return end
  if current_close_on_jump and winid and vim.api.nvim_win_is_valid(winid) then
    M.close()
  end
  local target = vim.uri_to_bufnr(uri)
  vim.fn.bufload(target)
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
  return M
end

return M

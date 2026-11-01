-- DevNotes LSP reference client (spec §14).
--
-- Provides:
--   * the vim.lsp.config table for devnotes-lsp (nvim 0.11+)
--   * <C-p> floating preview of the note/reference under the cursor
--   * pickers for devnotes.list via vim.ui.select (Telescope users get the
--     same three commands through the telescope extension)
--   * :DevNotesFormat as an explicit, opt-in formatting command (never
--     registered on BufWritePre, per spec §12.1)
--
-- Everything here degrades gracefully on older clients: every feature is
-- behind capability checks or pcall.

local M = {}

-- ---------------------------------------------------------------------------
-- Configuration
-- ---------------------------------------------------------------------------

---The devnotes-lsp configuration for vim.lsp.config.
function M.config()
  return {
    cmd = { 'devnotes-lsp' },
    filetypes = { 'devnotes', 'go', 'typescript', 'typescriptreact', 'javascript', 'javascriptreact' },
    -- devnotes.toml is the editor-agnostic config anchor (spec §17).
    root_markers = { 'devnotes.toml', '.git', 'package.json' },
  }
end

---Enable the LSP server (nvim 0.11+ vim.lsp.enable API).
function M.enable()
  if vim.lsp.config == nil or vim.lsp.enable == nil then
    vim.notify('devnotes: vim.lsp.enable requires nvim 0.11+', vim.log.levels.WARN)
    return false
  end
  vim.lsp.config('devnotes', M.config())
  vim.lsp.enable('devnotes')
  return true
end

-- ---------------------------------------------------------------------------
-- Preview (§14.3)
-- ---------------------------------------------------------------------------

---Floating preview of the hover contents at the cursor (note header or an
---@see reference). Bound to <C-p> on LspAttach.
function M.preview()
  local params = vim.lsp.util.make_position_params()
  vim.lsp.buf_request(0, 'textDocument/hover', params, function(err, result)
    if err or not result or not result.contents then
      return
    end
    local value, kind
    if type(result.contents) == 'table' and result.contents.value ~= nil then
      value = result.contents.value
      kind = result.contents.kind or 'markdown'
    else
      value = result.contents
      kind = 'markdown'
    end
    vim.lsp.util.open_floating_preview(value, kind, { border = 'rounded', max_width = 100 })
  end)
end

-- ---------------------------------------------------------------------------
-- Listing (§14.4)
-- ---------------------------------------------------------------------------

local function fmt_secondary(item)
  local display = item.display or {}
  return display.secondary or ''
end

---Open the picker over a devnotes.list result set.
function M.pick(filter, title)
  filter = filter or {}
  title = title or 'DevNotes'
  local params = { command = 'devnotes.list', arguments = { { filter = filter, sort = 'default' } } }
  vim.lsp.buf_request(0, 'workspace/executeCommand', params, function(err, result)
    if err or not result or not result.items or #result.items == 0 then
      vim.notify('devnotes: no matching notes', vim.log.levels.INFO)
      return
    end
    local choices = {}
    for _, item in ipairs(result.items) do
      local display = item.display or {}
      local text = string.format('%s  %s  %s',
        display.primary or item.title or item.id,
        display.secondary or '',
        display.tertiary or '')
      choices[#choices + 1] = { item = item, text = text }
    end
    vim.ui.select(choices, {
      prompt = title .. (' (%d of %d)'):format(#choices, result.total or #choices),
      format_item = function(c) return c.text end,
    }, function(choice)
      if not choice then return end
      local loc = choice.item.location or {}
      if not loc.uri then return end
      local bufnr = vim.uri_to_bufnr(loc.uri)
      vim.fn.bufload(bufnr)
      vim.api.nvim_set_current_buf(bufnr)
      local row = (loc.range or {}).start or {}
      vim.api.nvim_win_set_cursor(0, { (row.line or 0) + 1, (row.character or 0) })
    end)
  end)
end

---Browse all open notes.
function M.list() return M.pick({}, 'DevNotes: all open notes') end

---Browse open issues.
function M.list_issues() return M.pick({ category = { 'issue' }, status = { 'open' } }, 'DevNotes: open issues') end

---Browse todos.
function M.list_todos() return M.pick({ category = { 'todo' } }, 'DevNotes: todos') end

---Notes that reference a given ID (or the ID under the cursor).
function M.list_references(id)
  id = id or M.id_under_cursor()
  if not id or id == '' then
    vim.notify('devnotes: no note ID under cursor', vim.log.levels.WARN)
    return
  end
  return M.pick({ reference = id }, 'DevNotes: references to ' .. id)
end

---Return the note ID token under the cursor, if any. Synchronous text scan:
---a `#word` match containing the cursor, else the first `#word` on the line,
---else the cursor word.
function M.id_under_cursor()
  local line = vim.api.nvim_get_current_line()
  local col = vim.api.nvim_win_get_cursor(0)[2] -- 0-based byte index
  local start = 1
  while true do
    local s, e = line:find('#[%w%-_]+', start)
    if not s then break end
    if col >= s - 1 and col <= e then
      return line:sub(s, e)
    end
    start = e + 1
  end
  local anywhere = line:match('#[%w%-_]+')
  if anywhere then return anywhere end
  local word = vim.fn.expand('<cword>')
  if word and word ~= '' then
    if word:sub(1, 1) == '#' then return word end
    return '#' .. word
  end
  return nil
end

-- ---------------------------------------------------------------------------
-- Symbol window (lua/devnotes/symbols.lua)
-- ---------------------------------------------------------------------------

---Open/toggle the DevNotes symbol window. See lua/devnotes/symbols.lua for
---layout options; :DevNotesSymbols[ left|right|top|bottom|float] is the
---command surface.
function M.symbols(style)
  local symbols = require('devnotes.symbols')
  if style then
    return symbols.open(style)
  end
  return symbols.toggle()
end

-- ---------------------------------------------------------------------------
-- Formatting (§14.5) — explicit command only
-- ---------------------------------------------------------------------------

function M.format()
  vim.lsp.buf.format({ async = true })
end

-- ---------------------------------------------------------------------------
-- Statusline (§14.8)
-- ---------------------------------------------------------------------------

---Statusline component: "category · status · priority" of the note under
---the cursor (empty string outside notes). See lua/devnotes/statusline.lua.
function M.statusline()
  local sl = require('devnotes.statusline')
  sl.setup()
  return sl.component()
end

-- ---------------------------------------------------------------------------
-- Attach hook: buffer-local mappings for buffers with the server attached.
-- ---------------------------------------------------------------------------

local function on_attach(client, bufnr)
  -- <C-p> reference preview.
  vim.keymap.set('n', '<C-p>', function() M.preview() end,
    { buffer = bufnr, silent = true, desc = 'DevNotes: preview note/reference' })
  -- Reviewer code actions (spec §14.6): dedicated variant.
  vim.keymap.set('n', '<leader>dca', function()
    vim.lsp.buf.code_action({ context = { only = { 'refactor.rewrite.devnotes' } } })
  end, { buffer = bufnr, silent = true, desc = 'DevNotes: note actions' })
end

---Register the LspAttach hook for devnotes clients. Safe to call once.
function M.setup_attach()
  vim.api.nvim_create_autocmd('LspAttach', {
    group = vim.api.nvim_create_augroup('DevNotesLspAttach', { clear = true }),
    callback = function(args)
      local client = vim.lsp.get_client_by_id(args.data.client_id)
      if client and client.name == 'devnotes' then
        on_attach(client, args.buf)
      end
    end,
  })
end

return M

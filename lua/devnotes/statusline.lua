-- Statusline component for the DevNotes LSP (spec §14.8).
--
-- component() returns "category · status · priority" when the cursor is
-- inside a note, or "" otherwise.
--
-- The statusline evaluation context forbids API calls, so this module keeps
-- a small per-buffer cache refreshed from CursorMoved/CursorHold autocmds
-- (async, outside the fast context) rather than querying LSP synchronously.
--
-- Usage:
--   statusline = '%{luaeval("require(\'devnotes.statusline\').component()")}'
-- or from a lua statusline:
--   require('devnotes.statusline').component()
--
-- Call setup() once (the plugin does this automatically) to start refreshing.

local M = {}

local cache = {}  -- bufnr -> { row = <0-based line>, value = <string> }
local pending = {} -- bufnr -> { row = ... , done = bool }

local function apply(bufnr, row)
  local results_ok, results = pcall(vim.lsp.buf_request_all, bufnr,
    'textDocument/documentSymbol',
    { textDocument = vim.lsp.util.make_text_document_params(bufnr) },
    function(results)
      local value = ''
      for _, res in pairs(results or {}) do
        for _, sym in ipairs(res.result or {}) do
          local r = (sym.selectionRange or sym.range or {})
          local s = (r.start or {}).line or -1
          local e = (r['end'] or {}).line or -1
          if row >= s and row <= e then
            value = sym.detail or ''
          end
        end
      end
      cache[bufnr] = { row = row, value = value }
      pending[bufnr] = nil
      vim.cmd('redrawstatus')
    end)
  if not results_ok then
    pending[bufnr] = nil
  end
end

local function refresh()
  local bufnr = vim.api.nvim_get_current_buf()
  if not vim.api.nvim_buf_is_valid(bufnr) then return end
  local row = vim.api.nvim_win_get_cursor(0)[1] - 1
  local c = cache[bufnr]
  if c and c.row == row and pending[bufnr] == nil then
    return
  end
  if pending[bufnr] then return end
  -- Only refresh for buffers with an attached devnotes client.
  for _, client in pairs(vim.lsp.get_clients({ bufnr = bufnr })) do
    if client.name == 'devnotes' then
      pending[bufnr] = { row = row }
      vim.schedule(function() apply(bufnr, row) end)
      return
    end
  end
  cache[bufnr] = { row = row, value = '' }
end

---Start the cache-refresh autocmds. Idempotent.
function M.setup()
  if M._setup then return end
  M._setup = true
  local group = vim.api.nvim_create_augroup('DevNotesStatusline', { clear = true })
  vim.api.nvim_create_autocmd({ 'CursorMoved', 'CursorHold', 'LspAttach' }, {
    group = group,
    callback = function()
      vim.schedule(refresh)
    end,
  })
end

---Statusline text for the current buffer/cursor ("" outside notes).
function M.component()
  local bufnr = vim.api.nvim_get_current_buf()
  local row = vim.api.nvim_win_get_cursor(0)[1] - 1
  local c = cache[bufnr]
  if c and c.row == row then
    return c.value
  end
  refresh()
  if c then return c.value end
  return ''
end

return M

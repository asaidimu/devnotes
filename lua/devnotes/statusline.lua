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

local function devnotes_client(bufnr)
  for _, client in pairs(vim.lsp.get_clients({ bufnr = bufnr })) do
    if client.name == 'devnotes' then return client end
  end
end

local function apply(bufnr, row)
  local client = devnotes_client(bufnr)
  if not client then
    cache[bufnr] = { row = row, value = '' }
    pending[bufnr] = nil
    return
  end
  local params = {
    command = 'devnotes.atPosition',
    arguments = { { uri = vim.uri_from_bufnr(bufnr), line = row } },
  }
  local handler = function(err, result)
    local value = ''
    if not err and result then
      local parts = {}
      if result.category and result.category ~= '' then parts[#parts + 1] = result.category end
      if result.status and result.status ~= '' then parts[#parts + 1] = result.status end
      if result.priority and result.priority ~= '' then parts[#parts + 1] = result.priority end
      value = table.concat(parts, ' · ')
    end
    cache[bufnr] = { row = row, value = value }
    pending[bufnr] = nil
    vim.cmd('redrawstatus')
  end
  local ok
  if vim.fn.has('nvim-0.11') == 1 then
    ok = pcall(function() client:request('workspace/executeCommand', params, handler, bufnr) end)
  else
    ok = pcall(function() client.request('workspace/executeCommand', params, handler, bufnr) end)
  end
  if not ok then pending[bufnr] = nil end
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
  if not devnotes_client(bufnr) then
    cache[bufnr] = { row = row, value = '' }
    return
  end
  pending[bufnr] = { row = row }
  vim.schedule(function() apply(bufnr, row) end)
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

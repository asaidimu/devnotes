-- DevNotes editor integration for Neovim.
--
-- setup() wires up:
--   1. filetype detection for .dn / .devnotes files
--   2. registration of the local devnotes parser with nvim-treesitter
--      (modern main-branch API: parsers table + install_info.path)
--   3. an F3 adapter that highlights DevNotes notes written inside Go/TS
--      comments by re-parsing each comment with the pure devnotes grammar.

local M = {}

-- Repo root derived from this file's own path:
--   <repo>/lua/devnotes/init.lua  ->  <repo>
local function default_path()
  local src = debug.getinfo(1, 'S').source or '?'
  local p = src:sub(1, 1) == '@' and src:sub(2) or src
  p = vim.fn.fnamemodify(p, ':p')
  return vim.fn.fnamemodify(p, ':h:h:h')
end

local opts = { path = default_path() }

local HOST_FILETYPES = {
  go = true,
  typescript = true,
  typescriptreact = true,
  javascript = true,
  javascriptreact = true,
}

local configured = false

function M.setup(user)
  user = user or {}
  opts = vim.tbl_deep_extend('force', opts, user)
  if configured then
    return M
  end
  configured = true

  -- 1. Filetypes
  vim.filetype.add({ extension = { dn = 'devnotes', devnotes = 'devnotes' } })
  vim.treesitter.language.register('devnotes', { 'devnotes' })

  -- 2. Local parser registration for nvim-treesitter (new main).
  vim.api.nvim_create_autocmd('User', {
    pattern = 'TSUpdate',
    callback = function()
      require('nvim-treesitter.parsers')['devnotes'] = {
        install_info = { path = opts.path, queries = 'queries' },
      }
    end,
  })

  -- 3. F3 in-comment highlighting (Go/TS host files only; .dn files are
  -- highlighted natively by tree-sitter once the parser is installed).
  local f3 = require('devnotes.f3')
  vim.api.nvim_create_autocmd('FileType', {
    callback = function(args)
      if not HOST_FILETYPES[vim.bo[args.buf].filetype] then
        return
      end
      vim.keymap.set('n', 'F3', function()
        f3.highlight_buf(args.buf)
      end, { buffer = args.buf })
    end,
  })

  vim.api.nvim_create_user_command('DevnotesF3', function(e)
    f3.highlight_buf(e.buf)
  end, {})

  return M
end

---@return string path to the devnotes checkout
function M.path()
  return opts.path
end

return M
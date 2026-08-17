-- Plugin entry for the repo-local DevNotes nvim integration.
--
-- Usable two ways:
--   1. From lazy.nvim:   { dir = '~/devnotes/nvim' }
--   2. Manually:         `:luafile ~/devnotes/nvim/plugin/devnotes.lua`
if vim.g.loaded_devnotes == nil then
  vim.g.loaded_devnotes = true
  pcall(function()
    require('devnotes').setup()
  end)
end
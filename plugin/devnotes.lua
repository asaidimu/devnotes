-- Plugin entry for the DevNotes nvim integration.
--
-- From lazy.nvim:
--   {
--     'asaidimu/devnotes',            -- repo root IS the plugin
--     build = ':TSInstall devnotes',  -- compile the parser
--   }
-- Manual:
--   `:luafile ~/devnotes/plugin/devnotes.lua`
if vim.g.loaded_devnotes == nil then
  vim.g.loaded_devnotes = true
  pcall(function()
    require('devnotes').setup()
  end)
end
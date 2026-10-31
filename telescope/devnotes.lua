-- Telescope extension for DevNotes (spec §14.4).
--
-- Loaded via require('telescope').load_extension('devnotes') when the
-- telescope plugin is present; the vim.ui.select fallback in
-- lua/devnotes/lsp.lua must be shipped regardless.
--
-- Commands:
--   :Telescope devnotes list
--   :Telescope devnotes list_issues
--   :Telescope devnotes list_todos

local has_telescope, telescope = pcall(require, 'telescope')
if not has_telescope then
  return
end

local pickers = require('telescope.pickers')
local finders = require('telescope.finders')
local sorters = require('telescope.sorters')
local actions = require('telescope.actions.state')

local M = {}

local function open_note(item)
  local loc = item.location or {}
  if not loc.uri then return end
  local target = vim.uri_to_bufnr(loc.uri)
  vim.fn.bufload(target)
  vim.api.nvim_set_current_buf(target)
  local row = (loc.range or {}).start or {}
  vim.api.nvim_win_set_cursor(0, { (row.line or 0) + 1, (row.character or 0) })
end

local function run_filter(opts, filter)
  opts = opts or {}
  local params = { command = 'devnotes.list', arguments = { { filter = filter, sort = 'default' } } }
  vim.lsp.buf_request(0, 'workspace/executeCommand', params, function(err, result)
    if err or not result or not result.items then
      vim.notify('devnotes: no matching notes', vim.log.levels.INFO)
      return
    end
    pickers.new(opts, {
      prompt_title = (opts.prompt_title or 'DevNotes') .. (' (%d of %d)'):format(#result.items, result.total or #result.items),
      finder = finders.new_table({
        results = result.items,
        entry_maker = function(item)
          local display = item.display or {}
          return {
            value = item,
            display = string.format('%-60s %s', display.primary or item.id, display.secondary or ''),
            ordinal = (display.primary or '') .. ' ' .. (display.secondary or '') .. ' ' .. item.id,
          }
        end,
      }),
      sorter = sorters.get_fuzzy_sorter(),
      attach_mappings = function(prompt_bufnr)
        actions.select_default:replace(function()
          local selection = actions.get_selected_entry()
          actions.close(prompt_bufnr)
          if selection then
            open_note(selection.value)
          end
        end)
        return true
      end,
    }):find()
  end)
end

M.list = function(opts) run_filter(opts, {}) end
M.list_issues = function(opts) run_filter(opts, { category = { 'issue' }, status = { 'open' } }) end
M.list_todos = function(opts) run_filter(opts, { category = { 'todo' } }) end

return telescope.register_extension({ exports = M })

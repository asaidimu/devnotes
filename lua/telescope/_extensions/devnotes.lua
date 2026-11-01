-- Telescope extension for DevNotes (spec §14.4).
--
-- Loaded with:
--   require('telescope').load_extension('devnotes')
--   :Telescope devnotes notes|issues|todos|references|symbols
--
-- This file lives in lua/telescope/_extensions/ so that telescope's
-- load_extension() can resolve it on the runtimepath. The vim.ui.select
-- fallback in lua/devnotes/lsp.lua ships regardless of telescope.

local M = {}

local has_telescope, telescope = pcall(require, 'telescope')
if not has_telescope then
  -- Required outside telescope (e.g. by init.lua probing): expose nothing.
  return M
end

local pickers = require('telescope.pickers')
local finders = require('telescope.finders')
local previewers = require('telescope.previewers')
local conf = require('telescope.config').values
local action_state = require('telescope.actions.state')

local GLYPHS = { open = '○', resolved = '✔', wontfix = '⊘', deprecated = '◌' }

local function open_note(item)
  local loc = item.location or {}
  if not loc.uri then return end
  local target = vim.uri_to_bufnr(loc.uri)
  vim.fn.bufload(target)
  vim.api.nvim_set_current_buf(target)
  local row = (loc.range or {}).start or {}
  vim.api.nvim_win_set_cursor(0, { (row.line or 0) + 1, (row.character or 0) })
end

local function run_filter(opts, filter, title)
  opts = opts or {}
  local params = { command = 'devnotes.list', arguments = { { filter = filter, sort = opts.sort or 'default', includeBody = true } } }
  vim.lsp.buf_request(0, 'workspace/executeCommand', params, function(err, result)
    if err or not result or not result.items then
      vim.notify('devnotes: no matching notes', vim.log.levels.INFO)
      return
    end
    local items = result.items
    pickers.new(opts, {
      prompt_title = (opts.prompt_title or title) .. (' (%d)'):format(#items),
      finder = finders.new_table({
        results = items,
        entry_maker = function(item)
          local display = item.display or {}
          local glyph = GLYPHS[item.status or 'open'] or GLYPHS.open
          local prio = item.priority or ''
          local cat = item.category or ''
          local path = display.tertiary or ''
          local title_text = display.primary or item.title or item.id or '?'
          return {
            value = item,
            display = ('%s %-2s %-11s %-24s %s'):format(glyph, prio, cat, title_text, path),
            ordinal = glyph .. ' ' .. prio .. ' ' .. cat .. ' ' .. (item.id or '') .. ' '
              .. title_text .. ' ' .. table.concat(item.tags or {}, ' ') .. ' ' .. path,
          }
        end,
      }),
      sorter = conf.generic_sorter(opts),
      previewer = previewers.new_buffer_previewer({
        title = 'Note',
        define_preview = function(self, entry)
          local item = entry.value
          -- Server IDs include the leading '#' (grammar token); normalize.
          local id = item.id or '?'
          if id:sub(1, 1) ~= '#' then id = '#' .. id end
          local lines = {
            ('### %s · %s'):format(id, item.title or '(untitled)'),
            '',
            ('`%s` · `%s`%s'):format(item.category or '', item.status or 'open',
              item.priority and (' · `' .. item.priority .. '`') or ''),
          }
          local body = item.body
          if body and body ~= '' then
            lines[#lines + 1] = ''
            for _, l in ipairs(vim.split(body, '\n', { plain = true })) do
              lines[#lines + 1] = l
            end
          end
          vim.api.nvim_buf_set_lines(self.state.bufnr, 0, -1, false, lines)
        end,
      }),
      attach_mappings = function(prompt_bufnr)
        action_state.select_default:replace(function()
          local selection = action_state.get_selected_entry()
          local item = selection and selection.value or nil
          require('telescope.actions').close(prompt_bufnr)
          if item then
            open_note(item)
          end
        end)
        return true
      end,
    }):find()
  end)
end

-- Pickers -------------------------------------------------------------------

function M.notes(opts) run_filter(opts, {}, 'DevNotes: all open notes') end
function M.issues(opts) run_filter(opts, { category = { 'issue' }, status = { 'open' } }, 'DevNotes: open issues') end
function M.todos(opts) run_filter(opts, { category = { 'todo' } }, 'DevNotes: todos') end
function M.symbols(opts) run_filter(opts, { includeResolved = true, includeDeprecated = true }, 'DevNotes: symbols') end

---References to a note ID (default: the #id under the cursor).
function M.references(opts)
  opts = opts or {}
  local id = opts.id
  if not id or id == '' then
    local line = vim.api.nvim_get_current_line()
    id = line:match('#[%w%-_]+')
  end
  if not id or id == '' then
    vim.notify('devnotes: no note ID under cursor', vim.log.levels.WARN)
    return
  end
  run_filter(opts, { reference = id }, 'DevNotes: references to ' .. id)
end

-- Legacy aliases (the names shipped with the first extension revision). -----
M.list = M.notes
M.list_issues = M.issues
M.list_todos = M.todos

return telescope.register_extension({
  exports = M,
})

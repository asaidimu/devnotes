-- F3 adapter: highlights DevNotes notes embedded in Go/TS/JS comments.
--
-- For every comment region in the host buffer it strips the comment markers
-- (matching the SPEC 5 normalization the Go engine applies), re-parses the
-- normalized text with the pure devnotes grammar, and paints the highlight
-- captures back onto the host comment with NVIM extmarks.
--
-- The mapping is byte-column based, matching nvim's own (row, col) convention.

local M = {}

-- Capture name -> highlight group. nvim's Query.captures strips the leading
-- "@" (e.g. "keyword" for "@keyword").
local GROUP = {
  keyword = 'TSKeyword',
  constant = 'TSConstant',
  type = 'TSType',
  string = 'TSString',
  tag = 'TSTag',
  ['punctuation.delimiter'] = 'TSPunctDelimiter',
  ['function'] = 'TSFunction',
  ['string.special'] = 'TSStringSpecial',
}

-- ---------------------------------------------------------------------------
-- Comment collection
-- ---------------------------------------------------------------------------

local function deep_comments(node, out)
  if node:type() == 'comment' then
    out[#out + 1] = node
    return
  end
  for child in node:iter_children() do
    deep_comments(child, out)
  end
end

local function node_text(bufnr, node)
  local t = vim.treesitter.get_node_text(node, bufnr)
  if type(t) == 'table' then
    t = table.concat(t, '\n')
  end
  return t
end

-- Merge contiguous line comments into single regions so a note spanning
-- several `//` lines is parsed as one unit, mirroring the Go adapters.
local function comment_regions(bufnr, nodes)
  local regions = {}
  local cur
  for _, n in ipairs(nodes) do
    local sr, sc, er, ec = n:range()
    local text = node_text(bufnr, n)
    local is_line = vim.startswith(text, '//')
    local above = cur ~= nil
      and cur.style == 'line'
      and er == cur.end_row + 1
      and sc == cur.start_col
    if above then
      cur.lines[#cur.lines + 1] = text
      cur.end_row, cur.end_col = er, ec
    else
      if cur then
        regions[#regions + 1] = cur
      end
      local lines = is_line and { text } or vim.split(text, '\n', { plain = true })
      cur = {
        style = is_line and 'line' or 'block',
        start_row = sr,
        start_col = sc,
        end_row = er,
        end_col = ec,
        lines = lines,
      }
    end
  end
  if cur then
    regions[#regions + 1] = cur
  end
  return regions
end

-- ---------------------------------------------------------------------------
-- Normalization (mirrors internal/normalize)
-- ---------------------------------------------------------------------------

local function strip_marker(line, marker, len)
  if vim.startswith(line, marker) then
    local rest = line:sub(len + 1)
    if vim.startswith(rest, ' ') then
      rest = rest:sub(2)
    end
    return rest
  end
  return line
end

-- Normalize a region into per-line strings plus a shift table so captures can
-- be mapped back to host byte columns. Returns out, shift, rowoff, nbuffer:
--   out[i]    normalized line i (0-based)
--   shift[i]  bytes to add to a normalized column on line i to get the host col
--   rowoff    host row of normalized line 0
--   nbuffer   number of normalized lines (captures beyond this are skipped)
local function normalize_region(region)
  local out, shift = {}, {}
  local rowoff, nbuffer
  if region.style == 'line' then
    rowoff = region.start_row
    nbuffer = #region.lines
    for i, line in ipairs(region.lines) do
      local stripped = strip_marker(line, '//', 2)
      shift[#shift + 1] = #line - #stripped
      out[#out + 1] = stripped
    end
  else
    local lines = region.lines
    if #lines == 1 then
      -- Single-line block: /* ... */
      rowoff = region.start_row
      nbuffer = 1
      local line = lines[1]:gsub('^%s*/%*', ''):gsub('%*/%s*$', '')
      local stripped = line:gsub('^%s*%*%s?', '')
      shift[#shift + 1] = #lines[1] - #stripped
      out[#out + 1] = stripped
    else
      -- Multi-line block: drop the /* and */ delimiter rows, keep only the
      -- interior lines (mirrors the engine's normalizeBlock).
      rowoff = region.start_row + 1
      nbuffer = #lines - 2
      for i = 2, #lines - 1 do
        local line = lines[i]
        local stripped = line:gsub('^%s*%*%s?', '')
        shift[#shift + 1] = #line - #stripped
        out[#out + 1] = stripped
      end
    end
  end
  for i = 1, #out do
    if out[i]:find('\r$') then
      out[i] = out[i]:sub(1, -2)
    end
  end
  return out, shift, rowoff, nbuffer
end

-- ---------------------------------------------------------------------------
-- Highlighting
-- ---------------------------------------------------------------------------

local lua_root = vim.fn.fnamemodify(debug.getinfo(1, 'S').source:sub(2), ':p:h:h:h:h')

local function devnotes_query()
  local ok, query = pcall(vim.treesitter.query.get, 'devnotes', 'highlights')
  if ok and query then
    return query
  end
  -- Fallback: load the repo's queries/highlights.scm directly.
  local scm = lua_root .. '/queries/highlights.scm'
  local content = table.concat(vim.fn.readfile(scm), '\n')
  return vim.treesitter.query.parse('devnotes', content)
end

-- Parse normalized lines with the devnotes grammar on a scratch buffer.
local function parse_normalized(lines)
  local buf = vim.api.nvim_create_buf(false, true)
  vim.api.nvim_buf_set_lines(buf, 0, -1, false, lines)
  local ok, parser = pcall(vim.treesitter.get_parser, buf, 'devnotes')
  if not ok then
    vim.api.nvim_buf_delete(buf, { force = true })
    return nil
  end
  local tree = parser:parse()[1]
  local root = tree and tree:root()
  if not root or root:has_error() then
    vim.api.nvim_buf_delete(buf, { force = true })
    return nil
  end
  return buf, root
end

local ns = vim.api.nvim_create_namespace('devnotes-f3')

local function paint(bufnr, ns_id, region)
  if #region.lines == 0 then
    return
  end
  -- Pad with a blank line so a trailing note parses even without a final
  -- separator (SPEC 5/26); captures beyond the region are never painted.
  local norm, shift, rowoff, nbuffer = normalize_region(region)
  local padded = vim.deepcopy(norm)
  padded[#padded + 1] = ''
  local buf, root = parse_normalized(padded)
  if not buf then
    return
  end
  local query = devnotes_query()

  for id, node, _ in query:iter_captures(root, buf) do
    local group = GROUP[query.captures[id]]
    if group then
      local nr, nc, ner, nec = node:range()
      if nr < nbuffer and ner <= nbuffer then
        vim.api.nvim_buf_set_extmark(bufnr, ns_id,
          nr + rowoff, shift[nr + 1] + nc,
          { end_row = ner + rowoff, end_col = shift[ner + 1] + nec, hl_group = group })
      end
    end
  end
  vim.api.nvim_buf_delete(buf, { force = true })
end

---Highlight every DevNote found in comments of the given buffer.
---@param bufnr number
function M.highlight_buf(bufnr)
  vim.api.nvim_buf_clear_namespace(bufnr, ns, 0, -1)
  local ok, parser = pcall(vim.treesitter.get_parser, bufnr)
  if not ok then
    vim.notify('devnotes: host parser not available', vim.log.levels.WARN)
    return
  end
  local tree = parser:parse()[1]
  if not tree then
    return
  end
  local comments = {}
  deep_comments(tree:root(), comments)
  local regions = comment_regions(bufnr, comments)
  for _, r in ipairs(regions) do
    paint(bufnr, ns, r)
  end
end

return M
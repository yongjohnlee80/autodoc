---autodoc.preview.render.highlights — the preview's highlight groups.
---
---Every group is defined with `default = true` as a link to a group the
---stock colorschemes already carry, so a colorscheme or the user can
---restyle any of them with a plain `:hi`. `apply()` is idempotent and is
---re-run on `ColorScheme` (a `:hi clear` drops default links).
---@module 'autodoc.preview.render.highlights'

local M = {}

---@type table<string, string>
M.LINKS = {
  AutodocPreviewH1          = "@markup.heading.1",
  AutodocPreviewH2          = "@markup.heading.2",
  AutodocPreviewH3          = "@markup.heading.3",
  AutodocPreviewH4          = "@markup.heading.4",
  AutodocPreviewH5          = "@markup.heading.5",
  AutodocPreviewH6          = "@markup.heading.6",
  AutodocPreviewBullet      = "@markup.list",
  AutodocPreviewListNumber  = "@markup.list",
  AutodocPreviewTaskOpen    = "@markup.list.unchecked",
  AutodocPreviewTaskDone    = "@markup.list.checked",
  AutodocPreviewQuote       = "@markup.quote",
  AutodocPreviewNote        = "DiagnosticInfo",
  AutodocPreviewTip         = "DiagnosticOk",
  AutodocPreviewImportant   = "DiagnosticHint",
  AutodocPreviewWarning     = "DiagnosticWarn",
  AutodocPreviewCaution     = "DiagnosticError",
  AutodocPreviewCode        = "ColorColumn",
  AutodocPreviewCodeLabel   = "Comment",
  AutodocPreviewHint        = "DiagnosticHint",
  AutodocPreviewRule        = "FloatBorder",
  AutodocPreviewTableBorder = "FloatBorder",
  AutodocPreviewTableHead   = "Title",
  AutodocPreviewFrontmatter = "Special",
  AutodocPreviewImage       = "@markup.link",
  AutodocPreviewLinkDef     = "Comment",
}

function M.apply()
  for name, target in pairs(M.LINKS) do
    vim.api.nvim_set_hl(0, name, { link = target, default = true })
  end
end

return M

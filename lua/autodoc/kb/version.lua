-- The AutoDoc version the KB's managed files (KB_OPERATIONS.md, _schema/frontmatter.yaml) are
-- stamped with. A release that changes either file bumps this, so KBs scaffolded by an older
-- AutoDoc take the new copy; a release that changes KB_OPERATIONS.md's surface also bumps its
-- `revision:` (docs/decisions/crew.md#managed-kb-files).
return {
  autodoc = "0.3.6",
}

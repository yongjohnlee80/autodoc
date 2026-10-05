-- The AutoDoc version the KB's managed files (KB_OPERATIONS.md, _schema/frontmatter.yaml) are
-- stamped with. A release that changes either file bumps this, so KBs scaffolded by an older
-- AutoDoc take the new copy; a release that changes KB_OPERATIONS.md's surface also bumps its
-- `revision:` (ADR 1791209946 §3.4).
return {
  autodoc = "0.1.15",
}

-- The AutoDoc version the KB's managed files (KB_OPERATIONS.md, _schema/frontmatter.yaml) are
-- stamped with. A release that changes either file bumps this, so KBs scaffolded by an older
-- AutoDoc take the new copy; a release that changes KB_OPERATIONS.md's surface also bumps its
-- `revision:` (docs/decisions/crew.md#managed-kb-files).
--
-- It is the one source of that stamp: the daemon embeds this file and stamps KB_OPERATIONS.md with
-- the same version (autodoc.KBManagedVersion). digest is sha256(KB_OPERATIONS.md, NUL,
-- _schema/frontmatter.yaml) as this version ships them; a test (kbversion_test.go) fails when
-- either file changes without a new version and digest here.
return {
  autodoc = "0.3.7",
  digest = "6d1e0d17399d8a5cb8bb142b357211d19b47281dd114827795dc708b9ab6dbd9",
}

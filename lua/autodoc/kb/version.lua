-- The AutoDoc version the KB's managed files (KB_OPERATIONS.md, _schema/frontmatter.yaml) are
-- stamped with. A release that changes either file bumps this, so KBs scaffolded by an older
-- AutoDoc take the new copy; a release that changes KB_OPERATIONS.md's surface also bumps its
-- `revision:` (docs/decisions/crew.md#managed-kb-files).
--
-- It is the one source of that stamp: the daemon embeds this file and stamps KB_OPERATIONS.md with
-- the same version (autodoc.KBManagedVersion). A test (kbversion_test.go) fails when either managed
-- file differs from the last release's while this version isn't newer than that release's.
return {
  autodoc = "0.3.8",
}

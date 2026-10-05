---
type: about
status: active
created: {{date}}
tags: [kb, about, scripts]
abstract: "What belongs in scripts/: the KB's helper scripts, such as test-harness launchers."
---

# scripts/

- **Holds:** helper scripts the KB's documents tell agents to run (test-harness launchers and the like).
- **Naming:** `<kebab-name>.<ext>`, executable when meant to be run.
- **Type:** scripts carry no frontmatter. This ABOUT.md is the folder's document.
- **Rules:** no credentials in scripts. A script that needs a secret reads it from the environment or a credential store.

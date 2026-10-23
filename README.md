# Blaze desktop builds (temporary)

Temporary public build runner for Blaze desktop. Builds run only on pushes to short-lived `app/desktop*` branches
(workflow `.github/workflows/desktop.yml`); artifacts are kept 3 days.

Branches hold scrubbed source snapshots (no history) and are deleted after their artifacts are collected.
Nothing here is signed, released or published: official downloads come from the Blaze site only, signed outside GitHub.
Workflows have read-only permissions and use no secrets.

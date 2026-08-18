# Documentation website

The guides are published as a MkDocs Material site at
<https://abubakarsiddik31.github.io/golem/>. `docs/guides/` remains the
single source of truth: the site renders those files verbatim, with the
nav in `mkdocs.yml` mirroring the reading order in
`docs/guides/index.md` — keep the two in sync when adding a guide.

Only `docs/guides/` is published (`docs_dir` in `mkdocs.yml`).
Contributor documents — `foundation.md`, `patterns.md`, `adr/`, and
`upstream-references.md` — stay in the repository, as does
`docs/guides/TEMPLATE.md` (`exclude_docs`).

## Local preview

```bash
python3 -m venv .venv-docs && . .venv-docs/bin/activate
pip install -r requirements-docs.txt
mkdocs serve
```

`mkdocs build --strict` is what CI runs; it fails on warnings, so
reproduce with it before pushing when in doubt.

## Publishing

`.github/workflows/docs.yml` builds the site on every change to the
guides and deploys it with the official Pages actions. The deploy job
is gated on `github.repository_visibility == 'public'` while the
repository is private, so only the strict build runs.

To go live:

1. Make the repository public.
2. In Settings → Pages, set Source to "GitHub Actions".
3. Run the workflow once via *Actions → Docs → Run workflow*.

Links to `edit_uri` point at `edit/main/docs/guides/`, so the "edit on
GitHub" pencil opens the guide source directly.

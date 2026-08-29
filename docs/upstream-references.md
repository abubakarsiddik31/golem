# Upstream reference cache

Golem keeps a local, Git-ignored checkout of the official [Pydantic AI repository](https://github.com/pydantic/pydantic-ai) for design research.

## Location and scope

The cache lives at `reference/pydantic-ai/docs/`. It is a shallow sparse checkout containing only the upstream `docs/` directory, so it remains local and does not enter Golem commits.

It was initially fetched on 2026-08-15 at upstream commit `25a70926cfafdfc63b3d32c1b5f2c7f139e2c58c`.
Last refreshed on 2026-08-28 at upstream commit `ecce65f0a64b9c48412db3deb0ab86fa0aee8073`.

## Refresh

From the Golem repository root, run:

```bash
git -C reference/pydantic-ai pull --ff-only
git -C reference/pydantic-ai rev-parse HEAD
```

If the cache does not exist, recreate it with:

```bash
git clone --depth 1 --filter=blob:none --sparse https://github.com/pydantic/pydantic-ai.git reference/pydantic-ai
git -C reference/pydantic-ai sparse-checkout set docs
```

## Use it well

Treat Pydantic AI as a source of product and API ideas. Translate the underlying intent into Go's explicit types, `context.Context`, errors, and package boundaries; do not port Python decorators, runtime metaprogramming, or provider assumptions.

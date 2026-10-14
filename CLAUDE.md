# db-backup

- For any Go change, use the `golang-*` skills in `.agents/skills` (code style, error handling, concurrency, context, safety, security, naming, testing, lint) and write the test first.
- Before pushing: `lefthook install` once, then the hooks run gofmt, vet, shellcheck, hygiene, race tests, golangci-lint, go mod tidy and govulncheck.
- This repository is public and generic: no names, hosts or paths from any deployment.
- Configuration is the YAML file described in `docs/reference.md`; the upper-case names in the code (`GZIP_LEVEL`) are internal.
- Restore the skills with `npx skills experimental_install`.

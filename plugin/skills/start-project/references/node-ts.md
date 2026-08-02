# Stack: Node + TypeScript

## Commands (fill AGENTS.md with these)

| Task | Command |
|---|---|
| Install | `npm ci` |
| Build | `npm run build` |
| Test (all) | `npm test` |
| Test (single) | `npm test -- path/to/file.test.ts` |
| Lint | `npm run lint` |
| Format | `npm run format` |
| Typecheck | `npm run typecheck` |

Swap `npm` for `pnpm`/`yarn`/`bun` if the user said so — keep it consistent
everywhere, including CI.

## CI setup block

```yaml
      - uses: actions/setup-node@v4
        with:
          node-version: '22'
          cache: 'npm'
```

## Dependabot ecosystem

```yaml
  - package-ecosystem: npm
    directory: "/"
    schedule: { interval: weekly }
    open-pull-requests-limit: 10
    labels: [dependencies]
    commit-message: { prefix: "chore(deps)" }
    groups:
      minor-and-patch:
        update-types: [minor, patch]
```

## CodeQL language

`javascript-typescript`

## Notes
- The `format-and-lint` hook already runs `prettier --write` then `eslint --fix`
  on changed files via `npx --no-install`. Add both as devDependencies or the
  hook silently no-ops.
- Set `"type": "module"` unless there is a reason not to.

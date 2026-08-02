# Stack: Go

## Commands

| Task | Command |
|---|---|
| Install | `go mod download` |
| Build | `go build ./...` |
| Test (all) | `go test ./...` |
| Test (single) | `go test ./pkg -run TestName` |
| Lint | `golangci-lint run` |
| Format | `gofmt -w .` |
| Typecheck | `go vet ./...` (the compiler is the typechecker) |

## CI setup block

```yaml
      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'
          cache: true
```

## Dependabot ecosystem

```yaml
  - package-ecosystem: gomod
    directory: "/"
    schedule: { interval: weekly }
    labels: [dependencies]
```

## CodeQL language

`go`

## Notes
- The `format-and-lint` hook runs `gofmt -w` then `goimports -w` on changed files.
- Go has no separate typecheck step; map Typecheck to `go vet ./...` in AGENTS.md
  rather than leaving the row blank.

# Stack: Rust

## Commands

| Task | Command |
|---|---|
| Install | `cargo fetch` |
| Build | `cargo build --release` |
| Test (all) | `cargo test` |
| Test (single) | `cargo test test_name` |
| Lint | `cargo clippy -- -D warnings` |
| Format | `cargo fmt` |
| Typecheck | `cargo check` |

## CI setup block

```yaml
      - uses: dtolnay/rust-toolchain@stable
        with:
          components: clippy, rustfmt
      - uses: Swatinem/rust-cache@v2
```

## Dependabot ecosystem

```yaml
  - package-ecosystem: cargo
    directory: "/"
    schedule: { interval: weekly }
    labels: [dependencies]
```

## CodeQL language

CodeQL has no Rust analyzer. Rely on `clippy -D warnings` in CI and leave
`codeql.yml` on `workflow_dispatch`.

## Notes
- The `format-and-lint` hook runs `rustfmt` on changed `.rs` files.

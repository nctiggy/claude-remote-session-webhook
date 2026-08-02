# Stack: Python

## Commands

| Task | Command |
|---|---|
| Install | `uv sync` (or `pip install -e ".[dev]"`) |
| Build | `uv build` |
| Test (all) | `pytest` |
| Test (single) | `pytest path/to/test_file.py::test_name` |
| Lint | `ruff check .` |
| Format | `ruff format .` |
| Typecheck | `mypy .` (or `pyright`) |

## CI setup block

```yaml
      - uses: actions/setup-python@v5
        with:
          python-version: '3.12'
      - run: pipx install uv && uv sync
```

## Dependabot ecosystem

```yaml
  - package-ecosystem: pip
    directory: "/"
    schedule: { interval: weekly }
    labels: [dependencies]
```

## CodeQL language

`python`

## Notes
- The `format-and-lint` hook prefers `ruff` and falls back to `black`+`isort`.
  Choosing ruff means the hook works with no extra config.
- Prefer `pyproject.toml` over setup.py; the CI stack-detection looks for it.

# Contract: Precedence and provenance

**Files**: `internal/config/source.go` (new), `internal/config/config.go` (wiring only)
**Tests**: `internal/config/source_test.go`
**Satisfies**: FR-003, FR-004, FR-011, FR-018; enables SC-002 and SC-004

This is the keystone of the milestone. It is deliberately tiny, and its size is
the design: **precedence has one implementation, four lines long.**

---

## The chain

```
flag  >  environment  >  file  >  default
```

A flag is typed by a person at the moment of running, so the most immediate
statement wins. The environment beats the file so a container or a test can
change one value without writing one (FR-004). The file beats nothing but the
built-in default, which is why a daemon with no file behaves exactly as it does
today (FR-003, SC-002).

## The shim

`config.LoadFrom(getenv func(string) string, warn io.Writer, opts ...Option)`
already exists and is already the single place every value is validated. The file
becomes a *source behind that seam*, never a system beside it:

```go
// withFile answers from the environment first and the file second, and records
// which one answered. It is the only code that knows, which is why it is also
// the only code that records.
func withFile(getenv func(string) string, f *File, src map[string]Source) func(string) string {
	return func(key string) string {
		if v := getenv(key); v != "" {
			src[key] = SourceEnv
			return v
		}
		if v, ok := f.Lookup(key); ok {
			src[key] = SourceFile
			return v
		}
		src[key] = SourceDefault
		return ""
	}
}
```

**No bound, no default, and no refusal is written twice.** A value supplied by a
file travels through the exact loader an environment variable travels through, so
it cannot mean one thing in a unit test and another in a file.

## Provenance

`Source` is recorded as a byproduct of deciding, never inferred afterwards:

```go
type Source uint8
const (
	SourceDefault Source = iota
	SourceFile
	SourceEnv
	SourceFlag
)
```

Inference is rejected explicitly: a value present and equal in both sources is
indistinguishable by comparison, and that is precisely the case where an operator
is asking why their edit did nothing.

## Worked example

Given the file from [config-file.md](./config-file.md), and an environment
holding `CRSW_LISTEN=0.0.0.0:9000` and nothing else:

| Key | Value used | Source |
|---|---|---|
| `CRSW_LISTEN` | `0.0.0.0:9000` | `SourceEnv` — the environment beat the file |
| `CRSW_ALLOWED_ROOTS` | `/home/nctiggy/code,/home/nctiggy/work` | `SourceFile` |
| `CRSW_IDLE_TIMEOUT` | `-1` | `SourceFile` |
| `CRSW_PANE_BOUND` | (built-in default) | `SourceDefault` |

The first row is the one the settings page exists to show. An operator who edited
`listen` in the file and saw no change reads "environment" and stops guessing.

## Contract tests

| Test | Asserts | **Must fail when** |
|---|---|---|
| `TestEnvBeatsFile` | Env value wins; source is `SourceEnv` | The file is merged over the environment |
| `TestFileBeatsDefault` | File value wins over built-in; source is `SourceFile` | The file is parsed but never consulted — the exact bug on the abandoned branch |
| `TestNoFileMatchesTodayExactly` | With no file, every value and every error equals the pre-milestone behaviour | Absence changes any default — this is SC-002 |
| `TestEmptyEnvValueDoesNotBeatFile` | `CRSW_LISTEN=""` falls through to the file | The shim tests presence instead of non-emptiness |
| `TestSourceRecordedForEveryKey` | Every key in `config.go` has a `Source` after `Load` | A key is added to `config.go` and the shim never sees it |
| `TestSourceIsNotInferred` | Value equal in file and env is still reported `SourceEnv` | Provenance is computed by comparing sources after the fact |
| `TestFileValueIsValidatedIdentically` | A bad bound from a file produces the *same* error as from the environment | The file layer adds validation of its own |
| `TestSecretNeverInProvenanceLog` | Nothing written to `warn` contains a secret value | A debug line prints what it resolved |

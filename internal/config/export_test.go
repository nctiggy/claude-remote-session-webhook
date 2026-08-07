package config

// The one internal seam this package's external tests reach through, compiled
// only under `go test` and absent from every shipping build.
//
// It exists for the rename mechanism (T004). renamedKeys is empty and stays
// empty until a rename actually ships, so the only way to prove that a former
// spelling loads — rather than being refused as an unknown key — is to hand the
// parser a fixture table. The alternative is entering a rename that never
// happened, which would tell operators to migrate off a key no released daemon
// ever read, and would leave the mechanism first exercised by the release that
// depends on it.

import "io"

// ParseFileWithRenames is parseFile with the rename table injected.
func ParseFileWithRenames(path string, data []byte, renames map[string]string, warn io.Writer) (*File, error) {
	return parseFile(path, data, renames, warn)
}

// RenamedKeys reports how many renames ship today, so a test can pin that the
// table is still empty without being able to add to it.
func RenamedKeys() int { return len(renamedKeys) }

// MigrateWithRenames is migrate with the rename table injected, for the same
// reason: the half of `crswd config migrate` that rewrites a former spelling
// has nothing to rewrite until a rename ships, and a migration first run by the
// release that needs it is run against the operator's only copy.
func MigrateWithRenames(path string, data []byte, renames map[string]string, warn io.Writer) ([]byte, bool, error) {
	return migrate(path, data, renames, warn)
}

// MaxConfigFileBytes is the read bound, exposed so the test that proves a file
// past it is refused builds its fixture from the same number the check uses. A
// test carrying its own copy passes the day the bound changes and the fixture
// stops being oversize.
const MaxConfigFileBytes = maxConfigFileBytes

// CheckDependenciesWith is Config.CheckDependencies with the PATH probe and the
// host's own identification injected (T029, T030), so a test can describe a
// Debian host with no tmux on it.
//
// The alternative is emptying the process's own PATH, which is one variable
// shared by every test in this binary: the case would have to run alone, and the
// suite it ran alone in would be one os.Setenv away from probing the real host
// instead of the described one. /etc/os-release is worse again — it is the
// machine the suite is running on, so the refusal's install command would be
// whatever the developer or the CI runner happens to be.
func CheckDependenciesWith(c Config, lookPath func(string) (string, error), osRelease func() []byte, warn io.Writer) error {
	return c.checkDependencies(lookPath, osRelease, warn)
}

// InstallAdviceFor is the install clause the tmux refusal carries, derived from
// the bytes of an /etc/os-release and a GOOS (T030).
//
// Exported for the tests because the derivation is the part that has to be
// exercised against six platforms and this binary only ever runs on one of them.
func InstallAdviceFor(osRelease []byte, goos string) string {
	return installAdvice(osRelease, goos)
}

// GenericInstallAdvice is what an unrecognised platform is told, exposed so the
// test for it asserts against the sentence the daemon really prints. A test
// carrying its own copy passes on the day the two stop matching, which is the
// day the assertion was for.
const GenericInstallAdvice = genericInstallAdvice

// ReadOsRelease is the production reader, and MaxOsReleaseBytes its bound.
//
// Both are exposed for the one claim an injected fixture cannot make: that the
// file this daemon opens is the file the host writes. Every other case hands the
// derivation bytes, so a reader opening the wrong path would leave all of them
// green and every Linux host in the world unidentified.
func ReadOsRelease() []byte { return readOsRelease() }

// MaxOsReleaseBytes is the read bound, so the test comparing against the real
// file truncates it by the same number rather than by its own copy of one.
const MaxOsReleaseBytes = maxOsReleaseBytes

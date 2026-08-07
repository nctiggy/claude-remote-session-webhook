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

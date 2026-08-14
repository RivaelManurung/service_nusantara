package role_test

import (
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/modules/role"
)

// migrationPath is the seed that populates the permissions table.
const migrationPath = "../../platform/database/migrations/0003_permissions.sql"

// seedRow matches one ('code', 'label', 'Group') tuple of the seed INSERT.
var seedRow = regexp.MustCompile(`\('([a-z_]+\.[a-z_]+)',\s*'([^']+)',\s*'([^']+)'\)`)

// TestCatalogMatchesMigrationSeed is the reason the catalogue cannot drift.
//
// The Go list is what middleware and the service validate against; the SQL is
// what actually exists in the database. If they disagree, a permission the code
// checks has no row -- so it can never be granted and the guard is permanently
// closed -- or a row exists that nothing enforces, and the admin UI offers a
// checkbox that does nothing. Neither failure is visible at runtime, which is
// why it is caught here.
func TestCatalogMatchesMigrationSeed(t *testing.T) {
	// Arrange
	contents, err := os.ReadFile(migrationPath)
	require.NoError(t, err, "the permissions migration must exist")

	matches := seedRow.FindAllStringSubmatch(string(contents), -1)
	require.NotEmpty(t, matches, "no seed rows found in %s", migrationPath)

	seeded := make(map[string]role.Definition, len(matches))
	for _, match := range matches {
		seeded[match[1]] = role.Definition{Code: match[1], Label: match[2], Group: match[3]}
	}

	// Act / Assert
	catalog := role.Catalog()
	assert.Len(t, seeded, len(catalog),
		"the migration seeds a different number of permissions than the catalogue declares")

	for _, def := range catalog {
		row, ok := seeded[def.Code]
		if !assert.True(t, ok, "%s is in the Go catalogue but not seeded by the migration", def.Code) {
			continue
		}
		assert.Equal(t, def.Label, row.Label, "label drifted for %s", def.Code)
		assert.Equal(t, def.Group, row.Group, "group drifted for %s", def.Code)
	}

	known := role.KnownCodes()
	for code := range seeded {
		_, ok := known[code]
		assert.True(t, ok, "%s is seeded by the migration but nothing in the code checks it", code)
	}
}

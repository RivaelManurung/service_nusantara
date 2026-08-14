package seed

import "github.com/google/uuid"

// namespace anchors every seeded identifier. It is an arbitrary but fixed UUID:
// changing it would orphan every row a previous run created.
var namespace = uuid.MustParse("3f2b8c14-6d5a-4e91-9a77-2c8e5b1d4a03")

// id derives a stable UUID from a kind and a key.
//
// Deterministic ids are what make the seeder idempotent: running it twice
// updates the same rows instead of inserting a second copy, and a product can
// reference its shop without either having been inserted yet.
func id(kind, key string) uuid.UUID {
	return uuid.NewSHA1(namespace, []byte(kind+":"+key))
}

// Helpers for the model's nullable columns.
func ptr[T any](value T) *T { return &value }

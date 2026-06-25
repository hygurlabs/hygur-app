package controlplane

import (
	"crypto/rand"
	"math/big"
)

// Friendly instance names: adjective-color-noun (e.g. "brave-azure-harbor"),
// DNS- and namespace-safe (lowercase + hyphens). Used as the default tenant id
// so a customer's space has a memorable URL (cloud.hygur.ai/brave-azure-harbor)
// instead of an opaque number. ~20³ ≈ 8k combinations; the caller enforces
// uniqueness and regenerates on the rare collision.
var (
	nameAdjectives = []string{"brave", "calm", "clever", "bright", "gentle", "bold", "quiet", "swift", "warm", "wise", "keen", "lively", "merry", "noble", "proud", "sunny", "witty", "eager", "fair", "glad"}
	nameColors     = []string{"amber", "azure", "coral", "crimson", "emerald", "golden", "indigo", "ivory", "jade", "olive", "scarlet", "silver", "teal", "violet", "cobalt", "hazel", "ruby", "slate", "sage", "rose"}
	nameNouns      = []string{"harbor", "meadow", "summit", "river", "forest", "canyon", "lagoon", "beacon", "garden", "haven", "orchard", "valley", "cove", "grove", "ridge", "bay", "cliff", "delta", "fjord", "glade"}
)

func pickWord(words []string) string {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(words))))
	if err != nil {
		return words[0]
	}
	return words[n.Int64()]
}

// GenerateInstanceName returns a friendly adjective-color-noun slug.
func GenerateInstanceName() string {
	return pickWord(nameAdjectives) + "-" + pickWord(nameColors) + "-" + pickWord(nameNouns)
}

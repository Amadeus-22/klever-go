package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestP2PKeyFileToLoad(t *testing.T) {
	t.Parallel()

	const path = "./config/node/p2pKey.pem"

	cases := []struct {
		name            string
		explicitlySet   bool
		redundancyLevel int64
		expected        string
	}{
		{"main machine, default path", false, 0, path},
		{"main machine, explicit path", true, 0, path},
		{"redundancy disabled, default path", false, -1, path},
		// a backup's config folder is often a copy of the main's: never pick up its key implicitly
		{"backup, default path", false, 1, ""},
		{"second backup, default path", false, 2, ""},
		{"backup, explicit path", true, 1, path},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, c.expected, p2pKeyFileToLoad(path, c.explicitlySet, c.redundancyLevel))
		})
	}
}

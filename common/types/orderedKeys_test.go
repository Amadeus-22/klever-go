package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOrderedKeys(t *testing.T) {
	for range 100 {
		require.Equal(t, []string{"a", "b", "c"}, OrderedKeys(map[string]int{"c": 3, "a": 1, "b": 2}, true))
		require.Equal(t, []int32{-1, 2, 10}, OrderedKeys(map[int32]string{10: "c", -1: "a", 2: "b"}, true))
	}
	for _, sorted := range []bool{false, true} {
		require.Empty(t, OrderedKeys(map[string]int(nil), sorted))
		require.Empty(t, OrderedKeys(map[string]int{}, sorted))
		require.ElementsMatch(t, []string{"c", "a", "b"}, OrderedKeys(map[string]int{"c": 3, "a": 1, "b": 2}, sorted))
	}
}

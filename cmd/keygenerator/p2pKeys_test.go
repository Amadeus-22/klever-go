package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klever-io/klever-go/network/p2p/libp2p/p2pkey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveP2PKeys_WritesLoadableKeyFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	require.NoError(t, saveP2PKeys(1, false))

	path := filepath.Join(dir, "p2pKey.pem")
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())

	sk, err := p2pkey.LoadOrCreate(path)
	require.NoError(t, err)
	assert.NotNil(t, sk)
}

func TestSaveP2PKeys_MultipleKeysUseOneFolderEach(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	require.NoError(t, saveP2PKeys(2, false))

	sk0, err := p2pkey.LoadOrCreate(filepath.Join(dir, "node-0", "p2pKey.pem"))
	require.NoError(t, err)
	sk1, err := p2pkey.LoadOrCreate(filepath.Join(dir, "node-1", "p2pKey.pem"))
	require.NoError(t, err)
	assert.False(t, sk0.Equals(sk1))
}

func TestSaveP2PKeys_NoSplitWritesAllKeysToOneFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	require.NoError(t, saveP2PKeys(3, true))

	content, err := os.ReadFile(filepath.Join(dir, "p2pKey.pem"))
	require.NoError(t, err)
	assert.Equal(t, 3, strings.Count(string(content), "-----BEGIN PRIVATE KEY for "))
}

func TestOutputP2PKeys_InvalidCountErrors(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	assert.Error(t, outputP2PKeys(0, false, false))
	assert.Error(t, outputP2PKeys(0, true, false))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "an invalid count must not create any file")
}

func TestCheckP2PKeyPassword_RejectsEncryption(t *testing.T) {
	t.Parallel()

	assert.NoError(t, checkP2PKeyPassword("", ""))
	assert.ErrorIs(t, checkP2PKeyPassword("secret", ""), errP2PKeyPassword)
	assert.ErrorIs(t, checkP2PKeyPassword("", "/some/file"), errP2PKeyPassword)
}

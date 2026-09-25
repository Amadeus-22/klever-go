package p2pkey_test

import (
	"bytes"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/klever-io/klever-go/network/p2p/libp2p/p2pkey"
	libp2pCrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func peerIDOf(t *testing.T, sk libp2pCrypto.PrivKey) peer.ID {
	t.Helper()
	pid, err := peer.IDFromPrivateKey(sk)
	require.NoError(t, err)
	return pid
}

func TestGenerate_ReturnsDistinctSecp256k1Keys(t *testing.T) {
	t.Parallel()

	sk1, err := p2pkey.Generate()
	require.NoError(t, err)
	sk2, err := p2pkey.Generate()
	require.NoError(t, err)

	assert.Equal(t, libp2pCrypto.Secp256k1, int(sk1.Type()))
	assert.False(t, sk1.Equals(sk2))
}

func TestEncode_WritesMultiversXCompatiblePEM(t *testing.T) {
	t.Parallel()

	sk, err := p2pkey.Generate()
	require.NoError(t, err)

	buf := &bytes.Buffer{}
	require.NoError(t, p2pkey.Encode(buf, sk))

	blk, rest := pem.Decode(buf.Bytes())
	require.NotNil(t, blk)
	assert.Empty(t, rest)
	assert.Equal(t, "PRIVATE KEY for "+peerIDOf(t, sk).String(), blk.Type)

	raw, err := sk.Raw()
	require.NoError(t, err)
	assert.Equal(t, hex.EncodeToString(raw), string(blk.Bytes))
}

func TestLoadOrCreate_EmptyPathErrors(t *testing.T) {
	t.Parallel()

	sk, err := p2pkey.LoadOrCreate("")
	assert.ErrorIs(t, err, p2pkey.ErrEmptyKeyFilePath)
	assert.Nil(t, sk)
}

func TestLoadOrCreate_MissingFileCreatesAndPersists(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sub", "p2pKey.pem")

	sk, err := p2pkey.LoadOrCreate(path)
	require.NoError(t, err)
	require.NotNil(t, sk)

	info, err := os.Stat(path)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	}

	again, err := p2pkey.LoadOrCreate(path)
	require.NoError(t, err)
	assert.True(t, sk.Equals(again), "second start must load the persisted key")
	assert.Equal(t, peerIDOf(t, sk), peerIDOf(t, again))
}

func TestLoadOrCreate_LoadsExistingFile(t *testing.T) {
	t.Parallel()

	sk, err := p2pkey.Generate()
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "p2pKey.pem")
	buf := &bytes.Buffer{}
	require.NoError(t, p2pkey.Encode(buf, sk))
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0600))

	loaded, err := p2pkey.LoadOrCreate(path)
	require.NoError(t, err)
	assert.True(t, sk.Equals(loaded))
}

func TestLoadOrCreate_SaveFailureErrors(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("read-only directory is not enforced for this user/platform")
	}

	dir := filepath.Join(t.TempDir(), "ro")
	require.NoError(t, os.Mkdir(dir, 0500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) }) // #nosec G302 -- restore a test dir so TempDir cleanup can remove it

	sk, err := p2pkey.LoadOrCreate(filepath.Join(dir, "p2pKey.pem"))
	assert.Error(t, err)
	assert.Nil(t, sk)
}

func TestLoadOrCreate_MalformedFileErrors(t *testing.T) {
	t.Parallel()

	cases := map[string][]byte{
		"empty":     {},
		"not pem":   []byte("garbage"),
		"bad label": pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: []byte("00")}),
		"bad hex":   pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY for x", Bytes: []byte("zz")}),
		"short key": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY for x", Bytes: []byte("0102")}),
	}

	for name, content := range cases {
		content := content
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "p2pKey.pem")
			require.NoError(t, os.WriteFile(path, content, 0600))

			sk, err := p2pkey.LoadOrCreate(path)
			assert.Error(t, err)
			assert.Nil(t, sk)

			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, content, after, "a malformed key file must never be overwritten")
		})
	}
}

func TestLoadOrGenerate_MissingFileUsesEphemeralKeyWithoutSaving(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "p2pKey.pem")

	sk1, fromFile, err := p2pkey.LoadOrGenerate(path)
	require.NoError(t, err)
	require.NotNil(t, sk1)
	assert.False(t, fromFile)

	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err), "a node must not persist a generated key")

	sk2, _, err := p2pkey.LoadOrGenerate(path)
	require.NoError(t, err)
	assert.False(t, sk1.Equals(sk2), "each start without a key file gets a fresh identity")
}

func TestLoadOrGenerate_EmptyPathUsesEphemeralKey(t *testing.T) {
	t.Parallel()

	sk, fromFile, err := p2pkey.LoadOrGenerate("")
	require.NoError(t, err)
	assert.NotNil(t, sk)
	assert.False(t, fromFile)
}

func TestLoadOrGenerate_LoadsExistingFile(t *testing.T) {
	t.Parallel()

	sk, err := p2pkey.Generate()
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "p2pKey.pem")
	buf := &bytes.Buffer{}
	require.NoError(t, p2pkey.Encode(buf, sk))
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0600))

	loaded, fromFile, err := p2pkey.LoadOrGenerate(path)
	require.NoError(t, err)
	assert.True(t, fromFile)
	assert.True(t, sk.Equals(loaded))
}

func TestLoadOrGenerate_MalformedFileErrors(t *testing.T) {
	t.Parallel()

	path := writeKeyFile(t, "PRIVATE KEY for x", strings.Repeat("00", 32))

	sk, fromFile, err := p2pkey.LoadOrGenerate(path)
	assert.ErrorIs(t, err, p2pkey.ErrInvalidKey)
	assert.Nil(t, sk)
	assert.False(t, fromFile)
}

func danglingKeyLink(t *testing.T) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need extra privileges on windows")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "p2pKey.pem")
	require.NoError(t, os.Symlink(filepath.Join(dir, "unmounted", "p2pKey.pem"), path))

	return path
}

func TestLoadOrGenerate_DanglingLinkErrors(t *testing.T) {
	t.Parallel()

	sk, fromFile, err := p2pkey.LoadOrGenerate(danglingKeyLink(t))
	assert.Error(t, err, "a configured key file that cannot be read must not fall back to a random identity")
	assert.Nil(t, sk)
	assert.False(t, fromFile)
}

func TestLoadOrCreate_DanglingLinkErrorsWithoutGenerating(t *testing.T) {
	t.Parallel()

	path := danglingKeyLink(t)

	sk, err := p2pkey.LoadOrCreate(path)
	require.Error(t, err)
	assert.Nil(t, sk)
	assert.Contains(t, err.Error(), "while loading p2p key file", "the dangling link must be reported as a load failure")

	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".p2pKey-*"))
	require.NoError(t, err)
	assert.Empty(t, leftovers)
}

func pathUnderRegularFile(t *testing.T) string {
	t.Helper()

	file := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0600))

	return filepath.Join(file, "p2pKey.pem")
}

func TestLoadOrGenerate_PathUnderRegularFileErrors(t *testing.T) {
	t.Parallel()

	sk, fromFile, err := p2pkey.LoadOrGenerate(pathUnderRegularFile(t))
	assert.Error(t, err, "an unusable configured path must not fall back to a random identity")
	assert.Nil(t, sk)
	assert.False(t, fromFile)
}

func TestLoadOrCreate_PathUnderRegularFileErrors(t *testing.T) {
	t.Parallel()

	sk, err := p2pkey.LoadOrCreate(pathUnderRegularFile(t))
	assert.Error(t, err)
	assert.Nil(t, sk)
}

// not parallel: replaces the package-level directory sync
func TestLoadOrCreate_DirSyncFailureAfterLinkKeepsTheKey(t *testing.T) {
	restore := p2pkey.SetSyncDir(func(string) error {
		return errors.New("fsync on directory not supported")
	})
	defer restore()

	path := filepath.Join(t.TempDir(), "p2pKey.pem")

	sk, err := p2pkey.LoadOrCreate(path)
	require.NoError(t, err, "the key file is already published, so the node must run with it")
	require.NotNil(t, sk)

	persisted, err := p2pkey.LoadOrCreate(path)
	require.NoError(t, err)
	assert.True(t, sk.Equals(persisted))
}

func TestLoadOrCreate_RemovesStaleTempKeyFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	stale := filepath.Join(dir, ".p2pKey-stale.tmp")
	fresh := filepath.Join(dir, ".p2pKey-fresh.tmp")
	require.NoError(t, os.WriteFile(stale, []byte("leftover key"), 0600))
	require.NoError(t, os.WriteFile(fresh, []byte("in-flight key"), 0600))
	old := time.Now().Add(-2 * p2pkey.StaleTempFileAge)
	require.NoError(t, os.Chtimes(stale, old, old))

	_, err := p2pkey.LoadOrCreate(filepath.Join(dir, "p2pKey.pem"))
	require.NoError(t, err)

	_, err = os.Stat(stale)
	assert.True(t, os.IsNotExist(err), "a leftover temporary key file must be removed")
	_, err = os.Stat(fresh)
	assert.NoError(t, err, "a recent temporary file may belong to a concurrent creator and must be kept")
}

func writeKeyFile(t *testing.T, label string, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "p2pKey.pem")
	content := pem.EncodeToMemory(&pem.Block{Type: label, Bytes: []byte(body)})
	require.NoError(t, os.WriteFile(path, content, 0600))

	return path
}

func TestLoadOrCreate_OutOfRangeScalarErrors(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"zero": strings.Repeat("00", 32),
		// the secp256k1 curve order N, which reduces to zero
		"curve order": "fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141",
		"above order": "fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364142",
	}

	for name, body := range cases {
		body := body
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := writeKeyFile(t, "PRIVATE KEY for x", body)
			before, err := os.ReadFile(path)
			require.NoError(t, err)

			sk, err := p2pkey.LoadOrCreate(path)
			assert.ErrorIs(t, err, p2pkey.ErrInvalidKey)
			assert.Nil(t, sk)

			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
}

func TestLoadOrCreate_LabelMismatchErrors(t *testing.T) {
	t.Parallel()

	sk, err := p2pkey.Generate()
	require.NoError(t, err)
	raw, err := sk.Raw()
	require.NoError(t, err)

	// e.g. a validatorKey.pem given by mistake: same label prefix, a BLS public key as the suffix
	path := writeKeyFile(t, "PRIVATE KEY for "+strings.Repeat("ab", 96), hex.EncodeToString(raw))

	loaded, err := p2pkey.LoadOrCreate(path)
	assert.ErrorIs(t, err, p2pkey.ErrPeerIDMismatch)
	assert.Nil(t, loaded)
}

func TestLoadOrCreate_ConcurrentCreatesAgreeOnOneKey(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "p2pKey.pem")

	const numCallers = 16
	keys := make([]libp2pCrypto.PrivKey, numCallers)
	errs := make([]error, numCallers)

	start := make(chan struct{})
	wg := sync.WaitGroup{}
	wg.Add(numCallers)
	for i := 0; i < numCallers; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start
			keys[idx], errs[idx] = p2pkey.LoadOrCreate(path)
		}(i)
	}
	close(start)
	wg.Wait()

	persisted, err := p2pkey.LoadOrCreate(path)
	require.NoError(t, err)

	for i := 0; i < numCallers; i++ {
		require.NoError(t, errs[i])
		assert.True(t, persisted.Equals(keys[i]), "caller %d runs with a key that is not the persisted one", i)
	}

	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".p2pKey-*"))
	require.NoError(t, err)
	assert.Empty(t, leftovers)
}

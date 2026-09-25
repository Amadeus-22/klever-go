// Package p2pkey loads, generates and persists the libp2p identity key of a node.
//
// The key file uses the MultiversX layout: a PEM block labelled "PRIVATE KEY for <peerID>"
// whose body is the hex encoding of the raw 32-byte secp256k1 scalar.
package p2pkey

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	logger "github.com/klever-io/klever-go-logger"
	"github.com/klever-io/klever-go/common"
	"github.com/klever-io/klever-go/tools"
	libp2pCrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

const pemTypePrefix = "PRIVATE KEY for "

const keyFileMode = 0600

const logKeyPeerID = "peer ID"

// staleTempFileAge is how old a leftover temporary key file must be before it is removed; younger
// ones may belong to a creator that is still running
const staleTempFileAge = time.Minute

const tempFilePattern = ".p2pKey-*.tmp"

// ErrEmptyKeyFilePath signals that no p2p key file path was provided
var ErrEmptyKeyFilePath = errors.New("empty p2p key file path")

// ErrInvalidKey signals that the key file holds a scalar outside [1, N-1]
var ErrInvalidKey = errors.New("invalid p2p private key")

// ErrPeerIDMismatch signals that the key file label names a different peer ID than its key
var ErrPeerIDMismatch = errors.New("p2p key file label does not match its key")

var log = logger.GetOrCreate("p2p/p2pkey")

// Generate returns a new secp256k1 identity key drawn from crypto/rand
func Generate() (libp2pCrypto.PrivKey, error) {
	sk, _, err := libp2pCrypto.GenerateSecp256k1Key(rand.Reader)
	return sk, err
}

// Encode writes the key to the writer as a PEM block
func Encode(w io.Writer, sk libp2pCrypto.PrivKey) error {
	pid, err := peer.IDFromPrivateKey(sk)
	if err != nil {
		return err
	}

	raw, err := sk.Raw()
	if err != nil {
		return err
	}

	return pem.Encode(w, &pem.Block{
		Type:  pemTypePrefix + pid.String(),
		Bytes: []byte(hex.EncodeToString(raw)),
	})
}

// LoadOrGenerate loads the key stored at path. When path is empty or the file does not exist,
// it returns a new random key that is used for this run only and never saved, so a node gets a
// fresh identity on each start unless its operator provides a key file. fromFile reports whether
// the key came from the file. An existing file that cannot be parsed is an error.
func LoadOrGenerate(path string) (sk libp2pCrypto.PrivKey, fromFile bool, err error) {
	if len(path) > 0 {
		_, err = os.Lstat(path)
		if err == nil {
			sk, err = load(path)
			return sk, err == nil, err
		}
		if !os.IsNotExist(err) {
			return nil, false, fmt.Errorf("%w while checking p2p key file %s", err, path)
		}
	}

	sk, err = Generate()
	if err != nil {
		return nil, false, err
	}

	pid, _ := peer.IDFromPrivateKey(sk)
	log.Info("no p2p key file, using a random identity for this run", "file", path, logKeyPeerID, pid.String())

	return sk, false, nil
}

// LoadOrCreate loads the key stored at path. When the file does not exist, a new key is
// generated and saved there; failing to save it is an error so a node never runs with an
// identity it cannot keep. An existing file that cannot be parsed is an error and is left untouched.
func LoadOrCreate(path string) (libp2pCrypto.PrivKey, error) {
	if len(path) == 0 {
		return nil, ErrEmptyKeyFilePath
	}

	_, err := os.Lstat(path)
	if err == nil {
		return load(path)
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("%w while checking p2p key file %s", err, path)
	}

	sk, err := Generate()
	if err != nil {
		return nil, err
	}

	err = save(path, sk)
	if errors.Is(err, fs.ErrExist) {
		// another process created the file first: run with the key that was persisted
		return load(path)
	}
	if err != nil {
		return nil, fmt.Errorf("%w while saving new p2p key file %s", err, path)
	}

	pid, _ := peer.IDFromPrivateKey(sk)
	log.Info("generated a new p2p identity key", "file", path, logKeyPeerID, pid.String())

	return sk, nil
}

func load(path string) (libp2pCrypto.PrivKey, error) {
	hexKey, label, err := tools.LoadSkPkFromPemFile(path, 0, "")
	if err != nil {
		return nil, fmt.Errorf("%w while loading p2p key file %s", err, path)
	}

	raw, err := hex.DecodeString(string(hexKey))
	if err != nil {
		return nil, fmt.Errorf("%w while decoding p2p key file %s", err, path)
	}

	// UnmarshalSecp256k1PrivateKey silently reduces mod N, so reject what would not round-trip
	var scalar secp256k1.ModNScalar
	overflow := scalar.SetByteSlice(raw)
	if len(raw) != secp256k1.PrivKeyBytesLen || overflow || scalar.IsZero() {
		return nil, fmt.Errorf("%w in p2p key file %s: expected a %d-byte scalar in [1, N-1]",
			ErrInvalidKey, path, secp256k1.PrivKeyBytesLen)
	}

	sk, err := libp2pCrypto.UnmarshalSecp256k1PrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("%w while parsing p2p key file %s", err, path)
	}

	pid, err := peer.IDFromPrivateKey(sk)
	if err != nil {
		return nil, err
	}
	if label != pid.String() {
		return nil, fmt.Errorf("%w in %s: label names %q, key belongs to %s",
			ErrPeerIDMismatch, path, label, pid.String())
	}

	warnIfExposed(path)
	log.Info("loaded p2p identity key", "file", path, logKeyPeerID, pid.String())

	return sk, nil
}

// warnIfExposed logs when the key file is readable by group or others. It does not refuse:
// secret mounts (e.g. Kubernetes defaults to 0644) would otherwise stop a node from starting.
func warnIfExposed(path string) {
	if runtime.GOOS == "windows" {
		return
	}

	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if info.Mode().Perm()&0077 != 0 {
		log.Warn("p2p key file is accessible by group or others, consider chmod 600",
			"file", path, "mode", info.Mode().Perm().String())
	}
}

// save writes the key to a temporary file in the same directory and hard-links it into place.
// A crash mid-write never leaves a truncated key file behind, and an existing file is never
// replaced: if another process created it first, save returns an error wrapping fs.ErrExist.
func save(path string, sk libp2pCrypto.PrivKey) error {
	dir := filepath.Dir(path)
	err := os.MkdirAll(dir, common.DefaultDirPermission)
	if err != nil {
		return err
	}

	removeStaleTempFiles(dir)

	tmp, err := os.CreateTemp(dir, tempFilePattern)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	err = tmp.Chmod(keyFileMode)
	if err == nil {
		err = Encode(tmp, sk)
	}
	if err == nil {
		err = tmp.Sync()
	}
	errClose := tmp.Close()
	if err != nil {
		return err
	}
	if errClose != nil {
		return errClose
	}

	err = os.Link(tmpName, path)
	if err != nil {
		return err
	}

	// persist the new directory entry too, or a power loss could drop the file and the next
	// start would silently generate a different identity. The file is already published, so a
	// filesystem that rejects a directory fsync only costs that guarantee.
	err = syncDir(dir)
	if err != nil {
		log.Warn("could not sync the p2p key file directory, the new key may not survive a power loss",
			"dir", dir, "error", err.Error())
	}

	return nil
}

// removeStaleTempFiles deletes temporary key files left behind by a creator that was killed
// before publishing its key. Recent ones are kept: they may belong to a creator still running.
func removeStaleTempFiles(dir string) {
	leftovers, err := filepath.Glob(filepath.Join(dir, tempFilePattern))
	if err != nil {
		return
	}

	for _, name := range leftovers {
		info, errStat := os.Lstat(name)
		if errStat != nil || time.Since(info.ModTime()) < staleTempFileAge {
			continue
		}

		errRemove := os.Remove(name)
		if errRemove != nil {
			log.Warn("could not remove a stale temporary p2p key file", "file", name, "error", errRemove.Error())
		}
	}
}

// syncDir is a variable so tests can simulate filesystems that reject a directory fsync
var syncDir = syncDirEntries

func syncDirEntries(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}

	d, err := os.Open(filepath.Clean(dir))
	if err != nil {
		return err
	}

	errSync := d.Sync()
	errClose := d.Close()
	if errSync != nil {
		return errSync
	}

	return errClose
}

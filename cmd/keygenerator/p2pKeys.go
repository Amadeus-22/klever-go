package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/klever-io/klever-go/network/p2p/libp2p/p2pkey"
)

const p2pType = "p2p"

var p2pKeyFilenameTemplate = "p2pKey%s.pem"

var errP2PKeyPassword = errors.New("p2p keys cannot be password-encrypted: nodes load them unattended at startup")

func checkP2PKeyPassword(password string, passwordFilePath string) error {
	if len(password) > 0 || len(passwordFilePath) > 0 {
		return errP2PKeyPassword
	}

	return nil
}

func outputP2PKeys(numKeys int, consoleOut bool, noSplit bool) error {
	if numKeys < 1 {
		return fmt.Errorf("number of keys should be a number greater or equal to 1")
	}

	if consoleOut {
		return printP2PKeys(numKeys)
	}

	return saveP2PKeys(numKeys, noSplit)
}

func printP2PKeys(numKeys int) error {
	buf := bytes.NewBufferString("P2P keys:\n")
	err := writeNewP2PKeys(buf, numKeys)
	if err != nil {
		return err
	}

	log.Info(buf.String())
	return nil
}

// saveP2PKeys writes one key per node-<i> folder, or all keys into a single file with noSplit
func saveP2PKeys(numKeys int, noSplit bool) error {
	if noSplit {
		return saveP2PKeysFile(0, numKeys, true)
	}

	for i := 0; i < numKeys; i++ {
		err := saveP2PKeysFile(i, numKeys, false)
		if err != nil {
			return err
		}
	}

	return nil
}

// saveP2PKeysFile writes all numKeys keys into one file with noSplit, otherwise the single key of node <index>
func saveP2PKeysFile(index int, numKeys int, noSplit bool) (err error) {
	keysInFile := 1
	if noSplit {
		keysInFile = numKeys
	}

	file, err := generateFile(index, numKeys, noSplit, p2pKeyFilenameTemplate)
	if err != nil {
		return err
	}
	defer func() {
		errClose := file.Close()
		if err == nil {
			err = errClose
		}
	}()

	return writeNewP2PKeys(file, keysInFile)
}

func writeNewP2PKeys(w io.Writer, count int) error {
	for i := 0; i < count; i++ {
		sk, err := p2pkey.Generate()
		if err != nil {
			return err
		}

		err = p2pkey.Encode(w, sk)
		if err != nil {
			return err
		}
	}

	return nil
}

package main

// p2pKeyFileToLoad returns the p2p key file the node should load, or "" for a random key used
// for this run only. A redundancy backup ignores the default path: its config folder is often a
// copy of the main machine's, and sharing the main's peer ID makes the backup treat the main's
// consensus messages as its own, consider it inactive and sign with the same validator key.
// A backup loads a key file only when its path is set explicitly.
func p2pKeyFileToLoad(path string, explicitlySet bool, redundancyLevel int64) string {
	if redundancyLevel > 0 && !explicitlySet {
		return ""
	}

	return path
}

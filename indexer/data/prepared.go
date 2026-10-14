package data

// PreparedBlockData carries the result of prepareTransactionsForDatabase
// shared between the websocket dispatcher and the elastic worker.
//
// Accounts and AccountsKDA are the resolved account documents. They are filled on
// the commit goroutine, where the live state adapter still holds the committed state
// of the block being indexed. The elastic worker drains its queue later, so resolving
// there would read whatever block has committed since and write it under this block's
// timestamp. The worker therefore never resolves them itself: nil Accounts means no
// snapshot was taken and the block is indexed without account documents, and an account
// that could not be resolved is left out of the map. When websocket subscribers are on,
// eventsProcessor fills Accounts with the documents it dispatched and PrepareBlockData
// reuses them rather than resolving those accounts a second time. The genesis block
// takes this path too and resolves from the live accountsDB; only indexGenesisAccounts
// bypasses it, through SaveAccounts with detached handlers unmarshalled from a captured
// root hash.
type PreparedBlockData struct {
	Txs         []*Transaction
	TxsMap      map[string]*Transaction
	Altered     *AlteredData
	Accounts    map[string]*AccountInfo
	AccountsKDA []*AccountKDA
}

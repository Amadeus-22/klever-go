package bootstrap

import (
	"context"
	"maps"
	"math"
	"sync"
	"time"

	"github.com/klever-io/klever-go/common"
	"github.com/klever-io/klever-go/core"
	"github.com/klever-io/klever-go/core/process"
	"github.com/klever-io/klever-go/crypto/hashing"
	"github.com/klever-io/klever-go/data/block"
	"github.com/klever-io/klever-go/tools/check"
	"github.com/klever-io/klever-go/tools/marshal"
)

const durationBetweenChecks = 200 * time.Millisecond
const durationBetweenReRequests = 1 * time.Second
const durationBetweenCheckingNumConnectedPeers = 500 * time.Millisecond
const minNumPeersToConsiderMetaBlockValid = 1
const minNumConnectedPeers = 1
const logFromPeer = "from peer"

var _ process.InterceptorProcessor = (*epochStartBlockProcessor)(nil)

// metaBlockVote is the latest epoch start metablock a peer delivered. The header hash identifies
// the block; the hash of the whole metablock also covers the signatures, transaction hashes and
// results, which the header hash leaves out. Both the header and the copy returned need the
// quorum.
type metaBlockVote struct {
	headerHash string
	blockHash  string
	metaBlock  *block.Block
}

type epochStartBlockProcessor struct {
	messenger                         Messenger
	requestHandler                    RequestHandler
	marshalizer                       marshal.Marshalizer
	hasher                            hashing.Hasher
	mutReceivedMetaBlocks             sync.RWMutex
	mapVotes                          map[core.PeerID]metaBlockVote
	expectedEpoch                     uint32
	hasExpectedEpoch                  bool
	startPeerCountFloor               int
	consensusPercentage               int
	minNumConnectedPeers              int
	minNumOfPeersToConsiderBlockValid int
}

// NewEpochStartBlockProcessor will return a interceptor processor for epoch start meta block
func NewEpochStartBlockProcessor(
	messenger Messenger,
	handler RequestHandler,
	marshalizer marshal.Marshalizer,
	hasher hashing.Hasher,
	consensusPercentage uint8,
	minNumConnectedPeersConfig int,
	minNumOfPeersToConsiderBlockValidConfig int,
) (*epochStartBlockProcessor, error) {
	if check.IfNil(messenger) {
		return nil, common.ErrNilMessenger
	}
	if check.IfNil(handler) {
		return nil, common.ErrNilRequestHandler
	}
	if check.IfNil(marshalizer) {
		return nil, common.ErrNilMarshalizer
	}
	if check.IfNil(hasher) {
		return nil, common.ErrNilHasher
	}
	if consensusPercentage <= 0 || consensusPercentage > 100 {
		return nil, common.ErrInvalidConsensusThreshold
	}
	if minNumConnectedPeersConfig < minNumConnectedPeers {
		return nil, common.ErrNotEnoughNumConnectedPeers
	}
	if minNumOfPeersToConsiderBlockValidConfig < minNumPeersToConsiderMetaBlockValid {
		return nil, common.ErrNotEnoughNumOfPeersToConsiderBlockValid
	}

	processor := &epochStartBlockProcessor{
		messenger:                         messenger,
		requestHandler:                    handler,
		marshalizer:                       marshalizer,
		hasher:                            hasher,
		consensusPercentage:               int(consensusPercentage),
		minNumConnectedPeers:              minNumConnectedPeersConfig,
		minNumOfPeersToConsiderBlockValid: minNumOfPeersToConsiderBlockValidConfig,
		mutReceivedMetaBlocks:             sync.RWMutex{},
		mapVotes:                          make(map[core.PeerID]metaBlockVote),
	}

	processor.waitForEnoughNumConnectedPeers(messenger)

	log.Debug("consensus percentage for epoch start meta block ", "value (%)", consensusPercentage,
		"connected peers", len(messenger.ConnectedPeers()))
	return processor, nil
}

// computePeerCountTarget returns how many distinct connected peers must deliver the same
// metablock for it to be accepted: the consensus percentage of the connected peers, rounded up,
// and never below minNumOfPeersToConsiderBlockValid. It is recomputed on every check so a
// target sampled while few peers were connected does not stick.
func (e *epochStartBlockProcessor) computePeerCountTarget(numConnectedPeers int) int {
	return max((e.consensusPercentage*numConnectedPeers+99)/100, e.minNumOfPeersToConsiderBlockValid)
}

// Validate will return nil as there is no need for validation
func (e *epochStartBlockProcessor) Validate(_ process.InterceptedData, _ core.PeerID) error {
	return nil
}

func (e *epochStartBlockProcessor) waitForEnoughNumConnectedPeers(messenger Messenger) {
	for {
		numConnectedPeers := len(messenger.ConnectedPeers())
		if numConnectedPeers >= e.minNumConnectedPeers {
			break
		}

		log.Debug("epoch bootstrapper: not enough connected peers",
			"wanted", e.minNumConnectedPeers,
			"actual", numConnectedPeers)
		time.Sleep(durationBetweenCheckingNumConnectedPeers)
	}
}

// Save will handle the consensus mechanism for the fetched metablocks
// All errors are just logged because if this function returns an error, the processing is finished. This way, we ignore
// wrong received data and wait for relevant intercepted data
func (e *epochStartBlockProcessor) Save(data process.InterceptedData, fromConnectedPeer core.PeerID, topic string) error {
	if check.IfNil(data) {
		log.Debug("epoch bootstrapper: nil intercepted data")
		return nil
	}

	log.Debug("received header", "type", data.Type(), "hash", data.Hash())
	interceptedHdr, ok := data.(process.HdrValidatorHandler)
	if !ok {
		log.Warn("saving epoch start meta block error", "error", common.ErrWrongTypeAssertion)
		return nil
	}

	metaBlock := interceptedHdr.HeaderHandler().(*block.Block)
	if !metaBlock.GetIsEpochStart() {
		log.Warn("received metablock is not of type epoch start", "error", common.ErrNotEpochStartBlock)
		return nil
	}

	log.Debug("received epoch start meta", "epoch", metaBlock.GetEpoch(), logFromPeer, fromConnectedPeer.Pretty())
	// the peer is the message author: direct sends pin it to the sending peer, and pubsub only
	// accepts gossip signed by its author's key (StrictSign), so a peer cannot vote under another
	// peer's ID. A relayed message from a peer we are not connected to could never count, so it
	// is dropped before it is marshaled and hashed
	if !e.messenger.IsConnected(fromConnectedPeer) {
		log.Debug("ignoring epoch start meta from a peer that is not connected", "peer", fromConnectedPeer.Pretty())
		return nil
	}
	metaBlockBuff, err := e.marshalizer.Marshal(metaBlock)
	if err != nil {
		log.Debug("ignoring epoch start meta that cannot be marshaled", "error", err)
		return nil
	}

	e.mutReceivedMetaBlocks.Lock()
	defer e.mutReceivedMetaBlocks.Unlock()

	if e.hasExpectedEpoch && metaBlock.GetEpoch() != e.expectedEpoch {
		log.Debug("ignoring epoch start meta for another epoch",
			"epoch", metaBlock.GetEpoch(), "expected", e.expectedEpoch, logFromPeer, fromConnectedPeer.Pretty())
		return nil
	}

	// a peer's newer answer replaces its older one. Messages are handled on separate goroutines,
	// so an older answer can arrive last: a vote only moves to a block at least as new
	previousVote, voted := e.mapVotes[fromConnectedPeer]
	if voted && previousVote.metaBlock.GetNonce() > metaBlock.GetNonce() {
		log.Debug("ignoring an epoch start meta older than the peer's vote",
			"nonce", metaBlock.GetNonce(), "vote nonce", previousVote.metaBlock.GetNonce(), logFromPeer, fromConnectedPeer.Pretty())
		return nil
	}
	e.mapVotes[fromConnectedPeer] = metaBlockVote{
		headerHash: string(interceptedHdr.Hash()),
		blockHash:  string(e.hasher.Compute(string(metaBlockBuff))),
		metaBlock:  metaBlock,
	}

	return nil
}

func (e *epochStartBlockProcessor) Notify(data process.InterceptedData, fromConnectedPeer core.PeerID, topic string) error {
	//
	return nil
}

// GetEpochStartBlock will return the metablock after it is confirmed or an error if the number of tries was exceeded
// This is a blocking method which will end after the consensus for the meta block is obtained or the context is done
func (e *epochStartBlockProcessor) GetEpochStartBlock(ctx context.Context) (*block.Block, error) {
	// votes that arrived before the fetch started are kept
	e.beginFetch(false, 0)

	return e.fetch(ctx, e.requestMetaBlock)
}

// GetPrevEpochStartBlock will return the metablock after it is confirmed or an error if the number of tries was exceeded
// This is a blocking method which will end after the consensus for the meta block is obtained or the context is done
func (e *epochStartBlockProcessor) GetPrevEpochStartBlock(ctx context.Context, epoch uint32) (*block.Block, error) {
	e.beginFetch(true, epoch)

	return e.fetch(ctx, func() error {
		return e.requestMetaBlockByEpoch(epoch)
	})
}

// fetch sends the request, repeats it every durationBetweenReRequests and checks the votes every
// durationBetweenChecks until a metablock has the quorum or the context is done
func (e *epochStartBlockProcessor) fetch(ctx context.Context, request func() error) (*block.Block, error) {
	originalIntra, originalCross, err := e.requestHandler.GetNumPeersToQuery(common.BlocksTopic)
	if err != nil {
		return nil, err
	}

	defer func() {
		err = e.requestHandler.SetNumPeersToQuery(common.BlocksTopic, originalIntra, originalCross)
		if err != nil {
			log.Warn("epoch bootstrapper: error setting num of peers intra/cross for resolver",
				"resolver", common.BlocksTopic,
				"error", err)
		}
	}()

	err = request()
	if err != nil {
		return nil, err
	}

	chanRequests := time.After(durationBetweenReRequests)
	chanCheckMaps := time.After(durationBetweenChecks)
	for {
		select {
		case <-ctx.Done():
			// one last check against the same target: there is no weaker fallback, so without
			// consensus the fetch fails and the bootstrap can be retried
			return e.getConfirmedMetaBlock()
		case <-chanRequests:
			err = request()
			if err != nil {
				return nil, err
			}
			chanRequests = time.After(durationBetweenReRequests)
		case <-chanCheckMaps:
			metaBlock, found := e.checkMaps()
			if found {
				return metaBlock, nil
			}
			chanCheckMaps = time.After(durationBetweenChecks)
		}
	}
}

// beginFetch prepares the state for a fetch in a single critical section, so no Save can land
// between the steps:
//   - the previous epoch fetch drops the votes of the latest epoch fetch and makes Save ignore
//     metablocks for any other epoch, so late answers to the latest request cannot vote
//   - a strict majority of the peers connected now floors every target of the fetch, so the
//     peers left after others disconnect cannot decide on their own, while peers that stay can
//     still meet it after attacker connections drop
func (e *epochStartBlockProcessor) beginFetch(prevEpoch bool, epoch uint32) {
	numConnectedPeers := len(uniquePeers(e.messenger.ConnectedPeers()))

	e.mutReceivedMetaBlocks.Lock()
	defer e.mutReceivedMetaBlocks.Unlock()

	if prevEpoch {
		e.mapVotes = make(map[core.PeerID]metaBlockVote)
	}
	e.expectedEpoch = epoch
	e.hasExpectedEpoch = prevEpoch
	e.startPeerCountFloor = numConnectedPeers/2 + 1
}

func (e *epochStartBlockProcessor) getConfirmedMetaBlock() (*block.Block, error) {
	metaBlock, found := e.checkMaps()
	if !found {
		return nil, common.ErrTimeoutWaitingForBlock
	}

	return metaBlock, nil
}

// selectMetaBlock returns the metablock that won the vote of the connected peers. Each connected
// peer holds one vote, for the latest metablock it delivered, and peers that are no longer
// connected hold none. The winning header needs the peer count target, never below a strict
// majority of the peers connected when the fetch started, and an outright lead. The copy returned
// needs the same target among that header's voters, so peers pairing the header with forged
// signatures or transaction data cannot have their copy returned without the quorum. Checking
// the header first keeps a failed quorum apart from copies of an agreed header that split. A tie
// at either step selects nothing, so the choice never depends on map iteration order.
// This func should be called under mutex protection.
func (e *epochStartBlockProcessor) selectMetaBlock(connectedPeers map[core.PeerID]struct{}) (*block.Block, bool) {
	target := max(e.computePeerCountTarget(len(connectedPeers)), e.startPeerCountFloor)

	votesPerHeader := make(map[string]int)
	for pid := range connectedPeers {
		vote, voted := e.mapVotes[pid]
		if voted {
			votesPerHeader[vote.headerHash]++
		}
	}
	headerHash, votes, found := outrightLeader(votesPerHeader)
	if !found || votes < target {
		return nil, false
	}

	votesPerBlock := make(map[string]int)
	metaBlocks := make(map[string]*block.Block)
	for pid := range connectedPeers {
		vote, voted := e.mapVotes[pid]
		if voted && vote.headerHash == headerHash {
			votesPerBlock[vote.blockHash]++
			metaBlocks[vote.blockHash] = vote.metaBlock
		}
	}
	blockHash, blockVotes, found := outrightLeader(votesPerBlock)
	if !found || blockVotes < target {
		log.Debug("no copy of the agreed epoch start header has the quorum",
			"copies", len(votesPerBlock), "votes", blockVotes, "target", target)
		return nil, false
	}

	log.Info("got consensus for epoch start metablock", "len", votes, "target", target)
	return metaBlocks[blockHash], true
}

// outrightLeader returns the key with the most votes, and found only when no other key has as many
func outrightLeader(votesPerKey map[string]int) (string, int, bool) {
	leader := ""
	votes := 0
	runnerUpVotes := 0
	for key, keyVotes := range votesPerKey {
		switch {
		case keyVotes > votes:
			runnerUpVotes = votes
			votes = keyVotes
			leader = key
		case keyVotes > runnerUpVotes:
			runnerUpVotes = keyVotes
		}
	}

	return leader, votes, votes > runnerUpVotes
}

// dropVotesOfDisconnectedPeers keeps the stored votes bounded by the connected peers; the
// dropped votes could not count anyway.
// This func should be called under mutex protection.
func (e *epochStartBlockProcessor) dropVotesOfDisconnectedPeers(connectedPeers map[core.PeerID]struct{}) {
	maps.DeleteFunc(e.mapVotes, func(pid core.PeerID, _ metaBlockVote) bool {
		_, connected := connectedPeers[pid]
		return !connected
	})
}

func uniquePeers(peers []core.PeerID) map[core.PeerID]struct{} {
	unique := make(map[core.PeerID]struct{}, len(peers))
	for _, pid := range peers {
		unique[pid] = struct{}{}
	}

	return unique
}

func (e *epochStartBlockProcessor) requestMetaBlock() error {
	numConnectedPeers := len(e.messenger.ConnectedPeers())
	err := e.requestHandler.SetNumPeersToQuery(common.BlocksTopic, numConnectedPeers, numConnectedPeers)
	if err != nil {
		return err
	}

	unknownEpoch := uint32(math.MaxUint32)
	e.requestHandler.RequestStartOfEpochBlock(unknownEpoch)
	return nil
}

func (e *epochStartBlockProcessor) requestMetaBlockByEpoch(epoch uint32) error {
	numConnectedPeers := len(e.messenger.ConnectedPeers())
	err := e.requestHandler.SetNumPeersToQuery(common.BlocksTopic, numConnectedPeers, numConnectedPeers)
	if err != nil {
		return err
	}

	e.requestHandler.RequestStartOfEpochBlock(epoch)
	return nil
}

// checkMaps returns the metablock the connected peers agree on, if any. It runs on the fetch
// loop, which returns the selected block straight away.
func (e *epochStartBlockProcessor) checkMaps() (*block.Block, bool) {
	connectedPeers := uniquePeers(e.messenger.ConnectedPeers())

	e.mutReceivedMetaBlocks.Lock()
	defer e.mutReceivedMetaBlocks.Unlock()

	e.dropVotesOfDisconnectedPeers(connectedPeers)

	return e.selectMetaBlock(connectedPeers)
}

// RegisterHandler registers a callback function to be notified of incoming epoch start metablocks
func (e *epochStartBlockProcessor) RegisterHandler(_ func(topic string, hash []byte, data interface{})) {
	panic("not implemented")
}

// IsInterfaceNil returns true if there is no value under the interface
func (e *epochStartBlockProcessor) IsInterfaceNil() bool {
	return e == nil
}

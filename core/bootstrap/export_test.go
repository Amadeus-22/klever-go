package bootstrap

import (
	"github.com/klever-io/klever-go/data/block"
)

func (e *epochStartMetaSyncer) SetEpochStartBlockInterceptorProcessor(proc EpochStartBlockInterceptorProcessor) {
	e.blockProcessor = proc
}

func (e *epochStartBlockProcessor) GetMapMetaBlock() map[string]*block.Block {
	e.mutReceivedMetaBlocks.RLock()
	defer e.mutReceivedMetaBlocks.RUnlock()

	metaBlocks := make(map[string]*block.Block)
	for _, vote := range e.mapVotes {
		metaBlocks[vote.headerHash] = vote.metaBlock
	}

	return metaBlocks
}

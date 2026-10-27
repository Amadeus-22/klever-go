package node

import "github.com/klever-io/klever-go/kapps"

// GetNetworkDegradedThreshold -
func (n *Node) GetNetworkDegradedThreshold() uint32 {
	return n.networkDegradedThreshold
}

// GetNetworkDegradedCooldownSlots -
func (n *Node) GetNetworkDegradedCooldownSlots() uint32 {
	return n.networkDegradedCooldownSlots
}

// PublishProposalController -
func (n *Node) PublishProposalController(controller kapps.ActiveProposalController) error {
	return n.publishProposalController(controller)
}

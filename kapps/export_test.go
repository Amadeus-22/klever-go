package kapps

// ProposalControllerOf exposes the wrapped proto so tests can plant states the
// locked API cannot produce, such as a nil parameter entry
func ProposalControllerOf(a *activeProposalController) *ProposalController {
	return a.pc
}

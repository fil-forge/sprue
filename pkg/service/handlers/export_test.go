package handlers

// SetContainerTokenBudget lowers the response and agent-message token budget
// for the duration of a test, so the oversized-conclusion paths can be
// exercised with a handful of blobs instead of the couple of thousand the
// real budget needs.
func SetContainerTokenBudget(t interface{ Cleanup(func()) }, tokens int) {
	previous := containerTokenBudget
	containerTokenBudget = tokens
	t.Cleanup(func() { containerTokenBudget = previous })
}

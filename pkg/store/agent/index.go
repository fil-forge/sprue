package agent

import "github.com/fil-forge/ucantone/ucan"

func Index(message ucan.Container) []IndexEntry {
	var entries []IndexEntry
	for _, inv := range message.Invocations() {
		entry := IndexEntry{
			Invocation: &InvocationSource{
				Task:       inv.Task().Link(),
				Invocation: inv,
			},
		}
		entries = append(entries, entry)
	}
	for _, rcpt := range message.Receipts() {
		entry := IndexEntry{
			Receipt: &ReceiptSource{
				Task:    rcpt.Ran(),
				Receipt: rcpt,
			},
		}
		entries = append(entries, entry)
	}
	return entries
}

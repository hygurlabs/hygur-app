package store

// Knowledge-item source types. Prefer these constants over bare string literals
// so a typo or a missed variant can't silently drop items from a query.
const (
	SourceTypeMail  = "mail"  // canonical mail item (IMAP connector, edge)
	SourceTypeEmail = "email" // legacy variant still in some KBs (EmailIndexer)
	SourceTypeNote  = "note"
	SourceTypeFile  = "file"
	SourceTypeEvent = "event"
	SourceTypeTask  = "task" // note-like to-do (body+tags+project) + task_attrs state
	// SourceTypeDecision is a decision/commitment: a note-like item (statement +
	// rationale + tags + project) plus decision_attrs state (status, decided_on,
	// the source ids that ground it). Either logged by the user or proposed by the
	// nightly scan from the user's own records.
	SourceTypeDecision = "decision"
)

// MailSourceTypes is every source_type that represents a mail/email item.
//
// Mail has historically been stored as BOTH "mail" (IMAP connector, edge) and
// "email" (EmailIndexer), so any query that targets "the user's mail" MUST use
// this set. Querying a single literal is the recurring cause of "0 items" bugs
// (daily brief, Tier-1 reindex): the data was there under the other label.
// Route every mail query through here so the variant can't be forgotten again.
var MailSourceTypes = []string{SourceTypeMail, SourceTypeEmail}

// MailAndSourceTypes returns MailSourceTypes followed by the given extra source
// types, as a fresh slice (never mutates MailSourceTypes). Use it to build the
// source-type list for queries spanning mail plus notes/files/events/etc.
func MailAndSourceTypes(extra ...string) []string {
	out := make([]string, 0, len(MailSourceTypes)+len(extra))
	out = append(out, MailSourceTypes...)
	out = append(out, extra...)
	return out
}

// IsMailSourceType reports whether a source_type denotes a mail/email item.
func IsMailSourceType(sourceType string) bool {
	for _, t := range MailSourceTypes {
		if t == sourceType {
			return true
		}
	}
	return false
}

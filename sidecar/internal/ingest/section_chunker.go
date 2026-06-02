package ingest

import (
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/hygur/sidecar/internal/store"
)

const (
	// DefaultChunkTokenBudget caps the size of an embed/FTS chunk. Kept well
	// under the embedding model's 512-token input limit because our chars/token
	// estimate (4) badly undercounts dense content (URLs, tracking links, code),
	// where the model's real tokenizer emits ~2.7 chars/token. A "512-token"
	// chunk by our estimate (~2048 chars) was really ~740 real tokens for such
	// content → the embedder rejected it (400) and the whole item was rolled
	// back. 300 (≈1200 chars) stays under 512 real tokens even at ~2.7
	// chars/token, so chunks embed without truncation.
	DefaultChunkTokenBudget = 300

	// flatSectionTokenBudget bounds a section cut from an unstructured document
	// (no headings). Keeps fallback blocks coherent and bounded instead of
	// turning a whole flat PDF into one giant block.
	flatSectionTokenBudget = 1500
)

// atxHeadingRe matches a Markdown ATX heading line (# … to ###### …). Requires
// whitespace after the hashes so "#tag" is treated as content, and tolerates a
// trailing closing run of '#'. Capture 1 = hashes (depth), capture 2 = title.
var atxHeadingRe = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*#*\s*$`)

// SectionChunk bundles a logical section (the small-to-big block) with the
// precise, embed-sized chunks cut from it. Every chunk's SectionID points back
// at Section.SectionID.
type SectionChunk struct {
	Section store.Section
	Chunks  []store.Chunk
}

// docBlock is an intermediate parse result: a heading (level>0) or a
// preamble/flat block (level 0), with the full block text including any
// heading line.
type docBlock struct {
	heading string
	level   int
	text    string
}

// headingFrame tracks an open heading on the parent-resolution stack.
type headingFrame struct {
	level       int
	sectionID   string
	headingPath []string
}

// BuildSections parses a Markdown-ish document into a hierarchy of sections and
// the embed-sized chunks within each:
//
//   - Structural (preferred): split on ATX headings (#, ##, …). Each heading and
//     its body (down to the next heading) is one section; parent_section_id and
//     heading_path capture the hierarchy.
//   - Fallback: a document with no headings is segmented into bounded blocks by
//     paragraph, each a flat section. (Embedding-distance "semantic" chunking is
//     a later refinement; this keeps blocks coherent without extra LLM calls.)
//
// Sections carry FullText (the complete block handed to the LLM). Chunks are
// ≤ chunkTokenBudget-token slices of that block, embedded + FTS-indexed for
// recall. contentID scopes the output; CreatedAt/EmbeddingModel are left to the
// caller. Returns nil for empty input.
func BuildSections(contentID, text string, chunkTokenBudget int) []SectionChunk {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if chunkTokenBudget <= 0 {
		chunkTokenBudget = DefaultChunkTokenBudget
	}

	blocks := splitByHeadings(text)
	if len(blocks) == 0 {
		blocks = splitFlat(text) // no headings → bounded fallback blocks
	}

	var (
		out     []SectionChunk
		ordinal int
		stack   []headingFrame // open heading sections, for parent resolution
	)
	for _, b := range blocks {
		// Resolve parent: pop frames at the same or deeper level than this
		// heading. Level-0 (preamble/flat) blocks never participate.
		if b.level > 0 {
			for len(stack) > 0 && stack[len(stack)-1].level >= b.level {
				stack = stack[:len(stack)-1]
			}
		}
		var parentID *string
		var ancestorPath []string
		if b.level > 0 && len(stack) > 0 {
			top := stack[len(stack)-1]
			pid := top.sectionID
			parentID = &pid
			ancestorPath = top.headingPath
		}

		sectionID := uuid.New().String()
		headingPath := append([]string{}, ancestorPath...)
		if b.heading != "" {
			headingPath = append(headingPath, b.heading)
		}

		sec := store.Section{
			SectionID:       sectionID,
			ContentID:       contentID,
			ParentSectionID: parentID,
			Heading:         b.heading,
			HeadingPath:     headingPath,
			Level:           b.level,
			Ordinal:         ordinal,
			FullText:        b.text,
			TokenCount:      estimateTokens(b.text),
		}
		ordinal++

		out = append(out, SectionChunk{
			Section: sec,
			Chunks:  chunksForSection(contentID, sectionID, b.text, chunkTokenBudget, headingPath),
		})

		if b.level > 0 {
			stack = append(stack, headingFrame{level: b.level, sectionID: sectionID, headingPath: headingPath})
		}
	}
	return out
}

// splitByHeadings cuts text on ATX headings. The text before the first heading
// becomes a level-0 preamble block. Returns nil when the document has no
// heading at all (the caller then falls back to splitFlat).
func splitByHeadings(text string) []docBlock {
	var blocks []docBlock
	var sb strings.Builder
	curHeading, curLevel := "", 0
	hasHeading := false

	flush := func() {
		if strings.TrimSpace(sb.String()) != "" {
			blocks = append(blocks, docBlock{
				heading: curHeading,
				level:   curLevel,
				text:    strings.TrimRight(sb.String(), "\n"),
			})
		}
		sb.Reset()
	}

	for _, line := range strings.Split(text, "\n") {
		if m := atxHeadingRe.FindStringSubmatch(line); m != nil {
			hasHeading = true
			flush() // close the previous block (preamble or prior section)
			curHeading = strings.TrimSpace(m[2])
			curLevel = len(m[1])
		}
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	flush()

	if !hasHeading {
		return nil
	}
	return blocks
}

// splitFlat segments an unstructured document into bounded blocks by paragraph,
// each becoming a flat (level-0) section. This is the fallback when no headings
// exist, so a long flat document is broken into coherent pieces rather than one
// oversized block.
func splitFlat(text string) []docBlock {
	budgetChars := tokensToChars(flatSectionTokenBudget)
	var blocks []docBlock
	var sb strings.Builder

	flush := func() {
		if strings.TrimSpace(sb.String()) != "" {
			blocks = append(blocks, docBlock{level: 0, text: strings.TrimRight(sb.String(), "\n")})
		}
		sb.Reset()
	}

	for _, p := range strings.Split(text, "\n\n") {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if sb.Len() > 0 && sb.Len()+len(p) > budgetChars {
			flush()
		}
		sb.WriteString(p)
		sb.WriteString("\n\n")
	}
	flush()

	if len(blocks) == 0 {
		blocks = append(blocks, docBlock{level: 0, text: strings.TrimSpace(text)})
	}
	return blocks
}

// chunksForSection cuts a section's full text into ≤budget-token chunks, all
// linked to sectionID. A section that already fits the budget yields a single
// chunk equal to its text. heading_path is stored in chunk metadata so the
// retriever can prepend a breadcrumb later if desired.
func chunksForSection(contentID, sectionID, fullText string, budget int, headingPath []string) []store.Chunk {
	chunker := NewChunker(ChunkerConfig{MaxTokens: budget, Overlap: budget / 8})
	pieces, _ := chunker.ChunkWithMetadata(fullText, headingPath)

	secID := sectionID
	chunks := make([]store.Chunk, 0, len(pieces))
	for _, p := range pieces {
		md := map[string]any{
			"position": p.Metadata.Position,
			"type":     p.Metadata.Type,
		}
		if len(headingPath) > 0 {
			md["heading_path"] = headingPath
		}
		chunks = append(chunks, store.Chunk{
			ChunkID:   uuid.New().String(),
			ContentID: contentID,
			SectionID: &secID,
			ChunkHash: hashContent(p.Text),
			Text:      p.Text,
			Metadata:  md,
		})
	}
	return chunks
}

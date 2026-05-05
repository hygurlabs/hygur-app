// Package tools provides AI-powered tools for processing and analyzing content.
package tools

import (
	"fmt"
	"strings"

	"github.com/hygur/sidecar/internal/mail"
)

// summarySystemPrompt is the system prompt for email thread summarization.
const summarySystemPrompt = `Tu es un assistant qui analyse des conversations email professionnelles.
Tu dois extraire les informations cles de maniere factuelle et concise.

Reponds UNIQUEMENT en JSON valide avec ce format exact :
{
  "decisions": ["liste des decisions prises dans la conversation"],
  "actions": ["liste des actions a entreprendre ou suivre"],
  "open_questions": ["questions non resolues ou points a clarifier"]
}

Regles :
- Sois factuel, ne specule pas
- Chaque item doit etre une phrase courte et claire
- Si aucun item dans une categorie, utilise un tableau vide []
- Maximum 5 items par categorie`

// maxPromptTextLength is the maximum length of thread content in the prompt.
const maxPromptTextLength = 10000

// buildSummaryPrompt constructs the user prompt for summarizing an email thread.
func buildSummaryPrompt(thread *mail.Thread, normalizedText string) string {
	// Limit text to avoid context overflow
	text := normalizedText
	if len(text) > maxPromptTextLength {
		text = text[:maxPromptTextLength] + "..."
	}

	// Format participants
	participants := strings.Join(thread.Participants, ", ")
	if participants == "" {
		participants = "(aucun participant)"
	}

	// Format date range
	dateStart := thread.DateRange[0].Format("2006-01-02")
	dateEnd := thread.DateRange[1].Format("2006-01-02")

	return fmt.Sprintf(`Analyse cette conversation email :

Sujet : %s
Participants : %s
Periode : %s a %s
Nombre de messages : %d

--- CONTENU ---
%s
--- FIN ---

Extrais les decisions, actions et questions ouvertes en JSON.`,
		thread.Subject,
		participants,
		dateStart,
		dateEnd,
		thread.MessageCount,
		text)
}

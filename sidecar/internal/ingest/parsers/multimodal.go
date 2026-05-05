// Package parsers provides parser implementations for various document formats.
package parsers

import (
	"github.com/hygur/sidecar/internal/ingest"
)

// RegisterMultimodalParsers registers the image and audio parsers with the
// given ingestor. visionEndpoint is passed to NewImageParser; llmBaseURL is
// passed to NewAudioParser. Either may be empty — both parsers are fail-soft
// when their respective endpoints are unavailable.
//
// Call this function after registering the standard text parsers in main so
// that the ingestor can handle image and audio file ingestion.
func RegisterMultimodalParsers(ingestor interface {
	RegisterParser(ingest.Parser)
}, visionEndpoint, llmBaseURL string) {
	ingestor.RegisterParser(NewImageParser(visionEndpoint))
	ingestor.RegisterParser(NewAudioParser(llmBaseURL))
}

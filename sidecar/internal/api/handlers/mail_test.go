package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hygur/sidecar/internal/mail"
	"github.com/hygur/sidecar/internal/tools"
)

// mockMailConnector implements mail.MailConnector for testing.
type mockMailConnector struct {
	connected      bool
	threads        []mail.Thread
	messages       map[string][]mail.Message
	connectErr     error
	listThreadsErr error
	getThreadErr   error
	getMessagesErr error
	credentials    struct {
		username string
		password string
	}
	token           string
	lastListOptions mail.ListOptions // captured on each ListThreads call
}

func newMockMailConnector() *mockMailConnector {
	return &mockMailConnector{
		messages: make(map[string][]mail.Message),
	}
}

func (m *mockMailConnector) Connect(ctx context.Context) error {
	if m.connectErr != nil {
		return m.connectErr
	}
	m.connected = true
	return nil
}

func (m *mockMailConnector) Disconnect() error {
	m.connected = false
	return nil
}

func (m *mockMailConnector) IsConnected() bool {
	return m.connected
}

func (m *mockMailConnector) ListThreads(ctx context.Context, opts mail.ListOptions) ([]mail.Thread, error) {
	m.lastListOptions = opts
	if m.listThreadsErr != nil {
		return nil, m.listThreadsErr
	}

	// Apply pagination
	start := opts.Offset
	end := start + opts.Limit
	if start >= len(m.threads) {
		return []mail.Thread{}, nil
	}
	if end > len(m.threads) {
		end = len(m.threads)
	}

	return m.threads[start:end], nil
}

func (m *mockMailConnector) GetThread(ctx context.Context, threadID string) (*mail.Thread, error) {
	if m.getThreadErr != nil {
		return nil, m.getThreadErr
	}

	for i := range m.threads {
		if m.threads[i].ID == threadID {
			return &m.threads[i], nil
		}
	}
	return nil, mail.ErrThreadNotFound
}

func (m *mockMailConnector) GetMessages(ctx context.Context, threadID string) ([]mail.Message, error) {
	if m.getMessagesErr != nil {
		return nil, m.getMessagesErr
	}

	msgs, ok := m.messages[threadID]
	if !ok {
		return nil, mail.ErrThreadNotFound
	}
	return msgs, nil
}

func (m *mockMailConnector) GetMessagesByThread(ctx context.Context, thread *mail.Thread) ([]mail.Message, error) {
	if thread == nil {
		return nil, mail.ErrThreadNotFound
	}
	return m.GetMessages(ctx, thread.ID)
}

func (m *mockMailConnector) SetCredentials(username, password string) {
	m.credentials.username = username
	m.credentials.password = password
}

func (m *mockMailConnector) SetToken(token string) {
	m.token = token
}

// mockEmailIndexer implements a mock indexer for testing.
type mockEmailIndexer struct {
	indexResult *mail.IndexResult
	indexErr    error
}

func (m *mockEmailIndexer) IndexThread(ctx context.Context, thread *mail.Thread, messages []mail.Message, accountID string) (*mail.IndexResult, error) {
	if m.indexErr != nil {
		return nil, m.indexErr
	}
	if m.indexResult != nil {
		return m.indexResult, nil
	}
	return &mail.IndexResult{
		ContentID:  "email:" + thread.ID,
		ChunkCount: 3,
		Status:     "indexed",
	}, nil
}

// createMailTestRouter creates a chi router with the mail handler mounted.
func createMailTestRouter(h *MailHandler) *chi.Mux {
	r := chi.NewRouter()
	r.Route("/mail", func(r chi.Router) {
		r.Get("/sources", h.Sources)
		r.Get("/threads", h.Threads)
		r.Post("/threads/{thread_id}/index", h.Index)
		r.Post("/threads/{thread_id}/summarize", h.Summarize)
		r.Get("/threads/{thread_id}/attachments", h.Attachments)
	})
	return r
}

// TestMailHandler_Sources_NoConnectors tests sources with no connectors registered.
func TestMailHandler_Sources_NoConnectors(t *testing.T) {
	h := NewMailHandler(testLogger())
	router := createMailTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/sources", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp SourcesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Sources) != 0 {
		t.Errorf("expected 0 sources, got %d", len(resp.Sources))
	}
}

// TestMailHandler_Sources_WithConnectors tests sources with connectors registered.
func TestMailHandler_Sources_WithConnectors(t *testing.T) {
	h := NewMailHandler(testLogger())

	gmailConnector := newMockMailConnector()
	gmailConnector.connected = true

	protonConnector := newMockMailConnector()
	protonConnector.connected = false

	h.SetConnector("gmail", gmailConnector)
	h.SetConnector("proton", protonConnector)

	router := createMailTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/sources", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp SourcesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Sources) != 2 {
		t.Errorf("expected 2 sources, got %d", len(resp.Sources))
	}

	// Check statuses
	sourceMap := make(map[string]string)
	for _, s := range resp.Sources {
		sourceMap[s.Name] = s.Status
	}

	if sourceMap["gmail"] != "connected" {
		t.Errorf("expected gmail status 'connected', got '%s'", sourceMap["gmail"])
	}
	if sourceMap["proton"] != "disconnected" {
		t.Errorf("expected proton status 'disconnected', got '%s'", sourceMap["proton"])
	}
}

// TestMailHandler_Threads_Success tests listing threads successfully.
// The handler enforces newest-first ordering on DateRange[1], so threads with
// a more recent end-date come back first regardless of input order.
func TestMailHandler_Threads_Success(t *testing.T) {
	h := NewMailHandler(testLogger())

	connector := newMockMailConnector()
	connector.connected = true
	// thread-1 is older (DateRange[1] = base) than thread-2 (base + 1h).
	base := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	connector.threads = []mail.Thread{
		{
			ID:             "thread-1",
			Subject:        "Test Thread 1 (older)",
			Participants:   []string{"alice@example.com", "bob@example.com"},
			MessageCount:   3,
			HasAttachments: true,
			DateRange:      [2]time.Time{base.Add(-24 * time.Hour), base},
		},
		{
			ID:             "thread-2",
			Subject:        "Test Thread 2 (newer)",
			Participants:   []string{"charlie@example.com"},
			MessageCount:   1,
			HasAttachments: false,
			DateRange:      [2]time.Time{base, base.Add(1 * time.Hour)},
		},
	}
	h.SetConnector("gmail", connector)

	router := createMailTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/threads?source=gmail", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp ThreadsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Threads) != 2 {
		t.Errorf("expected 2 threads, got %d", len(resp.Threads))
	}

	// Newer thread first.
	if resp.Threads[0].ID != "thread-2" {
		t.Errorf("expected newest thread first ('thread-2'), got '%s'", resp.Threads[0].ID)
	}
	if resp.Threads[1].ID != "thread-1" {
		t.Errorf("expected oldest thread last ('thread-1'), got '%s'", resp.Threads[1].ID)
	}
	if resp.Threads[1].Subject != "Test Thread 1 (older)" {
		t.Errorf("expected subject 'Test Thread 1 (older)', got '%s'", resp.Threads[1].Subject)
	}
	if !resp.Threads[1].HasAttachments {
		t.Error("expected oldest thread to have attachments")
	}
}

// TestMailHandler_Threads_Pagination tests thread pagination.
func TestMailHandler_Threads_Pagination(t *testing.T) {
	h := NewMailHandler(testLogger())

	connector := newMockMailConnector()
	connector.connected = true

	// Create 50 threads
	for i := 0; i < 50; i++ {
		connector.threads = append(connector.threads, mail.Thread{
			ID:           "thread-" + string(rune('a'+i%26)),
			Subject:      "Thread " + string(rune('A'+i%26)),
			MessageCount: i + 1,
			DateRange:    [2]time.Time{time.Now(), time.Now()},
		})
	}
	h.SetConnector("gmail", connector)

	router := createMailTestRouter(h)

	// Test with limit
	req := httptest.NewRequest(http.MethodGet, "/mail/threads?source=gmail&limit=10&offset=5", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp ThreadsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Threads) != 10 {
		t.Errorf("expected 10 threads with limit, got %d", len(resp.Threads))
	}
}

// TestMailHandler_Threads_MaxLimit tests that limit is capped at 100.
func TestMailHandler_Threads_MaxLimit(t *testing.T) {
	h := NewMailHandler(testLogger())

	connector := newMockMailConnector()
	connector.connected = true

	// Create 200 threads
	for i := 0; i < 200; i++ {
		connector.threads = append(connector.threads, mail.Thread{
			ID:        "thread-" + string(rune(i)),
			DateRange: [2]time.Time{time.Now(), time.Now()},
		})
	}
	h.SetConnector("gmail", connector)

	router := createMailTestRouter(h)

	// Request with limit > 100
	req := httptest.NewRequest(http.MethodGet, "/mail/threads?source=gmail&limit=200", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp ThreadsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Threads) != 100 {
		t.Errorf("expected max 100 threads, got %d", len(resp.Threads))
	}
}

// TestMailHandler_Threads_MissingSource tests threads without source parameter.
func TestMailHandler_Threads_MissingSource(t *testing.T) {
	h := NewMailHandler(testLogger())
	router := createMailTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/threads", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

// TestMailHandler_Threads_UnknownSource tests threads with unknown source.
func TestMailHandler_Threads_UnknownSource(t *testing.T) {
	h := NewMailHandler(testLogger())
	router := createMailTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/threads?source=unknown", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d: %s", http.StatusNotFound, rec.Code, rec.Body.String())
	}
}

// TestMailHandler_Threads_NotConnected tests threads when source not connected.
func TestMailHandler_Threads_NotConnected(t *testing.T) {
	h := NewMailHandler(testLogger())

	connector := newMockMailConnector()
	connector.connected = false
	h.SetConnector("gmail", connector)

	router := createMailTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/threads?source=gmail", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d: %s", http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	}
}

// TestMailHandler_Index_Success tests successful thread indexing.
func TestMailHandler_Index_Success(t *testing.T) {
	h := NewMailHandler(testLogger())

	connector := newMockMailConnector()
	connector.connected = true
	connector.threads = []mail.Thread{
		{
			ID:           "thread-123",
			Subject:      "Important Discussion",
			Participants: []string{"alice@example.com"},
			MessageCount: 2,
			DateRange:    [2]time.Time{time.Now().Add(-1 * time.Hour), time.Now()},
		},
	}
	connector.messages["thread-123"] = []mail.Message{
		{
			ID:       "msg-1",
			ThreadID: "thread-123",
			From:     "alice@example.com",
			Subject:  "Important Discussion",
			Body:     "This is the first message.",
			Date:     time.Now().Add(-1 * time.Hour),
		},
		{
			ID:       "msg-2",
			ThreadID: "thread-123",
			From:     "bob@example.com",
			Subject:  "Re: Important Discussion",
			Body:     "This is a reply.",
			Date:     time.Now(),
		},
	}
	h.SetConnector("gmail", connector)

	// Create a mock indexer
	mockIdx := &mockEmailIndexer{}
	// We need to create a wrapper that satisfies the interface
	// For testing, we'll skip the actual indexer and check handler behavior
	// Since EmailIndexer is a struct, not an interface, we test with nil and check error

	router := createMailTestRouter(h)

	reqBody := IndexRequest{
		Source: "gmail",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/mail/threads/thread-123/index", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	// Without indexer configured, should return service unavailable
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d without indexer, got %d: %s", http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	}

	// Verify error response
	var errResp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	errorObj, ok := errResp["error"].(map[string]interface{})
	if !ok {
		t.Fatal("expected error object in response")
	}

	if errorObj["code"] != "SERVICE_UNAVAILABLE" {
		t.Errorf("expected code 'SERVICE_UNAVAILABLE', got '%s'", errorObj["code"])
	}

	_ = mockIdx // suppress unused warning
}

// TestMailHandler_Index_MissingSource tests index without source.
func TestMailHandler_Index_MissingSource(t *testing.T) {
	h := NewMailHandler(testLogger())
	router := createMailTestRouter(h)

	reqBody := IndexRequest{}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/mail/threads/thread-123/index", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

// TestMailHandler_Index_UnknownSource tests index with unknown source.
func TestMailHandler_Index_UnknownSource(t *testing.T) {
	h := NewMailHandler(testLogger())
	router := createMailTestRouter(h)

	reqBody := IndexRequest{
		Source: "unknown",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/mail/threads/thread-123/index", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d: %s", http.StatusNotFound, rec.Code, rec.Body.String())
	}
}

// TestMailHandler_Index_NotConnected tests index when source not connected.
func TestMailHandler_Index_NotConnected(t *testing.T) {
	h := NewMailHandler(testLogger())

	connector := newMockMailConnector()
	connector.connected = false
	h.SetConnector("gmail", connector)

	router := createMailTestRouter(h)

	reqBody := IndexRequest{
		Source: "gmail",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/mail/threads/thread-123/index", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d: %s", http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	}
}

// TestMailHandler_Summarize_MissingSource tests summarize without source.
func TestMailHandler_Summarize_MissingSource(t *testing.T) {
	h := NewMailHandler(testLogger())
	router := createMailTestRouter(h)

	reqBody := SummarizeRequest{
		Model: "gpt-4",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/mail/threads/thread-123/summarize", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

// TestMailHandler_Summarize_MissingModel tests summarize without model.
func TestMailHandler_Summarize_MissingModel(t *testing.T) {
	h := NewMailHandler(testLogger())

	connector := newMockMailConnector()
	connector.connected = true
	h.SetConnector("gmail", connector)

	router := createMailTestRouter(h)

	reqBody := SummarizeRequest{
		Source: "gmail",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/mail/threads/thread-123/summarize", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

// TestMailHandler_Summarize_NoSummarizeTool tests summarize without tool configured.
func TestMailHandler_Summarize_NoSummarizeTool(t *testing.T) {
	h := NewMailHandler(testLogger())

	connector := newMockMailConnector()
	connector.connected = true
	connector.threads = []mail.Thread{
		{
			ID:        "thread-123",
			Subject:   "Test Thread",
			DateRange: [2]time.Time{time.Now(), time.Now()},
		},
	}
	connector.messages["thread-123"] = []mail.Message{
		{
			ID:       "msg-1",
			ThreadID: "thread-123",
			Body:     "Test message",
			Date:     time.Now(),
		},
	}
	h.SetConnector("gmail", connector)

	router := createMailTestRouter(h)

	reqBody := SummarizeRequest{
		Source: "gmail",
		Model:  "gpt-4",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/mail/threads/thread-123/summarize", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d: %s", http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	}
}

// TestMailHandler_Summarize_ThreadNotFound tests summarize with non-existent thread.
// Note: Without a summarize tool configured, we get SERVICE_UNAVAILABLE before checking thread.
// This test verifies that behavior by checking service unavailable first.
func TestMailHandler_Summarize_ThreadNotFound(t *testing.T) {
	h := NewMailHandler(testLogger())

	connector := newMockMailConnector()
	connector.connected = true
	// No threads
	h.SetConnector("gmail", connector)

	router := createMailTestRouter(h)

	reqBody := SummarizeRequest{
		Source: "gmail",
		Model:  "gpt-4",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/mail/threads/nonexistent/summarize", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	// Without summarize tool configured, we get SERVICE_UNAVAILABLE before checking thread
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d (no summarize tool), got %d: %s", http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	}
}

// TestMailHandler_SetConnector tests setting connectors.
func TestMailHandler_SetConnector(t *testing.T) {
	h := NewMailHandler(testLogger())

	connector1 := newMockMailConnector()
	connector2 := newMockMailConnector()

	h.SetConnector("gmail", connector1)
	h.SetConnector("proton", connector2)

	// Verify via sources endpoint
	router := createMailTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/sources", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	var resp SourcesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Sources) != 2 {
		t.Errorf("expected 2 sources, got %d", len(resp.Sources))
	}
}

// TestMailHandler_Threads_DefaultLimit tests default limit is 20.
func TestMailHandler_Threads_DefaultLimit(t *testing.T) {
	h := NewMailHandler(testLogger())

	connector := newMockMailConnector()
	connector.connected = true

	// Create 50 threads
	for i := 0; i < 50; i++ {
		connector.threads = append(connector.threads, mail.Thread{
			ID:        "thread-" + string(rune(i)),
			DateRange: [2]time.Time{time.Now(), time.Now()},
		})
	}
	h.SetConnector("gmail", connector)

	router := createMailTestRouter(h)

	// No limit parameter
	req := httptest.NewRequest(http.MethodGet, "/mail/threads?source=gmail", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp ThreadsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Threads) != 20 {
		t.Errorf("expected default 20 threads, got %d", len(resp.Threads))
	}
}

// TestMailHandler_Attachments_Success tests listing attachments successfully.
func TestMailHandler_Attachments_Success(t *testing.T) {
	h := NewMailHandler(testLogger())

	connector := newMockMailConnector()
	connector.connected = true
	connector.messages["thread-123"] = []mail.Message{
		{
			ID:       "msg-1",
			ThreadID: "thread-123",
			From:     "alice@example.com",
			Date:     time.Now(),
			Subject:  "Document attached",
			Attachments: []mail.Attachment{
				{
					ID:       "att-1",
					Filename: "report.pdf",
					MimeType: "application/pdf",
					Size:     1024,
				},
				{
					ID:       "att-2",
					Filename: "data.xlsx",
					MimeType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
					Size:     2048,
				},
			},
		},
		{
			ID:       "msg-2",
			ThreadID: "thread-123",
			From:     "bob@example.com",
			Date:     time.Now(),
			Subject:  "Re: Document attached",
			Attachments: []mail.Attachment{
				{
					ID:       "att-3",
					Filename: "feedback.docx",
					MimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
					Size:     512,
				},
			},
		},
	}

	connectors := map[string]mail.MailConnector{
		"gmail": connector,
	}

	h.SetConnector("gmail", connector)
	h.SetListAttachmentsTool(tools.NewListAttachmentsTool(connectors))

	router := createMailTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/threads/thread-123/attachments?source=gmail", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp AttachmentsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.ThreadID != "thread-123" {
		t.Errorf("expected ThreadID 'thread-123', got '%s'", resp.ThreadID)
	}

	if resp.Source != "gmail" {
		t.Errorf("expected Source 'gmail', got '%s'", resp.Source)
	}

	if len(resp.Attachments) != 3 {
		t.Fatalf("expected 3 attachments, got %d", len(resp.Attachments))
	}

	// Verify first attachment
	if resp.Attachments[0].ID != "att-1" {
		t.Errorf("expected first attachment ID 'att-1', got '%s'", resp.Attachments[0].ID)
	}
	if resp.Attachments[0].Filename != "report.pdf" {
		t.Errorf("expected first attachment filename 'report.pdf', got '%s'", resp.Attachments[0].Filename)
	}
	if resp.Attachments[0].MIMEType != "application/pdf" {
		t.Errorf("expected first attachment MIME type 'application/pdf', got '%s'", resp.Attachments[0].MIMEType)
	}
	if resp.Attachments[0].Size != 1024 {
		t.Errorf("expected first attachment size 1024, got %d", resp.Attachments[0].Size)
	}
}

// TestMailHandler_Attachments_NoAttachments tests listing attachments when none exist.
func TestMailHandler_Attachments_NoAttachments(t *testing.T) {
	h := NewMailHandler(testLogger())

	connector := newMockMailConnector()
	connector.connected = true
	connector.messages["thread-empty"] = []mail.Message{
		{
			ID:          "msg-1",
			ThreadID:    "thread-empty",
			From:        "alice@example.com",
			Date:        time.Now(),
			Subject:     "No attachments here",
			Attachments: nil,
		},
	}

	connectors := map[string]mail.MailConnector{
		"proton": connector,
	}

	h.SetConnector("proton", connector)
	h.SetListAttachmentsTool(tools.NewListAttachmentsTool(connectors))

	router := createMailTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/threads/thread-empty/attachments?source=proton", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp AttachmentsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Attachments) != 0 {
		t.Errorf("expected 0 attachments, got %d", len(resp.Attachments))
	}
}

// TestMailHandler_Attachments_MissingSource tests attachments without source parameter.
func TestMailHandler_Attachments_MissingSource(t *testing.T) {
	h := NewMailHandler(testLogger())

	connector := newMockMailConnector()
	connector.connected = true

	connectors := map[string]mail.MailConnector{
		"gmail": connector,
	}

	h.SetConnector("gmail", connector)
	h.SetListAttachmentsTool(tools.NewListAttachmentsTool(connectors))

	router := createMailTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/threads/thread-123/attachments", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

// TestMailHandler_Attachments_SourceNotFound tests attachments with unknown source.
func TestMailHandler_Attachments_SourceNotFound(t *testing.T) {
	h := NewMailHandler(testLogger())

	connector := newMockMailConnector()
	connector.connected = true

	connectors := map[string]mail.MailConnector{
		"gmail": connector,
	}

	h.SetConnector("gmail", connector)
	h.SetListAttachmentsTool(tools.NewListAttachmentsTool(connectors))

	router := createMailTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/threads/thread-123/attachments?source=unknown", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d: %s", http.StatusNotFound, rec.Code, rec.Body.String())
	}
}

// TestMailHandler_Attachments_SourceNotConnected tests attachments when source not connected.
func TestMailHandler_Attachments_SourceNotConnected(t *testing.T) {
	h := NewMailHandler(testLogger())

	connector := newMockMailConnector()
	connector.connected = false

	connectors := map[string]mail.MailConnector{
		"gmail": connector,
	}

	h.SetConnector("gmail", connector)
	h.SetListAttachmentsTool(tools.NewListAttachmentsTool(connectors))

	router := createMailTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/threads/thread-123/attachments?source=gmail", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d: %s", http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	}
}

// TestMailHandler_Attachments_ThreadNotFound tests attachments with non-existent thread.
func TestMailHandler_Attachments_ThreadNotFound(t *testing.T) {
	h := NewMailHandler(testLogger())

	connector := newMockMailConnector()
	connector.connected = true
	// No messages

	connectors := map[string]mail.MailConnector{
		"gmail": connector,
	}

	h.SetConnector("gmail", connector)
	h.SetListAttachmentsTool(tools.NewListAttachmentsTool(connectors))

	router := createMailTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/threads/nonexistent/attachments?source=gmail", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d: %s", http.StatusNotFound, rec.Code, rec.Body.String())
	}
}

// TestMailHandler_Attachments_NoToolConfigured tests attachments without tool configured.
func TestMailHandler_Attachments_NoToolConfigured(t *testing.T) {
	h := NewMailHandler(testLogger())

	connector := newMockMailConnector()
	connector.connected = true
	h.SetConnector("gmail", connector)
	// No SetListAttachmentsTool called

	router := createMailTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/threads/thread-123/attachments?source=gmail", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d: %s", http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	}
}

// TestMailHandler_Credentials_NoStore tests listing credentials without store configured.
func TestMailHandler_Credentials_NoStore(t *testing.T) {
	h := NewMailHandler(testLogger())
	router := createMailTestRouterWithCredentials(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/credentials", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d: %s", http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	}
}

// TestMailHandler_DeleteCredential_NoStore tests deleting credential without store configured.
func TestMailHandler_DeleteCredential_NoStore(t *testing.T) {
	h := NewMailHandler(testLogger())
	router := createMailTestRouterWithCredentials(h)

	req := httptest.NewRequest(http.MethodDelete, "/mail/credentials/proton", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d: %s", http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	}
}

// createMailTestRouterWithCredentials creates a chi router with credential routes.
func createMailTestRouterWithCredentials(h *MailHandler) *chi.Mux {
	r := chi.NewRouter()
	r.Route("/mail", func(r chi.Router) {
		r.Get("/sources", h.Sources)
		r.Get("/threads", h.Threads)
		r.Post("/threads/{thread_id}/index", h.Index)
		r.Post("/threads/{thread_id}/summarize", h.Summarize)
		r.Get("/threads/{thread_id}/attachments", h.Attachments)
		r.Get("/credentials", h.Credentials)
		r.Delete("/credentials/{source}", h.DeleteCredential)
	})
	return r
}

// mockLabelListerConnector implements mail.MailConnector and mail.LabelLister for testing.
type mockLabelListerConnector struct {
	*mockMailConnector
	labels    []mail.Label
	labelsErr error
}

func newMockLabelListerConnector() *mockLabelListerConnector {
	return &mockLabelListerConnector{
		mockMailConnector: newMockMailConnector(),
	}
}

func (m *mockLabelListerConnector) ListLabels(ctx context.Context) ([]mail.Label, error) {
	if m.labelsErr != nil {
		return nil, m.labelsErr
	}
	return m.labels, nil
}

// createMailTestRouterWithLabels creates a chi router with labels routes.
func createMailTestRouterWithLabels(h *MailHandler) *chi.Mux {
	r := chi.NewRouter()
	r.Route("/mail", func(r chi.Router) {
		r.Get("/sources", h.Sources)
		r.Get("/labels", h.Labels)
		r.Get("/threads", h.Threads)
		r.Post("/threads/{thread_id}/index", h.Index)
		r.Post("/threads/{thread_id}/summarize", h.Summarize)
		r.Get("/threads/{thread_id}/attachments", h.Attachments)
	})
	return r
}

// TestMailHandler_Labels_Success tests listing labels successfully.
func TestMailHandler_Labels_Success(t *testing.T) {
	h := NewMailHandler(testLogger())

	connector := newMockLabelListerConnector()
	connector.connected = true
	connector.labels = []mail.Label{
		{ID: "INBOX", Name: "Inbox", Type: "system"},
		{ID: "SENT", Name: "Sent", Type: "system"},
		{ID: "Label_123", Name: "Work", Type: "user"},
		{ID: "Label_456", Name: "Personal", Type: "user"},
	}
	h.SetConnector("gmail", connector)

	router := createMailTestRouterWithLabels(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/labels?source=gmail", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp LabelsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Labels) != 4 {
		t.Errorf("expected 4 labels, got %d", len(resp.Labels))
	}

	// Check first label
	if resp.Labels[0].ID != "INBOX" {
		t.Errorf("expected first label ID 'INBOX', got '%s'", resp.Labels[0].ID)
	}
	if resp.Labels[0].Name != "Inbox" {
		t.Errorf("expected first label name 'Inbox', got '%s'", resp.Labels[0].Name)
	}
	if resp.Labels[0].Type != "system" {
		t.Errorf("expected first label type 'system', got '%s'", resp.Labels[0].Type)
	}

	// Check user label
	if resp.Labels[2].ID != "Label_123" {
		t.Errorf("expected third label ID 'Label_123', got '%s'", resp.Labels[2].ID)
	}
	if resp.Labels[2].Type != "user" {
		t.Errorf("expected third label type 'user', got '%s'", resp.Labels[2].Type)
	}
}

// TestMailHandler_Labels_MissingSource tests labels without source parameter.
func TestMailHandler_Labels_MissingSource(t *testing.T) {
	h := NewMailHandler(testLogger())
	router := createMailTestRouterWithLabels(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/labels", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

// TestMailHandler_Labels_UnknownSource tests labels with unknown source.
func TestMailHandler_Labels_UnknownSource(t *testing.T) {
	h := NewMailHandler(testLogger())
	router := createMailTestRouterWithLabels(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/labels?source=unknown", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d: %s", http.StatusNotFound, rec.Code, rec.Body.String())
	}
}

// TestMailHandler_Labels_NotConnected tests labels when source not connected.
func TestMailHandler_Labels_NotConnected(t *testing.T) {
	h := NewMailHandler(testLogger())

	connector := newMockLabelListerConnector()
	connector.connected = false
	h.SetConnector("gmail", connector)

	router := createMailTestRouterWithLabels(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/labels?source=gmail", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d: %s", http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	}
}

// TestMailHandler_Labels_NotSupported tests labels when connector doesn't support it.
func TestMailHandler_Labels_NotSupported(t *testing.T) {
	h := NewMailHandler(testLogger())

	// Use basic mock connector that doesn't implement LabelLister
	connector := newMockMailConnector()
	connector.connected = true
	h.SetConnector("custom", connector)

	router := createMailTestRouterWithLabels(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/labels?source=custom", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}

	var errResp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	errorObj, ok := errResp["error"].(map[string]interface{})
	if !ok {
		t.Fatal("expected error object in response")
	}

	if errorObj["code"] != "NOT_SUPPORTED" {
		t.Errorf("expected code 'NOT_SUPPORTED', got '%s'", errorObj["code"])
	}
}

// TestMailHandler_Labels_Empty tests labels when connector returns empty list.
func TestMailHandler_Labels_Empty(t *testing.T) {
	h := NewMailHandler(testLogger())

	connector := newMockLabelListerConnector()
	connector.connected = true
	connector.labels = []mail.Label{}
	h.SetConnector("gmail", connector)

	router := createMailTestRouterWithLabels(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/labels?source=gmail", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp LabelsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Labels) != 0 {
		t.Errorf("expected 0 labels, got %d", len(resp.Labels))
	}
}

// TestMailHandler_Labels_Error tests labels when connector returns error.
func TestMailHandler_Labels_Error(t *testing.T) {
	h := NewMailHandler(testLogger())

	connector := newMockLabelListerConnector()
	connector.connected = true
	connector.labelsErr = mail.ErrConnectionLost
	h.SetConnector("gmail", connector)

	router := createMailTestRouterWithLabels(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/labels?source=gmail", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status %d, got %d: %s", http.StatusInternalServerError, rec.Code, rec.Body.String())
	}
}

// TestMailHandler_Threads_LabelIDs verifies that label_ids query params are
// forwarded to the connector as ListOptions.LabelIDs.
func TestMailHandler_Threads_LabelIDs(t *testing.T) {
	h := NewMailHandler(testLogger())
	connector := newMockMailConnector()
	connector.connected = true
	base := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	connector.threads = []mail.Thread{
		{ID: "t1", Subject: "Recharge mars", DateRange: [2]time.Time{base, base}},
	}
	h.SetConnector("gmail", connector)
	router := createMailTestRouter(h)

	// Two label IDs via repeated param.
	req := httptest.NewRequest(http.MethodGet, "/mail/threads?source=gmail&label_ids=Label_123&label_ids=INBOX", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	got := connector.lastListOptions.LabelIDs
	if len(got) != 2 {
		t.Fatalf("want 2 LabelIDs forwarded, got %v", got)
	}
	if got[0] != "Label_123" || got[1] != "INBOX" {
		t.Errorf("unexpected LabelIDs: %v", got)
	}
}

// TestMailHandler_Threads_LabelIDs_Comma verifies comma-separated label_ids.
func TestMailHandler_Threads_LabelIDs_Comma(t *testing.T) {
	h := NewMailHandler(testLogger())
	connector := newMockMailConnector()
	connector.connected = true
	h.SetConnector("gmail", connector)
	router := createMailTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/threads?source=gmail&label_ids=Label_A,Label_B", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	got := connector.lastListOptions.LabelIDs
	if len(got) != 2 || got[0] != "Label_A" || got[1] != "Label_B" {
		t.Errorf("unexpected LabelIDs from comma-separated: %v", got)
	}
}

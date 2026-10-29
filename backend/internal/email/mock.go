package email

import "context"

// MockSender records email sends for test assertions.
type MockSender struct {
	Calls []MockCall
	Err   error // if set, all sends return this error
}

// MockCall records the arguments of a single Send or SendWithAttachment call.
type MockCall struct {
	To       string
	Subject  string
	HTML     string
	Filename string
	// Headers are the extra headers of a SendWithHeaders call.
	Headers map[string]string
}

func (m *MockSender) Send(_ context.Context, to, subject, html string) error {
	m.Calls = append(m.Calls, MockCall{To: to, Subject: subject, HTML: html})
	return m.Err
}

func (m *MockSender) SendWithAttachment(_ context.Context, to, subject, html, filename string, _ []byte) error {
	m.Calls = append(m.Calls, MockCall{To: to, Subject: subject, HTML: html, Filename: filename})
	return m.Err
}

func (m *MockSender) SendWithHeaders(_ context.Context, to, subject, html string, headers map[string]string) error {
	m.Calls = append(m.Calls, MockCall{To: to, Subject: subject, HTML: html, Headers: headers})
	return m.Err
}

// Compile-time check.
var _ Sender = (*MockSender)(nil)

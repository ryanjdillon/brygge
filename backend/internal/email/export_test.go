package email

// BuildMessage exposes the SMTP message builder to the external test package,
// which renders a real faktura PDF with finance and so cannot live in package
// email without importing it there.
func (c *SMTPClient) BuildMessage(to, subject, htmlBody, filename string, attachment []byte) ([]byte, error) {
	return c.buildMessage(to, subject, htmlBody, filename, attachment, nil)
}

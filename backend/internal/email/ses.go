package email

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// SES SMTP interface: https://docs.aws.amazon.com/ses/latest/dg/send-email-smtp.html
// Port 465 is implicit TLS, which SMTPClient selects by port.
const SESSMTPPort = 465

// SESSMTPHost is the regional SES SMTP endpoint
// (https://docs.aws.amazon.com/general/latest/gr/ses.html).
func SESSMTPHost(region string) string {
	return "email-smtp." + region + ".amazonaws.com"
}

// SESSMTPPassword derives the SES SMTP password from an IAM secret access
// key, so the app needs no secret beyond the key pair its event worker
// already uses. The algorithm is AWS's documented conversion
// (https://docs.aws.amazon.com/ses/latest/dg/smtp-credentials.html#smtp-credentials-convert):
// a SigV4 signing-key chain over the fixed date "11111111", the region,
// "ses" and "aws4_request", an HMAC of "SendRawEmail" with that key, the
// version byte 0x04 prepended, base64. The password is region-specific.
func SESSMTPPassword(secretAccessKey, region string) string {
	sign := func(key []byte, msg string) []byte {
		h := hmac.New(sha256.New, key)
		h.Write([]byte(msg))
		return h.Sum(nil)
	}
	k := sign([]byte("AWS4"+secretAccessKey), "11111111")
	for _, part := range []string{region, "ses", "aws4_request", "SendRawEmail"} {
		k = sign(k, part)
	}
	return base64.StdEncoding.EncodeToString(append([]byte{0x04}, k...))
}

// SES event-publishing headers
// (https://docs.aws.amazon.com/ses/latest/dg/event-publishing-send-email.html).
const (
	headerSESConfigurationSet = "X-SES-CONFIGURATION-SET"
	headerSESMessageTags      = "X-SES-MESSAGE-TAGS"
)

// sesTagValue keeps only the characters SES allows in a message tag value
// (ASCII letters, digits, '_', '-', '.', '@'). SES rejects a send whose tag
// breaks the rule, so an unexpected character becomes '_' rather than
// failing the mail.
func sesTagValue(v string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '_', r == '-', r == '.', r == '@':
			return r
		}
		return '_'
	}, v)
}

// sesMessageTags renders the X-SES-MESSAGE-TAGS value: send_id links SES
// delivery events to the email_sends row, kind lets them be grouped without
// a database lookup. send_id is omitted when the send was not recorded.
func sesMessageTags(sendID string, kind Kind) string {
	tags := make([]string, 0, 2)
	if sendID != "" {
		tags = append(tags, "send_id="+sesTagValue(sendID))
	}
	tags = append(tags, "kind="+sesTagValue(string(kind)))
	return strings.Join(tags, ", ")
}

// sesMessageID returns the SES message id from the reply to the end of
// DATA, which SES sends as "Ok <message-id>". Anything else yields "".
func sesMessageID(reply string) string {
	f := strings.Fields(reply)
	if len(f) == 2 && strings.EqualFold(f[0], "ok") {
		return f[1]
	}
	return ""
}

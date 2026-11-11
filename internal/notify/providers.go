package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Email is a rendered message for a provider.
type Email struct {
	FromName, FromEmail, ReplyTo string
	ToName, ToEmail              string
	Subject, HTML, Text          string
	MessageID                    string // stable per message: providers can dedupe retries
}

// Provider delivers one email. Implementations must be safe to retry.
type Provider interface {
	Name() string
	Send(ctx context.Context, e Email) (ref string, err error)
}

// Outbox is the development provider: nothing leaves the server; the message
// stays in the log where the admin can read it.
type Outbox struct{}

// Name implements Provider.
func (Outbox) Name() string { return "outbox" }

// Send implements Provider.
func (Outbox) Send(context.Context, Email) (string, error) { return "outbox", nil }

// SMTPConfig configures the SMTP adapter.
type SMTPConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
}

// SMTP sends through any SMTP relay (STARTTLS on 587/25, implicit TLS on 465).
type SMTP struct {
	Config       SMTPConfig
	AllowPrivate bool // tests and local relays; production refuses private addresses
	Timeout      time.Duration
}

// Name implements Provider.
func (*SMTP) Name() string { return "smtp" }

// ErrUnsafeHost means the provider host resolves to a non-public address.
var ErrUnsafeHost = errors.New("notify: el servidor de correo debe ser una dirección pública")

func publicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast())
}

func (s *SMTP) dial(ctx context.Context) (net.Conn, error) {
	to := s.Timeout
	if to == 0 {
		to = 10 * time.Second
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, s.Config.Host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("notify: no se pudo resolver %s", s.Config.Host)
	}
	ip := ips[0].IP
	if !s.AllowPrivate && !publicIP(ip) {
		return nil, ErrUnsafeHost
	}
	d := net.Dialer{Timeout: to}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), strconv.Itoa(s.Config.Port)))
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(3 * to))
	if s.Config.Port == 465 {
		tc := tls.Client(conn, &tls.Config{ServerName: s.Config.Host, MinVersion: tls.VersionTLS12})
		if err := tc.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, err
		}
		return tc, nil
	}
	return conn, nil
}

// Send implements Provider.
func (s *SMTP) Send(ctx context.Context, e Email) (string, error) {
	conn, err := s.dial(ctx)
	if err != nil {
		return "", err
	}
	c, err := smtp.NewClient(conn, s.Config.Host)
	if err != nil {
		conn.Close()
		return "", err
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: s.Config.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return "", err
		}
	} else if s.Config.Username != "" && s.Config.Port != 465 && !s.AllowPrivate {
		return "", errors.New("notify: el servidor no ofrece STARTTLS; no se envían credenciales sin cifrar")
	}
	if s.Config.Username != "" {
		if ok, _ := c.Extension("AUTH"); ok {
			if err := c.Auth(plainAuth{s.Config.Username, s.Config.Password}); err != nil {
				return "", err
			}
		}
	}
	if err := c.Mail(e.FromEmail); err != nil {
		return "", err
	}
	if err := c.Rcpt(e.ToEmail); err != nil {
		return "", err
	}
	w, err := c.Data()
	if err != nil {
		return "", err
	}
	if _, err := w.Write(MIME(e)); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	_ = c.Quit()
	return e.MessageID, nil
}

// plainAuth is PLAIN auth without net/smtp's "TLS or localhost only" check:
// the caller already guarantees TLS (or an explicitly allowed private relay).
type plainAuth struct{ user, pass string }

func (a plainAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.pass), nil
}
func (a plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("notify: unexpected SMTP auth challenge")
	}
	return nil, nil
}

func header(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}

func address(name, email string) string {
	if name == "" {
		return "<" + header(email) + ">"
	}
	return mime.QEncoding.Encode("utf-8", header(name)) + " <" + header(email) + ">"
}

// MIME builds a multipart/alternative message (text + HTML, quoted-printable).
func MIME(e Email) []byte {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	boundary := "apexx-" + hex.EncodeToString(b)
	var buf bytes.Buffer
	domain := "apexx.local"
	if i := strings.LastIndex(e.FromEmail, "@"); i >= 0 {
		domain = e.FromEmail[i+1:]
	}
	fmt.Fprintf(&buf, "From: %s\r\n", address(e.FromName, e.FromEmail))
	fmt.Fprintf(&buf, "To: %s\r\n", address(e.ToName, e.ToEmail))
	if e.ReplyTo != "" {
		fmt.Fprintf(&buf, "Reply-To: %s\r\n", address("", e.ReplyTo))
	}
	fmt.Fprintf(&buf, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", header(e.Subject)))
	fmt.Fprintf(&buf, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&buf, "Message-ID: <%s@%s>\r\n", header(e.MessageID), header(domain))
	buf.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)
	for _, part := range []struct{ ctype, body string }{{"text/plain", e.Text}, {"text/html", e.HTML}} {
		fmt.Fprintf(&buf, "--%s\r\nContent-Type: %s; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", boundary, part.ctype)
		qp := quotedprintable.NewWriter(&buf)
		_, _ = qp.Write([]byte(part.body))
		_ = qp.Close()
		buf.WriteString("\r\n")
	}
	fmt.Fprintf(&buf, "--%s--\r\n", boundary)
	return buf.Bytes()
}

// SendGrid sends through the SendGrid v3 Mail Send API.
type SendGrid struct {
	APIKey  string
	BaseURL string // default https://api.sendgrid.com
	HTTP    *http.Client
}

// Name implements Provider.
func (*SendGrid) Name() string { return "sendgrid" }

// Send implements Provider.
func (s *SendGrid) Send(ctx context.Context, e Email) (string, error) {
	type addr struct {
		Email string `json:"email"`
		Name  string `json:"name,omitempty"`
	}
	body := map[string]any{
		"personalizations": []map[string]any{{"to": []addr{{e.ToEmail, e.ToName}}}},
		"from":             addr{e.FromEmail, e.FromName},
		"subject":          e.Subject,
		"content":          []map[string]string{{"type": "text/plain", "value": e.Text}, {"type": "text/html", "value": e.HTML}},
		"custom_args":      map[string]string{"apexx_message_id": e.MessageID},
	}
	if e.ReplyTo != "" {
		body["reply_to"] = addr{Email: e.ReplyTo}
	}
	raw, _ := json.Marshal(body)
	base := s.BaseURL
	if base == "" {
		base = "https://api.sendgrid.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v3/mail/send", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+s.APIKey)
	req.Header.Set("Content-Type", "application/json")
	hc := s.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("sendgrid: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return resp.Header.Get("X-Message-Id"), nil
}

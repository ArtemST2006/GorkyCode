// Package email sends reminder letters over a plain SMTP connection.
package email

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"gorkycode/backend/internal/config"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Client sends reminder letters over SMTP. Every letter is text/plain with
// UTF-8 headers: a plain reminder letter is fine for desktop mail agents.
type Client struct {
	host     string
	port     int
	username string
	password string
	from     string
	timeout  time.Duration
}

// New constructs the SMTP sender from the configured account.
func New(cfg config.Email) *Client {
	return &Client{
		host:     cfg.Host,
		port:     cfg.Port,
		username: cfg.Username,
		password: cfg.Password,
		from:     cfg.From,
		timeout:  cfg.Timeout,
	}
}

// Send delivers one message. ctx bounds the whole conversation.
func (c *Client) Send(ctx context.Context, to, subject, body string) error {
	addr := net.JoinHostPort(c.host, strconv.Itoa(c.port))

	dial := net.Dialer{Timeout: c.timeout}
	conn, err := dial.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()
	setDeadline(ctx, conn)

	// Port 465 speaks TLS from the first byte; any other port starts
	// plaintext and upgrades through STARTTLS when the server offers it.
	if c.port == 465 {
		conn = tls.Client(conn, &tls.Config{ServerName: c.host})
		setDeadline(ctx, conn)
	}
	client, err := smtp.NewClient(conn, c.host)
	if err != nil {
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer func() { _ = client.Close() }()

	if ext, _ := client.Extension("STARTTLS"); ext && c.port != 465 {
		if err = client.StartTLS(&tls.Config{ServerName: c.host}); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
		setDeadline(ctx, conn)
	}

	auth, err := chooseAuth(client, c.host, c.username, c.password)
	if err != nil {
		return err
	}
	if auth != nil {
		if err = client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err = client.Mail(c.from); err != nil {
		return fmt.Errorf("smtp mail: %w", err)
	}
	if err = client.Rcpt(to); err != nil {
		return fmt.Errorf("smtp rcpt: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err = writer.Write(buildMessage(c.from, to, subject, body)); err != nil {
		return fmt.Errorf("smtp body: %w", err)
	}
	if err = writer.Close(); err != nil {
		return fmt.Errorf("smtp body: %w", err)
	}
	return client.Quit()
}

// chooseAuth picks the strongest supported mechanism of the server: a
// configurable host may prefer PLAIN (Yandex, Mail.ru, Gmail) or LOGIN.
func chooseAuth(client *smtp.Client, host, username, password string) (smtp.Auth, error) {
	if username == "" {
		return nil, nil
	}
	authExt, mechanisms := client.Extension("AUTH")
	if !authExt {
		return nil, nil
	}
	for name := range strings.FieldsSeq(mechanisms) {
		switch strings.ToUpper(name) {
		case "PLAIN":
			return smtp.PlainAuth("", username, password, host), nil
		case "LOGIN":
			return loginAuth{username: username, password: password}, nil
		}
	}
	return nil, fmt.Errorf("no supported smtp auth mechanism")
}

// loginAuth implements the LOGIN mechanism many mail providers use instead
// of PLAIN.
type loginAuth struct {
	username string
	password string
}

func (a loginAuth) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", []byte(a.username), nil
}

func (a loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	switch {
	case !more:
		return nil, nil
	case strings.EqualFold(strings.TrimSpace(string(fromServer)), "username:"):
		return []byte(a.username), nil
	case strings.EqualFold(strings.TrimSpace(string(fromServer)), "password:"):
		return []byte(a.password), nil
	default:
		return nil, fmt.Errorf("unexpected smtp login prompt")
	}
}

// buildMessage renders an RFC 3501-style UTF-8 message with a base64 body so
// cyrillic subjects and text survive every SMTP relay.
func buildMessage(from, to, subject, body string) []byte {
	encoded := base64.StdEncoding.EncodeToString([]byte(body))
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Content-Type: text/plain; charset=utf-8\r\n")
	fmt.Fprintf(&b, "Content-Transfer-Encoding: base64\r\n")
	fmt.Fprintf(&b, "Mime-Version: 1.0\r\n\r\n")
	b.WriteString(encoded)
	return []byte(b.String())
}

// setDeadline applies the remaining request budget to every socket size.
func setDeadline(ctx context.Context, conn net.Conn) {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(time.Minute)
	}
	_ = conn.SetDeadline(deadline)
}

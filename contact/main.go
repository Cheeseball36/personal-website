// Command contact handles the website's contact form.
//
// A visitor gives their name, email address and the answer to a human check.
// If everything checks out, they get one short, fixed email from Jackson
// asking them to reply, and Jackson gets a note saying who it was. Jackson's
// address never appears on the website itself.
//
// On the server it runs as a CGI program under OpenBSD httpd + slowcgi and
// hands email to the server's own mail program (smtpd), which relays it
// through SMTP2GO. On a computer, `go run . -dev :4174` serves the site from
// ../public with the form working, printing emails instead of sending them.
package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/http/cgi"
	"net/mail"
	"net/netip"
	"net/smtp"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

//go:embed visitor-email.txt
var visitorEmail string

// Where the browser is sent afterwards. These are pages in public/contact/.
const (
	formPage    = "/contact/"
	sentPage    = "/contact/sent.html"
	notSentPage = "/contact/not-sent.html"
)

// trapField is the hidden bot trap in the form. Its name means nothing, so
// browsers and password managers have no reason to fill it in.
const trapField = "hp_x7"

// Abuse limits.
const (
	maxTriesPerIPPerHour = 5                   // any submission, right or wrong
	addressQuiet         = 30 * 24 * time.Hour // one email per inbox per 30 days
	maxSendsPerDay       = 20                  // emails to visitors, in total
	maxBodyBytes         = 4096
	maxLogBytes          = 1 << 20 // past this, refuse everything rather than grow
)

// ---------- settings ----------

// Config is read from a KEY=VALUE file. See dev.conf for an example.
type Config struct {
	From     string // "Jackson Mercuri <jackson@example.com>": must be on your own domain
	ReplyTo  string // optional: where replies go, if not the From address
	NotifyTo string // who gets told about each new contact
	LastName string // the answer to "What is my last name?"
	SMTPAddr string // the server's own mail program, which relays through SMTP2GO
	Proxy    string // address of relayd; requests from it carry the visitor's IP in X-Forwarded-For
	DataDir  string // writable folder for the rate-limit log
	DryRun   bool   // write emails to a file instead of sending them
}

func loadConfig(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	c := Config{SMTPAddr: "127.0.0.1:25"}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return Config{}, fmt.Errorf("%s line %d: expected KEY = VALUE", path, n)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			v = v[1 : len(v)-1]
		}
		switch k {
		case "FROM":
			c.From = v
		case "REPLY_TO":
			c.ReplyTo = v
		case "NOTIFY_TO":
			c.NotifyTo = v
		case "LAST_NAME":
			c.LastName = v
		case "SMTP_ADDR":
			c.SMTPAddr = v
		case "PROXY":
			c.Proxy = v
		case "DATA_DIR":
			c.DataDir = v
		case "DRY_RUN":
			switch v {
			case "1":
				c.DryRun = true
			case "0", "":
				c.DryRun = false
			default:
				return Config{}, fmt.Errorf("%s line %d: DRY_RUN must be 1 or 0", path, n)
			}
		default:
			return Config{}, fmt.Errorf("%s line %d: unknown setting %q", path, n, k)
		}
	}
	if err := sc.Err(); err != nil {
		return Config{}, err
	}
	switch {
	case c.From == "" || c.NotifyTo == "" || c.DataDir == "":
		return Config{}, fmt.Errorf("%s: FROM, NOTIFY_TO and DATA_DIR are required", path)
	case strings.TrimSpace(c.LastName) == "":
		return Config{}, fmt.Errorf("%s: LAST_NAME is required (an empty answer would let bots through)", path)
	}
	for key, v := range map[string]string{"FROM": c.From, "NOTIFY_TO": c.NotifyTo, "REPLY_TO": c.ReplyTo} {
		if v == "" {
			continue
		}
		if _, err := mail.ParseAddress(v); err != nil || strings.ContainsAny(v, "\r\n") {
			return Config{}, fmt.Errorf("%s: %s is not a valid address: %q", path, key, v)
		}
	}
	if c.Proxy != "" {
		if _, err := netip.ParseAddr(c.Proxy); err != nil {
			return Config{}, fmt.Errorf("%s: PROXY must be an IP address", path)
		}
	}
	return c, nil
}

// ---------- checking what the visitor typed ----------

// cleanName allows letters (with their accents and vowel signs), spaces and
// . ' - only, up to 50 characters, so the name can't carry links or other
// spam into the email that goes out.
func cleanName(s string) (string, bool) {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" || utf8.RuneCountInString(s) > 50 {
		return "", false
	}
	rs := []rune(s)
	marks := 0
	for i, r := range rs {
		switch {
		case unicode.Is(unicode.M, r):
			// accents and vowel signs, only straight after a letter, a few at most
			marks++
			if i == 0 || marks > 3 || !(unicode.IsLetter(rs[i-1]) || unicode.Is(unicode.M, rs[i-1])) {
				return "", false
			}
			continue
		case r >= 0xFF00 && r <= 0xFFEF: // fullwidth look-alikes
			return "", false
		case unicode.Is(unicode.Lm, r) && r != 'ʻ' && r != 'ʼ': // modifier letters, except the ʻokina
			return "", false
		case unicode.IsLetter(r), r == ' ', r == '\'', r == '-', r == '’':
		case r == '.':
			// "J. R. R." is fine, "spam.com" is not: mail apps turn that into a link.
			if i+1 < len(rs) && rs[i+1] != ' ' {
				return "", false
			}
		default:
			return "", false
		}
		marks = 0
	}
	return s, true
}

// cleanEmail accepts one plain ASCII address (no display name, no extra
// recipients, no line breaks, a real domain) and returns it with the domain
// lowercased.
func cleanEmail(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 254 || strings.ContainsAny(s, "\r\n<>,;\" ") {
		return "", false
	}
	for _, r := range s {
		if r > unicode.MaxASCII || !unicode.IsPrint(r) {
			return "", false
		}
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || a.Name != "" {
		return "", false
	}
	local, domain, ok := strings.Cut(s, "@")
	if !ok || local == "" || strings.Contains(domain, "@") {
		return "", false
	}
	domain = strings.ToLower(domain)
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return "", false
	}
	for _, l := range labels {
		if l == "" {
			return "", false
		}
	}
	tld := labels[len(labels)-1]
	if len(tld) < 2 || strings.Trim(tld, "abcdefghijklmnopqrstuvwxyz") != "" {
		return "", false
	}
	return local + "@" + domain, true
}

// inboxKey maps the many spellings of one inbox to one key, so the
// one-email-per-inbox limit can't be dodged with bob+1@ or b.ob@gmail.com.
func inboxKey(addr string) string {
	local, domain, _ := strings.Cut(strings.ToLower(addr), "@")
	local, _, _ = strings.Cut(local, "+")
	if domain == "gmail.com" || domain == "googlemail.com" {
		local = strings.ReplaceAll(local, ".", "")
		domain = "gmail.com"
	}
	return local + "@" + domain
}

// cleanWhere keeps printable characters only. It goes to Jackson, never to
// the visitor.
func cleanWhere(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > 60 {
		s = string([]rune(s)[:60])
	}
	return s
}

// clientKey identifies a visitor for the per-visitor limit. IPv6 visitors
// are grouped by /64, since one home or server usually has a whole /64.
//
// On the server every request arrives from relayd, which replaces any
// X-Forwarded-For the visitor sent with their real address. That header is
// only trusted when the request really comes from relayd (proxy).
func clientKey(r *http.Request, proxy string) string {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if proxy != "" && host == proxy {
		host = strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
		if _, err := netip.ParseAddr(host); err != nil {
			return "proxy:unknown" // one shared bucket, so a bad header can't dodge limits
		}
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return "raw:" + host
	}
	a = a.Unmap()
	if a.Is6() {
		p, _ := a.Prefix(64)
		return p.String()
	}
	return a.String()
}

// sameSite reports whether the form was sent from this site's own page, so
// other websites can't make their visitors' browsers submit it.
func sameSite(r *http.Request) bool {
	if s := r.Header.Get("Sec-Fetch-Site"); s != "" && s != "same-origin" {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || !strings.EqualFold(u.Host, r.Host) {
			return false
		}
	}
	return true
}

// ---------- rate limits ----------

// Limiter keeps a small log of recent events in its folder. Addresses and IPs
// are stored as salted hashes, never in plain text. Tries are kept for an
// hour and sends for 30 days.
type Limiter struct {
	dir string
	now func() time.Time
}

type event struct {
	at   int64
	kind string // "try" (per visitor) or "sent" (per inbox)
	key  string
}

func (l *Limiter) path(name string) string { return filepath.Join(l.dir, name) }

func (l *Limiter) salt() (string, error) {
	p := l.path("salt")
	if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
		return string(b), nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	s := hex.EncodeToString(b)
	return s, os.WriteFile(p, []byte(s), 0o600)
}

func hashKey(salt, v string) string {
	h := sha256.Sum256([]byte(salt + "\x00" + v))
	return hex.EncodeToString(h[:16])
}

// read returns the events still worth keeping.
func (l *Limiter) read() ([]event, error) {
	p := l.path("log")
	if info, err := os.Stat(p); err == nil && info.Size() > maxLogBytes {
		return nil, fmt.Errorf("rate log is over %d bytes; refusing until it shrinks", maxLogBytes)
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	now := l.now()
	tryCutoff, sentCutoff := now.Add(-time.Hour).Unix(), now.Add(-addressQuiet).Unix()
	var evs []event
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		at, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil || (f[1] == "try" && at < tryCutoff) || (f[1] == "sent" && at < sentCutoff) {
			continue
		}
		evs = append(evs, event{at, f[1], f[2]})
	}
	return evs, nil
}

func (l *Limiter) write(evs []event) error {
	var buf bytes.Buffer
	for _, e := range evs {
		fmt.Fprintf(&buf, "%d %s %s\n", e.at, e.kind, e.key)
	}
	tmp, err := os.CreateTemp(l.dir, "log-*.tmp")
	if err != nil {
		return err
	}
	_, err = tmp.Write(buf.Bytes())
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), l.path("log"))
}

// withLog runs fn with the log locked, saving the events fn returns (if any).
func (l *Limiter) withLog(fn func(salt string, evs []event) ([]event, error)) error {
	unlock, err := lockFile(l.path("lock"))
	if err != nil {
		return err
	}
	defer unlock()
	salt, err := l.salt()
	if err != nil {
		return err
	}
	evs, err := l.read()
	if err != nil {
		return err
	}
	out, err := fn(salt, evs)
	if err != nil || out == nil {
		return err
	}
	return l.write(out)
}

// noteTry records a submission from a visitor and reports whether it is
// within limits. Tries over the limit are not recorded, so the log can't be
// flooded.
func (l *Limiter) noteTry(visitor string) (ok bool, err error) {
	err = l.withLog(func(salt string, evs []event) ([]event, error) {
		key, n := hashKey(salt, "ip:"+visitor), 0
		for _, e := range evs {
			if e.kind == "try" && e.key == key {
				n++
			}
		}
		if n >= maxTriesPerIPPerHour {
			return nil, nil
		}
		ok = true
		return append(evs, event{l.now().Unix(), "try", key}), nil
	})
	return ok && err == nil, err
}

// reserveSend checks the per-inbox and daily limits and, if there is room,
// records the send. Call release if the email then fails to go out.
func (l *Limiter) reserveSend(addr string) (ok bool, release func(), err error) {
	var ev event
	err = l.withLog(func(salt string, evs []event) ([]event, error) {
		key, dayAgo, perInbox, today := hashKey(salt, "to:"+inboxKey(addr)), l.now().Add(-24*time.Hour).Unix(), 0, 0
		for _, e := range evs {
			if e.kind != "sent" {
				continue
			}
			if e.key == key {
				perInbox++
			}
			if e.at >= dayAgo {
				today++
			}
		}
		if perInbox > 0 || today >= maxSendsPerDay {
			return nil, nil
		}
		ok = true
		ev = event{l.now().Unix(), "sent", key}
		return append(evs, ev), nil
	})
	if err != nil || !ok {
		return false, nil, err
	}
	release = func() {
		l.withLog(func(_ string, evs []event) ([]event, error) {
			for i, e := range evs {
				if e == ev {
					return append(evs[:i], evs[i+1:]...), nil
				}
			}
			return nil, nil
		})
	}
	return true, release, nil
}

// ---------- sending ----------

type Message struct {
	From, To, ReplyTo, Subject, Body string
}

type Mailer interface {
	Send(Message) error
}

// printMailer is for testing: it writes the email to the log instead.
type printMailer struct{ w io.Writer }

func (p printMailer) Send(m Message) error {
	fmt.Fprintf(p.w, "\n----- email (not sent: DRY_RUN) -----\nFrom: %s\nTo: %s\n", m.From, m.To)
	if m.ReplyTo != "" {
		fmt.Fprintf(p.w, "Reply-To: %s\n", m.ReplyTo)
	}
	fmt.Fprintf(p.w, "Subject: %s\n\n%s\n-------------------------------------\n", m.Subject, m.Body)
	return nil
}

// buildMessage writes a complete plain-text email. Headers are encoded so a
// name with accents works, and any line break in a header is refused.
func buildMessage(m Message, now time.Time) ([]byte, error) {
	from, err := mail.ParseAddress(m.From)
	if err != nil {
		return nil, fmt.Errorf("bad From: %w", err)
	}
	for _, v := range []string{m.From, m.To, m.ReplyTo, m.Subject} {
		if strings.ContainsAny(v, "\r\n") {
			return nil, errors.New("line break in a header")
		}
	}
	_, domain, _ := strings.Cut(from.Address, "@")
	id := make([]byte, 12)
	rand.Read(id)

	var b bytes.Buffer
	header := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	header("From", from.String())
	header("To", m.To)
	if m.ReplyTo != "" {
		header("Reply-To", m.ReplyTo)
	}
	header("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	header("Date", now.Format(time.RFC1123Z))
	header("Message-ID", "<"+hex.EncodeToString(id)+"@"+domain+">")
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=utf-8")
	header("Content-Transfer-Encoding", "quoted-printable")
	b.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&b)
	if _, err := qp.Write([]byte(m.Body)); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// smtpMailer hands email to the server's own mail program (OpenBSD smtpd),
// which relays it through SMTP2GO. The SMTP2GO password stays in
// /etc/mail/secrets, readable only by root, never in the web folder.
type smtpMailer struct {
	addr string
}

func (s smtpMailer) Send(m Message) error {
	msg, err := buildMessage(m, time.Now())
	if err != nil {
		return err
	}
	from, _ := mail.ParseAddress(m.From)
	_, domain, _ := strings.Cut(from.Address, "@")

	conn, err := net.DialTimeout("tcp", s.addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("mail server: %w", err)
	}
	conn.SetDeadline(time.Now().Add(20 * time.Second))
	host, _, _ := net.SplitHostPort(s.addr)
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("mail server: %w", err)
	}
	defer c.Close()
	// No STARTTLS: this connection never leaves the server.
	steps := []func() error{
		func() error { return c.Hello(domain) },
		func() error { return c.Mail(from.Address) },
		func() error { return c.Rcpt(m.To) },
		func() error {
			w, err := c.Data()
			if err != nil {
				return err
			}
			if _, err := w.Write(msg); err != nil {
				return err
			}
			return w.Close()
		},
		c.Quit,
	}
	for _, step := range steps {
		if err := step(); err != nil {
			// The server's reply can quote the address, so only its code is logged.
			var tpErr *textproto.Error
			if errors.As(err, &tpErr) {
				return fmt.Errorf("mail server refused (code %d)", tpErr.Code)
			}
			return errors.New("mail server: connection failed")
		}
	}
	return nil
}

// ---------- the form handler ----------

type Handler struct {
	cfg     Config
	limiter *Limiter
	mailer  Mailer
	logger  *log.Logger
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, formPage, http.StatusSeeOther)
		return
	}
	if !sameSite(r) {
		h.fail(w, r, "sent from another website")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, "unreadable form")
		return
	}

	ok, err := h.limiter.noteTry(clientKey(r, h.cfg.Proxy))
	if err != nil {
		h.logger.Printf("contact: rate log: %v", err)
		h.fail(w, r, "")
		return
	}
	if !ok {
		h.fail(w, r, "too many tries from one visitor")
		return
	}

	if t := r.PostForm.Get(trapField); t != "" {
		// Logged so a real person caught by autofill would show up.
		h.fail(w, r, fmt.Sprintf("bot trap filled (%d characters)", utf8.RuneCountInString(t)))
		return
	}

	name, okName := cleanName(r.PostForm.Get("name"))
	email, okEmail := cleanEmail(r.PostForm.Get("email"))
	human := strings.TrimSpace(r.PostForm.Get("human"))
	where := cleanWhere(r.PostForm.Get("where"))
	switch {
	case !okName:
		h.fail(w, r, "bad name")
		return
	case !okEmail:
		h.fail(w, r, "bad email")
		return
	case !strings.EqualFold(human, h.cfg.LastName):
		h.fail(w, r, "wrong last name")
		return
	}

	ok, release, err := h.limiter.reserveSend(email)
	if err != nil {
		h.logger.Printf("contact: rate log: %v", err)
		h.fail(w, r, "")
		return
	}
	if !ok {
		h.fail(w, r, "inbox already emailed in the last 30 days, or daily cap reached")
		return
	}

	err = h.mailer.Send(Message{
		From:    h.cfg.From,
		To:      email,
		ReplyTo: h.cfg.ReplyTo,
		Subject: "Please reply to this email",
		Body:    strings.ReplaceAll(visitorEmail, "{name}", name),
	})
	if err != nil {
		release()
		h.logger.Printf("contact: sending to visitor: %v", err)
		h.fail(w, r, "")
		return
	}

	note := fmt.Sprintf("New contact from the website form.\n\nName:  %s\nEmail: %s\n", name, email)
	if where != "" {
		note += "Where: " + where + "\n"
	}
	note += "\nThey were asked to reply to you. Add them to your contacts so their reply isn't marked as spam.\n"
	if err := h.mailer.Send(Message{
		From:    h.cfg.From,
		To:      h.cfg.NotifyTo,
		Subject: "Contact form: " + name,
		Body:    note,
	}); err != nil {
		h.logger.Printf("contact: sending note to you: %v", err) // the visitor's email already went out
	}

	http.Redirect(w, r, sentPage, http.StatusSeeOther)
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, why string) {
	if why != "" {
		h.logger.Printf("contact: rejected: %s", why)
	}
	http.Redirect(w, r, notSentPage, http.StatusSeeOther)
}

// ---------- main ----------

// openCapped opens a file for appending, starting it afresh once it passes
// 256 KB so it can never fill the disk.
func openCapped(path string) (*os.File, error) {
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if info, err := os.Stat(path); err == nil && info.Size() > 256<<10 {
		flags |= os.O_TRUNC
	}
	return os.OpenFile(path, flags, 0o600)
}

func main() {
	dev := flag.String("dev", "", "run a local test server on this address, e.g. :4174 (needs DRY_RUN = 1)")
	site := flag.String("site", "../public", "with -dev: the folder of site files to serve")
	conf := flag.String("config", "", "settings file (default: dev.conf with -dev, /contact/contact.conf on the server)")
	flag.Parse()

	logger := log.New(os.Stderr, "", log.LstdFlags)
	path := *conf
	if path == "" {
		path = "/contact/contact.conf" // inside httpd's /var/www chroot
		if *dev != "" {
			path = "dev.conf"
		}
	}
	cfg, err := loadConfig(path)
	if err != nil {
		logger.Fatal(err)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		logger.Fatal(err)
	}

	var m Mailer = smtpMailer{addr: cfg.SMTPAddr}
	if *dev == "" {
		// On the server, notes go to files in DATA_DIR, where they can be read
		// without hunting through system logs.
		if f, err := openCapped(filepath.Join(cfg.DataDir, "contact.log")); err == nil {
			defer f.Close()
			logger.SetOutput(io.MultiWriter(os.Stderr, f))
		}
		if cfg.DryRun {
			f, err := openCapped(filepath.Join(cfg.DataDir, "dry-run.txt"))
			if err != nil {
				logger.Fatal(err)
			}
			defer f.Close()
			m = printMailer{f}
		}
	} else if cfg.DryRun {
		m = printMailer{os.Stderr}
	}
	h := &Handler{cfg: cfg, limiter: &Limiter{dir: cfg.DataDir, now: time.Now}, mailer: m, logger: logger}

	if *dev == "" {
		if err := cgi.Serve(h); err != nil {
			logger.Fatal(err)
		}
		return
	}

	if !cfg.DryRun {
		logger.Fatal("-dev only runs with DRY_RUN = 1, so testing never sends real email")
	}
	addr := *dev
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr // this computer only
	}
	mux := http.NewServeMux()
	mux.Handle("/cgi-bin/contact", h)
	mux.Handle("/", http.FileServer(http.Dir(*site)))
	logger.Printf("dev server on http://%s (emails are printed here, not sent)", addr)
	logger.Fatal(http.ListenAndServe(addr, mux))
}

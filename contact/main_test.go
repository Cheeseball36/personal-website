package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCleanName(t *testing.T) {
	good := map[string]string{
		"Ada Lovelace":          "Ada Lovelace",
		"  Mary-Jane   O'Neil ": "Mary-Jane O'Neil",
		"José Núñez":            "José Núñez",
		"J. R. R. Tolkien":      "J. R. R. Tolkien",
		"राहुल शर्मा":           "राहुल शर्मा", // Devanagari vowel signs are marks
		"José":                 "José",       // accent typed as a separate mark
		"Keʻalohilani":          "Keʻalohilani",
	}
	for in, want := range good {
		if got, ok := cleanName(in); !ok || got != want {
			t.Errorf("cleanName(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	bad := []string{"", "   ", "visit spam.com/now", "Visit spam.com", "a\r\nBcc: x@y.com",
		"name<script>", "http://x", "buy 100 pills", strings.Repeat("a", 51),
		"exampleꓸcom", // Lisu modifier letter that looks like a dot
		"ｓｐａｍ",        // fullwidth letters
		"́abc",        // mark with no letter before it
		"Zalgò́̂̃",   // piled-up marks
	}
	for _, in := range bad {
		if _, ok := cleanName(in); ok {
			t.Errorf("cleanName(%q) accepted", in)
		}
	}
}

func TestCleanEmail(t *testing.T) {
	good := map[string]string{
		"ada@Example.COM":             "ada@example.com",
		" first.last+tag@mail.co.uk ": "first.last+tag@mail.co.uk",
	}
	for in, want := range good {
		if got, ok := cleanEmail(in); !ok || got != want {
			t.Errorf("cleanEmail(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	bad := []string{"", "no-at-sign", "a@b", "a@.com", "a@b.", "two@@b.com", "a@b..com",
		"Ada <ada@example.com>", "a@b.com, c@d.com", "a@b.com\r\nBcc: c@d.com",
		"a@b.com;c@d.com", "a b@c.com", strings.Repeat("a", 250) + "@b.com",
		"a@1.2.3.4", "a@ä.com", "ä@b.com", "a@b.c0m"}
	for _, in := range bad {
		if _, ok := cleanEmail(in); ok {
			t.Errorf("cleanEmail(%q) accepted", in)
		}
	}
}

func TestInboxKey(t *testing.T) {
	same := []string{"bob@gmail.com", "Bob+1@gmail.com", "b.o.b@googlemail.com", "BOB+x@GMAIL.COM"}
	for _, a := range same {
		if got := inboxKey(a); got != "bob@gmail.com" {
			t.Errorf("inboxKey(%q) = %q", a, got)
		}
	}
	if inboxKey("b.ob+news@example.com") != "b.ob@example.com" {
		t.Error("dots should only be dropped for Gmail")
	}
}

func TestCleanWhere(t *testing.T) {
	if got := cleanWhere("  Cleveland,\n OH \x00"); got != "Cleveland, OH" {
		t.Errorf("got %q", got)
	}
	if got := cleanWhere(strings.Repeat("é", 80)); len([]rune(got)) != 60 {
		t.Errorf("not cut to 60: %d", len([]rune(got)))
	}
}

func TestClientKey(t *testing.T) {
	cases := map[string]string{
		"1.2.3.4:5555":                 "1.2.3.4",
		"[::ffff:1.2.3.4]:5555":        "1.2.3.4",
		"[2001:db8:1:2:aaaa::1]:5555":  "2001:db8:1:2::/64",
		"[2001:db8:1:2:bbbb::99]:5555": "2001:db8:1:2::/64",
	}
	for in, want := range cases {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.RemoteAddr = in
		if got := clientKey(r); got != want {
			t.Errorf("clientKey(%q) = %q; want %q", in, got, want)
		}
	}
}

func writeConf(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "c.conf")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadConfig(t *testing.T) {
	cfg, err := loadConfig("dev.conf")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DryRun || cfg.LastName != "Mercuri" || cfg.From == "" {
		t.Errorf("unexpected config: %+v", cfg)
	}
	base := "FROM = a <a@b.com>\nNOTIFY_TO = me@b.com\nDATA_DIR = d\n"
	if _, err := loadConfig(writeConf(t, base+"LAST_NAME =\nDRY_RUN = 1\n")); err == nil {
		t.Error("empty LAST_NAME accepted")
	}
	if _, err := loadConfig(writeConf(t, base+"LAST_NAME = Mercuri\nDRY_RUN = yes\n")); err == nil {
		t.Error("DRY_RUN = yes accepted")
	}
	if _, err := loadConfig(writeConf(t, base+"LAST_NAME = Mercuri\n")); err == nil {
		t.Error("real sending without an API key accepted")
	}
	c, err := loadConfig(writeConf(t, base+"LAST_NAME = \"Mercuri\"\nDRY_RUN = 1\n"))
	if err != nil || c.LastName != "Mercuri" {
		t.Errorf("quotes not removed: %q, %v", c.LastName, err)
	}
}

// ---------- handler ----------

type fakeMailer struct {
	mu   sync.Mutex
	sent []Message
	fail bool
}

func (f *fakeMailer) Send(m Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("boom")
	}
	f.sent = append(f.sent, m)
	return nil
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newTestHandler(t *testing.T) (*Handler, *fakeMailer, *clock) {
	t.Helper()
	c := &clock{time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)}
	m := &fakeMailer{}
	h := &Handler{
		cfg: Config{From: "Jackson <hi@example.com>", ReplyTo: "reply@example.com",
			NotifyTo: "me@example.com", LastName: "Mercuri", DataDir: t.TempDir()},
		mailer: m,
		logger: log.New(io.Discard, "", 0),
	}
	h.limiter = &Limiter{dir: h.cfg.DataDir, now: c.now}
	return h, m, c
}

func postWith(h http.Handler, ip string, form url.Values, hdr map[string]string) string {
	r := httptest.NewRequest(http.MethodPost, "/cgi-bin/contact", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	r.RemoteAddr = ip + ":5555"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		return "status " + w.Result().Status
	}
	return w.Header().Get("Location")
}

func post(h http.Handler, ip string, form url.Values) string { return postWith(h, ip, form, nil) }

func form(name, email, human string) url.Values {
	return url.Values{"name": {name}, "email": {email}, "human": {human}, "where": {"Ohio"}}
}

func TestGoodSubmission(t *testing.T) {
	h, m, _ := newTestHandler(t)
	got := postWith(h, "1.2.3.4", form("Ada", "ada@example.com", " mercuri "),
		map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "http://example.com"})
	if got != sentPage {
		t.Fatalf("went to %s", got)
	}
	if len(m.sent) != 2 {
		t.Fatalf("sent %d emails, want 2", len(m.sent))
	}
	v, n := m.sent[0], m.sent[1]
	if v.To != "ada@example.com" || v.ReplyTo != "reply@example.com" || !strings.HasPrefix(v.Body, "Hi Ada -") {
		t.Errorf("visitor email wrong: %+v", v)
	}
	if strings.Contains(v.Subject, "Ada") || strings.Contains(v.Body, "Ohio") {
		t.Errorf("visitor text leaked where it shouldn't: %+v", v)
	}
	if n.To != "me@example.com" || !strings.Contains(n.Body, "ada@example.com") || !strings.Contains(n.Body, "Ohio") {
		t.Errorf("note to Jackson wrong: %+v", n)
	}
}

func TestRejections(t *testing.T) {
	cases := map[string]url.Values{
		"wrong last name": form("Ada", "ada@example.com", "Smith"),
		"empty last name": form("Ada", "ada@example.com", ""),
		"bad email":       form("Ada", "ada@example", "Mercuri"),
		"spammy name":     form("Visit spam.com", "ada@example.com", "Mercuri"),
		"header attack":   form("Ada", "ada@example.com\r\nBcc: x@y.com", "Mercuri"),
	}
	for name, f := range cases {
		h, m, _ := newTestHandler(t)
		if got := post(h, "1.2.3.4", f); got != notSentPage {
			t.Errorf("%s: went to %s", name, got)
		}
		if len(m.sent) != 0 {
			t.Errorf("%s: sent %d emails", name, len(m.sent))
		}
	}
}

func TestOtherWebsitesRejected(t *testing.T) {
	for _, hdr := range []map[string]string{
		{"Sec-Fetch-Site": "cross-site"},
		{"Sec-Fetch-Site": "same-site"},
		{"Origin": "https://evil.example"},
	} {
		h, m, _ := newTestHandler(t)
		if got := postWith(h, "1.2.3.4", form("Ada", "ada@example.com", "Mercuri"), hdr); got != notSentPage {
			t.Errorf("%v: went to %s", hdr, got)
		}
		if len(m.sent) != 0 {
			t.Errorf("%v: sent", hdr)
		}
	}
}

func TestTrapField(t *testing.T) {
	h, m, _ := newTestHandler(t)
	f := form("Ada", "ada@example.com", "Mercuri")
	f.Set(trapField, "http://spam")
	if got := post(h, "1.2.3.4", f); got != notSentPage {
		t.Errorf("went to %s", got)
	}
	if len(m.sent) != 0 {
		t.Errorf("sent %d emails", len(m.sent))
	}
}

func TestGetRedirectsToForm(t *testing.T) {
	h, _, _ := newTestHandler(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/cgi-bin/contact", nil))
	if w.Header().Get("Location") != formPage {
		t.Errorf("GET went to %q", w.Header().Get("Location"))
	}
}

func TestOversizedBodyRejected(t *testing.T) {
	h, m, _ := newTestHandler(t)
	f := form("Ada", "ada@example.com", "Mercuri")
	f.Set("padding", strings.Repeat("x", maxBodyBytes))
	if got := post(h, "1.2.3.4", f); got != notSentPage {
		t.Errorf("went to %s", got)
	}
	if len(m.sent) != 0 {
		t.Error("sent despite oversized body")
	}
}

func TestOneEmailPerInbox(t *testing.T) {
	h, m, c := newTestHandler(t)
	post(h, "1.1.1.1", form("Bob", "bob@gmail.com", "Mercuri"))
	// other spellings of the same inbox, from other visitors
	for i, addr := range []string{"bob@GMAIL.com", "bob+1@gmail.com", "b.o.b@googlemail.com"} {
		if got := post(h, fmt.Sprintf("2.2.2.%d", i), form("Bob", addr, "Mercuri")); got != notSentPage {
			t.Errorf("%s went to %s", addr, got)
		}
	}
	c.t = c.t.Add(29 * 24 * time.Hour)
	if got := post(h, "3.3.3.3", form("Bob", "bob@gmail.com", "Mercuri")); got != notSentPage {
		t.Errorf("29 days later went to %s", got)
	}
	c.t = c.t.Add(2 * 24 * time.Hour)
	if got := post(h, "4.4.4.4", form("Bob", "bob@gmail.com", "Mercuri")); got != sentPage {
		t.Errorf("31 days later went to %s", got)
	}
	if len(m.sent) != 4 { // two sends x (visitor + note)
		t.Errorf("sent %d emails, want 4", len(m.sent))
	}
}

func TestTriesPerVisitorPerHour(t *testing.T) {
	h, _, c := newTestHandler(t)
	for i := 0; i < maxTriesPerIPPerHour; i++ {
		post(h, "9.9.9.9", form("Ada", "ada@example.com", "wrong"))
	}
	// even the right answer is refused now
	if got := post(h, "9.9.9.9", form("Bo", "bo@example.com", "Mercuri")); got != notSentPage {
		t.Errorf("went to %s", got)
	}
	// another visitor is unaffected
	if got := post(h, "8.8.8.8", form("Bo", "bo@example.com", "Mercuri")); got != sentPage {
		t.Errorf("other visitor went to %s", got)
	}
	c.t = c.t.Add(61 * time.Minute)
	if got := post(h, "9.9.9.9", form("Cy", "cy@example.com", "Mercuri")); got != sentPage {
		t.Errorf("an hour later went to %s", got)
	}
}

func TestIPv6GroupedBy64(t *testing.T) {
	h, _, _ := newTestHandler(t)
	for i := 0; i < maxTriesPerIPPerHour; i++ {
		post(h, fmt.Sprintf("[2001:db8:1:2::%x]", i+1), form("Ada", "ada@example.com", "wrong"))
	}
	if got := post(h, "[2001:db8:1:2::ffff]", form("Bo", "bo@example.com", "Mercuri")); got != notSentPage {
		t.Errorf("new address in the same /64 went to %s", got)
	}
}

func TestRefusedTriesAreNotLogged(t *testing.T) {
	h, _, _ := newTestHandler(t)
	for i := 0; i < 200; i++ {
		post(h, "9.9.9.9", form("Ada", "ada@example.com", "wrong"))
	}
	b, _ := os.ReadFile(filepath.Join(h.cfg.DataDir, "log"))
	if n := bytes.Count(b, []byte("\n")); n != maxTriesPerIPPerHour {
		t.Errorf("log has %d lines after a flood, want %d", n, maxTriesPerIPPerHour)
	}
}

func TestOversizedLogFailsClosed(t *testing.T) {
	h, m, _ := newTestHandler(t)
	big := bytes.Repeat([]byte("1 try x\n"), maxLogBytes/8+1)
	os.WriteFile(filepath.Join(h.cfg.DataDir, "log"), big, 0o600)
	if got := post(h, "1.2.3.4", form("Ada", "ada@example.com", "Mercuri")); got != notSentPage {
		t.Errorf("went to %s", got)
	}
	if len(m.sent) != 0 {
		t.Error("sent despite oversized log")
	}
}

func TestDailyCap(t *testing.T) {
	h, _, _ := newTestHandler(t)
	for i := 0; i < maxSendsPerDay; i++ {
		if got := post(h, fmt.Sprintf("10.0.0.%d", i), form("Ada", fmt.Sprintf("p%d@example.com", i), "Mercuri")); got != sentPage {
			t.Fatalf("send %d went to %s", i, got)
		}
	}
	if got := post(h, "11.0.0.1", form("Ada", "last@example.com", "Mercuri")); got != notSentPage {
		t.Errorf("past the cap went to %s", got)
	}
}

func TestFailedSendFreesTheInbox(t *testing.T) {
	h, m, _ := newTestHandler(t)
	m.fail = true
	if got := post(h, "1.1.1.1", form("Ada", "ada@example.com", "Mercuri")); got != notSentPage {
		t.Errorf("went to %s", got)
	}
	m.fail = false
	if got := post(h, "2.2.2.2", form("Ada", "ada@example.com", "Mercuri")); got != sentPage {
		t.Errorf("retry after failure went to %s", got)
	}
}

func TestConcurrentSubmissionsKeepLimits(t *testing.T) {
	h, m, _ := newTestHandler(t)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			post(h, fmt.Sprintf("10.1.0.%d", i), form("Bob", "bob@gmail.com", "Mercuri"))
		}(i)
	}
	wg.Wait()
	if len(m.sent) != 2 { // exactly one visitor email + one note
		t.Errorf("30 simultaneous submissions for one inbox sent %d emails, want 2", len(m.sent))
	}
}

func TestLogStoresNoPlainAddresses(t *testing.T) {
	h, _, _ := newTestHandler(t)
	post(h, "1.2.3.4", form("Ada", "ada@example.com", "Mercuri"))
	b, err := os.ReadFile(filepath.Join(h.cfg.DataDir, "log"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("ada")) || bytes.Contains(b, []byte("1.2.3.4")) {
		t.Errorf("log holds plain text: %s", b)
	}
}

func TestSMTP2GOResult(t *testing.T) {
	cases := map[string]bool{
		`{"data":{"succeeded":1,"failed":0}}`:          true,
		`{"data":{"succeeded":0,"failed":0}}`:          false,
		`{"data":{"succeeded":0,"failed":1}}`:          false,
		`{"data":{"error_code":"E_ApiResponseCodes"}}`: false,
		`not json`: false,
	}
	for body, want := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Smtp2go-Api-Key") != "k" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			io.WriteString(w, body)
		}))
		err := smtp2go{key: "k", client: srv.Client(), url: srv.URL}.Send(Message{From: "a@b.com", To: "c@d.com"})
		srv.Close()
		if (err == nil) != want {
			t.Errorf("reply %s: err = %v", body, err)
		}
		if err != nil && strings.Contains(err.Error(), "c@d.com") {
			t.Errorf("error message contains the address: %v", err)
		}
	}
}

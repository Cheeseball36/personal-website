# Contact form program

Handles the form at `public/contact/index.html`, the same way
https://sive.rs/contact works: your email address never appears on the site.

1. A visitor gives their name, email, an optional location, and the answer to
   "What is my last name?".
2. If it all checks out, they get one short, fixed email from you
   (`visitor-email.txt`) asking them to reply, and you get a note saying who
   it was, so you can add them to your contacts.
3. Their reply is ordinary email from then on.

The browser is sent to `public/contact/sent.html` or `not-sent.html`.

## Protections

- Visitors can't put their own message or links into the email. Only their
  name goes in the body: letters (any script), spaces and `. ' -` only, at most
  50 characters, and no `word.word` that mail apps would turn into a link. The
  subject is fixed.
- The email address must be one plain ASCII address with a real domain, so
  nobody can add recipients or headers.
- Limits:
  - 5 tries per visitor per hour (IPv6 visitors counted per /64 network).
  - 1 email per inbox per 30 days. `bob+1@gmail.com`, `b.ob@gmail.com` and
    `bob@googlemail.com` count as the same inbox.
  - 20 emails per day in total.
- The form only works when sent from your own site, so other websites can't
  make their visitors' browsers submit it.
- A hidden trap field (`hp_x7`): anything typed there is refused and logged.
- The rate-limit log stores addresses and IPs only as salted hashes, keeps
  tries for an hour and sends for 30 days, and refuses everything if it ever
  grows past 1 MB.
- Error logs never include visitor addresses.
- The program never holds an email password. It hands each email to the
  server's own mail program (`smtpd`), which relays it through SMTP2GO using
  the password in `/etc/mail/secrets` (readable only by root).
- Visitors are told apart by their real address, which `relayd` passes along
  in a header it sets itself (`PROXY` setting).

## Files

| File | What it is |
|---|---|
| `main.go` | The program |
| `lock_unix.go` | Stops two submissions changing the rate-limit log at once |
| `install.sh` | Installs or updates the program on the server (see below) |
| `main_test.go` | Tests |
| `visitor-email.txt` | The email visitors get. `{name}` is replaced with their name. Rebuild after editing. |
| `dev.conf` | Settings for testing on your computer (prints emails, never sends) |
| `contact.conf` | Real settings, only on the server at `/var/www/contact/contact.conf`. **Never commit it.** |

## Settings (`KEY = VALUE`, one per line)

| Key | Meaning |
|---|---|
| `FROM` | Sender, e.g. `Jackson Mercuri <jackson@yourdomain.com>`. Must be on your own domain, or mail lands in spam. |
| `REPLY_TO` | Optional. Where replies go, if not the `FROM` address. |
| `NOTIFY_TO` | Where your "new contact" notes go. |
| `LAST_NAME` | The answer to the human check. Not case-sensitive. Required. |
| `SMTP_ADDR` | The server's mail program. Default `127.0.0.1:25`. |
| `PROXY` | `127.0.0.1` on the server: trust `relayd`'s visitor-address header. |
| `DATA_DIR` | A folder the program can write its rate-limit log to. |
| `DRY_RUN` | `1` writes emails to `DATA_DIR/dry-run.txt` (on the server) instead of sending; `0` or blank sends. Anything else is an error. |

The program refuses to start if a setting is missing or misspelled. The `-dev`
test server only runs with `DRY_RUN = 1`, and only listens on your own computer.

## Testing on your computer

Windows' Smart App Control blocks Go on this PC, so Go runs inside WSL
(Ubuntu), installed at `~/.local/go`. From an Ubuntu terminal:

```bash
cd "/mnt/c/Users/Jackson Mercuri/projects/personal-website/contact"
~/.local/go/bin/go test ./...
~/.local/go/bin/go run . -dev :4174
```

Then open http://localhost:4174/contact/ and submit the form. The emails it
would send are printed in the terminal. In the Claude app this is the
`contact-dev` preview server.

## On the server

Installed on 2026-11-26 and running in test mode (`DRY_RUN = 1`) until
SMTP2GO approves the domain. The contact page shows an "opens soon" note
instead of the form until then.

| Where | What |
|---|---|
| `/var/www/cgi-bin/contact` | The program |
| `/var/www/contact/contact.conf` | Settings (root:www, 0640) |
| `/var/www/contact/data/` | Rate-limit log, `contact.log`, `dry-run.txt` (www only) |
| `/var/www/usr/lib/`, `/var/www/usr/libexec/ld.so` | System libraries the program needs |
| `/etc/httpd.conf` | `location "/cgi-bin/contact"` in the port 8080 server (backup: `/etc/httpd.conf.before-contact`) |

### Updating the program, or after `syspatch` / an OpenBSD upgrade

From WSL, in this folder:

```bash
GOOS=openbsd GOARCH=amd64 CGO_ENABLED=0 ~/.local/go/bin/go build -trimpath -o /tmp/contact-openbsd .
scp /tmp/contact-openbsd install.sh jackson@jacksonmercuri.com:
ssh jackson@jacksonmercuri.com 'mv contact-openbsd contact-new && doas sh install.sh ~/contact-new'
```

After an upgrade, just run the last line: it re-copies the system libraries.

### Going live once SMTP2GO approves the domain

1. Check plain email works first: send yourself something from `jackson@jacksonmercuri.com`.
2. On the server: `doas sed -i 's/^DRY_RUN = 1$/DRY_RUN = 0/' /var/www/contact/contact.conf`
   (optionally set `REPLY_TO` and `NOTIFY_TO` at the same time).
3. In `public/contact/index.html`, delete the "opens soon" note and the word
   `hidden` on the form, then upload the site.
4. Submit the form with an address you can check, and confirm the email arrives.

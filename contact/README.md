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
- Sending uses an SMTP2GO **send-only** API key. If the server were broken
  into, the worst case is someone sending email until you revoke the key; it
  can't read any mailbox.

## Files

| File | What it is |
|---|---|
| `main.go` | The program |
| `lock_unix.go` | Stops two submissions changing the rate-limit log at once |
| `main_test.go` | Tests |
| `visitor-email.txt` | The email visitors get. `{name}` is replaced with their name. Rebuild after editing. |
| `dev.conf` | Settings for testing on your computer (prints emails, never sends) |
| `contact.conf` | Real settings on the server. **Never commit it** (it is in `.gitignore`). |

## Settings (`KEY = VALUE`, one per line)

| Key | Meaning |
|---|---|
| `FROM` | Sender, e.g. `Jackson Mercuri <jackson@yourdomain.com>`. Must be on your own domain, or mail lands in spam. |
| `REPLY_TO` | Optional. Where replies go, if not the `FROM` address. |
| `NOTIFY_TO` | Where your "new contact" notes go. |
| `LAST_NAME` | The answer to the human check. Not case-sensitive. Required. |
| `SMTP2GO_API_KEY` | Send-only key from SMTP2GO. |
| `DATA_DIR` | A folder the program can write its rate-limit log to. |
| `DRY_RUN` | `1` prints emails instead of sending; `0` or blank sends. Anything else is an error. |

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

## On the server (not set up yet)

To be written once the server from https://sive.rs/ti is running. Known pieces:

- Build for OpenBSD: `GOOS=openbsd GOARCH=amd64 CGO_ENABLED=0 go build -o contact .`
  (or `pkg_add go` and build on the server itself).
- Copy the binary to `/var/www/cgi-bin/contact`, enable `slowcgi`, and add a
  `location "/cgi-bin/*"` block with `fastcgi` to `httpd.conf`.
- `httpd` runs programs locked inside `/var/www`, so everything the program
  needs must be copied in there:
  - **System libraries.** Go programs on OpenBSD use the system's C library,
    so the "static" build still needs `/usr/libexec/ld.so` and the libraries
    that `ldd contact` lists, copied to the same paths under `/var/www`.
    Re-copy them after every `syspatch` or OpenBSD upgrade.
  - `/var/www/etc/resolv.conf` and `/var/www/etc/ssl/cert.pem`, to reach
    SMTP2GO. Re-copy `cert.pem` after upgrades too.
  - `/var/www/contact/contact.conf`: owned by `root`, group `www`, mode `0640`,
    in a folder `www` can't write to.
  - `/var/www/contact/data/`: owned by `www`, mode `0700`.
  - Nothing under `/var/www/contact` may be inside the website's `htdocs`.
- In SMTP2GO, verify your domain and create an API key limited to sending.
- Test with `DRY_RUN = 1` first: the emails then go to httpd's error log.

# Personal website

Jackson Mercuri's personal site: static HTML, CSS and a little JavaScript. See
`README.md` for the file layout, local preview and server upload steps.

## How to work here

- **Build in small previews.** Make one small stage, show it in the preview, and
  wait for Jackson's reaction before the next. Do not build large pieces in one pass.
- **Jackson writes all site text.** Use Lorem Ipsum for anything new. The only real
  content so far is the companies, titles and years on the experience cards.
- **Do not use or reference the `personal-website-design` skill.**
- **Commit and push only when asked.** Work goes straight to `main`.

## Hard rules

- No framework, no build step, no package manager. Pages are plain files that can
  be edited by hand.
- Nothing loads from another server: no CDN fonts, scripts, images or analytics.
  Lato is self-hosted in `public/fonts/`.
- Only `public/` is the site. `pre-build-files/` holds the resume (phone number,
  email) and is gitignored — never copy contact details from it into `public/`,
  and check for them before committing.
- Use document-relative links (`../css/style.css` from `projects/` and `writing/`),
  so pages work both on the server and when opened directly.
- The site will be served by OpenBSD `httpd` from `/var/www/htdocs`
  (https://sive.rs/ti), so everything must work as static files.

## Structure conventions

- The header, nav and footer are repeated in every HTML file. A nav change must be
  made in all pages. Nav order: Projects, Writing, Interests.
- The current page's nav link gets `aria-current="page"`. On the home page the
  site name carries it instead; elsewhere the name is muted grey.
- New projects and posts are copies of `projects/example.html` and
  `writing/example.html`, added to the matching `index.html` list.
- External links open in a new tab with `rel="noopener noreferrer"` and a
  visually hidden "(opens in new tab)"; internal links do not.

## Design conventions

- One centered 640px column, text left-aligned.
- Headings are the same size as body text and stand out by weight and color only.
- Colors are CSS variables at the top of `public/css/style.css`: a warm off-white
  light theme and a warm off-black dark theme, following `prefers-color-scheme`.
  No theme toggle. Avoid pure black, pure white and brown.
- `--muted` text must keep at least 4.5:1 contrast against the page in both themes.
- Icons and arrows are inline SVG, not text characters or icon fonts.
- Motion: `.page` fades in over 0.9s; `.glow` is the cursor light (375px radius,
  deliberately faint). Keep the fade-in off `<body>` and off any ancestor of
  `.glow`, or the light drifts when scrolling. Both are off under
  `prefers-reduced-motion`, and the light ignores touch input.
- Hover effects go inside `@media (hover: hover)` so they don't stick on phones.

## Contact form program

- `contact/` (repo root, not `public/`) is a Go CGI program for the contact form;
  see `contact/README.md`. It runs under OpenBSD `httpd` + `slowcgi` and sends
  through SMTP2GO with a send-only key. The form posts to `/cgi-bin/contact`
  (root-relative on purpose).
- Jackson's email address must never appear in `public/`.
- Windows Smart App Control blocks Go on this PC. Do not change or work around
  that setting; run Go inside WSL Ubuntu at `~/.local/go/bin/go`. From Git Bash:
  `MSYS_NO_PATHCONV=1 wsl.exe -d Ubuntu -- bash -c 'cd "/mnt/c/Users/Jackson Mercuri/projects/personal-website/contact" && GOFLAGS=-buildvcs=false ~/.local/go/bin/go test ./...'`
- `contact/contact.conf` holds real settings and the API key: never commit it.

## Preview

`.claude/launch.json` defines a `site` server (Python `http.server` on port 4173
serving `public/`) and `contact-dev` (port 4174: the site plus a working form,
emails printed to the server log instead of sent). The browser pane caches the stylesheet; hard refresh after
CSS changes. Check changes in dark, light and at phone width.

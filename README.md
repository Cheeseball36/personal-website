# Personal website

Jackson Mercuri's personal site. Plain HTML, CSS and a few lines of JavaScript —
no framework and no build step. Nothing is loaded from other servers; the Lato
font files live in `public/fonts/`.

## Layout

```
public/                  everything that goes on the server
  index.html             home: intro, experience cards, links
  projects/index.html    project list
  projects/example.html  one project page — copy it for each project
  writing/index.html     post list
  writing/example.html   one post — copy it for each post
  interests.html
  css/style.css          all styles; colors are the variables at the top
  js/site.js             the light that follows the cursor
  fonts/                 Lato (SIL Open Font License, see OFL.txt)
  contact/               contact form, plus the sent / not-sent pages
contact/                 the program behind the form (Go); not uploaded with the site
brand/                   JM monogram files
```

The header and footer are repeated in every page, so a change to the nav has to
be made in each HTML file. Pages inside `projects/` and `writing/` reach shared
files with `../` (for example `../css/style.css`).

## Preview locally

```bash
python -m http.server 4173 --directory public
```

Then open http://localhost:4173.

## Upload to the server

The site is static, so publishing is copying `public/` into the web server's
folder (`/var/www/htdocs` on OpenBSD `httpd`, as in https://sive.rs/ti):

```bash
rsync -avz --delete public/ USER@YOUR-SERVER:/var/www/htdocs/
```

Replace `USER@YOUR-SERVER`. `--delete` removes files on the server that are no
longer in `public/`. Windows has no `rsync` by default; run it from WSL, or use
`scp -r public/* USER@YOUR-SERVER:/var/www/htdocs/` instead.

Only `public/` is ever uploaded. `pre-build-files/` (resume) is ignored by git
and must stay off the server.

### Server note: font file type

`httpd` should label `.woff2` files as fonts. If `httpd.conf` does not already
load the system list of file types, add this inside it:

```
types { include "/usr/share/misc/mime.types" }
```

Then check the result with `curl -I https://YOUR-DOMAIN/fonts/lato-400-normal.woff2`
and look for `Content-Type: font/woff2`.

## Not done yet

- All text is Lorem Ipsum placeholder.
- The contact form works locally but isn't set up on the server yet (see `contact/README.md`).

## Monogram

`brand/monogram.svg` is the square JM mark (scales to any size) and
`brand/monogram-512.png` is the same mark as a picture for profile photos.
The site's own icon files are `public/favicon.svg`, `public/favicon.ico` and
`public/apple-touch-icon.png`.

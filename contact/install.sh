#!/bin/sh
# Install or update the contact form program on the OpenBSD server.
#
#   doas sh install.sh /home/jackson/contact-new [/home/jackson/contact.conf]
#
# Safe to run again: after a new build, and after every `syspatch` or
# OpenBSD upgrade (the program needs fresh copies of the system libraries
# inside httpd's /var/www chroot).
set -eu

BIN=${1:?usage: install.sh BINARY [CONFIG]}
CONF=${2:-}
CHROOT=/var/www

# 1. The program itself.
install -o root -g bin -m 0555 "$BIN" "$CHROOT/cgi-bin/contact"
echo "installed $CHROOT/cgi-bin/contact"

# 2. The system libraries it loads (Go on OpenBSD uses libc), copied into
#    the chroot at the same paths.
for lib in $(ldd "$BIN" | awk '$NF ~ /^\/usr\// { print $NF }'); do
	install -d -o root -g bin -m 0755 "$CHROOT$(dirname "$lib")"
	install -o root -g bin -m 0555 "$lib" "$CHROOT$lib"
	echo "copied $lib"
done

# 3. Settings (readable by www, writable only by root) and a private data folder.
install -d -o root -g www -m 0750 "$CHROOT/contact"
install -d -o www -g www -m 0700 "$CHROOT/contact/data"
if [ -n "$CONF" ]; then
	install -o root -g www -m 0640 "$CONF" "$CHROOT/contact/contact.conf"
	echo "installed settings to $CHROOT/contact/contact.conf"
fi
[ -f "$CHROOT/contact/contact.conf" ] || { echo "missing $CHROOT/contact/contact.conf" >&2; exit 1; }

# 4. slowcgi runs CGI programs for httpd.
rcctl enable slowcgi
rcctl check slowcgi >/dev/null || rcctl start slowcgi

# 5. Tell httpd about the program (once), inside the server block that
#    relayd forwards to. The old settings are kept, and restored if the new
#    ones don't pass httpd's own check.
if ! grep -q '"/cgi-bin/contact"' /etc/httpd.conf; then
	backup=/etc/httpd.conf.before-contact
	cp -p /etc/httpd.conf "$backup"
	awk '
		{ print }
		/default type text\/html/ && !done {
			print "  location \"/cgi-bin/contact\" {"
			print "    fastcgi"
			print "    root \"/\""
			print "  }"
			done = 1
		}
	' "$backup" > /etc/httpd.conf
	if ! httpd -n; then
		cp -p "$backup" /etc/httpd.conf
		echo "httpd.conf check failed; old settings restored" >&2
		exit 1
	fi
	echo "added /cgi-bin/contact to /etc/httpd.conf (backup: $backup)"
fi
httpd -n
rcctl reload httpd
echo "done"

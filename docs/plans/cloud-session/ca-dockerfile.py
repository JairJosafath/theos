# Writes a copy of a Dockerfile in which every stage trusts the session
# proxy's CA (named build context "ccr") and Debian's apt uses HTTPS, which
# the proxy serves; plain HTTP it does not.
import re, sys
src, out = sys.argv[1], sys.argv[2]
lines = open(src).read().split('\n')
res = []
for l in lines:
    res.append(l)
    if re.match(r'^FROM\s', l, re.I):
        res += ['COPY --from=ccr ca-bundle.crt /etc/ssl/certs/ca-certificates.crt',
                'COPY --from=ccr ca-bundle.crt /usr/local/share/ca-certificates/ccr-bundle.crt',
                'ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt NODE_EXTRA_CA_CERTS=/usr/local/share/ca-certificates/ccr-bundle.crt']
        if 'distroless' not in l and 'scratch' not in l and not l.split()[1].startswith(('${UV_IMAGE}', 'ghcr.io/astral-sh/uv')):
            res.append('RUN for f in /etc/apt/sources.list.d/debian.sources /etc/apt/sources.list; do [ -f "$f" ] && sed -i "s#http://deb.debian.org#https://deb.debian.org#g" "$f"; done; true')
open(out, 'w').write('\n'.join(res))

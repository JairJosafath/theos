# Security policy

## Reporting a vulnerability

Don't open an issue. While the repository is private, report to the owner,
@JairJosafath ([MAINTAINERS.md](MAINTAINERS.md)), privately. Once it is public,
use GitHub's private vulnerability reporting (*Security* → *Report a
vulnerability*).

Include the version (`vX.Y.Z`, or the commit), what an attacker gains, and how
to reproduce it.

## What happens

- You get an acknowledgement within 3 business days, and never later than 14
  days ([OpenSSF Best Practices](https://www.bestpractices.dev/en/criteria/0)).
- You get an estimate for the fix within 10 business days.
- The fix lands on main and ships as a patch release
  ([SemVer](https://semver.org/)). The release notes list it, with a GitHub
  security advisory, and a CVE when one is warranted.

Security reports are reserved to the owner ([GOVERNANCE.md](GOVERNANCE.md)).
Whoever finds one in an issue, a person or an agent, takes the details out of
the issue and sends it to the owner.

## Supported versions

Only the newest release. The project is pre-1.0 (`0.y.z`).

## Leaked credentials

If a credential turns up in the repository, a pull request or an issue,
rotate it first; the owner holds the accounts. Then remove it. The
[`secrets` skill](.claude/skills/secrets/SKILL.md) has the rules for handling
credentials.

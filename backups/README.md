# Release backups

Source code of every release (git tag) of the apps the factory builds, kept in
this repo so that pushing it to GitHub backs them up. Only the `cel-lens`
project is covered for now.

## Layout

```
backups/<gitea org>/<repo>/<repo>-<tag>.zip
backups/<gitea org>/<repo>/SHA256SUMS
```

The zips are Gitea's own tag archives (`GET /api/v1/repos/{org}/{repo}/archive/{tag}.zip`),
so they hold the source tree at that tag, without Git history. Verify with
`cd backups/<org>/<repo> && sha256sum -c SHA256SUMS`.

## Running it

After creating a release tag in Gitea:

```
make backup-releases            # PROJECT=cel-lens, commits backups/ and pushes
make backup-releases PUSH=0     # commit only
make backup-releases PROJECT=other-org
```

It needs the Kind cluster: it reads the Gitea admin password, and uses
`make gitea-forward` if that is running, otherwise its own port-forward. Tags
already backed up are skipped, since tags never move. It commits only
`backups/`, and only when something is new.

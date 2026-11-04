# Starting the self-hosting plan: what the owner does

The plan is [../self-hosting.md](../self-hosting.md). You start it once, on
the device that runs the relay and the Kind clusters. After that, the only
thing that waits for you is a change to the fence (the plan's rule 5).

## 1. Check the device

These are the things the plan uses and can't make itself:
- **Claude Code logged in at `~/.claude`.** That is the relay's login, used by
  step 0, the relay and the verifier sessions.
- **The agents' token at `.dev/secrets/claude-token`** (mode 600). Step 8
  stores it in the cluster.
- **A GitHub token from the git credential helper.** `git push` from the
  checkout works without a prompt.
- **Docker running, plus Kind, kubectl and Helm** (`./dev setup`).
- **systemd in WSL, with your user's manager in the `docker` group**
  ([devices.md](../../dev/devices.md), "The relay watch as a service").

The script checks all of these, changes nothing, and says how to fix what's
missing:

```bash
cd ~/theos
git switch main && git pull --ff-only origin main
docs/plans/self-hosting/start.sh --check
```

## 2. Start it

From the same plain shell:

```bash
docs/plans/self-hosting/start.sh
```

It checks again, then:
1. **Runs step 0.** A headless session, a few minutes long, files the plan's
   parent issue and its 10 steps, readies the relay's 7 and checks the relay
   runs.
2. **Posts your `binding` comment** on the parent, from your account.
3. **Installs the verifier watch**, the systemd user timer
   `theos-verifier`, which starts steps 8 to 10 when they're due.

It prints the parent's link at the end. Running it again is safe; it skips
what is done.

## 3. Leave it

Keep the PC on, with WSL and Docker Desktop running, as the relay already
needs. The order:
1. The relay builds steps 1 to 7.
2. The watch runs the cut-over, which moves the repository to Gitea in
   cluster `theos-self` and retires the relay.
3. The factory works its own issues.
4. The watch runs one promotion drill, closes the plan and turns itself off.

**Where to look, whenever you choose:**
- the parent issue on GitHub; every step comments what it did;
- `.dev/self-hosting-verifier.log` on the device;
- after the cut-over, the factory's UI: its needs-you list and the Crew's
  Tasks.

**What may still reach you:**
- **A `Fence:` issue in Gitea.** That is work that needs a protected path
  changed; it waits for you by design.
- **A step at `status/needs-maintainer`** after 48 hourly tries. Its comments
  say what stopped it. Often this is a command auto mode refused, which you
  can run yourself.
- **Docker Desktop losing its data disk.** Restart it, as today
  ([devices.md](../../dev/devices.md), "The keeper").

## Stopping it

```bash
systemctl --user disable --now theos-verifier.timer   # the verifier watch
hack/relay-watch.sh --uninstall                           # the relay, before the cut-over
```

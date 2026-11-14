# Plan: giving a value your own type becomes an experimental feature

The owner, 13 November 2026, in the session: "Add the feature of setting
your own types as experimental and also include a gif in the readme."

## Goal

Giving a `dyn` value a type of your own (**Give it a type** and the
`.cellens` file that holds the types) is an experimental feature, listed with
the others in the README and turned on through `cel-lens.experimental`. The
README shows it with a GIF.

## Where things stand

- The feature was released in engine v0.28.0 and the extension's v0.36.0. It
  is on for everyone, and the README's two feature tables (*Stable* and
  *Experimental*) list it in neither.
- The engine knows three experimental features (`pkg/feature`): `kyverno`,
  `crd-typed-policies` and `fitting-completion`. The extension sends the
  setting `cel-lens.experimental`; the engine ignores an unknown name and
  reports it once.
- `.cellens` touches the line above the rule (the **Give it a type** and
  **Edit** parts, `params: {…}` typed from it), the checks, hover and
  completion of `.cellens` itself, the example results (a `.cellens` type
  types `params`), *Examples in the workspace*, *Use a Go type*, and the
  command **cel-lens: Give a Value a Type…**.
- The README's top has `images/give-type.gif`. The section *Giving a value a
  type* has six pictures still to take, as comments.

## Decisions

1. **Its name is `cellens-types`.** It names the file people commit, which
   is what turning it on or off decides. Rejected: `own-types` (vague) and
   `give-type` (names one command, not the types read from the file).
2. **Off means the engine acts as if no `.cellens` existed,** as
   `crd-typed-policies` off leaves its types `dyn`:
   - no `.cellens` is read or searched for;
   - the line above a rule shows no **Give it a type** or **Edit**;
   - a value `.cellens` would type is `dyn`, and its hover says the
     `cellens-types` feature is off;
   - an open `.cellens` gets no diagnostics, hover or completion;
   - the example results run with those values as `dyn`.

   No engine path checks the feature twice: one place answers whether
   `.cellens` is read, and everything above asks it.
3. **People who already use it are told, once.** Off, when the workspace
   holds a `.cellens`, the engine says so once per session, in a
   notification with a button that adds `cellens-types` to the setting at the
   scope that holds `cel-lens.experimental`. The reason:
   - turning it off is a change in behaviour for anyone who committed a
     `.cellens` since v0.36.0;
   - a feature that silently stops is worse than one that asks.
4. **Turning it on or off takes effect without a reload,** as the other
   features do: open documents are checked again, and the `.cellens` search
   runs when it turns on.
5. **The command still exists when off.** **cel-lens: Give a Value a Type…**
   says the feature is off and offers the same button, rather than vanishing
   from the palette where people look for it.
6. **The README moves it to the experimental table** with why it is
   experimental: the types are yours, and nothing checks them against the
   cluster. The *Giving a value a type* section opens with how to turn it on.
7. **The GIF is `images/cellens-types.gif`, at the top of *Giving a value a
   type*.** It takes the place of the comment for `give-type-schema.png`. It
   shows the whole loop in a dark theme:
   - `params: dyn · Give it a type` above a rule;
   - **Write a schema in .cellens…** picked;
   - `.cellens` opening beside the rule;
   - the schema typed with completion;
   - the line above the rule following to `params: {maxReplicas} · Edit`.

   It is taken by `npm run pictures` with the same lossless encoder as the
   other GIFs. The top GIF, `give-type.gif`, stays: it shows the picker of
   examples, this one shows `.cellens`.
8. **Released on its own, before the rule list.**
   - Engine v0.32.0 from the released v0.31.0, and the extension's v0.40.0.
   - The rule list for cel-lens#2 moves to engine v0.33.0 and the extension's
     v0.41.0.
   - Why: the owner asked for this now, and the rule list has two engine
     steps left.

## Steps

| Step | Repository | What |
| --- | --- | --- |
| 1 | cel-lens-core | `cellens-types`: the feature, off by default, decisions 2 to 4; Prepare v0.32.0 in the same pull request |
| 2 | GitHub | engine v0.32.0 through `release.yml` |
| 3 | cel-lens-vscode | Pin v0.32.0; the setting's list, the command when off, the README and the GIF; Prepare v0.40.0 |
| 4 | GitHub | the extension's v0.40.0 through `release.yml` |

## Status

- **13 November, evening.** Planned. Nothing built.
- **14 November, 02:20 UTC. Steps 1 and 2 done.** cel-lens-core#46 (issue
  #44) was approved in one round; its five smaller findings were fixed
  before publishing, among them a notice sent after the person had turned
  the feature off. It merged as JairJosafath/cel-lens#29 (02eaf25), and
  `release.yml` published
  [cel-lens core v0.32.0](https://github.com/JairJosafath/cel-lens/releases/tag/core-v0.32.0).
  Checked here: `checksums.txt`'s signature and the Linux binary's
  provenance verify with cosign, the binary matches `checksums.txt`, and
  its `initialize` lists `cellens-types` among the experimental features.
  Decision from the review: with the feature off, the engine still walks
  the folders once, reading no `.cellens`, to notice one and say so; the
  issue asked both for no search and for the notice, and only this gives
  both. Step 3 (cel-lens-vscode#29, with the GIF) is being built.
- **14 November, 03:15 UTC. Done.** cel-lens-vscode#31 (issue #29, two
  review rounds: the README's new row linked a page that exists only in
  the engine's folder, and the engine notice's action could have written
  any name into the setting; both fixed, with a test that every relative
  README link names a file) merged as JairJosafath/cel-lens#30 (0e0eda8),
  and `release.yml` published
  [cel-lens for VS Code v0.40.0](https://github.com/JairJosafath/cel-lens/releases/tag/vscode-v0.40.0).
  Checked here: the Linux `.vsix`'s signature and provenance verify with
  cosign, its engine is v0.32.0 with the pinned hash, its `package.json`
  offers `cellens-types`, and the README's GIF answers on `main`. The
  pins were compared with the `checksums.txt` verified here before
  publishing. Not tried in a real VS Code: the engine's notice and its
  button (tested against the stand-in); the GIF shows the rest of the
  loop in a real one. The rule list (cel-lens#2) follows as engine
  v0.33.0 and the extension's v0.41.0.

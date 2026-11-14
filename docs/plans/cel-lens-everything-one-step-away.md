# Plan: every cel-lens feature one step away, and one command from off

## Request

JairJosafath/cel-lens#3, the owner's, of 9 November 2026: "Every cel-lens
feature one step away from where I am, and one command from off." Written
on 14 November by the session the owner ran ("Continue work via theos on
cel-lens"), after [cel-lens-rule-view.md](cel-lens-rule-view.md) closed #2,
from JairJosafath/cel-lens `main` at 3b3af71 (engine v0.33.0, extension
v0.41.0).

The request's list, in its words:

- each thing cel-lens draws in my editor (the colours, and each kind of
  inline result as they arrive) has a **Toggle** command and a setting,
  also in the status bar menu, taking effect at once, without a reload;
- right-clicking in YAML with CEL gives a cel-lens submenu with what
  applies there, and nothing in files where cel-lens does nothing;
- the status bar item's menu is the hub: the Kubernetes version, the
  colour style, the toggles, the logs, restarting the engine; each entry
  shows its current value and has an icon only where it helps;
- a keyboard shortcut for the toggles I'd use often, that doesn't take one
  VS Code or a common extension already uses, listed in the README;
- a short *Get started with cel-lens* walkthrough on VS Code's Welcome
  page: open a sample CRD, see a problem and fix it, pick the Kubernetes
  version, choose the colours; each step one action; cel-lens opens
  nothing by itself on install or update;
- the README lists every command with its shortcut, and every setting, in
  the order a person meets them;
- everything reads well in light, dark and high-contrast themes, and
  nothing relies on colour alone.

"Features I can't find don't exist for me, and annotations I can't turn off
make me uninstall an extension."

## Goal

A person who installs cel-lens can find everything it does from where they
are: the Command Palette, a *cel-lens* submenu when they right-click in YAML
with CEL, the status bar item's menu, and a *Get started with cel-lens*
walkthrough on VS Code's Welcome page. Everything cel-lens draws in the
editor has a **Toggle** command, a setting and an entry in the status bar
menu, and goes away at once when turned off. The line above each rule has a
shortcut. Light, dark and both high-contrast themes read well, and nothing
says anything by colour alone.

Done when the request's own list holds on a released extension, the README
lists every command with its shortcut and every setting in the order a
person meets them, and the theme pictures are saved as evidence.

## Where things stand

From `vscode/package.json` and `vscode/src/` at 3b3af71.

- **What the extension draws in the editor**:
  - *The colours.* `ColorPainter` (`color_painter.ts`) paints the parts of
    CEL as text decorations, with the palettes of `cel_styles.ts` for each
    theme kind (light, dark, `hcDark`, `hcLight`). The setting is
    `cel-lens.colorStyle` (`cel`, `classic`, `off`; resource scope), picked
    with **Choose Color Style** (`chooseColorStyle`) or the status bar menu.
    It repaints at once when the setting or the theme changes. There is no
    *Toggle* command: off is one of three choices in a picker.
  - *The line above each rule* (a CodeLens line from the engine,
    `internal/inline`). Its parts, in the order `inline.Line` documents:
    1. the rule's state, `self: Widget.spec · compiles` (or `2 problems`,
       `not checked: schema error`), which runs `cel-lens.showRule`;
    2. the values it reads as `dyn` or with a type from `.cellens`
       (`params: dyn · Give it a type`, `…: {maxReplicas} · Edit`,
       `Remove`, `variables.limit: int`, `+N more`), sent only while the
       experimental feature `cellens-types` is on (`inlineValues` in
       `internal/lsp/codelens.go`);
    3. what the rule does to its examples (`3 examples pass`,
       `refuses 1 example it should allow · 2 pass`), which arrives later,
       from the background check of the examples.

    `cel-lens.inlineInfo` and **Toggle Inline Info** turn the whole line
    off (the engine sends none). `cel-lens.exampleResults` and **Toggle
    Example Results** turn off part 3, the examples' section of the hover
    and the wrong verdicts' warnings (the engine stops checking examples;
    a folder that turns it off is filtered in the client by
    `withoutExampleResults`). Part 2 has no toggle of its own: only taking
    `cellens-types` out of `cel-lens.experimental`, which also stops the
    types from applying.
  - *Problems, hover, completion and quick fixes* come from the engine and
    show only on a problem or when asked.
- **Toggles today.** Both toggles go through `toggleBooleanSetting`
  (`toggle_setting.ts`): they flip the value in force for the active file,
  writing the narrowest scope that sets it (folder, workspace, user), and on
  a failed write show one error with *Open Settings*. Both take effect at
  once: `sendLineSettings` tells the engine and fires the CodeLens refresh;
  e2e covers `inlineInfo`.
- **The commands** (21). On a YAML file the palette offers eight: *Restart
  Engine*, *Pick Kubernetes Version*, *Show Actions*, *Show Logs* (on any
  file), *Choose Color Style*, *Toggle Inline Info*, *Toggle Example
  Results*, *Give a Value a Type…*; *Use This Example as the Type* only on
  a pasted example's editor (`cel-lens-example:`), where it is also the one
  editor-title icon. Twelve are hidden from the palette (`when: false`):
  the line's parts (`showRule`, `showExampleResults`, `listValues`,
  `giveType`, `editType`, `removeType`) and the rule view's
  (`rules.showProblems`, `rules.showExampleResults` on a row;
  `rules.showProblemsOnly`, `rules.showAll`, `rules.followEditor`,
  `rules.stopFollowing` in its title bar). `showRule` is titled *Show Rule*
  though the view calls the same action *Show Problems*.
- **Menus.** `commandPalette`, `editor/title` (the pasted example only),
  `view/title`, `view/item/context`. No `editor/context`, no `submenus`, no
  `keybindings`, no `walkthroughs`.
- **The status bar item** (`version_status.ts`) shows `CEL · K8s 1.36` on a
  YAML file while the engine runs, and `$(error) CEL · Engine down` on any
  file when it failed or crashed; it sets no `accessibilityInformation`. Its
  menu (`actions_menu.ts`, **Show Actions**) is one flat quick pick, in this
  order: *Color style* (`CEL`), *Kubernetes version* (`1.36`), *Inline info:
  on*, *Example results: on*, *Restart Engine*, *Show Logs*; with the engine
  down, *Restart Engine* and *Show Logs* come first. The value is in the
  description for two entries and in the label for the two toggles; there
  are no separators and no icons. Running an entry closes the menu.
- **Settings** (8), by `order`: `colorStyle` 0, `inlineInfo` 1,
  `exampleResults` 2, `kubernetesVersion` 3, `trace.server` 4, `serverPath`
  5, `experimental` 6, `ruleView.followEditor` 7. The README's table has
  another order (`ruleView.followEditor` fourth). Each applies live.
- **The rule view** has its filter and following (the setting
  `cel-lens.ruleView.followEditor`, flipped by *Follow Editor* / *Stop
  Following* in its title bar); its context keys are
  `cel-lens.ruleView.state`, `…problemsOnly`, `…followEditor` and
  `…kyvernoOff`. The extension holds the engine's list of every file with
  rules (`rule_list.ts`, `cel-lens.ruleList` from engine v0.33.0), open
  files in no folder included, and finds the rule at a position
  (`rule_at_cursor.ts`). No context key says whether the active editor has
  rules.
- **On activation** the extension starts the engine and opens nothing. The
  only notifications it can show unasked are the offer to move a pre-0.3.0
  `celLens.colorStyle`, and the once-a-session notice for a workspace with
  a `.cellens` while `cellens-types` is off.
- **The engine reads files on disk only** (`documentSelector`
  `{scheme: 'file', language: 'yaml'}`): an untitled buffer gets nothing,
  so a sample CRD must be a file.
- **Themes.** `cel_styles.test.ts` holds every colour of both styles to
  4.5:1 against `#FFFFFF` and `#1E1E1E` and 7:1 against the high-contrast
  backgrounds; `#1E1E1E` is Dark+, not Dark Modern (`#1F1F1F`), VS Code's
  default dark theme since 1.78. The rule view's icons are product icons in
  theme colours, each state its own shape, and its rows say the state in
  words and in `accessibilityInformation`. `npm run pictures` takes every
  picture in Default Dark Modern only; the light and high-contrast themes
  were never tried (the rule-view plan's status says so). The agents'
  sandbox lacks Chromium's system libraries, so pictures are the session's
  step (`vscode/docs/project-notes.md`).
- **Against the request's list:**
  - *A Toggle and a setting for each thing drawn, also in the menu, live:*
    done for the line and example results; missing for the colours (no
    Toggle) and for the value parts (no toggle, no setting).
  - *A cel-lens submenu in YAML with CEL, nothing elsewhere:* missing.
  - *The menu as the hub, in its order, each entry with its value, icons
    where they help:* exists in another order, values in two places, the
    colours toggle and the value parts missing.
  - *A shortcut:* missing.
  - *A walkthrough, opening nothing by itself:* missing; nothing opens today.
  - *The README's commands with shortcuts and settings, in the order met:*
    the tables exist, without shortcuts, in an order nobody meets them in.
  - *Light, dark, high contrast; nothing by colour alone:* the palettes are
    tested; nothing was looked at in light or high contrast.

## Decisions

1. **Four things are drawn in the editor, and each gets a toggle: the
   colours, the line, its example results, its value types.** "Drawn" is
   what cel-lens adds to the text without being asked: the colours, and
   the line above each rule with its two later kinds of parts (the value
   parts and the example part), each of which a person may want without
   the other. Problems (squiggles), hover, completion and quick fixes are
   not drawn in that sense: they show only where a rule is wrong or when
   asked, they are what the extension is for, and VS Code already hides
   squiggles (`problems.visibility`) and turns the extension off. The rule
   view and the status bar item are not in the editor: VS Code hides a view
   or a status bar item from its own context menu.

   Rejected: a toggle for problems (turning off the checker is disabling
   the extension, which VS Code does); one toggle for the line's state part
   apart from the line (the line without its first part has no subject).

2. **The names follow `inlineInfo` and `exampleResults`: the setting is the
   thing, the command is *Toggle* and the thing.**

   | Thing | Setting | Command | Menu entry |
   | --- | --- | --- | --- |
   | the colours | `cel-lens.colorStyle` (exists; `off` is off) | `cel-lens.toggleColors`, *Toggle Colors* | *Colors* `on`/`off` |
   | the line | `cel-lens.inlineInfo` (exists) | `cel-lens.toggleInlineInfo` (exists) | *Inline info* |
   | example results | `cel-lens.exampleResults` (exists) | `cel-lens.toggleExampleResults` (exists) | *Example results* |
   | value types | `cel-lens.valueTypes` (new, boolean, default `true`, resource scope) | `cel-lens.toggleValueTypes`, *Toggle Value Types* | *Value types* |

   "Value types" is the word the commands already use (*Give a Value a
   Type…*, *Edit the Value's Type*). The colours keep one setting:
   *Toggle Colors* writes `off`, and on again writes the style this toggle
   last turned off (kept in `globalState`), else `cel`, at the scope the
   other toggles write. Every toggle writes as `toggleBooleanSetting`
   does, and every one takes effect at once.

   Rejected: a boolean `cel-lens.colors` beside `colorStyle` (two settings
   that can disagree: `colors: true` with `colorStyle: off`);
   `cel-lens.inlineTypes` or `cel-lens.dynValues` (new words for an idea
   the commands already name).

3. **Value types off is the client's filter, so the types still apply.**
   With `cel-lens.valueTypes` off for a file, the CodeLens middleware that
   already drops the example part (`withoutExampleResults`) keeps, of each
   line, its first lens and the example lens, and drops the lenses between
   them: the value parts and `+N more`, by the order `inline.Line`
   documents and an e2e case holds. The `.cellens` types still type the
   rule, its hover and its completion, and *Give a Value a Type…* still
   works; only the parts are hidden. The engine fitted the example part's
   words to the whole line, so it may stay in its short form (`3 pass`)
   with room to spare; accepted, since a line refits when the engine sends
   it again. The palette entry and the menu entry show only while
   `cellens-types` is on (a context key, `cel-lens.cellensTypes`), since
   there is nothing to hide otherwise.

   Rejected: an engine setting (a release of the engine for something the
   client can hide); hiding by command name (the inferred part runs
   `showRule`, as the first part does).

4. **The status bar menu is the hub, in the request's order, the value in
   the description, icons only on the two engine entries.**

   | Label | Description | Runs |
   | --- | --- | --- |
   | *Kubernetes version* | `1.36` (or the engine's state) | *Pick Kubernetes Version* |
   | *Color style* | `CEL`, `Classic`, `Off` | *Choose Color Style* |
   | separator *In the editor* | | |
   | *Colors* | `on`/`off` | *Toggle Colors* |
   | *Inline info* | `on`/`off` | *Toggle Inline Info* |
   | *Example results* | `on`/`off` | *Toggle Example Results* |
   | *Value types* (while `cellens-types` is on) | `on`/`off` | *Toggle Value Types* |
   | separator *Engine* | | |
   | `$(output)` *Show Logs* | | *Show Logs* |
   | `$(debug-restart)` *Restart Engine* | | *Restart Engine* |

   The values are for the active file's folder, as the toggles flip them.
   A quick pick shows the current value in `description` (the style
   guide), so *Inline info: on* becomes *Inline info* with `on`. The two
   engine entries get icons because the output and restart icons are the
   ones VS Code uses for the same actions; a settings entry's value is a
   word, and an icon beside it would be a second sign of the same thing.
   With the engine down, the *Engine* group comes first, as it does today.
   Running an entry closes the menu, as now.

   Rejected: icons on every entry (noise, and an on/off icon says what the
   word says); keeping the menu open after a toggle (VS Code's quick pick
   has no way to update an item in place without rebuilding it, and one
   click reopens it).

5. **The editor gets one *cel-lens* submenu, shown only in a YAML file
   the engine lists rules for; the extension sets the key from the list it
   already holds.** `contributes.submenus` has `cel-lens.editor`
   (label *cel-lens*), in `editor/context` (group `1_cel-lens`, after
   VS Code's navigation group) and in the editor title's overflow menu
   (`editor/title`, not `navigation`, so no icon), both with
   `editorLangId == yaml && cel-lens.editorHasRules`. It holds, by group:
   - *at the rule*, while `cel-lens.ruleAtCursor` is set: *Show Problems*
     (`cel-lens.showRule`, retitled from *Show Rule* to the view's word),
     *Show Example Results* (only when the rule has example words: the key
     is `ruleWithExamples`), *Reveal in Rule View* (new,
     `cel-lens.rules.reveal`), and *Give a Value a Type…* while
     `cellens-types` is on;
   - *toggles*: *Toggle Colors*, *Toggle Inline Info*, *Toggle Example
     Results*, *Toggle Value Types* (while `cellens-types` is on);
   - *settings*: *Pick Kubernetes Version*, *Choose Color Style*.

   *Show Logs* and *Restart Engine* stay in the status bar menu: they are
   about the engine, not the place. `cel-lens.editorHasRules` is true when
   the active editor's file is in the engine's list
   (`cel-lens.ruleList`), set on an editor change and on each answer;
   `cel-lens.ruleAtCursor` is `rule`, `ruleWithExamples` or unset, from
   `ruleAtCursor` on each selection change, set only when it changes. The
   commands look the rule up again when run, so a key that lags never runs
   the wrong rule: with no rule at the cursor, they say so in the status
   bar for three seconds. Run from a menu, `showRule` and
   `showExampleResults` get a URI, not a location, and use the rule at the
   cursor.

   **No context key from the engine.** The engine already says which files
   have rules, through the list the view asks for; a second message would
   repeat it. The language id alone is not enough: a GitHub workflow, a
   Compose file or a Helm values file is YAML where cel-lens does nothing,
   and the request says nothing shows there. Known limit: a file just
   opened shows the submenu once the engine's list has it (it follows the
   open within a second), and with an engine that cannot list rules or is
   down the submenu does not show; the status bar menu still does.

   Rejected: the language id alone (above); the client's own CEL lexer
   (`cel_ranges.ts`), a second opinion that would disagree with the engine
   on the kinds it does not check, such as a `v1beta1` CRD; an icon in the
   editor title (an icon on every CRD's tab bar for an action the line and
   the status bar already offer; the overflow menu costs nothing);
   `toggled` checkmarks in the submenu (the menu entries say *Toggle*; the
   values are the hub's job).

6. **Every command a person can run is in the palette.** New in the
   palette: *Get Started* (on any file), *Toggle Colors*, *Toggle Value
   Types* (while `cellens-types` is on), *Reveal in Rule View* (YAML with
   rules), and the view's *Show Problems Only* / *Show All Rules* and
   *Follow Editor* / *Stop Following*, one of each pair by the context keys
   the title bar uses, on any file; run from the palette, the filter
   commands also reveal the view, so the change is seen. Still hidden: the
   commands that need a rule or a row as their argument (the line's parts,
   the view's row commands).

7. **One shortcut: `Ctrl+K L` (`Cmd+K L` on macOS) for *Toggle Inline
   Info*, while typing in YAML.** The line is the annotation a person
   flips while reading and editing; colours, example results and value
   types are set once and are in the menu and the submenu, one step away.
   The binding is `{"key": "ctrl+k l", "mac": "cmd+k l", "when":
   "editorTextFocus && editorLangId == yaml"}`, the palette entry's clause
   plus the focus. `L` for the line. A chord after `Ctrl+K`, VS Code's own
   chord prefix, types no character on any layout, unlike `Alt+…` on macOS
   (Option types characters) or `Ctrl+Alt+…` on Windows (AltGr: `Ctrl+Alt+L`
   is `ł` in Polish) and Linux desktops (`Ctrl+Alt+L` locks the screen in
   Xfce and Cinnamon).

   **Checked, on 14 November, by a script** that normalises every binding
   and reports an exact match or a chord-prefix clash, against:
   - VS Code 1.146.0's default keybindings for Linux, Windows and macOS
     (the lists published at github.com/codebling/vs-code-default-keybindings);
   - the `contributes.keybindings` of GitLens 19.6.0 (both keymaps,
     `alternate` and `chorded`), Red Hat YAML 2.1.0, Kubernetes Tools
     1.4.1, Prettier 12.4.0, ESLint 3.0.35, GitHub Copilot Chat 0.44.0,
     Vim 1.32.4, Docker 2.0.0, Python 2026.11.0, Jupyter 2026.6.1, Go
     0.60.0, Error Lens 3.30.0, Git Graph 1.30.0, Todo Tree 0.0.224 and Helm
     Intellisense 0.15.0, from their repositories' `package.json`.

   `ctrl+k l` and `cmd+k l` match nothing. Near it: `ctrl+k ctrl+l`
   (`cmd+k cmd+l`) is VS Code's *Toggle Fold*, a different chord. The
   prefix alone is bound only where no editor has focus (the terminal's
   and REPL's *Clear* on macOS, a `when: false` Markdown entry), and by
   Vim, which takes `Ctrl+K` in the editor while `vim.use<C-k>` (every
   `Ctrl+K` chord of VS Code's own included); the README says so. Step 4
   keeps the check: the script and its output (`src/taken_keys.json`)
   are committed, and a unit test fails on a clash.

   Rejected: one shortcut per toggle (the style guide: keybindings only
   for actions people repeat); `Shift+Alt+L` and `Ctrl+Alt+L` (free in
   every list, but type or lock as above); `Ctrl+Shift+Alt+L` (VS Code's
   *Quick Chat*).

8. **The walkthrough: four steps, a sample CRD written to the extension's
   own storage, pictures drawn in theme colours.** `contributes.walkthroughs`
   has one, `getStarted`, *Get started with cel-lens*: "Check the CEL rules
   of your Kubernetes YAML as you type." Each step is one action, its
   button label starting with a verb:

   | Step | Button | Done when |
   | --- | --- | --- |
   | *Open a sample CRD*: a CustomResourceDefinition with four rules, coloured, each with its line | *Open the Sample CRD* (`cel-lens.openSample`) | `onCommand:cel-lens.openSample` |
   | *See a problem and fix it*: one rule reads `self.replica`, which the schema does not have | *Fix the Problem* (`cel-lens.fixSampleProblem`: shows the sample, puts the cursor on the problem, opens VS Code's quick fix list, where *Change to `replicas`* is first) | `onContext:cel-lens.sampleFixed`, set once the sample has no error |
   | *Pick the Kubernetes version*: the status bar item shows it, and its menu has every setting and toggle | *Pick Kubernetes Version* | `onCommand:cel-lens.pickKubernetesVersion` |
   | *Choose the colors*: CEL, Classic or Off; *Toggle Colors* and the menu turn them off | *Choose Color Style* | `onCommand:cel-lens.chooseColorStyle` |

   The sample is the pictures' `widget_crd.yaml` shape (a `Widget` with
   `name`, `replicas`, `minReplicas`, four rules, one reading
   `self.replica`), kept as a constant in the bundle and written with
   `vscode.workspace.fs` to `get-started/widgets.yaml` under
   `context.globalStorageUri`, because the engine checks only files on disk
   and the person's workspace is theirs. *Open the Sample CRD* writes a
   fresh copy unless the sample is open, then shows it, so the problem is
   back on a second run. The view lists it under *Open files* while it is
   open, as any open file in no folder.

   Each step's picture is an SVG drawn only with VS Code's theme variables
   (`var(--vscode-editor-background)`, `--vscode-editorCodeLens-foreground`,
   `--vscode-editorError-foreground`, `--vscode-quickInput-background`, …)
   and `--vscode-contrastBorder` outlines, of what the step's action opens:
   the line above a rule, the squiggle and the quick fix list, the version
   picker, the colour picker. The walkthrough's `svg` media takes those
   variables, so one file reads in all four themes; each has `altText`.
   *cel-lens: Get Started* opens the walkthrough from the palette.

   **It opens nothing by itself.** No activation code opens the
   walkthrough, the sample, the README, the changelog or a notification on
   install or update; a test holds it. VS Code's own setting
   `workbench.welcomePage.walkthroughs.openOnInstall` decides whether the
   Welcome page shows a newly installed extension's walkthrough; cel-lens
   neither forces nor fights it, and the README names the setting.

   Rejected: an untitled buffer (the engine checks files only, and
   changing that is an engine change); writing the sample into the
   workspace (a file the person did not ask for, in their repository);
   per-theme screenshots as the walkthrough's pictures (sixteen PNGs in
   every `.vsix`, retaken by a person for each change, while the theme
   variables make one SVG per step right in every theme); a `when` clause
   that hides the walkthrough until the extension runs, to dodge VS Code's
   open-on-install (it would vanish from the Welcome page until a YAML
   file is opened); a fifth step for the toggles (step 3 points at the
   menu, which holds them).

9. **One order everywhere: the menu's.** The README's *Commands* table, its
   *Settings* table and the settings' `order` (so the Settings editor)
   follow the order a person meets things in, which is the walkthrough's
   and then the menu's:
   - commands: *Get Started*, *Show Actions*, *Pick Kubernetes Version*,
     *Choose Color Style*, *Toggle Colors*, *Toggle Inline Info*, *Toggle
     Example Results*, *Toggle Value Types*, *Reveal in Rule View*, *Show
     Problems Only*, *Show All Rules*, *Follow Editor*, *Stop Following*,
     *Give a Value a Type…*, *Use This Example as the Type*, *Show Logs*,
     *Restart Engine*; with a *Shortcut* column (`Ctrl+K L` / `Cmd+K L` on
     *Toggle Inline Info*, `none` on the rest, and one sentence on
     binding any of them in *Keyboard Shortcuts*);
   - settings: `kubernetesVersion`, `colorStyle`, `inlineInfo`,
     `exampleResults`, `valueTypes`, `ruleView.followEditor`,
     `experimental`, `trace.server`, `serverPath`.

   The README's sections become: *What it looks like*, *Install*, *Get
   started* (new: the walkthrough, *Get Started*, and that cel-lens opens
   nothing by itself), *Where to find it* (new: the status bar menu and the
   editor's submenu, each with a picture, the palette, the shortcut, and a
   table of each thing drawn with its toggle, setting and menu entry), then
   *Give a value a type*, *Example results*, *Rule view*, *Commands*,
   *Settings* and the rest as today.

   Rejected: ordering settings by how often they change (no data, and a
   second order to learn); the alphabet (puts `colorStyle` before the
   version the walkthrough sets first).

10. **Themes are checked at the lowest tier that sees each element, then
    looked at in four themes; "nothing by colour alone" means a word or a
    shape says the same, per element.**
    - *The colours* mark the parts of a CEL expression whose words are
      already there; no state (error, ok) is ever a colour. Tier 1: every
      colour of both styles holds 4.5:1 against the editor background of
      Default Light Modern (`#FFFFFF`) and Default Dark Modern (`#1F1F1F`),
      as well as Dark+ (`#1E1E1E`), and 7:1 against Default High Contrast
      (`#000000`) and High Contrast Light (`#FFFFFF`).
    - *The line* is words in the theme's CodeLens colour; a wrong example
      says `refuses 1 example it should allow`, never a red part. Tier 1:
      the engine's words, already tested in `internal/inline`.
    - *The rule view's icons*: each state is its own product icon (`error`,
      `warning`, `pass`, `check`), its row's description says the state in
      words, and `accessibilityInformation` says it. Tier 1: no two states
      share an icon id; every description has the state's words.
    - *The status bar item* says `Engine down` in words beside `$(error)`,
      and gains `accessibilityInformation` (`cel-lens, Kubernetes 1.36`,
      `cel-lens engine down`). *The menu's* values are words.
    - *The walkthrough's pictures* use theme variables only. Tier 1: a test
      reads each SVG and fails on a literal colour.
    - *Looked at:* `npm run pictures -- themes` takes six scenes (a CRD with
      its colours and lines, a problem with its hover, the rule view with
      every state, the status bar menu, the editor's submenu, the
      walkthrough) in Default Light Modern, Default Dark Modern, Default
      High Contrast and Default High Contrast Light, into
      `out/pictures/themes/`, never `images/`. The session takes them, as
      it takes every picture (the agents' sandbox has no Chromium
      libraries), checks each against the list in step 6, and saves them
      in `docs/screenshots/cel-lens-everything-one-step-away/` as this
      plan's evidence.

    Rejected: contributing theme colours for the parts (`contributes.colors`)
    so themes could override them (a change of how colours work that nobody
    asked for, and the tested palettes already cover the four kinds);
    retaking the README's pictures in light (they show features, not
    themes; the theme pictures are evidence, not the store page).

11. **The engine does not change.** Each part of the request is the
    extension's: the toggles write settings the engine already reads
    (`inlineInfo`, `exampleResults`) or the client already owns (the
    colours, and the value parts, hidden by the middleware in decision 3);
    which file and which rule the cursor is in comes from `cel-lens.ruleList`
    of engine v0.33.0, which the extension already holds; the sample CRD is
    a file on disk, which the engine already checks, in or out of a
    workspace folder. So the extension keeps pinning engine v0.33.0, and
    no engine step or release is planned. If a step finds it needs one, it
    stops and says why, and this plan changes first.

12. **Release: the extension alone, v0.42.0.** The last step prepares
    v0.42.0 (`package.json`, `package-lock.json`, `CHANGELOG.md`'s
    *Unreleased* becoming `## [0.42.0] - <date>`), still carrying
    `cel-lens-core` v0.33.0, and `release.yml` publishes `vscode-v0.42.0`;
    the session checks the Linux `.vsix`'s signature and provenance with
    cosign, that it carries the walkthrough's SVGs and engine v0.33.0 with
    the pinned hash, and closes JairJosafath/cel-lens#3 with a comment going
    through its list. The version is the next minor unless another release
    comes first.

## Steps

Each is an issue in the session's Gitea (bodies in
[cloud-session/issues/](cloud-session/issues/)), stacked in this order.

| Step | Repository | What |
| --- | --- | --- |
| 1 | cel-lens-vscode | *Toggle Colors* and the colour style it restores; `cel-lens.valueTypes` and *Toggle Value Types*, hiding the value parts in the middleware; both live, in the palette with their `when` clauses (`g1.md`) |
| 2 | cel-lens-vscode | The status bar menu as the hub: the order, labels with the value in the description, separators, icons on the engine entries, the new toggles (`g2.md`) |
| 3 | cel-lens-vscode | The editor's *cel-lens* submenu (context menu and title overflow), the keys `cel-lens.editorHasRules` and `cel-lens.ruleAtCursor`, *Reveal in Rule View*, and the palette's missing commands (`g3.md`) |
| 4 | cel-lens-vscode | The shortcut `Ctrl+K L` / `Cmd+K L` for *Toggle Inline Info*, and the committed check of taken keys (`g4.md`) |
| 5 | cel-lens-vscode | The *Get started with cel-lens* walkthrough: the sample CRD, its four steps and SVGs, *Get Started*, nothing opened on activation (`g5.md`) |
| 6 | cel-lens-vscode | Themes and colour alone: the Modern backgrounds, icon and word tests, the status bar item's accessibility, `npm run pictures -- themes` (`g6.md`) |
| 7 | cel-lens-vscode | The README in the order a person meets things, *Get started* and *Where to find it* with their pictures, the shortcut column, the settings' `order` (`g7.md`) |
| 8 | cel-lens-vscode | Prepare v0.42.0, released with `release.yml`; #3 closed (`g8.md`) |

## Status

- **14 November, 04:40 UTC.** Planned. Nothing built.
- **14 November, 08:40 UTC. Done.** All eight steps, built and reviewed in
  the factory as cel-lens-vscode #44 (#35), #45 (#36), #48 (#37), #50
  (#38), #46 and #49 (#39, and #47, which step 3 of the walkthrough
  reached), #51 (the two halves joined, with the split-editor fix), #54
  (#40), #55 (#41) and #56 (#42), with #34 (#28) under them, merged as
  JairJosafath/cel-lens#33 (8399ef9). `release.yml` published
  [cel-lens for VS Code v0.42.0](https://github.com/JairJosafath/cel-lens/releases/tag/vscode-v0.42.0),
  still carrying engine v0.33.0. Checked here: the Linux `.vsix`'s
  signature and provenance verify with cosign, its engine is v0.33.0 with
  the pinned hash, and it contributes the walkthrough, `ctrl+k l` and the
  `cel-lens.editor` submenu; the README's new pictures answer on `main`.
  The theme pictures are in `docs/screenshots/cel-lens-everything-one-step-away/`.
  JairJosafath/cel-lens#3 is closed, with a comment going through its list.
  The reviews' main finds: the walkthrough's sample folder, hardened over
  three rounds against another local user (a refusal the person could not
  fix, a linked TMPDIR refused on Linux, a private folder re-created by
  name); split editors acting on the wrong rule; a README link to a page
  that did not exist. Not tried: VS Code desktop (openvscode-server in
  Chrome only), a real screen reader, the full `npm run pictures` in one
  run, and Windows for the temp-folder checks.

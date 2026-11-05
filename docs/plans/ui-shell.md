# Plan: a UI you navigate, not scroll

## Goal

Today every area is one long centred column: the sections of Work, Needs
you, the Loop, a Brief and every object page stack under each other, and
the reader scrolls to find what they came for. The UI should put each thing
one click away: a sidebar to move between areas, a top bar for where you are
and what you can do, tabs inside an area, object pages in a drawer over the
list you came from, forms in modals, and a theme the person picks.

## UX principles this plan applies

- **Navigation is always visible.** The areas sit in a left sidebar, never
  scrolled away; where you are is highlighted, and what waits on you is a
  count beside *Needs you*.
- **Overview first, details on demand.** An area shows a summary that fits
  the screen; details open in a drawer, a tab or a collapsed section, never
  below the fold by default.
- **Keep context.** Opening an object keeps the list it came from behind a
  drawer; Esc or Back closes it, and the address still names the object so a
  link opens it.
- **One primary action per screen,** in the same place (the top bar):
  *New brief* opens a modal instead of a form at the top of a page.
- **Use the width.** A wide screen shows two or three columns; a phone shows
  one column with the sidebar in a drawer.
- **Consistency.** One set of primitives (Modal, Drawer, Tabs, Menu,
  Command palette) and one set of tokens, used everywhere.
- **Accessible by default.** Dialogs trap and return focus, Esc closes,
  tabs use roving focus, every control has a name, contrast holds in both
  themes, and motion respects `prefers-reduced-motion`.
- **The UI still enforces nothing.** No new write; the same three (and the
  actions behind `ui.actions.enabled`) move into modals.

## Decisions

- **Shell.** A left sidebar (240px, collapsible to a 64px icon rail,
  remembered per browser): the brand, the project switcher, then the areas
  in three groups: *Needs you* (with its count); *Build*: Loop, Briefs,
  Story, Work; *Operate*: Factory. About and Settings sit at the bottom
  with the versions. A sticky top bar (56px) holds the page title and
  breadcrumb, a compact *Demo data* chip (instead of the full-width
  banner), the freshness dot, a *Search or jump* button (⌘K / Ctrl+K), the
  area's primary action, and the theme toggle. Below 768px the sidebar is a
  drawer behind a menu button. The long footer paragraph moves to About.
- **Theme.** System, Light or Dark, from a toggle in the top bar and in
  Settings, kept in `localStorage` (wrapped in try/catch; the page works
  without it) and applied as `data-theme` on `<html>`. `theme.css` keeps
  its tokens; the dark ones apply under
  `@media (prefers-color-scheme: dark) { :root:not([data-theme='light']) }`
  and under `:root[data-theme='dark']`.
- **Settings** is a modal: Appearance (System, Light, Dark), Density
  (Comfortable, Compact), and the sidebar's default.
- **Primitives** in `ui/src/components/ui/`: `Modal`, `Drawer` (from the
  right, `min(760px, 94vw)`), `Tabs` (the active tab in the address as
  `tab=`), `Menu`, `CommandPalette` (areas, projects and every object the
  UI watches, by kind, name and namespace; Enter opens it).
- **Object pages open in the Drawer** over the area they came from, with
  their header on top and their sections as tabs (*Overview*, *Status*,
  *Activity*, *Related*, as the kind has them) and an *Open full page*
  link; the address keeps `ns`, `kind` and `name`.
- **Areas:**
  - *Work*: tabs Board, Crews, Releases, Time & tokens, under a one-line
    *Needs a look* strip.
  - *Loop*: the ring and the selected stage side by side; a node selects
    its stage instead of scrolling to it; the review points come first.
  - *Story*: the diagram fills the area; the side panels collapse into a
    right panel.
  - *Briefs*: a grid of Brief cards; *New brief* opens the modal; a card
    opens its Brief in the Drawer.
  - *Needs you*: groups per project that collapse, and filter chips by kind.
  - *Factory*: a dashboard grid of the health cards, the charts and the
    agent slots.

## Steps

1. The shell, the theme and Settings, the primitives, the command palette,
   *New brief* in a modal, object pages in the Drawer.
2. The areas' layouts above, and object pages' sections as tabs.
3. Screenshots of every area in both themes, desktop and phone.

## Done when

- `cd ui && npm run check` passes after each step, with tests for the theme
  choice (stored, applied, System following the media query), the
  primitives' keyboard behaviour, the command palette's matching, and each
  area's tabs.
- Every area's first screen at 1440×900 shows its summary without
  scrolling, and no page scrolls sideways at 390px.
- `docs/screenshots/ui-shell-*.png` show each area in light and dark.

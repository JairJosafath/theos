# Plan: C4 Factory becomes theos

## Goal

The project continues as **theos**, THE Open Software-factory, in its own
repository (`JairJosafath/theos`), with c4factory's history. The C4 model
stays: theos still describes software as Persons, SoftwareSystems, Containers
and DataStores.

## Decisions

| What | Was | Is |
| --- | --- | --- |
| Name | C4 Factory, c4factory | theos |
| Go module | `github.com/JairJosafath/c4factory` | `github.com/JairJosafath/theos` |
| API group, annotations, labels | `c4.factory.io` | `theos.factory.io` (only the kubebuilder group changes; the domain stays `factory.io`) |
| `kubectl` category | `c4` | `theos` |
| Binaries | `c4agent`, `c4session` | `theos-agent`, `theos-session` |
| Chart, images | `c4-factory`, `ghcr.io/jairjosafath/c4factory/*` | `theos`, `ghcr.io/jairjosafath/theos/*` |
| Environment variables, npm scope | `C4_*`, `C4AGENT_*`, `C4SESSION_*`, `@c4factory` | `THEOS_*`, `THEOS_AGENT_*`, `THEOS_SESSION_*`, `@theos` |
| Identity file | `.c4factory.yaml` | `.theos.yaml` |

- **History stays where it happened.** Links to c4factory's issues, pull
  requests, milestones and commits keep pointing at c4factory, and the README
  says that issue numbers in the docs are c4factory's.
- **Versions go on.** theos keeps the chart's version line (0.30.0), so the
  deprecation notices that name versions stay true; c4factory's tags stay
  with c4factory.
- **No upgrade path.** An install of c4factory isn't converted: the API group
  is new, so theos is installed fresh.
- **`PROJECT` is edited by hand** for its group and name: kubebuilder has no
  command that renames a group.
- **The licence is unchanged.** Whether theos is open source is the owner's
  decision.

## Steps

1. Replace the names in every tracked text file and path with a one-off
   script: links to c4factory's history protected first, then the table
   above in order.
2. `make manifests generate`, then fix what lint finds in the longer names.
3. `make check`.

## Done when

`make check` passes, and `grep -rIiE 'c4[ _-]?factory|c4agent|c4session'`
finds only the README's note and links to c4factory's history.

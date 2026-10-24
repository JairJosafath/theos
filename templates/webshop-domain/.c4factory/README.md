# webshop-domain (Domain, catalog entry `webshop`)

What a webshop is about, as Markdown an agent reads before it plans or
reviews a change: a `README.md` with a glossary and one line per topic file,
and a topic file per area (catalog and search, cart, checkout and orders,
stock, payments, pricing and money, accounts and personal data), each with
*Practices*, *Common mistakes*, *Controversies* (the choice and why) and
*Sources*. Plain prose, no code to copy and no stack: the Standard has the
stack.

- **Named by:** a SoftwareSystem's `spec.domainRefs` (`webshop`). Every
  Container of that system gets it, whatever its type: a Domain has no
  `supportedTypes`.
- **Writes:** every file here under `docs/agents/domain/webshop/`; the
  operator adds the prefix, so the files name each other by their bare
  names. Markdown only: a file of any other kind fails the Container.
- **Read by:** the Standards' `AGENTS.md` and `docs/agents/reviewer.md`,
  which tell the implementer to read the README and the topic files for the
  part it touches, and the reviewer to check a change against each topic's
  *Common mistakes*.
- **Scaffold once:** written at the Container's scaffold and never updated
  by the operator; a person may update a scaffolded copy by hand
  (docs/plans/domains.md, decision 5).
- **Shape:** `TestDomainTemplatesHaveTheShape` in
  `internal/scaffold/templates_test.go` checks every `*-domain` template:
  Markdown only, a README naming every topic file, the four headings in each.

See the python-fastapi template's `.c4factory/README.md` for how templates are rendered.

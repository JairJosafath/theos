# Sources

Every source read to write `docs/style/`. The Python sources were read on
2026-10-07. The cross-language sources were read on 2026-09-30 for another
Standard, whose `principles.md` this one adapts; they were not read again.
Version or date as shown on the page; "live" when the page shows none.

## Python

| Source | Version / date |
|---|---|
| PEP 8, Style Guide for Python Code — https://peps.python.org/pep-0008/ | Active; created 05-Jul-2001, post-history to 01-Aug-2013 |
| PEP 257, Docstring Conventions — https://peps.python.org/pep-0257/ | created 29-May-2001, post-history 13-Jun-2001 |
| Google Python Style Guide — https://google.github.io/styleguide/pyguide.html | live |
| Typing best practices — https://typing.python.org/en/latest/reference/best_practices.html | live (the page shows no version) |
| mypy, common issues and solutions — https://mypy.readthedocs.io/en/stable/common_issues.html | mypy 2.4.0 (stable) |
| pytest, good integration practices — https://docs.pytest.org/en/stable/explanation/goodpractices.html | live (stable; no version on the page) |
| FastAPI tutorial — https://fastapi.tiangolo.com/tutorial/ | live |
| FastAPI, dependencies — https://fastapi.tiangolo.com/tutorial/dependencies/ | live |
| FastAPI, handling errors — https://fastapi.tiangolo.com/tutorial/handling-errors/ | live |
| FastAPI, testing — https://fastapi.tiangolo.com/tutorial/testing/ | live |
| FastAPI, concurrency and async/await — https://fastapi.tiangolo.com/async/ | live |

The FastAPI pages feed only the FastAPI section of `python.md`, which applies
to FastAPI services.

## Lints

| Source | Version / date |
|---|---|
| ruff — https://docs.astral.sh/ruff/rules/ | **0.16.10**, as pinned in `pyproject.toml` |
| mypy — https://mypy.readthedocs.io/en/stable/ | **2.4.0**, as pinned in `pyproject.toml` |

Every ruff code `python.md` cites was checked against ruff 0.16.10 with
`ruff rule <CODE>` (it exists and is not a preview rule, so `select = ["ALL"]`
turns it on), and most were run on a file written to break them. Rules the
Google docstring convention switches off (such as D401) are not cited. The
mypy flags named are those of `mypy.ini` (`strict` and its extra error codes).

## Cross-language (read 2026-09-30)

| Source | Version / date |
|---|---|
| LLVM Coding Standards — https://llvm.org/docs/CodingStandards.html | live |
| Chromium style guide index — https://chromium.googlesource.com/chromium/src/+/HEAD/styleguide/styleguide.md | HEAD |
| Mozilla C++ coding style — https://firefox-source-docs.mozilla.org/code-quality/coding-style/coding_style_cpp.html | live |
| Linux kernel coding style — https://docs.kernel.org/process/coding-style.html | live |
| C++ Core Guidelines — https://isocpp.github.io/CppCoreGuidelines/CppCoreGuidelines | Jun 14, 2026 |
| Rust API Guidelines, naming — https://rust-lang.github.io/api-guidelines/naming.html | live |
| GNU Coding Standards — https://www.gnu.org/prep/standards/standards.html | April 27, 2026 |

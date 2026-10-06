# The quality contract: `make check` is what CI and the agents run.
.PHONY: check
check:
	@echo 'make check: index.html must say "hello, world"'
	grep -q '<p>hello, world</p>' index.html

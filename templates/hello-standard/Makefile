# The quality contract: `make check` is what CI and the agents run.
.PHONY: check
check:
	@echo 'make check: hello.sh must print "hello, world"'
	sh hello.sh | grep -qx 'hello, world'

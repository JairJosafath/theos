# Harness transcripts

What each harness's CLI prints in the scenes of the contract every harness
passes (`harness_test.go`): a change, a review, a blocked issue, a failed
call and an unavailable service. The test has a stand-in CLI print the file
of a scene and holds the harness's agent to the same answers.

- `claude-code/`: written by hand, line by line in the form of
  `../claude-stream.jsonl`, which is a recorded run of Claude Code. They
  hold only the lines the parser reads.

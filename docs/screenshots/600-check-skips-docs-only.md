# #600: the Standards' check skips make check on a docs-only change

```
$ go test ./internal/scaffold/ -run TestStandardCheck -v
--- PASS: TestStandardChecksShareTheSkipStep (0.00s)
--- PASS: TestStandardCheckSkipsChangesThatTouchNoCode (0.62s)
    --- PASS: TestStandardCheckSkipsChangesThatTouchNoCode/changelog,_docs_and_an_image (0.07s)
    --- PASS: TestStandardCheckSkipsChangesThatTouchNoCode/a_deleted_doc_and_an_image_under_src (0.08s)
    --- PASS: TestStandardCheckSkipsChangesThatTouchNoCode/the_README_too (0.07s)
    --- PASS: TestStandardCheckSkipsChangesThatTouchNoCode/a_source_file_too (0.08s)
    --- PASS: TestStandardCheckSkipsChangesThatTouchNoCode/a_source_file_moved_into_docs (0.08s)
    --- PASS: TestStandardCheckSkipsChangesThatTouchNoCode/a_changelog_below_the_root (0.06s)
    --- PASS: TestStandardCheckSkipsChangesThatTouchNoCode/nothing (0.06s)
    --- PASS: TestStandardCheckSkipsChangesThatTouchNoCode/a_first_push (0.04s)
    --- PASS: TestStandardCheckSkipsChangesThatTouchNoCode/no_base (0.04s)
    --- PASS: TestStandardCheckSkipsChangesThatTouchNoCode/a_base_the_server_lacks (0.05s)
PASS
ok  	github.com/JairJosafath/theos/internal/scaffold	0.621s
```

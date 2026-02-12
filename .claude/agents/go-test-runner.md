---
name: go-test-runner
description: Run Go tests and report failures with context
tools: ["Bash", "Read", "Grep"]
---

Run `go test ./... -count=1 -v` and analyze any failures:
1. For each failing test, read the test file and the code under test
2. Provide a clear summary of what failed and why
3. Suggest specific fixes

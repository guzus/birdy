package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFollowingOverlapUsersAcceptsArrayAndPagedObject(t *testing.T) {
	arrayUsers, err := parseFollowingOverlapUsers([]byte(`[{"id":"1","username":"alpha"}]`))
	if err != nil {
		t.Fatalf("parse array: %v", err)
	}
	if len(arrayUsers) != 1 || arrayUsers[0].Username != "alpha" {
		t.Fatalf("unexpected array parse result: %+v", arrayUsers)
	}

	pagedUsers, err := parseFollowingOverlapUsers([]byte(`{"users":[{"id":"2","username":"beta"}],"nextCursor":"x"}`))
	if err != nil {
		t.Fatalf("parse paged object: %v", err)
	}
	if len(pagedUsers) != 1 || pagedUsers[0].Username != "beta" {
		t.Fatalf("unexpected paged parse result: %+v", pagedUsers)
	}
}

func TestFollowingOverlapAggregatesByMinimumFollowers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BIRDY_ACCOUNTS", `[{"name":"ci","auth_token":"token","ct0":"ct0"}]`)

	birdPath := filepath.Join(t.TempDir(), "bird")
	script := `#!/bin/sh
cmd="$1"
shift
if [ "$cmd" = "user-tweets" ]; then
  case "$1" in
    alpha) echo '[{"authorId":"101","author":{"username":"alpha","name":"Alpha"}}]' ;;
    beta) echo '[{"authorId":"202","author":{"username":"beta","name":"Beta"}}]' ;;
    gamma) echo '[{"authorId":"303","author":{"username":"gamma","name":"Gamma"}}]' ;;
    *) echo '[]' ;;
  esac
  exit 0
fi
if [ "$cmd" = "following" ]; then
  user=""
  while [ "$#" -gt 0 ]; do
    if [ "$1" = "--user" ]; then
      user="$2"
      shift 2
    else
      shift
    fi
  done
  case "$user" in
    101) echo '[{"id":"1","username":"shared","name":"Shared","followersCount":500},{"id":"2","username":"one","name":"One","followersCount":100}]' ;;
    202) echo '[{"id":"1","username":"shared","name":"Shared","followersCount":500},{"id":"3","username":"two","name":"Two","followersCount":200}]' ;;
    303) echo '[{"id":"1","username":"shared","name":"Shared","followersCount":500},{"id":"3","username":"two","name":"Two","followersCount":200}]' ;;
    *) echo '[]' ;;
  esac
  exit 0
fi
echo "unexpected command: $cmd" 1>&2
exit 9
`
	if err := os.WriteFile(birdPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake bird: %v", err)
	}
	t.Setenv("BIRDY_BIRD_PATH", birdPath)

	prevMin, prevPageSize, prevAll, prevMaxPages, prevJSON := followingOverlapMin, followingOverlapPageSize, followingOverlapAll, followingOverlapMaxPages, followingOverlapJSON
	prevAccount, prevStrategy := accountFlag, strategyFlag
	defer func() {
		followingOverlapMin = prevMin
		followingOverlapPageSize = prevPageSize
		followingOverlapAll = prevAll
		followingOverlapMaxPages = prevMaxPages
		followingOverlapJSON = prevJSON
		accountFlag = prevAccount
		strategyFlag = prevStrategy
	}()
	accountFlag = ""
	strategyFlag = "round-robin"

	cmd := newFollowingOverlapCmd()
	for name, value := range map[string]string{
		"min":       "2",
		"page-size": "50",
		"all":       "true",
		"max-pages": "1",
		"json":      "true",
	} {
		if err := cmd.Flags().Set(name, value); err != nil {
			t.Fatalf("set --%s: %v", name, err)
		}
	}
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	if err := cmd.RunE(cmd, []string{"@Alpha", "beta", "gamma"}); err != nil {
		t.Fatalf("run following-overlap: %v; stderr=%q", err, errOut.String())
	}

	var got []followingOverlapResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, out.String())
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 common accounts, got %d: %+v", len(got), got)
	}
	if got[0].Username != "shared" || got[0].Count != 3 {
		t.Fatalf("expected shared first with count 3, got %+v", got[0])
	}
	if strings.Join(got[0].FollowedBy, ",") != "alpha,beta,gamma" {
		t.Fatalf("unexpected followedBy for shared: %+v", got[0].FollowedBy)
	}
	if got[1].Username != "two" || got[1].Count != 2 {
		t.Fatalf("expected two second with count 2, got %+v", got[1])
	}
}

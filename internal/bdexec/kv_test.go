package bdexec

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestKVListReadsFlatJSONObjectWithoutGet(t *testing.T) {
	runner := NewMockRunner()
	runner.Stdout = `{"schema_version":1,"memory.note":"remembered","loom.sweep.dismiss.abc123":"{\"kind\":\"untracked-commit\"}"}`

	got, err := New(runner).KVList(context.Background(), wd)
	if err != nil {
		t.Fatalf("KVList: %v", err)
	}
	want := map[string]string{
		"schema_version":            "1",
		"memory.note":               "remembered",
		"loom.sweep.dismiss.abc123": `{"kind":"untracked-commit"}`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("KVList = %#v, want %#v", got, want)
	}
	calls := runner.Calls()
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].Args, []string{"kv", "list", "--json"}) {
		t.Fatalf("calls = %#v, want only kv list --json", calls)
	}
}

func TestKVGetJudgesMissingByFoundFieldNotExitStatus(t *testing.T) {
	const key = "initiative-retro.loom-d1q.2026-W32"

	t.Run("found", func(t *testing.T) {
		runner := NewMockRunner()
		runner.Stdout = `{"found":true,"key":"` + key + `","schema_version":1,"value":"{\"status\":\"draft\"}"}`

		value, found, err := New(runner).KVGet(context.Background(), wd, key)
		if err != nil {
			t.Fatalf("KVGet: %v", err)
		}
		if !found || value != `{"status":"draft"}` {
			t.Fatalf("KVGet = %q, %v; want the decoded value and found=true", value, found)
		}
		calls := runner.Calls()
		if len(calls) != 1 || !reflect.DeepEqual(calls[0].Args, []string{"kv", "get", key, "--json"}) {
			t.Fatalf("calls = %#v, want only kv get %s --json", calls, key)
		}
	})

	t.Run("missing exits zero", func(t *testing.T) {
		runner := NewMockRunner()
		// bd 1.1.2 exits 0 for an absent key and says so in the envelope.
		runner.Stdout = `{"found":false,"key":"` + key + `","schema_version":1,"value":""}`

		value, found, err := New(runner).KVGet(context.Background(), wd, key)
		if err != nil {
			t.Fatalf("KVGet: %v", err)
		}
		if found || value != "" {
			t.Fatalf("KVGet = %q, %v; want empty and found=false", value, found)
		}
	})

	t.Run("rejects a flag-shaped key before exec", func(t *testing.T) {
		runner := NewMockRunner()
		if _, _, err := New(runner).KVGet(context.Background(), wd, "--bad-key"); !errors.Is(err, ErrInvalidID) {
			t.Fatalf("flag-shaped key error = %v, want ErrInvalidID", err)
		}
		if len(runner.Calls()) != 0 {
			t.Fatalf("calls = %#v, want none", runner.Calls())
		}
	})

	t.Run("bd failure surfaces", func(t *testing.T) {
		runner := NewMockRunner()
		runner.Err = errors.New("exit status 1")
		runner.Stderr = "dolt server not reachable"
		if _, _, err := New(runner).KVGet(context.Background(), wd, key); err == nil {
			t.Fatal("KVGet error = nil, want the bd failure")
		}
	})

	t.Run("unparseable stdout is an error, not a missing key", func(t *testing.T) {
		runner := NewMockRunner()
		runner.Stdout = "not json"
		if _, _, err := New(runner).KVGet(context.Background(), wd, key); err == nil {
			t.Fatal("KVGet error = nil, want a decode failure")
		}
	})
}

func TestKVSetAndClearUseValidatedPositionalArguments(t *testing.T) {
	runner := NewMockRunner()
	client := New(runner)
	key := "loom.sweep.dismiss.a1b2c3"
	value := `{"materialDigest":"digest","kind":"untracked-commit"}`

	if err := client.KVSet(context.Background(), wd, key, value); err != nil {
		t.Fatalf("KVSet: %v", err)
	}
	if err := client.KVClear(context.Background(), wd, key); err != nil {
		t.Fatalf("KVClear: %v", err)
	}
	if err := client.KVSet(context.Background(), wd, "--bad-key", value); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("flag-shaped key error = %v, want ErrInvalidID", err)
	}
	if err := client.KVSet(context.Background(), wd, key, "--bad-value"); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("flag-shaped value error = %v, want ErrInvalidID", err)
	}

	calls := runner.Calls()
	want := [][]string{{"kv", "set", key, value}, {"kv", "clear", key}}
	if len(calls) != len(want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
	for i := range want {
		if !reflect.DeepEqual(calls[i].Args, want[i]) {
			t.Errorf("call %d = %v, want %v", i, calls[i].Args, want[i])
		}
	}
}

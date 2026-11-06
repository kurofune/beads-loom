package bdexec

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestInfoReadsServerTargetFromDoltShow(t *testing.T) {
	runner := NewMockRunner()
	// Verbatim bd 1.3.2 output for a server-mode workspace.
	runner.Stdout = `{
  "backend": "dolt",
  "connection_ok": true,
  "database": "loom",
  "embedded": false,
  "host": "127.0.0.1",
  "port": 3311,
  "schema_version": 1,
  "shared_server": false,
  "tls": false,
  "user": "root"
}`
	got, err := New(runner).Info(context.Background(), wd)
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	want := Target{Database: "loom", Host: "127.0.0.1", Port: 3311}
	if got != want {
		t.Fatalf("Info = %+v, want %+v", got, want)
	}
	calls := runner.Calls()
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].Args, []string{"dolt", "show", "--json"}) || calls[0].Workdir != wd {
		t.Fatalf("calls = %#v, want one dolt show --json in %s", calls, wd)
	}
}

func TestInfoReadsEmbeddedTarget(t *testing.T) {
	runner := NewMockRunner()
	runner.Stdout = `{"backend":"dolt","data_dir":"/w/.beads/embeddeddolt","database":"zz","embedded":true,"schema_version":1}`
	got, err := New(runner).Info(context.Background(), wd)
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if want := (Target{Database: "zz", Embedded: true}); got != want {
		t.Fatalf("Info = %+v, want %+v", got, want)
	}
}

func TestInfoFailsWhenBdFails(t *testing.T) {
	runner := NewMockRunner()
	runner.Stderr = "Error: no beads database found"
	runner.Err = errors.New("exit status 1")
	_, err := New(runner).Info(context.Background(), wd)
	be, ok := AsBdError(err)
	if !ok {
		t.Fatalf("err = %v, want a *BdError", err)
	}
	if be.Message() != "Error: no beads database found" {
		t.Fatalf("Message = %q, want bd's stderr", be.Message())
	}
}

func TestInfoFailsWhenOutputOmitsTheDatabase(t *testing.T) {
	for name, stdout := range map[string]string{
		"no database key": `{"backend":"dolt","embedded":false,"host":"127.0.0.1","port":3311}`,
		"blank database":  `{"database":"  ","embedded":false}`,
		"not JSON":        `Dolt configuration: ...`,
	} {
		t.Run(name, func(t *testing.T) {
			runner := NewMockRunner()
			runner.Stdout = stdout
			runner.Stderr = "warning: something"
			_, err := New(runner).Info(context.Background(), wd)
			if err == nil {
				t.Fatal("Info succeeded, want an error")
			}
			if !strings.Contains(err.Error(), "warning: something") {
				t.Fatalf("err = %v, want it to carry bd's stderr", err)
			}
		})
	}
}

package main

// This is the reference runner. It reads ../vectors/*.json exactly as a port's
// runner must (from the manifest's roster, generically, dispatching on `op`,
// never reaching into the generator's types) and asserts the committed vectors
// still describe what primitives-go does.
//
// It is a regression guard in both directions: a vector that drifts from the
// code fails here, and so does a behaviour change in primitives-go that nobody
// meant to make. It is also the worked example a port copies the shape of.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/primandproper/primitives-go/v2/bitmask"
	"github.com/primandproper/primitives-go/v2/encoding"
	errhttp "github.com/primandproper/primitives-go/v2/errors/http"
	"github.com/primandproper/primitives-go/v2/numbers"
	retrycfg "github.com/primandproper/primitives-go/v2/retry/config"
)

type vectorCase struct {
	ID     string          `json:"id"`
	Op     string          `json:"op"`
	Input  json.RawMessage `json:"input"`
	Expect json.RawMessage `json:"expect"`
}

type vectorFile struct {
	Package string       `json:"package"`
	Cases   []vectorCase `json:"cases"`
}

type manifestFile struct {
	Path    string   `json:"path"`
	Package string   `json:"package"`
	Ports   []string `json:"ports"`
}

type manifest struct {
	Files []manifestFile `json:"files"`
}

// port is the roster name this runner answers to. A port's runner hard-codes
// its own.
const port = "primitives-go"

// runners is every package this runner knows how to run. It is not the list
// of what gets run: the manifest's roster decides that.
var runners = map[string]func(*testing.T, vectorFile){
	"bitmask":  runBitmask,
	"numbers":  runNumbers,
	"errors":   runErrors,
	"retry":    runRetry,
	"encoding": runEncoding,
}

// plan splits the manifest into the files that name port and have a runner,
// and the files that name port and do not. A file that does not name port is
// in neither: it is not this port's concern, and skipping it is not a failure.
func plan(m manifest, port string, runners map[string]func(*testing.T, vectorFile)) (run, missing []manifestFile) {
	for _, f := range m.Files {
		if !slices.Contains(f.Ports, port) {
			continue
		}
		if _, ok := runners[f.Package]; ok {
			run = append(run, f)
		} else {
			missing = append(missing, f)
		}
	}
	return run, missing
}

// TestVectors is driven by the manifest, never by a list of its own. A runner
// that only ran the packages it already knew about would read green while a
// file naming it went untested.
func TestVectors(t *testing.T) {
	run, missing := plan(loadManifest(t), port, runners)
	for _, f := range missing {
		t.Errorf("%s names %s, but this runner has no arm for package %q", f.Path, port, f.Package)
	}
	for _, f := range run {
		t.Run(f.Package, func(t *testing.T) {
			runners[f.Package](t, load(t, f))
		})
	}
}

func TestPlan(t *testing.T) {
	noop := func(*testing.T, vectorFile) {}
	known := map[string]func(*testing.T, vectorFile){"bitmask": noop, "retry": noop}

	t.Run("runs a file that names the port", func(t *testing.T) {
		m := manifest{Files: []manifestFile{{Path: "vectors/bitmask.json", Package: "bitmask", Ports: []string{"primitives-go", "primitives-ts"}}}}
		run, missing := plan(m, "primitives-go", known)
		if len(run) != 1 || run[0].Package != "bitmask" || len(missing) != 0 {
			t.Fatalf("got run=%v missing=%v, want only bitmask run", run, missing)
		}
	})

	t.Run("skips a file that does not name the port", func(t *testing.T) {
		m := manifest{Files: []manifestFile{{Path: "vectors/retry.json", Package: "retry", Ports: []string{"primitives-ts"}}}}
		run, missing := plan(m, "primitives-go", known)
		if len(run) != 0 || len(missing) != 0 {
			t.Fatalf("got run=%v missing=%v, want the file ignored", run, missing)
		}
	})

	t.Run("reports a file that names the port and has no runner", func(t *testing.T) {
		m := manifest{Files: []manifestFile{{Path: "vectors/filtering.json", Package: "filtering", Ports: []string{"primitives-go"}}}}
		run, missing := plan(m, "primitives-go", known)
		if len(run) != 0 || len(missing) != 1 || missing[0].Package != "filtering" {
			t.Fatalf("got run=%v missing=%v, want filtering missing", run, missing)
		}
	})

	t.Run("a runner arm does not put a file in scope", func(t *testing.T) {
		m := manifest{Files: []manifestFile{{Path: "vectors/bitmask.json", Package: "bitmask", Ports: []string{"primitives-swift"}}}}
		run, missing := plan(m, "primitives-go", known)
		if len(run) != 0 || len(missing) != 0 {
			t.Fatalf("got run=%v missing=%v, want the file ignored", run, missing)
		}
	})
}

func loadManifest(t *testing.T) manifest {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "manifest.json"))
	if err != nil {
		t.Fatalf("reading manifest: %v", err)
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("parsing manifest: %v", err)
	}
	return m
}

func load(t *testing.T, mf manifestFile) vectorFile {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", mf.Path))
	if err != nil {
		t.Fatalf("reading vectors: %v", err)
	}
	var f vectorFile
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("parsing vectors: %v", err)
	}
	if f.Package != mf.Package {
		t.Fatalf("%s holds package %q, manifest says %q", mf.Path, f.Package, mf.Package)
	}
	if len(f.Cases) == 0 {
		t.Fatalf("%s has no cases", mf.Path)
	}
	return f
}

func into[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decoding %s: %v", string(raw), err)
	}
	return v
}

func runBitmask(t *testing.T, f vectorFile) {
	type in struct {
		Value uint32   `json:"value"`
		Flag  uint32   `json:"flag"`
		Flags []uint32 `json:"flags"`
	}
	for _, c := range f.Cases {
		t.Run(c.ID, func(t *testing.T) {
			i := into[in](t, c.Input)
			var got any
			switch c.Op {
			case "new":
				m := bitmask.New(i.Flags...)
				got = m.Value()
			case "set":
				m := bitmask.FromValue(i.Value)
				r := m.Set(i.Flags...)
				got = r.Value()
			case "clear":
				m := bitmask.FromValue(i.Value)
				r := m.Clear(i.Flags...)
				got = r.Value()
			case "toggle":
				m := bitmask.FromValue(i.Value)
				r := m.Toggle(i.Flags...)
				got = r.Value()
			case "has":
				m := bitmask.FromValue(i.Value)
				got = m.Has(i.Flag)
			case "hasAll":
				m := bitmask.FromValue(i.Value)
				got = m.HasAll(i.Flags...)
			case "hasAny":
				m := bitmask.FromValue(i.Value)
				got = m.HasAny(i.Flags...)
			case "isEmpty":
				m := bitmask.FromValue(i.Value)
				got = m.IsEmpty()
			case "count":
				m := bitmask.FromValue(i.Value)
				got = m.Count()
			default:
				t.Fatalf("unhandled op %q — a vector op with no runner arm is a silent pass", c.Op)
			}
			assertJSONEqual(t, c.Expect, got)
		})
	}
}

func runNumbers(t *testing.T, f vectorFile) {
	type in struct {
		Value         float32 `json:"value"`
		Factor        float32 `json:"factor"`
		Precision     uint8   `json:"precision"`
		OriginalYield int     `json:"originalYield"`
		DesiredYield  int     `json:"desiredYield"`
	}
	for _, c := range f.Cases {
		t.Run(c.ID, func(t *testing.T) {
			i := into[in](t, c.Input)
			var got float32
			switch c.Op {
			case "roundToDecimalPlaces":
				got = numbers.RoundToDecimalPlaces(i.Value, i.Precision)
			case "scale":
				got = numbers.Scale(i.Value, i.Factor, i.Precision)
			case "scaleToYield":
				got = numbers.ScaleToYield(i.Value, i.OriginalYield, i.DesiredYield, i.Precision)
			default:
				t.Fatalf("unhandled op %q", c.Op)
			}
			want64, err := strconv.ParseFloat(string(c.Expect), 32)
			if err != nil {
				t.Fatalf("expect %q is not a number: %v", string(c.Expect), err)
			}
			if want := float32(want64); got != want {
				t.Fatalf("got %v, want %v", got, want)
			}
		})
	}
}

func runErrors(t *testing.T, f vectorFile) {
	type in struct {
		Code string `json:"code"`
	}
	for _, c := range f.Cases {
		t.Run(c.ID, func(t *testing.T) {
			i := into[in](t, c.Input)
			if c.Op != "httpStatusForCode" {
				t.Fatalf("unhandled op %q", c.Op)
			}
			assertJSONEqual(t, c.Expect, errhttp.HTTPStatusForCode(errhttp.ErrorCode(i.Code)))
		})
	}
}

func runRetry(t *testing.T, f vectorFile) {
	type in struct {
		InitialDelayMs int64   `json:"initialDelayMs"`
		MaxDelayMs     int64   `json:"maxDelayMs"`
		Multiplier     float64 `json:"multiplier"`
		UseJitter      bool    `json:"useJitter"`
		Attempts       []int   `json:"attempts"`
		Attempt        int     `json:"attempt"`
	}
	type expect struct {
		DelayForMs          []int64 `json:"delayForMs"`
		ScheduledDelayForMs []int64 `json:"scheduledDelayForMs"`
	}
	for _, c := range f.Cases {
		t.Run(c.ID, func(t *testing.T) {
			i := into[in](t, c.Input)
			cfg := retrycfg.Config{
				InitialDelay: time.Duration(i.InitialDelayMs) * time.Millisecond,
				MaxDelay:     time.Duration(i.MaxDelayMs) * time.Millisecond,
				Multiplier:   i.Multiplier,
				UseJitter:    i.UseJitter,
			}
			switch c.Op {
			case "schedule":
				want := into[expect](t, c.Expect)
				if len(want.DelayForMs) != len(i.Attempts) {
					t.Fatalf("vector is malformed: %d attempts, %d expected delays", len(i.Attempts), len(want.DelayForMs))
				}
				for n, attempt := range i.Attempts {
					if got := retrycfg.DelayFor(cfg, uint(attempt)).Milliseconds(); got != want.DelayForMs[n] {
						t.Errorf("DelayFor(attempt %d) = %dms, want %dms", attempt, got, want.DelayForMs[n])
					}
					if got := retrycfg.ScheduledDelayFor(cfg, attempt).Milliseconds(); got != want.ScheduledDelayForMs[n] {
						t.Errorf("ScheduledDelayFor(attempt %d) = %dms, want %dms", attempt, got, want.ScheduledDelayForMs[n])
					}
				}
			case "delayFor":
				assertJSONEqual(t, c.Expect, retrycfg.DelayFor(cfg, uint(i.Attempt)).Milliseconds())
			default:
				t.Fatalf("unhandled op %q", c.Op)
			}
		})
	}
}

func runEncoding(t *testing.T, f vectorFile) {
	type in struct {
		ContentType string `json:"contentType"`
		Encoded     string `json:"encoded"`
	}
	ctx := context.Background()
	for _, c := range f.Cases {
		t.Run(c.ID, func(t *testing.T) {
			i := into[in](t, c.Input)
			if c.Op != "decode" {
				t.Fatalf("unhandled op %q", c.Op)
			}
			var got payload
			enc := encoding.NewClientEncoder(encoding.ContentType(i.ContentType))
			if err := enc.Unmarshal(ctx, []byte(i.Encoded), &got); err != nil {
				t.Fatalf("decoding %s: %v", i.ContentType, err)
			}
			want := into[payload](t, c.Expect)
			if !samePayload(got, want) {
				t.Fatalf("got %+v, want %+v", got, want)
			}
		})
	}
}

// assertJSONEqual compares through JSON so a vector's 5 and Go's uint32(5)
// agree without the runner caring which numeric type the op returned.
func assertJSONEqual(t *testing.T, want json.RawMessage, got any) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshalling result: %v", err)
	}
	var a, b any
	if err := json.Unmarshal(want, &a); err != nil {
		t.Fatalf("expect is not valid JSON: %v", err)
	}
	if err := json.Unmarshal(gotJSON, &b); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if fmt.Sprint(a) != fmt.Sprint(b) {
		t.Fatalf("got %s, want %s", gotJSON, want)
	}
}

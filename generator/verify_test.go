package main

// This is the reference runner. It reads ../vectors/*.json exactly as a port's
// runner must — generically, dispatching on `op`, never reaching into the
// generator's types — and asserts the committed vectors still describe what
// primitives-go does.
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

func load(t *testing.T, name string) vectorFile {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "vectors", name+".json"))
	if err != nil {
		t.Fatalf("reading vectors: %v", err)
	}
	var f vectorFile
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("parsing vectors: %v", err)
	}
	if len(f.Cases) == 0 {
		t.Fatalf("%s.json has no cases", name)
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

func TestBitmaskVectors(t *testing.T) {
	type in struct {
		Value uint32   `json:"value"`
		Flag  uint32   `json:"flag"`
		Flags []uint32 `json:"flags"`
	}
	for _, c := range load(t, "bitmask").Cases {
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

func TestNumbersVectors(t *testing.T) {
	type in struct {
		Value         float32 `json:"value"`
		Factor        float32 `json:"factor"`
		Precision     uint8   `json:"precision"`
		OriginalYield int     `json:"originalYield"`
		DesiredYield  int     `json:"desiredYield"`
	}
	for _, c := range load(t, "numbers").Cases {
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

func TestErrorsVectors(t *testing.T) {
	type in struct {
		Code string `json:"code"`
	}
	for _, c := range load(t, "errors").Cases {
		t.Run(c.ID, func(t *testing.T) {
			i := into[in](t, c.Input)
			if c.Op != "httpStatusForCode" {
				t.Fatalf("unhandled op %q", c.Op)
			}
			assertJSONEqual(t, c.Expect, errhttp.HTTPStatusForCode(errhttp.ErrorCode(i.Code)))
		})
	}
}

func TestRetryVectors(t *testing.T) {
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
	for _, c := range load(t, "retry").Cases {
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

func TestEncodingVectors(t *testing.T) {
	type in struct {
		ContentType string `json:"contentType"`
		Encoded     string `json:"encoded"`
	}
	ctx := context.Background()
	for _, c := range load(t, "encoding").Cases {
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

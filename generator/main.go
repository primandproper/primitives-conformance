package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/primandproper/primitives-go/v2/bitmask"
	"github.com/primandproper/primitives-go/v2/encoding"
	errhttp "github.com/primandproper/primitives-go/v2/errors/http"
	"github.com/primandproper/primitives-go/v2/numbers"
	retrycfg "github.com/primandproper/primitives-go/v2/retry/config"
)

type file struct {
	Package     string `json:"package"`
	Description string `json:"description"`
	Cases       []any  `json:"cases"`
}

// f32 renders a float32 as the shortest decimal that round-trips through a
// 32-bit float, so a port reading it back gets the same value Go had.
func f32(v float32) json.RawMessage {
	return json.RawMessage(strconv.FormatFloat(float64(v), 'g', -1, 32))
}

func bitmaskCases() []any {
	const (
		A uint32 = 1 << 0
		B uint32 = 1 << 1
		C uint32 = 1 << 2
		D uint32 = 1 << 3
	)
	out := []any{}
	add := func(id, op string, in, exp any) { out = append(out, map[string]any{"id": id, "op": op, "input": in, "expect": exp}) }

	m := bitmask.New(A, C)
	add("new-sets-listed-flags", "new", map[string]any{"flags": []uint32{A, C}}, m.Value())

	empty := bitmask.New[uint32]()
	add("new-of-nothing-is-zero", "new", map[string]any{"flags": []uint32{}}, empty.Value())

	z := bitmask.FromValue[uint32](0)
	add("zero-is-empty", "isEmpty", map[string]any{"value": uint32(0)}, z.IsEmpty())
	nz := bitmask.FromValue(A)
	add("nonzero-is-not-empty", "isEmpty", map[string]any{"value": A}, nz.IsEmpty())

	s1 := bitmask.FromValue(A)
	s2 := s1.Set(B, D)
	add("set-adds-flags", "set", map[string]any{"value": A, "flags": []uint32{B, D}}, s2.Value())
	s3 := bitmask.FromValue(A)
	s4 := s3.Set(A)
	add("set-existing-is-idempotent", "set", map[string]any{"value": A, "flags": []uint32{A}}, s4.Value())

	c1 := bitmask.FromValue(A | B | C)
	c2 := c1.Clear(B)
	add("clear-removes-only-named", "clear", map[string]any{"value": A | B | C, "flags": []uint32{B}}, c2.Value())
	c3 := bitmask.FromValue(A)
	c4 := c3.Clear(B)
	add("clear-absent-is-noop", "clear", map[string]any{"value": A, "flags": []uint32{B}}, c4.Value())

	t1 := bitmask.FromValue(A | B)
	t2 := t1.Toggle(B, C)
	add("toggle-flips-each", "toggle", map[string]any{"value": A | B, "flags": []uint32{B, C}}, t2.Value())

	h := bitmask.FromValue(A | C)
	add("has-present", "has", map[string]any{"value": A | C, "flag": C}, h.Has(C))
	add("has-absent", "has", map[string]any{"value": A | C, "flag": B}, h.Has(B))
	add("hasAll-partial-is-false", "hasAll", map[string]any{"value": A | C, "flags": []uint32{A, B}}, h.HasAll(A, B))
	add("hasAll-complete-is-true", "hasAll", map[string]any{"value": A | C, "flags": []uint32{A, C}}, h.HasAll(A, C))
	add("hasAny-one-match-is-true", "hasAny", map[string]any{"value": A | C, "flags": []uint32{B, C}}, h.HasAny(B, C))
	add("hasAny-no-match-is-false", "hasAny", map[string]any{"value": A | C, "flags": []uint32{B, D}}, h.HasAny(B, D))

	e := bitmask.FromValue[uint32](0)
	add("hasAny-on-empty-is-false", "hasAny", map[string]any{"value": uint32(0), "flags": []uint32{A}}, e.HasAny(A))

	cnt := bitmask.FromValue(A | C | D)
	add("count-counts-set-bits", "count", map[string]any{"value": A | C | D}, cnt.Count())
	cnt0 := bitmask.FromValue[uint32](0)
	add("count-of-empty-is-zero", "count", map[string]any{"value": uint32(0)}, cnt0.Count())
	return out
}

func numbersCases() []any {
	out := []any{}
	round := func(id string, v float32, p uint8) {
		out = append(out, map[string]any{"id": id, "op": "roundToDecimalPlaces",
			"input": map[string]any{"value": f32(v), "precision": p}, "expect": f32(numbers.RoundToDecimalPlaces(v, p))})
	}
	scale := func(id string, v, factor float32, p uint8) {
		out = append(out, map[string]any{"id": id, "op": "scale",
			"input": map[string]any{"value": f32(v), "factor": f32(factor), "precision": p}, "expect": f32(numbers.Scale(v, factor, p))})
	}
	yield := func(id string, v float32, oy, dy int, p uint8) {
		out = append(out, map[string]any{"id": id, "op": "scaleToYield",
			"input": map[string]any{"value": f32(v), "originalYield": oy, "desiredYield": dy, "precision": p},
			"expect": f32(numbers.ScaleToYield(v, oy, dy, p))})
	}
	round("round-half-up", 1.005, 2)
	round("round-down", 1.0049, 2)
	round("round-to-zero-places", 2.5, 0)
	round("round-negative", -1.2345, 3)
	round("round-already-exact", 3.14, 2)
	scale("scale-double", 2.5, 2, 2)
	scale("scale-half", 7, 0.5, 2)
	scale("scale-by-zero", 9.99, 0, 2)
	yield("yield-double-batch", 1.5, 4, 8, 2)
	yield("yield-halve-batch", 3, 8, 4, 2)
	yield("yield-same-batch", 2.25, 6, 6, 2)
	return out
}

func errorsCases() []any {
	out := []any{}
	for _, code := range []errhttp.ErrorCode{
		errhttp.ErrNothingSpecific, errhttp.ErrFetchingSessionContextData, errhttp.ErrDecodingRequestInput,
		errhttp.ErrValidatingRequestInput, errhttp.ErrDataNotFound, errhttp.ErrTalkingToDatabase,
		errhttp.ErrMisbehavingDependency, errhttp.ErrTalkingToSearchProvider, errhttp.ErrSecretGeneration,
		errhttp.ErrUserIsBanned, errhttp.ErrUserIsNotAuthorized, errhttp.ErrEncryptionIssue,
		errhttp.ErrCircuitBroken, errhttp.ErrIdempotencyKeyInFlight, errhttp.ErrIdempotencyKeyReused,
		errhttp.ErrResourceConflict, errhttp.ErrTooManyRequests, errhttp.ErrInvalidRequestSignature,
		errhttp.ErrNotEntitled, errhttp.ErrQuotaExhausted,
	} {
		out = append(out, map[string]any{
			"id": "status-for-" + string(code), "op": "httpStatusForCode",
			"input": map[string]any{"code": string(code)}, "expect": errhttp.HTTPStatusForCode(code),
		})
	}
	return out
}

func retryCases() []any {
	out := []any{}
	emit := func(id string, cfg retrycfg.Config, jitter bool) {
		cfg.EnsureDefaults()
		cfg.UseJitter = jitter
		delays := []int64{}
		scheduled := []int64{}
		for attempt := 1; attempt <= 8; attempt++ {
			delays = append(delays, retrycfg.DelayFor(cfg, uint(attempt)).Milliseconds())
			scheduled = append(scheduled, retrycfg.ScheduledDelayFor(cfg, attempt).Milliseconds())
		}
		out = append(out, map[string]any{
			"id": id, "op": "schedule",
			"input": map[string]any{
				"initialDelayMs": cfg.InitialDelay.Milliseconds(),
				"maxDelayMs":     cfg.MaxDelay.Milliseconds(),
				"multiplier":     cfg.Multiplier,
				"useJitter":      cfg.UseJitter,
				"attempts":       []int{1, 2, 3, 4, 5, 6, 7, 8},
			},
			"expect": map[string]any{"delayForMs": delays, "scheduledDelayForMs": scheduled},
		})
	}
	emit("defaults-no-jitter", retrycfg.Config{}, false)
	emit("fast-tight-cap", retrycfg.Config{InitialDelay: 10 * time.Millisecond, MaxDelay: 100 * time.Millisecond, Multiplier: 3}, false)
	emit("multiplier-one-is-flat", retrycfg.Config{InitialDelay: 250 * time.Millisecond, MaxDelay: 10 * time.Second, Multiplier: 1}, false)
	// Clamping: a sub-1 multiplier must not shrink the backoff.
	emit("multiplier-below-one-clamped", retrycfg.Config{InitialDelay: 50 * time.Millisecond, MaxDelay: time.Second, Multiplier: 0.5}, false)
	out = append(out, map[string]any{
		"id": "attempt-zero-treated-as-first", "op": "delayFor",
		"input":  map[string]any{"initialDelayMs": 100, "maxDelayMs": 5000, "multiplier": 2.0, "attempt": 0},
		"expect": retrycfg.DelayFor(func() retrycfg.Config { c := retrycfg.Config{}; c.EnsureDefaults(); return c }(), 0).Milliseconds(),
	})
	return out
}

type payload struct {
	Name    string   `json:"name"    yaml:"name"    xml:"name"    toml:"name"`
	Count   int      `json:"count"   yaml:"count"   xml:"count"   toml:"count"`
	Enabled bool     `json:"enabled" yaml:"enabled" xml:"enabled" toml:"enabled"`
	Tags    []string `json:"tags"    yaml:"tags"    xml:"tags"    toml:"tags"`
}

func encodingCases(ctx context.Context) []any {
	out := []any{}
	want := payload{Name: "widget", Count: 3, Enabled: true, Tags: []string{"a", "b"}}
	canonical := map[string]any{"name": "widget", "count": 3, "enabled": true, "tags": []string{"a", "b"}}
	for _, ct := range []encoding.ContentType{
		encoding.ContentTypeJSON, encoding.ContentTypeYAML, encoding.ContentTypeXML, encoding.ContentTypeTOML,
	} {
		enc := encoding.NewClientEncoder(ct)
		b, err := enc.Marshal(ctx, &want)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  !! marshal %s: %v\n", ct, err)
			continue
		}
		var back payload
		if err := enc.Unmarshal(ctx, b, &back); err != nil {
			fmt.Fprintf(os.Stderr, "  !! unmarshal %s: %v\n", ct, err)
			continue
		}
		roundTripped := samePayload(back, want)
		out = append(out, map[string]any{
			"id": "decode-" + string(ct), "op": "decode",
			"input":  map[string]any{"contentType": string(ct), "encoded": string(b)},
			"expect": canonical,
			"note":   map[string]any{"goRoundTripsCleanly": roundTripped},
		})
	}
	return out
}

// samePayload compares explicitly: payload holds a slice, so == will not do.
func samePayload(a, b payload) bool {
	if a.Name != b.Name || a.Count != b.Count || a.Enabled != b.Enabled || len(a.Tags) != len(b.Tags) {
		return false
	}
	for i := range a.Tags {
		if a.Tags[i] != b.Tags[i] {
			return false
		}
	}
	return true
}

func write(dir, name string, f file) {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		panic(err)
	}
	p := filepath.Join(dir, name+".json")
	if err := os.WriteFile(p, append(b, '\n'), 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("  %-14s %d cases\n", name+".json", len(f.Cases))
}

func main() {
	ctx := context.Background()
	dir := os.Args[1]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic(err)
	}
	write(dir, "bitmask", file{"bitmask", "Flag-set algebra over an unsigned integer. Values are decimal integers, not bit strings.", bitmaskCases()})
	write(dir, "numbers", file{"numbers", "Decimal rounding and scaling at 32-bit float precision. Expected values are the shortest decimal that round-trips a float32.", numbersCases()})
	write(dir, "errors", file{"errors", "The error-code to HTTP-status mapping. A port that disagrees here returns the wrong status to a real client.", errorsCases()})
	write(dir, "retry", file{"retry", "The exponential backoff schedule with jitter off. Delays are milliseconds.", retryCases()})
	write(dir, "encoding", file{"encoding", "Decoding: given these bytes for this content type, produce this value. Encoding is not asserted byte-for-byte because serializer formatting differs legitimately across ecosystems.", encodingCases(ctx)})
}

# Vector file format

One file per package, `vectors/<package>.json`:

```jsonc
{
  "package": "bitmask",
  "description": "What this file pins, and any precision caveats.",
  "cases": [
    {
      "id": "clear-removes-only-named",  // unique within the file; stable across versions
      "op": "value",                     // which operation to exercise
      "input": { "value": 7, "flags": [2] },
      "expect": 5
    }
  ]
}
```

- **`id`** is stable. Renaming one is a breaking change to the suite: a port skipping a case
  by id would silently start skipping a different one.
- **`op`** names the operation, not the method. Ports map it to their own idiomatic name —
  Go's `HasAll`, TypeScript's `hasAll`, Swift's `hasAll(_:)`.
- **`input`** and **`expect`** are plain JSON. No language-specific encodings, no base64
  unless the value is genuinely bytes.

## Type conventions

| concept | representation | why |
| --- | --- | --- |
| bitmask values | decimal integer | `7`, not `"0b111"` — every language parses it identically |
| durations | integer milliseconds, key suffixed `Ms` | no ISO-8601 parsing, no float seconds |
| 32-bit floats | JSON number, shortest round-tripping decimal | `strconv.FormatFloat(v, 'g', -1, 32)`; a port comparing exactly will match |
| errors | `{"valid": false}` or a code string | never a message — messages are not a contract |
| bytes | the literal string when printable, base64 otherwise | stated per case |

## Rules

1. **Never assert an error message.** Assert the code, the status, or a boolean. Messages
   are allowed to read naturally in each language.
2. **Never assert serializer output byte-for-byte.** `encoding` asserts *decoding*: given
   these bytes, produce this value. Two YAML libraries indent differently and are both
   correct; neither may misread a document.
3. **Nothing that needs a clock, a network or entropy.** If the answer can differ on a
   Tuesday, it is not a vector. Inject a fixed seed instead, or leave it out.
4. **Every `op` must be dispatchable on its own.** A runner switches on `op` and never parses
   `id`. An earlier draft used `op: "value"` for `new`, `set`, `clear` and `toggle` alike and
   only the `id` told them apart — which is unimplementable in a runner that treats `id` as a
   label. If two cases need different code paths, they need different ops.

## Writing a runner

`generator/verify_test.go` is the reference: it reads the JSON generically, switches on `op`,
and never reaches into the generator's types. Two things it does that yours must too:

- **Fail on an unknown `op`.** A runner that ignores ops it does not handle reports green
  while testing nothing. Every switch ends in `default: fail`.
- **Defeat your test cache.** The vectors are data files your test framework probably does not
  track as an input. Go's test cache does not: editing a vector and re-running reports a stale
  `ok (cached)` and the change passes unexamined. The reference runner is invoked with
  `go test -count=1`. Find your ecosystem's equivalent before trusting a green run — Vitest
  keys on source files, Gradle's `test` task is `UP-TO-DATE` unless the vectors are declared
  as inputs, and `swift test` caches per build plan.

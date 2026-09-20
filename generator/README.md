# generator

Writes `../vectors/*.json` by calling the real `primitives-go` packages and recording what
they do. Vectors are never written by hand: a hand-written expectation is a guess about
behaviour, a generated one is a recording of it.

```bash
cd generator
go run . ../vectors
```

`go.mod` has a `replace` pointing at a sibling `primitives-go` checkout, so you can
regenerate against unreleased behaviour before it ships.

After regenerating, rebuild the manifest and bump `VERSION` if any file's hash changed —
a changed vector is a behaviour change in the shared surface, and every port's drift check
should go red until it catches up.

## Adding a package

Add a `<package>Cases() []any` function returning `{id, op, input, expect}` maps, and a
`write(...)` line in `main()`. Keep to the type conventions in [../SCHEMA.md](../SCHEMA.md):
integers for bitmasks, milliseconds for durations, shortest-round-tripping decimals for
float32, and never an error message.

One case in `identifiers` (`generated-is-valid`) embeds a freshly generated ID, so it
changes on every run. That is deliberate — it proves the current generator's output
validates — but it means a regeneration always dirties that file. Check the diff is only
that line before bumping `VERSION`.

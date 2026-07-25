# Tomobit

> **Tomobit is not built to use AI. Tomobit is built to grow with it.**

*日本語版: [README.md](README.md) — the Japanese README is the canonical one.
All 48 design records (ADRs) are in Japanese.*

Tomobit stands in front of your coding AIs — Claude Code, Codex, local models —
and learns **which one to trust, in which context**, from what actually happened.

It is not a wrapper that forwards your prompt. It keeps a ledger.

## The one idea

Most routers ask a model which model to use. Tomobit doesn't.

**Meaning by Model, Judgment by Math.** A local LLM is allowed exactly one seat:
turning raw reality into vocabulary (`lang=go`, `cap=implement`, `size=small`).
Every *judgment* after that is a pure function over a decayed Beta ledger —
a pessimistic capability gate, then Thompson sampling over preference.

This is why Tomobit can always tell you *why*:

```
$ tomobit chat --view ndjson | jq -c 'select(.type == "decided")'
{"type":"decided","provider":"codex","q":0.2,"fallback":false,"candidates":[
  {"provider":"claude-code","scope":"cap=implement|lang=go","quantile":0.31,"passed":false,"wins":2},
  {"provider":"codex","scope":"cap=implement|lang=go","quantile":0.62,"passed":true,"wins":7}]}
```

`quantile` is the pessimistic lower bound on that provider's posterior for that
exact context; `passed` is whether it cleared the capability gate; `wins` is how
many Thompson draws it took. **No model was consulted to produce any of it.**
It is arithmetic over what happened to you
([ADR-0040](docs/decisions/ADR-0040-decision-audit-view.md)).

## The part nobody else does

A ledger keyed on context is easy. The hard question is **what "context" means** —
how finely should you slice the world before the statistics stop being useful?

Tomobit doesn't ask you to decide, and doesn't hard-code it. **Connections split
and merge on their own.**

When predictions keep missing in a way that correlates with some attribute,
Curiosity notices (excess surprisal: `−log P(y) − H`, self-normalizing and
bounded), and the Connection Engine runs the trial — a corrected log Bayes
factor with hysteresis, closed-form under Beta-Binomial. Split at `≥ +3`, merge
at `≤ 0`, hold in between. One knob.

So `cap=implement` may start as a single belief, and become
`cap=implement × lang=go` the day Go stops behaving like everything else —
because the evidence demanded it, not because you configured it.

When a Connection is born, it inherits **the mean but not the confidence**
(`Beta(μ·m₀, (1−μ)·m₀)`). It knows what its parent knew. It does not pretend to
be as sure.

## Your ledger is yours

One SQLite file at `~/.tomobit/tomobit.db`. Append-only truth
(`events`, `experiences`); every projection is rebuildable with `tomobit rebuild`.
By default nothing passes through anyone's hands — not a prohibition on sharing,
but a guarantee that only the owner can move it. Forgetting is a human verb:
`tomobit forget` physically deletes and VACUUMs. Tomo never proposes it.

This is called **Experience Sovereignty**, and it is the reason the whole thing
is one file you can carry.

## And there is a dog

`tomobit-face` opens a small transparent window with a puppy in it (shiba,
retriever, or pomeranian — 32×32, six greys).

It is **not** a progress bar with a costume on. Tomo's growth stage is a
*measurement*: S3 requires decayed surprisal to have settled (calibration), S4
additionally requires low decision wobble on islands where real competition
exists. There is no XP, and you cannot feed it. `tomobit status` will tell you
exactly which gate is still closed and by how much.

When Tomo sounds unsure, that is the posterior being thin. Nothing about the
character is decorative — every expression is a view of the ledger.

## Getting started

Requirements: **Go 1.26+**, and at least one provider CLI on `PATH`
([claude](https://claude.com/claude-code) or codex). A local perception backend
([Ollama](https://ollama.com) or mlx-lm) is recommended but optional — without
it, tasks still run and perception is deferred until you run `tomobit perceive`.

```sh
git clone https://github.com/Rererr/tomobit.git && cd tomobit
go install ./cmd/tomobit ./cmd/tomobit-face
tomobit setup      # interactive wiring → ~/.tomobit/config.json
tomobit            # companion view, then straight into conversation
```

`--provider` defaults to **auto**: Tomo picks from the ledger, considering only
providers that actually launch on this machine. Run `tomobit help` for commands.

A desktop chat GUI lives in a separate repository:
[tomobit-gui](https://github.com/Rererr/tomobit-gui).

> **Note on quota display — off by default.** Tomobit can show your remaining
> provider quota, but only if you say yes during `tomobit setup`. Until you do,
> **it never reads a Keychain item and never calls an external endpoint.**
> Enabled, it reads *your own* OAuth token from the macOS Keychain and calls a
> vendor usage endpoint that is **not officially documented** — it may disappear
> without warning, in which case the display simply reads 不明 (unknown). No
> estimates are ever invented, and quota never enters the decision rule.
> See [SECURITY.md](SECURITY.md),
> [ADR-0044](docs/decisions/ADR-0044-provider-quota-observation.md) and
> [ADR-0049](docs/decisions/ADR-0049-quota-observation-is-opt-in.md).

## Design records

The 48 ADRs in [`docs/decisions/`](docs/decisions/) are the real artifact of this
project. They are in Japanese, but they are worth a translator: they record the
judgments that were **overturned by measurement**, not just the ones that
survived.

- [ADR-0011](docs/decisions/ADR-0011-meaning-by-model-judgment-by-math.md) —
  Meaning by Model, Judgment by Math
- [ADR-0001](docs/decisions/ADR-0001-connection-granularity.md) /
  [ADR-0002](docs/decisions/ADR-0002-surprise-and-split-judgment.md) —
  coarse-then-split, and how a split is judged
- [ADR-0013](docs/decisions/ADR-0013-prior-inheritance-mean-only.md) —
  inherit the mean, never the confidence
- [ADR-0018](docs/decisions/ADR-0018-experience-sovereignty.md) —
  Experience Sovereignty
- [ADR-0037](docs/decisions/ADR-0037-merge-reachability.md) —
  **an attempt that failed.** Rehabilitation under inherited priors turned out to
  need roughly 15 years. Kept, because a design record that only contains
  successes is marketing.
- [ADR-0042](docs/decisions/ADR-0042-split-starvation-and-lexical-shadowing.md) —
  the ledger was quietly picking a provider with an 11-loss streak, because
  tie-breaking was alphabetical. Five alternatives, ranked by measurement.

Architecture overview: [COGNITIVE_ARCHITECTURE.md](docs/core/COGNITIVE_ARCHITECTURE.md).
Philosophy: [VISION.md](docs/core/VISION.md).

## Status

Early. Built and dogfooded by one person, on macOS, for real daily work.
CI runs on Linux and macOS; the quota reader and the notification silencer are
macOS-only. Linux is not yet a platform anyone has actually lived on — treat it
as "compiles and passes tests", not "supported".

**Tomobit is worst on day one** — an empty ledger knows nothing, judgments fall
back to uniform priors, and Tomo is a ball of fluff. That is the design, not a
defect. It is worth knowing before you decide whether to install it.

## Contributing

**No CLA.** Contributions are under the [DCO](https://developercertificate.org/)
(`git commit -s`), which means nobody — including me — can ever close this code.
Design changes start as an ADR draft, not a patch. See
[CONTRIBUTING.md](CONTRIBUTING.md).

## License

| | |
|---|---|
| `docs/`, `README*.md`, `VISION.ja.md` | [CC BY-SA 4.0](LICENSE-docs) |
| everything else | [AGPL-3.0-only](LICENSE) |

© 2026 Rererr

Licenses protect expression, not ideas — so nothing here stops you from
reimplementing this in another language under another name. **That is fine, and
genuinely welcome.** The share-alike on the docs stops only one thing: taking the
writing and removing where it came from.

If you build on the design, please cite [CITATION.cff](CITATION.cff).

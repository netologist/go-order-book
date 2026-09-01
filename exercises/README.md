# Order Book — Practice Exercises (TDD)

> **You write the code, the tests are pre-written. Make them pass.**

This folder contains 6 hands-on exercises that map 1:1 to **Phases 2–7** of the
study guide
`interviews/order-book/2026-06-15-deepseek-orderbook-study-guide.md`.

## Workflow

For each phase:

1. Open `phase-N-<topic>/README.md` — read the goal, what to build, hints.
2. Open `phase-N-<topic>/<topic>.go` — that's your **stub**. It compiles but
   does nothing useful. Implement the methods.
3. Open `phase-N-<topic>/<topic>_test.go` — these are your **acceptance
   tests**. They must all pass before moving on.
4. Run:

   ```bash
   
       test ./exercises/phase-N-<topic>/... -count=1 -v
   ```

5. When green, commit:

   ```bash
   git add exercises/phase-N-<topic>
   git commit -m "feat(exercises): phase N — <topic>"
   ```

## Phase map

| # | Phase | Folder | Test count (approx) |
|---|---|---|---|
| 2 | Order book (sorted slice) | `phase2-orderbook/` | ~10 |
| 3 | Matching engine (market + IOC + self-match) | `phase3-matcher/` | ~7 |
| 4 | Concurrency (mutex + race) | `phase4-concurrency/` | ~4 |
| 5 | State machine (Open/Live/Paused/Settled) | `phase5-state/` | ~8 |
| 6 | Ledger (balances + held, TOCTOU-safe) | `phase6-ledger/` | ~7 |
| 7 | Idempotency guard (TTL + dedup) | `phase7-idempotency/` | ~6 |

## Conventions

- All packages import the **shared** `internal/orderbook` for the `Order`,
  `Trade`, `Side`, `OrderType`, sentinels (`ErrInvalidPrice`, etc.) and
  `ValidationErrors` types. Don't redefine them.
- Money is always `int64` cents. Never `float64`.
- Each exercise is a **separate Go package** (e.g. `package phase2`).
- Tests use **table-driven** style where it makes sense.
- Phase 4+ tests **must pass under `go test -race`** — the README tells you.

## When you're stuck

- The stubs contain `panic("TODO: ...")`. Run `grep -rn TODO exercises/`
  to see what's left.
- Study guide references in each README point you at the right section.
- The Interview handoff (`/var/folders/.../order-book-orderbook-handoff.md`)
  has all the decisions we've made so far.

Good hunting.

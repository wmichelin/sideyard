# ClubDue task management

GitHub Issues are the team intake queue.

## Report a bug

Open **New issue → Bug report** in the ClubDue repository. Complete the area,
reproduction steps, expected/actual behavior, severity, and evidence. The form
automatically applies `bug` and `agent-ready`.

## Lifecycle

1. `agent-ready`: waiting for pickup.
2. `agent-claimed`: the Codex execution agent has accepted it and comments with
   the planned fix and test plan.
3. `staging-ready`: the fix is pushed to `main`, deployed to staging, and
   verified there.
4. The agent comments with the commit, staging URL, tests, and result, then
   closes the issue only when the acceptance criteria pass.
5. `blocked`: the agent cannot proceed without a product decision, secret, or
   external dependency. It comments the exact blocker.

The queue is checked before roadmap work. P0/P1 bugs take priority over the
next planned feature. Production is never changed automatically; promotion
still requires explicit approval.

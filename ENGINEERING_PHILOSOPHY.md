<!-- BEGIN managed: agent-skills engineering philosophy -->
<!-- source: https://github.com/wmichelin/agent-skills.git@a0e2141a435295a4eb9a04462341b7e21fb3d9e7 -->
# Engineering principles

This is the user's current engineering playbook. It is primarily for Go codebases. User instructions and repository-local rules take precedence.

## Priority order

1. Preserve correct, secure, observable behavior.
2. Make the smallest coherent change that solves the real problem.
3. Prefer clarity and explicitness over cleverness or abstraction for its own sake.
4. Keep ownership, boundaries, and dependencies easy to understand.
5. Leave the touched area easier to change and verify than before.

## Rules

- Start by discovering local conventions, public contracts, test commands, and the dirty-worktree state. Follow them unless there is a documented reason to change them.
- Optimize for the next maintainer: names should expose intent; control flow should be direct; surprising behavior deserves a concise explanation or test.
- Prefer one clear source of truth. Avoid duplicate representations, hidden coupling, and configuration that is not exercised.
- Add an abstraction only when it removes demonstrated complexity or supports a stable boundary. Do not create generic frameworks for one use.
- Keep modules cohesive and dependencies directional. Do not spread a concern across unrelated layers merely to make a local edit convenient.
- Keep the repository root intentionally sparse. It may contain module/build metadata, top-level documentation, operational configuration, and deliberate entrypoints; it must not become a dumping ground for unrelated application source files.
- Organize application code into cohesive folders around domains and ownership. Prefer a stable business capability over a generic technical bucket such as `utils`, `helpers`, or `common`; do not force a rigid domain taxonomy when the codebase or change has a clearer structure.
- In Go applications, put executable entrypoints in `cmd/<binary>` and non-public application packages under `internal/<domain>` (or a similarly clear domain directory). A root Go package is appropriate only for a deliberate public module API, not as a default home for application code.
- When a flat codebase needs structure, move one cohesive behavior at a time, preserve its public contracts, and validate after every move. Do not combine a broad directory reshuffle with behavior changes or unrelated cleanup.
- Preserve compatibility deliberately. Identify callers, persisted data, wire formats, migrations, and operational dependencies before changing them.
- Make failure behavior intentional: validate at boundaries, retain useful context, and never silently discard errors or data.
- Chase behavioral coverage, not line coverage: tests should prove observable outcomes, edge cases, error paths, and contracts. Strive for full behavioral coverage of changed code, while avoiding tests coupled to incidental implementation details.
- Remove dead code only when references, runtime paths, generated artifacts, and public compatibility have been checked. Do not combine broad deletion with unrelated behavior changes.
- Avoid drive-by formatting, renames, dependency upgrades, and broad refactors. Split them into separately reviewable work when they are worthwhile.
- Prefer reversible, observable delivery: small commits or checkpoints, targeted validation, and a clear rollback path for material changes.

## Go practices

- At boundaries, accept interfaces and provide concrete implementations. Define interfaces where the consuming code needs a capability, rather than mirroring every concrete type or creating speculative interfaces.
- Use inversion of control for every external dependency: pass clients, storage, clocks, random sources, process runners, network services, and similar effects into the code that uses them. Keep construction and wiring near the application boundary.
- Provide mocked implementations for external dependencies so tests can control success, failure, timing, and boundary behavior without relying on live systems. Keep mocks small, behavior-focused, and scoped to the interface they satisfy.
- Prefer table-driven tests for multiple cases or permutations. Name each case after the behavior it protects.
- Prefer `cmp.Diff` for meaningful comparisons, with `cmpopts` to explicitly normalize intentional differences. Treat an empty diff as the assertion; do not hide meaningful output behind broad ignores.

## Decision record

For any exception or material tradeoff, record: the principle affected, the evidence, the chosen option, and the risk accepted. Keep this proportional; a short note in the plan or PR is normally enough.
<!-- END managed: agent-skills engineering philosophy -->

## Project-specific additions

Explicit user direction and repository-local rules, including safety and compliance requirements, take precedence.

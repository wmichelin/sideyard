# ClubDue task management

GitHub Issues are the team intake queue for both bugs and feature requests.

## Report a bug

Open **New issue → Bug report** in the ClubDue repository. Complete the area,
reproduction steps, expected/actual behavior, severity, and evidence. The form
automatically applies `bug` and `agent-ready`.

For a product idea, use **New issue → Feature request**. It applies `feature`,
`team-review`, and `agent-ready`, and requires the problem, proposed behavior,
desired outcome, acceptance criteria, and priority.

## Lifecycle

1. `team-review` + `agent-ready`: waiting for the standing team to review and
   pick up the ticket. The execution agent documents the review before coding.
2. `agent-claimed`: the Codex execution agent has accepted it and comments with
   the planned fix and test plan.
3. `staging-ready`: the change is pushed to `main`, deployed to staging, and
   verified there.
4. The agent comments with the commit, staging URL, tests, and result, then
   closes the issue only when the acceptance criteria pass.
5. `blocked`: the agent cannot proceed without a product decision, secret, or
   external dependency. It comments the exact blocker.

## Status updates

The issue receives an automation comment when GitHub delivers the event to the
G5 runner. The dedicated issue-pickup worker then comments when it claims the
ticket, posts the six-role team review, reports implementation/test progress,
and reports the staging result. Label changes (`agent-claimed`,
`staging-ready`, or `blocked`) are also visible status signals. If no comment
appears within a few minutes, check the Actions run linked from the issue.
Issue intake is event-driven; there is no continuously running issue poller.
The roadmap iteration worker is a separate queue and never claims or dispatches
issues. Every issue-bot and execution-status comment includes the staging link,
even while deployment is pending: https://clubdue-staging.michelindigitalmedia.com

## Required team review comment

Before implementation, the ticket must receive one clearly labeled comment:

```text
## Team review
### Product Manager
### Software Architect
### Unit Testing Expert
### Staff Software Engineer
### DevOps Engineer
### QA Tester
### Decision and acceptance criteria
### Test plan
```

Each section must contain the role's recommendation, risks, and requested
acceptance/test checks. The execution agent may consolidate the six specialist
reviews into this one ticket comment, but it must preserve each role heading.

The queue is checked before roadmap work. P0/P1 bugs take priority over the
next planned feature. Production is never changed automatically; promotion
still requires explicit approval.

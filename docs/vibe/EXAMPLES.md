# Examples and worked flows for Compound Engineering

## Example: plan from an issue and implement

1. In Claude Code, run: `/ce-plan #151`
2. Answer clarifying questions to scope the plan.
3. Review the generated plan under `docs/plans/`.
4. Run: `/ce-work docs/plans/<plan-filename>.md`
5. The work skill executes each implementation unit, runs tests, and commits.
6. Run: `/ce-code-review`
7. Run: `/ce-commit-push-pr`
8. Request human review on the draft PR.

## Example: brainstorm a new feature, then plan and implement

1. In Claude Code, run: `/ce-brainstorm smart consumption detection`
2. Answer clarifying questions to define requirements, scope, and acceptance criteria.
3. Review the requirements doc under `docs/brainstorms/`.
4. Run: `/ce-plan` — it will pick up the brainstorm doc as the origin.
5. Review the plan under `docs/plans/`.
6. Run: `/ce-work docs/plans/<plan-filename>.md`
7. Run: `/ce-code-review`
8. Run: `/ce-commit-push-pr`

## Example: fix a bug with ce-debug

1. In Claude Code, run: `/ce-debug`
2. Describe the bug or paste the error.
3. The debug skill investigates root cause, proposes a fix, and verifies it.
4. Review the diff and run: `/ce-code-review`
5. Run: `/ce-commit-push-pr`

## Example: review before opening a PR

1. After implementing changes, run: `/ce-code-review`
2. The review dispatches correctness, security, performance, and maintainability personas.
3. Review the findings — safe fixes are auto-applied; gated fixes wait for confirmation.
4. After accepting fixes, run: `/ce-commit-push-pr`

## Tips

- Keep iterations small: shorter cycles make reviews easier.
- `STRATEGY.md` is the grounding document — read it before planning any feature to ensure alignment.
- `CLAUDE.md` carries coding standards — CE skills read it automatically.
- Use `/ce-compound` to document solutions and patterns worth reusing.
